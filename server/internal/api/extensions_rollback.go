package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/extension"
	"github.com/hikariming/dsh-ee/server/internal/mcp"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 扩展升级回滚（docs/plugins.md §9.4）：装过的每一版扩展都留在 asset_versions 里，
// 版本内容就是当时的快照——清单原文、接口文档、摘要、管理员参数、服务扩展地址和配置密文引用。
// 凭据仍按现有方式引用（assets.secret_enc、命名共享凭据、config 里的 $enc 密文），
// 快照里没有明文凭据，所以不需要为回滚另建一张历史表。
//
// 回滚 = 把历史版本的快照当成一次「重新安装」：复用安装前的检查和差异计算，按当前信任根重新验签，
// 先按目标快照把工具取回来（失败时还没写库），再追加一个新版本并替换工具清单。
// 旧版本不会被改写，回滚本身也可以再回滚。

const (
	// 本次改动之前装的版本没有存签名材料；回滚只能提醒，不能复核签名状态。
	rollbackLegacySignature = "这个历史版本安装时没有保存签名文件，回滚后无法复核签名状态"
)

// extRollbackVersion 是历史里的一个扩展版本，给回滚页面和计划用。
type extRollbackVersion struct {
	// AssetVersion 是连接的版本号（asset_versions.version），回滚时用它指定目标
	AssetVersion int `json:"asset_version"`
	// Extension 是扩展身份 作者/名字@版本
	Extension string `json:"extension"`
	Version   string `json:"version"`
	Digest    string `json:"digest"`
	// declarative / service
	Kind string `json:"kind"`
	// 安装时的签名状态：valid / unsigned / invalid / revoked / unknown（旧版本没存签名材料）
	Signature    string    `json:"signature"`
	SignatureKey string    `json:"signature_key,omitempty"`
	InstalledBy  string    `json:"installed_by"`
	InstalledAt  time.Time `json:"installed_at"`
	Current      bool      `json:"current"`
	// 当前策略下能不能回滚到这个版本（强制验签时，没存签名材料的旧版本不行）
	CanRollback bool `json:"can_rollback"`
}

// extRollbackPlan 是回滚前给管理员看的计划：从哪一版回到哪一版、会变什么、要不要确认。
type extRollbackPlan struct {
	Asset *store.Asset        `json:"asset"`
	From  *extRollbackVersion `json:"from"`
	To    *extRollbackVersion `json:"to"`
	// 工具、只读和地址的变化，和安装计划同一个口径
	Changes   []string `json:"changes"`
	Sensitive []string `json:"sensitive"`
	Warnings  []string `json:"warnings"`
	// 目标快照的检查结果（apps / icon 这类只有完整包才有的文件不参与）
	Check *extension.Result `json:"check"`
	// 目标快照的签名状态；旧版本没存签名材料时为空
	Signature *extension.SignatureStatus `json:"signature,omitempty"`
}

type extRollbackReq struct {
	// 目标连接版本号；不填表示上一版（最近一个比线上版本旧的扩展版本）
	Version int  `json:"version"`
	Confirm bool `json:"confirm"`
}

// extensionKind 把连接里存的扩展种类算出来：声明式扩展的 Kind 是空串。
func extensionKind(e *connExtension) string {
	if e == nil {
		return ""
	}
	if e.Kind == "" {
		return extension.KindDeclarative
	}
	return e.Kind
}

// extensionTarget 按连接标识（slug）或扩展 id（作者/名字）找到装出来的连接和线上版本。
// msg 是给管理员看的 404 文案；err 是内部错误。
func (s *Server) extensionTarget(ctx context.Context, target string) (*store.Asset, *store.AssetVersion, *assetSpec, string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, nil, nil, "缺少连接标识或扩展 id", nil
	}
	var a *store.Asset
	if strings.Contains(target, "/") {
		// 扩展 id 里有 /，路由段会先 URL 编码（%2F）；PathValue 已经解出一整段
		found, _, err := s.findExtension(ctx, target)
		if err != nil {
			return nil, nil, nil, "", err
		}
		if found == nil {
			return nil, nil, nil, "找不到扩展 " + target + " 装出来的连接", nil
		}
		a = found
	} else {
		found, err := s.store.GetAssetBySlug(ctx, target)
		if notFound(err) {
			return nil, nil, nil, "找不到连接 " + target, nil
		}
		if err != nil {
			return nil, nil, nil, "", err
		}
		if found.Kind != store.KindConnection {
			return nil, nil, nil, "这不是一个企业连接", nil
		}
		a = found
	}
	lv, err := s.store.LiveAssetVersion(ctx, a.ID)
	if notFound(err) {
		return nil, nil, nil, "这个连接还没有生效的版本", nil
	}
	if err != nil {
		return nil, nil, nil, "", err
	}
	sp := parseSpec(lv.Spec)
	if sp.Extension == nil {
		return nil, nil, nil, "这个连接不是扩展装出来的，没有安装历史", nil
	}
	return a, lv, sp, "", nil
}

// extensionVersions 列出这个连接上同一个扩展的已上架版本（从新到旧）。
// 只认和线上版本同种类（declarative / service）的版本：种类变了不是同一个扩展的升级线。
func (s *Server) extensionVersions(ctx context.Context, assetID int64, extID, kind string) ([]*store.AssetVersion, []*assetSpec, error) {
	all, err := s.store.ListAssetVersions(ctx, assetID)
	if err != nil {
		return nil, nil, err
	}
	versions := make([]*store.AssetVersion, 0, len(all))
	specs := make([]*assetSpec, 0, len(all))
	for _, v := range all {
		if v.Status != store.StatusPublished {
			continue
		}
		sp := parseSpec(v.Spec)
		if sp.Extension == nil || sp.Extension.ID != extID || extensionKind(sp.Extension) != kind {
			continue
		}
		versions = append(versions, v)
		specs = append(specs, sp)
	}
	return versions, specs, nil
}

// extensionVersionMeta 把版本内容转成给管理员看的一行历史。
func extensionVersionMeta(v *store.AssetVersion, sp *assetSpec, users userIndex) *extRollbackVersion {
	e := sp.Extension
	sig := e.SignatureStatus
	if sig == "" {
		sig = "unknown"
	}
	return &extRollbackVersion{
		AssetVersion: v.Version,
		Extension:    e.ID + "@" + e.Version,
		Version:      e.Version,
		Digest:       e.Digest,
		Kind:         extensionKind(e),
		Signature:    sig,
		SignatureKey: signatureKeyOf(sig),
		InstalledBy:  users.name(v.CreatedBy),
		InstalledAt:  v.CreatedAt,
	}
}

// signatureKeyOf 从 audit 口径的签名状态（valid:abc123）里取 key id。
func signatureKeyOf(status string) string {
	_, key, ok := strings.Cut(status, ":")
	if !ok {
		return ""
	}
	return key
}

// extensionSnapshotFiles 把历史版本的内容还原成安装时的文件集合，交给现有检查逻辑。
// 声明式扩展是清单 + 接口文档（+ 签名原文）；服务扩展只有清单，地址另外传。
func extensionSnapshotFiles(sp *assetSpec) (map[string][]byte, string, error) {
	e := sp.Extension
	if e == nil {
		return nil, "", errors.New("版本内容里没有扩展信息")
	}
	files := map[string][]byte{extension.ManifestFile: []byte(e.Manifest)}
	if e.service() {
		return files, e.ServiceURL, nil
	}
	m, err := e.manifest()
	if err != nil {
		return nil, "", err
	}
	if m.OpenAPI != "" {
		files[m.OpenAPI] = []byte(sp.OpenAPI)
	}
	if e.Signature != "" {
		files[extension.SignatureFile] = []byte(e.Signature)
	}
	return files, "", nil
}

// filterSnapshotCheck 去掉只有完整安装包才有的文件的检查结果（apps / icon 没存快照），
// 其余按原来的口径算成败。applyExtension 在同步工具时也是这么处理的。
func filterSnapshotCheck(res *extension.Result) *extension.Result {
	if res == nil {
		return nil
	}
	out := *res
	out.Results = make([]extension.Diagnostic, 0, len(res.Results))
	out.Errors, out.Warnings = 0, 0
	for _, d := range res.Results {
		if strings.HasPrefix(d.Code, "apps.") || strings.HasPrefix(d.Code, "icon.") {
			continue
		}
		out.Results = append(out.Results, d)
		switch d.Severity {
		case extension.SevError:
			out.Errors++
		case extension.SevWarning:
			out.Warnings++
		}
	}
	out.OK = out.Errors == 0
	return &out
}

// firstExtensionError 取检查结果里第一条错误，给 400 的 error 文案用。
func firstExtensionError(res *extension.Result) string {
	if res == nil {
		return ""
	}
	for _, d := range res.Results {
		if d.Severity == extension.SevError {
			if d.Where != "" {
				return d.Where + "：" + d.Message
			}
			return d.Message
		}
	}
	return ""
}

// extensionSnapshotTools 按给定的版本内容重新取一次工具清单（回滚写库之前先做）。
// 服务扩展要连它的 /mcp，连不上时这一步失败，当前版本还没动。
func (s *Server) extensionSnapshotTools(ctx context.Context, a *store.Asset, sp *assetSpec, v viewer, base string) ([]*store.ConnectionTool, error) {
	var client *mcp.Client
	if sp.Extension.service() {
		c, err := s.serviceClient(ctx, a, sp, base)
		if err != nil {
			return nil, err
		}
		client = c
	}
	ctx, cancel := context.WithTimeout(withCaller(ctx, v, base), 60*time.Second)
	defer cancel()
	tools, _, err := s.discoverTools(ctx, sp, client)
	if err != nil {
		return nil, err
	}
	return tools, nil
}

// abortRollbackVersion 撤销一次没完成的回滚：删掉刚追加的版本、把展示信息恢复回去。
// 工具清单是最后一步才替换的，这里失败时它还没动。
func (s *Server) abortRollbackVersion(ctx context.Context, a *store.Asset, newVer int, oldSP *assetSpec) {
	if err := s.store.DeleteAssetVersion(ctx, a.ID, newVer); err != nil {
		s.log.Error("回滚失败后撤销新版本失败", "asset", a.ID, "version", newVer, "err", err)
	}
	if err := s.store.UpdateAsset(ctx, a.ID, store.AssetPatch{Title: &oldSP.Title, Description: &oldSP.Description}); err != nil {
		s.log.Error("回滚失败后恢复连接标题失败", "asset", a.ID, "err", err)
	}
	s.dropConnClient(a.ID)
	s.hub.notify()
}

// GET /api/admin/extensions/{slug-or-id}/history：扩展的安装历史（已上架版本，从新到旧）。
func (s *Server) handleExtHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a, lv, liveSP, msg, err := s.extensionTarget(ctx, r.PathValue("target"))
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if msg != "" {
		writeErr(w, http.StatusNotFound, msg)
		return
	}
	live := liveSP.Extension
	versions, specs, err := s.extensionVersions(ctx, a.ID, live.ID, extensionKind(live))
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	users, err := s.userIndex(r)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	// 用当前信任根和签名策略把每一版重新核对一遍：历史条目上的签名状态要反映现在的信任状态
	trusted, err := s.trustedExtensionKeys(ctx)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	out := make([]*extRollbackVersion, len(versions))
	for i := range versions {
		item := extensionVersionMeta(versions[i], specs[i], users)
		item.Current = item.AssetVersion == lv.Version
		// 强制验签时，没存签名材料的旧版本没法在回滚时复核，不能选
		item.CanRollback = !item.Current && !(s.opts.ExtensionRequireSignature && specs[i].Extension.Signature == "")
		if files, _, ferr := extensionSnapshotFiles(specs[i]); ferr == nil {
			sig := extension.VerifyFilesWithKeys(files, trusted)
			if specs[i].Extension.SignatureStatus == "" && !specs[i].Extension.service() {
				// 本次改动之前装的声明式扩展没存 signature.json，只能显示「未知」，不能冒充 unsigned
				item.Signature, item.SignatureKey = "unknown", ""
			} else if sig != nil {
				item.Signature, item.SignatureKey = sig.Status, sig.KeyID
			}
			item.CanRollback = !item.Current && signatureProblem(s.opts.ExtensionRequireSignature, sig) == nil
		}
		out[i] = item
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"asset":           a,
		"extension_id":    live.ID,
		"current_version": lv.Version,
		"versions":        out,
	})
}

// POST /api/admin/extensions/{slug-or-id}/rollback：回到上一版或指定的历史版本。
// 复用安装的检查（清单 / 接口文档 / 签名 / 权限策略）和差异计算；必须 confirm。
// 先按目标快照取工具，取不到时当前版本不动；写库之后如果失败会撤销刚追加的版本。
func (s *Server) handleExtRollback(w http.ResponseWriter, r *http.Request) {
	var req extRollbackReq
	if !readJSONLimit(w, r, &req, 64<<10) {
		return
	}
	ctx := r.Context()
	a, lv, liveSP, msg, err := s.extensionTarget(ctx, r.PathValue("target"))
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if msg != "" {
		writeErr(w, http.StatusNotFound, msg)
		return
	}
	live := liveSP.Extension
	versions, specs, err := s.extensionVersions(ctx, a.ID, live.ID, extensionKind(live))
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	// 目标版本：指定的连接版本号，或最近一个比线上旧的版本
	idx := -1
	if req.Version > 0 {
		for i := range versions {
			if versions[i].Version == req.Version {
				idx = i
				break
			}
		}
		if idx < 0 {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("版本 %d 不是这个扩展可回滚的历史版本", req.Version))
			return
		}
		if versions[idx].Version == lv.Version {
			writeErr(w, http.StatusConflict, "当前已经是这个版本，不用回滚")
			return
		}
	} else {
		for i := range versions {
			if versions[i].Version < lv.Version {
				idx = i
				break
			}
		}
		if idx < 0 {
			writeErr(w, http.StatusBadRequest, "没有可回滚的历史版本：这个扩展只装过当前这一版")
			return
		}
	}
	targetV, targetSP := versions[idx], specs[idx]

	// 历史快照必须仍是一份合法的连接内容：清单身份、摘要、凭据引用都不能变
	snap := *targetSP
	if msg := validateSpec(store.KindConnection, &snap); msg != "" {
		writeErr(w, http.StatusBadRequest, "历史版本的快照不能直接回滚："+msg)
		return
	}
	if msg, err := s.checkSharedName(ctx, a.ID, &snap); err != nil {
		s.internalErr(w, r, err)
		return
	} else if msg != "" {
		writeErr(w, http.StatusBadRequest, "历史版本不能回滚："+msg)
		return
	}
	// 服务扩展的配置、OAuth 客户端的密文引用要还能解开（主密钥换过就会失败）
	if targetSP.Extension.service() {
		if _, err := s.openExtConfig(targetSP.Extension); err != nil {
			writeErr(w, http.StatusBadRequest, "历史版本的服务扩展配置解不开："+err.Error())
			return
		}
	}
	if c := targetSP.Extension.OAuthClient; c != nil && c.SecretEnc != "" {
		if _, err := s.oauthClientSecret(c); err != nil {
			writeErr(w, http.StatusBadRequest, "历史版本的 OAuth 客户端密钥解不开："+err.Error())
			return
		}
	}
	// 复用安装计划：同样的清单 / 接口文档检查、签名策略、版本兼容和升级差异
	files, svc, err := extensionSnapshotFiles(targetSP)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "历史版本的清单读不了："+err.Error())
		return
	}
	p, err := s.planExtension(ctx, files, svc)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	check := filterSnapshotCheck(p.Check)
	users, err := s.userIndex(r)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	from := extensionVersionMeta(lv, liveSP, users)
	from.Current = true
	to := extensionVersionMeta(targetV, targetSP, users)
	warnings := []string{}
	if targetSP.Extension.SignatureStatus == "" && !targetSP.Extension.service() {
		// 本次改动之前的快照没存 signature.json：默认策略下可以回滚但没法复核签名，强制验签不放行
		if s.opts.ExtensionRequireSignature {
			writeErr(w, http.StatusBadRequest, "这个历史版本没有保存签名文件，dsh-ee 配成了必须验证签名，不能回滚到它；"+
				"请重新安装目标版本，让 dsh-ee 重新验签")
			return
		}
		warnings = append(warnings, rollbackLegacySignature)
	}
	// 和安装时一样的必填参数检查：管理员设置的参数不能缺
	for _, ap := range p.AdminParams {
		if _, ok := targetSP.Extension.Admin[ap.Key]; !ok && len(ap.Default) == 0 {
			writeErr(w, http.StatusBadRequest, "历史版本缺少管理员参数「"+ap.Label+"」，不能回滚；请重新安装目标版本")
			return
		}
	}
	plan := &extRollbackPlan{Asset: a, From: from, To: to, Changes: p.Changes, Sensitive: p.Sensitive, Warnings: warnings, Check: check}
	if targetSP.Extension.SignatureStatus != "" {
		plan.Signature = p.Signature
	}
	if !check.OK {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "历史版本的快照检查没通过，不能回滚：" + firstExtensionError(check),
			"plan":  plan,
		})
		return
	}
	if !req.Confirm {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "回滚会把线上版本从 " + from.Extension + " 换成 " + to.Extension + "，需要确认；确认后按这份差异重建工具",
			"plan":  plan,
		})
		return
	}

	// 先按目标快照取工具：声明式扩展在本地解析，服务扩展要连它的 /mcp。
	// 这一步失败时什么都还没写，线上版本和工具都不受影响。
	v := viewerOf(r)
	base := s.callerBase(r)
	tools, err := s.extensionSnapshotTools(ctx, a, targetSP, v, base)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": "回滚没有执行：按历史版本重新同步工具失败，当前版本没有改动（" + err.Error() + "）",
			"from":  from,
			"to":    to,
		})
		return
	}
	newVer, err := s.store.AddAssetVersion(ctx, a.ID, targetV.Spec, store.StatusPublished, v.user.ID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if err := s.store.UpdateAsset(ctx, a.ID, store.AssetPatch{Title: &snap.Title, Description: &snap.Description}); err != nil {
		s.abortRollbackVersion(ctx, a, newVer, liveSP)
		s.internalErr(w, r, err)
		return
	}
	s.dropConnClient(a.ID)
	if err := s.store.ReplaceConnectionTools(ctx, a.ID, tools); err != nil {
		s.abortRollbackVersion(ctx, a, newVer, liveSP)
		writeErr(w, http.StatusInternalServerError, "回滚没有完成，已恢复到回滚前的版本："+err.Error())
		return
	}
	s.hub.notify()
	a.Title, a.Description, a.LiveVersion = snap.Title, snap.Description, newVer
	resp := map[string]any{
		"asset":            a,
		"version":          newVer,
		"rolled_back_to":   to,
		"rolled_back_from": from,
		"tools":            len(tools),
		"warnings":         warnings,
	}
	s.audit(r, auditExtRollback, "connection:"+a.Slug, map[string]any{
		"id": a.ID, "from": from.Extension + "#" + from.Digest, "to": to.Extension + "#" + to.Digest,
		"asset_version": newVer, "target_version": targetV.Version,
		"confirmed": p.Sensitive, "signature": signatureAudit(p.Signature),
	})
	writeJSON(w, http.StatusOK, resp)
}
