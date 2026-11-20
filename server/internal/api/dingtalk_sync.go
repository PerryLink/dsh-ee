package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/dingtalk"
	"github.com/hikariming/dsh-ee/server/internal/oidc"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 钉钉组织同步：和飞书组织同步同一套规则（feishu.go）——按通讯录建号、更新资料与部门映射的角色，
// 钉钉明确说找不到的成员按开关禁用；拉取失败或目录为空绝不改账号。

const dingTalkSyncLastKey = "dingtalk_sync_last"

type dingTalkSyncResult struct {
	At         time.Time `json:"at"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
	Remote     int       `json:"remote"`
	Created    int       `json:"created"`
	Reconciled int       `json:"reconciled"`
	// 关联在另一个来源（飞书）建的账号上的成员：不另外建号
	Linked    int      `json:"linked"`
	Disabled  []string `json:"disabled"`
	Conflicts []string `json:"conflicts"`
	Skipped   []string `json:"skipped"`
	// 目录里没看到、但钉钉没有明确说找不到的账号：没有禁用，列出来给管理员确认
	Kept []string `json:"kept"`
	// 钉钉明确说人不在了的关联身份：只解除关联，账号归主身份所在的来源管
	Unlinked []string `json:"unlinked"`
}

// dingTalkSyncConfig 找开着的钉钉配置（主登录方式或第二登录入口）。
func (s *Server) dingTalkSyncConfig(ctx context.Context) (*oidc.Config, error) {
	cfg, _ := s.oidcByPreset(ctx, dingtalk.Preset)
	if cfg == nil {
		return nil, errors.New("请先选择钉钉并开启统一登录")
	}
	if cfg.DingTalkCorpID == "" || cfg.ClientSecret == "" {
		return nil, errors.New("组织同步需要钉钉企业 CorpId 与 Client Secret")
	}
	return cfg, nil
}

func dingTalkDeptNames(dir *dingtalk.Directory, ids []int64) ([]string, []string) {
	var names, groups []string
	for _, id := range ids {
		groups = append(groups, strconv.FormatInt(id, 10))
	}
	for _, id := range ids {
		if name := strings.TrimSpace(dir.Departments[id]); name != "" {
			names = append(names, name)
			groups = append(groups, name)
		}
	}
	return names, groups
}

func (s *Server) syncDingTalk(ctx context.Context) (*dingTalkSyncResult, error) {
	res := &dingTalkSyncResult{At: time.Now().UTC(), Disabled: []string{}, Conflicts: []string{}, Skipped: []string{}, Kept: []string{}, Unlinked: []string{}}
	finish := func(err error) (*dingTalkSyncResult, error) {
		if err != nil {
			res.Error = err.Error()
		} else {
			res.OK = true
		}
		if storeErr := s.store.PutSettingJSON(ctx, dingTalkSyncLastKey, res, 0); storeErr != nil {
			return nil, storeErr
		}
		return res, nil
	}
	cfg, err := s.dingTalkSyncConfig(ctx)
	if err != nil {
		return finish(err)
	}
	client := s.dingTalkClient(cfg)
	dir, err := client.Directory(ctx)
	if err != nil {
		var apiErr *dingtalk.APIError
		if errors.As(err, &apiErr) && (apiErr.Code == dingtalk.CodeDeptOutOfScope || apiErr.Code == dingtalk.CodeNoPermission) {
			return finish(errors.New(err.Error() + "（请给应用开通通讯录部门与成员读权限，并把通讯录权限范围设为全部员工）"))
		}
		return finish(err)
	}
	if len(dir.Users) == 0 {
		return finish(errors.New("通讯录没有员工；为避免误更新或误禁用整批账号，本轮不生效"))
	}
	res.Remote = len(dir.Users)
	prefix := dingtalk.ExternalSubject(cfg.DingTalkCorpID, "")
	autoLink := s.identityAutoLink(ctx)
	seen := map[string]bool{}
	for _, remote := range dir.Users {
		subject := dingtalk.ExternalSubject(cfg.DingTalkCorpID, remote.UserID)
		if len(subject) > 512 {
			res.Conflicts = append(res.Conflicts, remote.Name+"（userId 太长）")
			continue
		}
		seen[subject] = true
		contacts := dingTalkContacts(remote)
		// 已经（或可以安全地）关联在飞书建的账号上：不另外建号，资料和角色由那边管
		if linked, suppressed, err := s.syncLinkedIdentity(ctx, dingtalk.Preset, subject, contacts, autoLink); err != nil {
			return finish(err)
		} else if suppressed {
			res.Skipped = append(res.Skipped, remote.UserID)
			continue
		} else if linked {
			res.Linked++
			continue
		}
		username := dingTalkUsername(subject, remote.UserID)
		var previous *store.User
		if existing, lookupErr := s.store.GetUserByExternalDN(ctx, store.AuthOIDC, subject); lookupErr == nil {
			previous = existing
			if previous.Disabled {
				res.Skipped = append(res.Skipped, previous.Username+"（本地已禁用，不自动恢复）")
				continue
			}
		} else if !notFound(lookupErr) {
			return finish(lookupErr)
		}
		if previous == nil && !cfg.AutoCreate {
			res.Skipped = append(res.Skipped, username+"（未预先开通）")
			continue
		}
		departments, groups := dingTalkDeptNames(dir, remote.DeptIDs)
		// 资料里显示的邮箱优先用企业邮箱；员工自填的邮箱只用来显示，不参与任何身份匹配
		email := strings.TrimSpace(remote.OrgEmail)
		if email == "" {
			email = strings.TrimSpace(remote.Email)
		}
		identity := oidc.Identity{Subject: subject, Username: username, DisplayName: remote.Name, Email: email,
			Department: strings.Join(departments, " / "), Groups: groups}
		synced, _, err := s.provisionOIDC(ctx, cfg, identity)
		if err == nil && synced != nil {
			s.applyDirectoryFields(ctx, dingtalk.Preset, synced.ID, remote.Attrs())
			s.recordSyncIdentity(ctx, dingtalk.Preset, synced, contacts)
		}
		switch {
		case errors.Is(err, errOIDCConflict), errors.Is(err, errOIDCIdentity), errors.Is(err, errOIDCEmailMismatch):
			res.Conflicts = append(res.Conflicts, username)
		case errors.Is(err, errBadCredentials), errors.Is(err, errNotProvisioned):
			res.Skipped = append(res.Skipped, username)
		case err != nil:
			return finish(err)
		case previous == nil:
			res.Created++
		default:
			res.Reconciled++
		}
	}
	if cfg.DingTalkSyncDisable {
		// 目录里没看到不等于离职：应用的通讯录授权范围可能只覆盖一部分员工。
		// 逐个向钉钉确认，只有钉钉明确说找不到这个人才算离职；看不到的留着。
		confirm := func(subject string) (dingtalk.UserState, error) {
			return client.UserState(ctx, strings.TrimPrefix(subject, prefix))
		}
		existing, err := s.store.ListUsersBySource(ctx, store.AuthOIDC)
		if err != nil {
			return finish(err)
		}
		for _, u := range existing {
			// 只管这个企业的钉钉账号；已经禁用的不用每一轮再禁一次
			if u.Disabled || !strings.HasPrefix(u.ExternalDN, prefix) || seen[u.ExternalDN] {
				continue
			}
			state, err := confirm(u.ExternalDN)
			if err != nil {
				return finish(err)
			}
			if state != dingtalk.UserGone {
				why := "钉钉里仍在职，但不在这次拉到的目录里"
				if state == dingtalk.UserOutOfScope {
					why = "不在钉钉应用的通讯录权限范围内，看不到状态"
				}
				res.Kept = append(res.Kept, u.Username+"（"+why+"）")
				continue
			}
			yes := true
			if err := s.store.UpdateUser(ctx, u.ID, store.UserPatch{Disabled: &yes}); err != nil {
				return finish(err)
			}
			if err := s.store.DeleteUserSessions(ctx, u.ID); err != nil {
				return finish(err)
			}
			s.purgeUserCredentials(ctx, u.ID)
			res.Disabled = append(res.Disabled, u.Username)
			s.auditSystem(ctx, auditUserUpdate, u.Username, map[string]any{"disabled": true, "reason": "dingtalk_missing"})
		}
		unlinked, err := s.pruneLinkedIdentities(ctx, dingtalk.Preset, prefix, seen, func(subject string) (bool, error) {
			state, err := confirm(subject)
			return state == dingtalk.UserGone, err
		})
		res.Unlinked = unlinked
		if err != nil {
			return finish(err)
		}
	}
	s.auditSystem(ctx, auditDingTalkSync, "", map[string]any{"remote": res.Remote, "created": res.Created, "reconciled": res.Reconciled, "linked": res.Linked,
		"disabled": len(res.Disabled), "kept": len(res.Kept), "unlinked": len(res.Unlinked), "conflicts": len(res.Conflicts)})
	return finish(nil)
}

// POST /api/admin/auth/dingtalk/sync：手动完整同步一次组织资料和角色映射；只有钉钉确认找不到的成员才按配置禁用。
func (s *Server) handleSyncDingTalk(w http.ResponseWriter, r *http.Request) {
	res, err := s.syncDingTalk(r.Context())
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.audit(r, auditDingTalkSync, "", map[string]any{"manual": true, "ok": res.OK, "remote": res.Remote, "created": res.Created, "reconciled": res.Reconciled,
		"linked": res.Linked, "disabled": len(res.Disabled), "conflicts": len(res.Conflicts)})
	writeJSON(w, http.StatusOK, res)
}

// GET /api/admin/auth/dingtalk/sync
func (s *Server) handleDingTalkSyncStatus(w http.ResponseWriter, r *http.Request) {
	var result *dingTalkSyncResult
	if _, err := s.store.GetSettingJSON(r.Context(), dingTalkSyncLastKey, &result); err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"last": result})
}

func (s *Server) runDingTalkSync(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.runLeased(ctx, leaseDingTalkSync, func(ctx context.Context) {
				cfg, _ := s.oidcByPreset(ctx, dingtalk.Preset)
				if cfg == nil || !cfg.DingTalkSync {
					return
				}
				result, err := s.syncDingTalk(ctx)
				if err != nil {
					s.log.Warn("钉钉组织同步失败", "err", err)
				} else if !result.OK {
					s.log.Warn("钉钉组织同步未完成", "err", result.Error)
				}
			})
		}
	}
}
