package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/dingtalk"
	"github.com/hikariming/dsh-ee/server/internal/feishu"
	"github.com/hikariming/dsh-ee/server/internal/oidc"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 身份关联：一个 dsh-ee 账号可以同时带一个飞书身份和一个钉钉身份，用哪边登录都进同一个账号。
//
// 规则（docs/identity-binding.md）：
//   - 账号的主身份还是 users.external_dn（建号时的那个）；资料、部门和角色映射只由主身份所在的来源管。
//   - 另一个身份记在 user_identities 里，只有两条路能进来：员工登录后自己绑定（explicit），
//     或者管理员开了「按核实过的联系方式自动关联」后，两边目录里的手机号 / 企业邮箱对得上（mobile / email）。
//   - 用来比对的联系方式只取服务端用应用凭据从企业通讯录读到的：手机号（两边都是登录号码）和企业分配的邮箱。
//     员工自己填的邮箱、显示名、用户名、登录时 IdP 给的声明都不参与，对不上或有歧义就不关联。
//   - 已经属于别的账号的身份不会被改挂，两个已有账号也不会被合并。

// identityProvider 从身份的前缀看它来自飞书还是钉钉；其它 IdP 的 sub 返回空。
func identityProvider(subject string) string {
	switch {
	case strings.HasPrefix(subject, "feishu:"):
		return "feishu"
	case strings.HasPrefix(subject, dingtalk.Preset+":"):
		return dingtalk.Preset
	}
	return ""
}

// identityContacts 是目录里核实过的联系方式的摘要；空表示没拿到。
type identityContacts struct{ mobile, email string }

func contactHash(kind, value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(kind + ":" + value))
	return hex.EncodeToString(sum[:])
}

func feishuContacts(u feishu.User) identityContacts {
	return identityContacts{mobile: contactHash("mobile", u.VerifiedMobile()), email: contactHash("email", u.EnterpriseEmail)}
}

func dingTalkContacts(u dingtalk.User) identityContacts {
	return identityContacts{mobile: contactHash("mobile", u.VerifiedMobile()), email: contactHash("email", u.OrgEmail)}
}

// identityAutoLink：管理员是否允许按核实过的联系方式自动关联（开关在第二登录入口上，默认关）。
func (s *Server) identityAutoLink(ctx context.Context) bool {
	cfg, ok := s.oidcSecondary(ctx)
	return ok && cfg.LinkByContact
}

// fetchContacts 登录时用应用凭据去通讯录读这个人的联系方式。读不到（没权限、没配通讯录地址）就是空，不影响登录。
func (s *Server) fetchContacts(ctx context.Context, cfg *oidc.Config, subject string) identityContacts {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	switch identityProvider(subject) {
	case "feishu":
		if cfg.Preset != "feishu" || cfg.FeishuAPIBase == "" || cfg.ClientSecret == "" {
			return identityContacts{}
		}
		u, err := feishu.NewClient(cfg.FeishuAPIBase, cfg.ClientID, cfg.ClientSecret, s.oidcHTTP().HTTP).Lookup(ctx, strings.TrimPrefix(subject, "feishu:"))
		if err != nil {
			s.log.Info("读取飞书成员联系方式失败，这次不做身份关联", "err", err)
			return identityContacts{}
		}
		return feishuContacts(*u)
	case dingtalk.Preset:
		userID, ok := strings.CutPrefix(subject, dingtalk.ExternalSubject(cfg.DingTalkCorpID, ""))
		if cfg.Preset != dingtalk.Preset || !ok || userID == "" {
			return identityContacts{}
		}
		u, err := s.dingTalkClient(cfg).Lookup(ctx, userID)
		if err != nil {
			s.log.Info("读取钉钉成员联系方式失败，这次不做身份关联", "err", err)
			return identityContacts{}
		}
		return dingTalkContacts(*u)
	}
	return identityContacts{}
}

// autoLinkIdentity 按核实过的联系方式，把一个还没见过的身份关联到另一个来源建的账号上。
// 只有恰好一个候选、候选是在用的员工统一登录账号、且两边没有互相矛盾的联系方式时才关联；
// 其它情况一律返回 nil，由调用方按普通流程处理（不合并）。
func (s *Server) autoLinkIdentity(ctx context.Context, provider, subject string, c identityContacts) (*store.User, error) {
	matches, err := s.store.MatchHomeIdentities(ctx, provider, c.mobile, c.email)
	if err != nil || len(matches) == 0 {
		return nil, err
	}
	via := matches[0].By
	for _, m := range matches {
		if m.UserID != matches[0].UserID {
			s.log.Warn("联系方式对上了不止一个账号，不自动关联身份", "provider", provider)
			return nil, nil
		}
		if m.By == store.IdentityMobile {
			via = store.IdentityMobile
		}
	}
	u, err := s.store.GetUser(ctx, matches[0].UserID)
	if err != nil {
		return nil, err
	}
	if u.Disabled || u.AccountType != store.AccountHuman || u.AuthSource != store.AuthOIDC || identityProvider(u.ExternalDN) == provider {
		return nil, nil
	}
	rows, err := s.store.ListUserIdentities(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.Provider == provider {
			return nil, nil
		}
		// 一项对上、另一项两边都有却不一样：证据互相矛盾，不关联
		if (c.mobile != "" && row.MobileHash != "" && c.mobile != row.MobileHash) || (c.email != "" && row.EmailHash != "" && c.email != row.EmailHash) {
			s.log.Warn("手机号和企业邮箱指向不一致，不自动关联身份", "user", u.Username, "provider", provider)
			return nil, nil
		}
	}
	if err := s.store.LinkIdentity(ctx, u.ID, provider, subject, via, c.mobile, c.email); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return nil, nil
		}
		return nil, err
	}
	s.auditSystem(ctx, auditIdentityLink, u.Username, map[string]any{"provider": provider, "via": via, "subject": subject})
	return u, nil
}

// linkedLogin 处理飞书 / 钉钉登录里「这个身份是某个账号的关联身份」的情况。
// 返回 nil, nil 表示不是，调用方继续走普通的建号 / 绑定流程。
func (s *Server) linkedLogin(ctx context.Context, cfg *oidc.Config, subject string) (*store.User, error) {
	provider := identityProvider(subject)
	// 只认从对应入口进来的身份：飞书入口只可能给出飞书身份，钉钉入口只可能给出钉钉身份
	if provider == "" || provider != cfg.Preset {
		return nil, nil
	}
	row, err := s.store.GetSecondaryIdentity(ctx, subject)
	if err == nil {
		if row.Via == store.IdentityUnlinked {
			return nil, errNotProvisioned
		}
		u, err := s.store.GetUser(ctx, row.UserID)
		if err != nil {
			return nil, err
		}
		if u.Disabled || u.AccountType != store.AccountHuman {
			return nil, errBadCredentials
		}
		return u, nil
	}
	if !notFound(err) {
		return nil, err
	}
	if !s.identityAutoLink(ctx) {
		return nil, nil
	}
	// 已经是某个账号的主身份：普通流程
	if _, err := s.store.GetUserByExternalDN(ctx, store.AuthOIDC, subject); err == nil || !notFound(err) {
		return nil, err
	}
	return s.autoLinkIdentity(ctx, provider, subject, s.fetchContacts(ctx, cfg, subject))
}

// recordLoginIdentity：两个入口都开着时，登录顺手记下主身份和核实过的联系方式，另一边才有东西可比对。
func (s *Server) recordLoginIdentity(ctx context.Context, cfg *oidc.Config, u *store.User) {
	provider := identityProvider(u.ExternalDN)
	if provider == "" || provider != cfg.Preset {
		return
	}
	if _, ok := s.oidcSecondary(ctx); !ok {
		return
	}
	c := s.fetchContacts(ctx, cfg, u.ExternalDN)
	if err := s.store.RecordHomeIdentity(ctx, u.ID, provider, u.ExternalDN, c.mobile, c.email); err != nil {
		s.log.Warn("记录登录身份失败", "user", u.Username, "err", err)
	}
}

// syncLinkedIdentity：组织同步里，这名成员是不是已经（或可以安全地）关联在另一个来源建的账号上。
func (s *Server) syncLinkedIdentity(ctx context.Context, provider, subject string, c identityContacts, auto bool) (linked, suppressed bool, err error) {
	row, err := s.store.GetSecondaryIdentity(ctx, subject)
	if err == nil {
		if row.Via == store.IdentityUnlinked {
			return false, true, nil
		}
		return true, false, s.store.TouchLinkedIdentity(ctx, row.ID, c.mobile, c.email)
	}
	if !notFound(err) {
		return false, false, err
	}
	if !auto {
		return false, false, nil
	}
	if _, err := s.store.GetUserByExternalDN(ctx, store.AuthOIDC, subject); err == nil || !notFound(err) {
		return false, false, err
	}
	u, err := s.autoLinkIdentity(ctx, provider, subject, c)
	return u != nil, false, err
}

// recordSyncIdentity 在组织同步建号 / 核对之后记下主身份和联系方式摘要。出错只记日志。
func (s *Server) recordSyncIdentity(ctx context.Context, provider string, u *store.User, c identityContacts) {
	if u == nil || identityProvider(u.ExternalDN) != provider {
		return
	}
	if err := s.store.RecordHomeIdentity(ctx, u.ID, provider, u.ExternalDN, c.mobile, c.email); err != nil {
		s.log.Warn("记录同步身份失败", "user", u.Username, "err", err)
	}
}

// pruneLinkedIdentities：来源明确说人已经不在了的关联身份解除关联，返回受影响的用户名。
// 账号本身不禁用——它归主身份所在的来源管，那边的同步会按同样的规则处理。
func (s *Server) pruneLinkedIdentities(ctx context.Context, provider, prefix string, seen map[string]bool, gone func(subject string) (bool, error)) ([]string, error) {
	rows, err := s.store.ListLinkedIdentities(ctx, provider)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, row := range rows {
		if !strings.HasPrefix(row.Subject, prefix) || seen[row.Subject] {
			continue
		}
		if left, err := gone(row.Subject); err != nil {
			return out, err
		} else if !left {
			continue
		}
		if deleted, err := s.store.DeleteLinkedIdentity(ctx, row.ID); err != nil {
			return out, err
		} else if !deleted {
			continue
		}
		name := ""
		if u, err := s.store.GetUser(ctx, row.UserID); err == nil {
			name = u.Username
		}
		out = append(out, name)
		s.auditSystem(ctx, auditIdentityUnlink, name, map[string]any{"provider": provider, "reason": provider + "_missing"})
	}
	return out, nil
}

// ---- 员工自己绑定 / 解除 ----

// sessionUser 读浏览器里的完整会话；没有、过期或受限会话返回 nil。只给公开路由里需要认人的地方用。
func (s *Server) sessionUser(r *http.Request) *store.User {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	u, limited, err := s.store.SessionInfo(r.Context(), secret.Hash(c.Value))
	if err != nil || limited || u.Disabled {
		return nil
	}
	return u
}

// bindError 回到个人账号页并带上绑定失败的原因。
func bindError(w http.ResponseWriter, code, msg string) {
	q := url.Values{"identity_error": {msg}, "identity_error_code": {code}}
	w.Header().Set("Location", "/account?"+q.Encode())
	writeErrCode(w, http.StatusFound, code, nil, msg)
}

// finishIdentityBind 是绑定流程的回调：员工已经在飞书 / 钉钉完成授权，把这个身份关联到发起绑定的账号上。
func (s *Server) finishIdentityBind(w http.ResponseWriter, r *http.Request, cfg *oidc.Config, id oidc.Identity, bindUser int64) {
	ctx := r.Context()
	provider := identityProvider(id.Subject)
	u := s.sessionUser(r)
	switch {
	case u == nil || u.ID != bindUser:
		bindError(w, codeIdentitySessionChanged, "登录状态变了，请重新登录后再绑定")
		return
	case provider == "" || provider != cfg.Preset:
		bindError(w, codeIdentityProviderInvalid, "只能绑定飞书或钉钉身份")
		return
	case u.AccountType != store.AccountHuman || u.AuthSource != store.AuthOIDC || u.ExternalDN == "":
		bindError(w, codeIdentityAccountIneligible, "只有通过企业统一登录建立的员工账号可以绑定飞书 / 钉钉身份")
		return
	case u.ExternalDN == id.Subject:
		http.Redirect(w, r, "/account?identity_bound="+provider, http.StatusFound)
		return
	case identityProvider(u.ExternalDN) == provider:
		bindError(w, codeIdentityHomeProvider, "这个账号本来就是用这个入口登录的，不能再绑定另一个同类身份")
		return
	}
	fail := func(reason, code, msg string) {
		s.auditAs(r, u, auditLoginFailed, u.Username, map[string]any{"via": "identity_bind", "provider": provider, "reason": reason})
		bindError(w, code, msg)
	}
	// 这个身份已经是别的账号的登录身份：不改挂、不合并
	if other, err := s.store.GetUserByExternalDN(ctx, store.AuthOIDC, id.Subject); err == nil && other != nil {
		fail("subject_is_home_of_another_account", codeIdentityHomeElsewhere, "这个身份已经是另一个 dsh-ee 账号的登录身份，不能绑定到当前账号；需要合并请联系管理员")
		return
	} else if err != nil && !notFound(err) {
		s.internalErr(w, r, err)
		return
	}
	if row, err := s.store.GetSecondaryIdentity(ctx, id.Subject); err == nil {
		if row.UserID != u.ID {
			fail("subject_linked_to_another_account", codeIdentityLinkedElsewhere, "这个身份属于另一个 dsh-ee 账号。若仍有绑定，请先在原账号解除；否则请联系管理员。")
			return
		}
		if row.Via != store.IdentityUnlinked {
			http.Redirect(w, r, "/account?identity_bound="+provider, http.StatusFound)
			return
		}
	} else if !notFound(err) {
		s.internalErr(w, r, err)
		return
	}
	c := s.fetchContacts(ctx, cfg, id.Subject)
	if err := s.store.LinkIdentity(ctx, u.ID, provider, id.Subject, store.IdentityExplicit, c.mobile, c.email); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			fail("account_already_linked", codeIdentityAlreadyLinked, "当前账号已经绑定过这个入口的身份；要换一个请先解除")
			return
		}
		s.internalErr(w, r, err)
		return
	}
	s.auditAs(r, u, auditIdentityLink, u.Username, map[string]any{"provider": provider, "via": store.IdentityExplicit, "subject": id.Subject})
	http.Redirect(w, r, "/account?identity_bound="+provider, http.StatusFound)
}

type identityView struct {
	Provider string `json:"provider"`
	// 登录页按钮上的名字
	Name  string `json:"name"`
	Bound bool   `json:"bound"`
	// home：账号的主身份；explicit / mobile / email：关联身份的来历
	Via       string `json:"via,omitempty"`
	CanBind   bool   `json:"canBind"`
	CanUnlink bool   `json:"canUnlink"`
}

// GET /api/me/identities：当前账号的飞书 / 钉钉身份。只有两个入口都开着时才有内容。
func (s *Server) handleMyIdentities(w http.ResponseWriter, r *http.Request) {
	out := []identityView{}
	secondary, ok := s.oidcSecondary(r.Context())
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"identities": out})
		return
	}
	primary, _ := s.oidcEnabled(r.Context())
	u := currentUser(r)
	rows, err := s.store.ListUserIdentities(r.Context(), u.ID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	eligible := u.AccountType == store.AccountHuman && u.AuthSource == store.AuthOIDC && u.ExternalDN != ""
	for _, cfg := range []*oidc.Config{primary, secondary} {
		v := identityView{Provider: cfg.Preset, Name: cfg.Name}
		if identityProvider(u.ExternalDN) == cfg.Preset {
			v.Bound, v.Via = true, store.IdentityHome
		}
		for _, row := range rows {
			if row.Provider == cfg.Preset && row.Via != store.IdentityHome && !v.Bound {
				v.Bound, v.Via, v.CanUnlink = true, row.Via, true
			}
		}
		v.CanBind = eligible && !v.Bound
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"identities": out})
}

// DELETE /api/me/identities/{provider}：解除自己的关联身份。主身份不能在这里解除（那是管理员的「解除身份绑定」）。
func (s *Server) handleUnlinkMyIdentity(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	u := currentUser(r)
	removed, err := s.store.SuppressLinkedIdentity(r.Context(), u.ID, provider)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if !removed {
		writeErrCode(w, http.StatusNotFound, codeIdentityUnlinkMissing, nil, "没有可以解除的关联身份")
		return
	}
	s.audit(r, auditIdentityUnlink, u.Username, map[string]any{"provider": provider, "reason": "self"})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
