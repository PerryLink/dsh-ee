package api

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/extension"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 扩展签名的信任根（docs/plugins.md §9.3、docs/plugin-signing.md §3、#134）：
// 管理员把受信任的签名公钥加进库，安装 / 检查计划验签时和 DSH_EE_EXTENSION_TRUSTED_KEYS
// 里的公钥合并；换下、撤销都保留记录，历史签名还能核对，撤销之后的新包不能装。

const (
	auditSigningKeyAdd    = "signing_key.add"
	auditSigningKeyRetire = "signing_key.retire"
	auditSigningKeyRevoke = "signing_key.revoke"
)

const (
	// 扩展签名（作者对清单/接口文档签名）用的公钥
	signingKeyPurposeExtension = "extension"
	// 企业目录整体签名用的公钥；目录验签时也接受 extension 用途的钥匙（复用 #134 的信任根）
	signingKeyPurposeDirectory = "directory"
)

// 管理 API 支持的用途。新增用途时同步这里和目录/验签的取用逻辑。
var signingKeyPurposes = []string{signingKeyPurposeExtension, signingKeyPurposeDirectory}

// signingKeyPending 是还没到 valid_from 的公钥：记录在，但验签时先不用。
const signingKeyPending = "pending"

var signingKeyIDRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

// signingKeyState 按记录里的时间算状态：撤销 > 换下 > 未生效 > 正在使用。
func signingKeyState(k store.TrustedSigningKey, now time.Time) string {
	switch {
	case k.RevokedAt != nil:
		return extension.KeyStateRevoked
	case k.RetiredAt != nil:
		return extension.KeyStateRetired
	case now.Before(k.ValidFrom):
		return signingKeyPending
	}
	return extension.KeyStateActive
}

// trustedExtensionKeys 合并库里的信任根和环境变量里的公钥，给扩展验签用。
func (s *Server) trustedExtensionKeys(ctx context.Context) ([]extension.TrustedKey, error) {
	rows, err := s.store.ListTrustedSigningKeys(ctx)
	if err != nil {
		return nil, err
	}
	return mergeTrustedSigningKeys(rows, s.opts.ExtensionTrustedKeys, time.Now().UTC(), signingKeyPurposeExtension), nil
}

// trustedDirectoryKeys 合并库里的信任根和环境变量里的公钥，给企业目录验签用。
// 目录签名复用 #134 的信任根：purpose=directory 的钥匙优先，purpose=extension 的也接受
// （企业只有一套签名钥匙时不用再加一遍）；env 里的公钥同样作为只读兼容来源。
func (s *Server) trustedDirectoryKeys(ctx context.Context) ([]extension.TrustedKey, error) {
	rows, err := s.store.ListTrustedSigningKeys(ctx)
	if err != nil {
		return nil, err
	}
	return mergeTrustedSigningKeys(rows, s.opts.ExtensionTrustedKeys, time.Now().UTC(), signingKeyPurposeExtension, signingKeyPurposeDirectory), nil
}

// mergeTrustedSigningKeys 合并两份信任来源，只取列出的用途。
// 环境变量是只读的兼容来源；同一个 key id 以库里的状态为准——不然撤销一把同时写在 env 里的
// 公钥不会生效。库里的记录坏掉（不是 PEM、key id 对不上）时跳过它，不影响其它公钥；
// 还没到 valid_from 的钥匙先不用。
func mergeTrustedSigningKeys(rows []store.TrustedSigningKey, env []ed25519.PublicKey, now time.Time, purposes ...string) []extension.TrustedKey {
	allowed := map[string]bool{}
	for _, p := range purposes {
		allowed[p] = true
	}
	out := make([]extension.TrustedKey, 0, len(rows)+len(env))
	seen := map[string]bool{}
	for _, r := range rows {
		if r.Purpose != "" && !allowed[r.Purpose] {
			continue
		}
		state := signingKeyState(r, now)
		if state == signingKeyPending {
			continue
		}
		pub, err := extension.ParsePublicKeyPEM([]byte(r.PublicKey))
		if err != nil {
			continue
		}
		// 库里记的 key id 要和公钥算出来的一致，防止手工改库把一把公钥挂到别的 id 上
		id := extension.KeyID(pub)
		if id != r.KeyID || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, extension.TrustedKey{Public: pub, State: state})
	}
	for _, pub := range env {
		id := extension.KeyID(pub)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, extension.TrustedKey{Public: pub, State: extension.KeyStateActive})
	}
	return out
}

// signingKeyView 是给管理员看的一条信任根：记录本身 + 按时间算出来的状态。
type signingKeyView struct {
	store.TrustedSigningKey
	// active / retired / revoked / pending
	State string `json:"state"`
}

// GET /api/admin/signing-keys：信任根列表，包括换下和撤销的历史记录。
func (s *Server) handleListSigningKeys(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.ListTrustedSigningKeys(r.Context())
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	now := time.Now().UTC()
	views := make([]signingKeyView, 0, len(rows))
	counts := map[string]int{extension.KeyStateActive: 0, extension.KeyStateRetired: 0, extension.KeyStateRevoked: 0, signingKeyPending: 0}
	for _, k := range rows {
		state := signingKeyState(k, now)
		counts[state]++
		views = append(views, signingKeyView{TrustedSigningKey: k, State: state})
	}
	env := make([]map[string]string, 0, len(s.opts.ExtensionTrustedKeys))
	for _, pub := range s.opts.ExtensionTrustedKeys {
		env = append(env, map[string]string{"key_id": extension.KeyID(pub), "source": "env"})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"keys":              views,
		"count":             len(views),
		"states":            counts,
		"env_keys":          env,
		"require_signature": s.opts.ExtensionRequireSignature,
		"purposes":          signingKeyPurposes,
	})
}

type signingKeyReq struct {
	// PEM：PUBLIC KEY 或 CERTIFICATE，只收一把
	PublicKey string `json:"public_key"`
	Purpose   string `json:"purpose"`
	Note      string `json:"note"`
}

// POST /api/admin/signing-keys：加一把受信任的签名公钥。私钥不收。
func (s *Server) handleAddSigningKey(w http.ResponseWriter, r *http.Request) {
	var req signingKeyReq
	if !readJSON(w, r, &req) {
		return
	}
	pemText := strings.TrimSpace(req.PublicKey)
	if pemText == "" {
		writeErr(w, http.StatusBadRequest, "public_key 必填：贴公钥文件的 PEM（PUBLIC KEY 或 CERTIFICATE），不要贴私钥")
		return
	}
	if len(pemText) > 64<<10 {
		writeErr(w, http.StatusBadRequest, "公钥内容太长")
		return
	}
	pub, err := extension.ParsePublicKeyPEM([]byte(pemText))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "公钥不对："+err.Error())
		return
	}
	purpose := strings.TrimSpace(req.Purpose)
	if purpose == "" {
		purpose = signingKeyPurposeExtension
	}
	if !slices.Contains(signingKeyPurposes, purpose) {
		writeErr(w, http.StatusBadRequest, "purpose 只能是 extension（扩展签名）或 directory（企业目录签名）")
		return
	}
	note := strings.TrimSpace(req.Note)
	if len(note) > 255 {
		writeErr(w, http.StatusBadRequest, "备注最多 255 个字符")
		return
	}
	keyID := extension.KeyID(pub)
	user := currentUser(r)
	k := &store.TrustedSigningKey{
		KeyID:     keyID,
		PublicKey: pemText,
		Purpose:   purpose,
		ValidFrom: time.Now().UTC(),
		Note:      note,
	}
	if user != nil {
		id := user.ID
		k.CreatedBy = &id
	}
	if err := s.store.CreateTrustedSigningKey(r.Context(), k); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			writeErr(w, http.StatusConflict, "这把公钥已经加过了（key id "+keyID+"）；撤销过的公钥不能重新加，要恢复信任请用新钥匙")
			return
		}
		s.internalErr(w, r, err)
		return
	}
	s.audit(r, auditSigningKeyAdd, "signing-key:"+keyID, map[string]any{"key_id": keyID, "purpose": purpose})
	writeJSON(w, http.StatusCreated, signingKeyView{TrustedSigningKey: *k, State: extension.KeyStateActive})
}

// loadSigningKey 取一把信任根；key id 不合法或不存在时写好响应并返回 false。
func (s *Server) loadSigningKey(w http.ResponseWriter, r *http.Request) (*store.TrustedSigningKey, bool) {
	keyID := strings.TrimSpace(r.PathValue("key_id"))
	if !signingKeyIDRe.MatchString(keyID) {
		writeErr(w, http.StatusBadRequest, "key id 不合法：应是 16 位十六进制")
		return nil, false
	}
	k, err := s.store.TrustedSigningKey(r.Context(), keyID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "没有这把签名公钥")
			return nil, false
		}
		s.internalErr(w, r, err)
		return nil, false
	}
	return k, true
}

// POST /api/admin/signing-keys/{key_id}/retire：换下一把公钥（历史签名仍认，不再用于新签名）。
func (s *Server) handleRetireSigningKey(w http.ResponseWriter, r *http.Request) {
	k, ok := s.loadSigningKey(w, r)
	if !ok {
		return
	}
	if k.RevokedAt != nil {
		writeErr(w, http.StatusConflict, "这把公钥已经撤销，不能再换下")
		return
	}
	changed, err := s.store.RetireTrustedSigningKey(r.Context(), k.KeyID, time.Now().UTC())
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if changed {
		s.audit(r, auditSigningKeyRetire, "signing-key:"+k.KeyID, map[string]any{"key_id": k.KeyID})
	}
	k, err = s.store.TrustedSigningKey(r.Context(), k.KeyID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, signingKeyView{TrustedSigningKey: *k, State: signingKeyState(*k, time.Now().UTC())})
}

// POST /api/admin/signing-keys/{key_id}/revoke：撤销一把公钥（历史签名显示 revoked，新装 / 升级拒绝）。
func (s *Server) handleRevokeSigningKey(w http.ResponseWriter, r *http.Request) {
	k, ok := s.loadSigningKey(w, r)
	if !ok {
		return
	}
	changed, err := s.store.RevokeTrustedSigningKey(r.Context(), k.KeyID, time.Now().UTC())
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if changed {
		s.audit(r, auditSigningKeyRevoke, "signing-key:"+k.KeyID, map[string]any{"key_id": k.KeyID, "retired": k.RetiredAt != nil})
	}
	k, err = s.store.TrustedSigningKey(r.Context(), k.KeyID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, signingKeyView{TrustedSigningKey: *k, State: signingKeyState(*k, time.Now().UTC())})
}
