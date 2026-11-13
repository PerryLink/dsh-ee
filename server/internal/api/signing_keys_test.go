package api_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/extension"
	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 扩展签名的信任根落库（#134、docs/plugin-signing.md §3）：
// 迁移、增删权限、撤销后新装拒绝、历史签名显示 revoked、没有 env 也能验签。
func TestTrustedSigningKeys(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secret.NewBox(key)
	// 故意不配 DSH_EE_EXTENSION_TRUSTED_KEYS：验签的钥匙只能来自库
	srv := api.New(st, box, api.Options{DataDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "管理员登录", code, 200, body)

	const manifest = `{
  "id": "acme/keyring-itsm", "version": "1.0.0", "name": "信任根测试的工单", "kind": "declarative",
  "openapi": "openapi.yaml", "servers": ["https://itsm.acme.internal"],
  "tools": [{ "name": "list_tickets", "readOnly": true }]
}`
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pemOf := func(p ed25519.PublicKey) string {
		der, err := x509.MarshalPKIXPublicKey(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	}
	sign := func(t *testing.T, key ed25519.PrivateKey) []byte {
		t.Helper()
		_, raw, err := extension.SignExtension([]byte(manifest), []byte(extOpenAPI), key)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	pkg := func(sig []byte) map[string]any {
		files := map[string]string{"./dsh-ee.extension.json": manifest, "./openapi.yaml": extOpenAPI}
		if sig != nil {
			files["./"+extension.SignatureFile] = string(sig)
		}
		return map[string]any{"files": files}
	}
	type plan struct {
		Check struct {
			OK      bool
			Errors  int
			Results []struct{ Code string }
		}
		Signature *struct {
			Status   string
			KeyID    string `json:"key_id"`
			KeyState string `json:"key_state"`
		}
	}
	check := func(t *testing.T, sig []byte) plan {
		t.Helper()
		code, body := admin.do("POST", "/api/admin/extensions/check", pkg(sig), nil)
		expect(t, "检查扩展", code, 200, body)
		return decodeAs[plan](t, body)
	}
	revokedCode := func(p plan) bool {
		for _, r := range p.Check.Results {
			if r.Code == "signature.revoked" {
				return true
			}
		}
		return false
	}

	// 库里还没有钥匙：签了名也只是「不受信任」，默认策略下不阻断，但显示 unsigned / invalid
	if p := check(t, sign(t, priv)); p.Signature == nil || p.Signature.Status != extension.SignatureInvalid || !p.Check.OK {
		t.Fatalf("库里没有钥匙时应显示 invalid、默认不阻断：%+v", p)
	}

	// 1. 加一把受信任公钥（不需要 env）
	code, body = admin.do("POST", "/api/admin/signing-keys", map[string]any{
		"public_key": pemOf(pub), "note": "作者钥匙",
	}, nil)
	expect(t, "加受信任公钥", code, 201, body)
	added := decodeAs[store.TrustedSigningKey](t, body)
	if added.KeyID != extension.KeyID(pub) || added.Purpose != "extension" || added.Note != "作者钥匙" || added.CreatedBy == nil {
		t.Fatalf("新增结果不对：%+v", added)
	}
	keyID := added.KeyID

	// 同一把钥匙不能重复加
	code, body = admin.do("POST", "/api/admin/signing-keys", map[string]any{"public_key": pemOf(pub)}, nil)
	expect(t, "重复加公钥", code, 409, body)

	// 2. 没有 env、只有库里的钥匙：签过名的扩展能验签通过
	signed := sign(t, priv)
	if p := check(t, signed); !p.Check.OK || p.Signature == nil || p.Signature.Status != extension.SignatureValid || p.Signature.KeyState != extension.KeyStateActive || p.Signature.KeyID != keyID {
		t.Fatalf("库里的钥匙应能验签：%+v", p)
	}

	// 3. 撤销后：历史签名显示 revoked，新装 / 升级拒绝（就算没开强制签名）
	code, body = admin.do("POST", "/api/admin/signing-keys/"+keyID+"/revoke", nil, nil)
	expect(t, "撤销公钥", code, 200, body)
	if got := decodeAs[store.TrustedSigningKey](t, body); got.RevokedAt == nil {
		t.Fatalf("撤销要记 revoked_at：%+v", got)
	}
	if p := check(t, signed); p.Check.OK || p.Signature == nil || p.Signature.Status != extension.SignatureRevoked || p.Signature.KeyState != extension.KeyStateRevoked || !revokedCode(p) {
		t.Fatalf("撤销后历史签名应显示 revoked 且不能安装：%+v", p)
	}
	code, body = admin.do("POST", "/api/admin/extensions/install", pkg(signed), nil)
	expect(t, "撤销后不能安装", code, 400, body)
	// 重复撤销幂等，换下已撤销的拒绝
	code, body = admin.do("POST", "/api/admin/signing-keys/"+keyID+"/revoke", nil, nil)
	expect(t, "重复撤销", code, 200, body)
	code, body = admin.do("POST", "/api/admin/signing-keys/"+keyID+"/retire", nil, nil)
	expect(t, "撤销后不能换下", code, 409, body)

	// 4. 换下（retired）：历史签名仍有效，计划里标出公钥已换下
	pub2, priv2, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	code, body = admin.do("POST", "/api/admin/signing-keys", map[string]any{"public_key": pemOf(pub2), "purpose": "extension"}, nil)
	expect(t, "加第二把公钥", code, 201, body)
	key2 := decodeAs[store.TrustedSigningKey](t, body).KeyID
	code, body = admin.do("POST", "/api/admin/signing-keys/"+key2+"/retire", nil, nil)
	expect(t, "换下公钥", code, 200, body)
	if got := decodeAs[store.TrustedSigningKey](t, body); got.RetiredAt == nil || got.RevokedAt != nil {
		t.Fatalf("换下要记 retired_at：%+v", got)
	}
	if p := check(t, sign(t, priv2)); !p.Check.OK || p.Signature == nil || p.Signature.Status != extension.SignatureValid || p.Signature.KeyState != extension.KeyStateRetired {
		t.Fatalf("换下的公钥仍认历史签名：%+v", p)
	}

	// 5. 列表和状态：库里的记录都在，环境变量为空
	code, body = admin.do("GET", "/api/admin/signing-keys", nil, nil)
	expect(t, "信任根列表", code, 200, body)
	list := decodeAs[struct {
		Keys []struct {
			KeyID     string  `json:"key_id"`
			State     string  `json:"state"`
			RevokedAt *string `json:"revoked_at"`
			RetiredAt *string `json:"retired_at"`
		} `json:"keys"`
		Count      int                      `json:"count"`
		EnvKeys    []struct{ KeyID string } `json:"env_keys"`
		RequireSig bool                     `json:"require_signature"`
	}](t, body)
	if list.Count != 2 || len(list.EnvKeys) != 0 || list.RequireSig {
		t.Fatalf("列表：%+v", list)
	}
	states := map[string]string{}
	for _, k := range list.Keys {
		states[k.KeyID] = k.State
	}
	if states[keyID] != extension.KeyStateRevoked || states[key2] != extension.KeyStateRetired {
		t.Fatalf("列表里的状态：%+v", states)
	}

	// 6. 审计：新增 / 撤销 / 换下都记了，target 带 key id
	for action, wantKey := range map[string]string{"signing_key.add": keyID, "signing_key.revoke": keyID, "signing_key.retire": key2} {
		var n int
		if err := st.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_logs WHERE action = ? AND target LIKE ?", action, "%"+wantKey+"%").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			t.Fatalf("没有写审计 %s（%s）", action, wantKey)
		}
	}

	// 7. 参数校验：私钥不收、非法 PEM 400、不认识的 key id 404
	privPEM, _ := extension.MarshalPrivateKeyPEM(priv)
	code, body = admin.do("POST", "/api/admin/signing-keys", map[string]any{"public_key": string(privPEM)}, nil)
	expect(t, "不收私钥", code, 400, body)
	code, body = admin.do("POST", "/api/admin/signing-keys", map[string]any{"public_key": "not pem"}, nil)
	expect(t, "非法 PEM", code, 400, body)
	code, body = admin.do("POST", "/api/admin/signing-keys/not-a-key/revoke", nil, nil)
	expect(t, "非法 key id", code, 400, body)
	code, body = admin.do("POST", "/api/admin/signing-keys/0000000000000000/revoke", nil, nil)
	expect(t, "不存在的 key id", code, 404, body)

	// 8. 权限：连接管理（connections.manage）和系统设置（settings.manage）任一即可；
	//    普通员工 403。用管理员建角色、建用户再登录验证。
	code, body = admin.do("GET", "/api/admin/roles", nil, nil)
	expect(t, "角色列表", code, 200, body)
	memberID := int64(0)
	for _, r := range decodeAs[struct{ Roles []store.Role }](t, body).Roles {
		if r.Code == rbac.RoleMember {
			memberID = r.ID
		}
	}
	if memberID == 0 {
		t.Fatal("找不到普通员工角色")
	}
	newRole := func(code, name, perm string) int64 {
		t.Helper()
		code2, body := admin.do("POST", "/api/admin/roles", map[string]any{"code": code, "name": name, "permissions": []string{perm}}, nil)
		expect(t, "建角色 "+code, code2, 201, body)
		return decodeAs[store.Role](t, body).ID
	}
	newUser := func(name string, roles []int64) *client {
		t.Helper()
		code, body := admin.do("POST", "/api/admin/users", map[string]any{
			"username": name, "password": "signing-key-pass-1", "department": "研发效能部", "role_ids": roles,
		}, nil)
		expect(t, "建用户 "+name, code, 201, body)
		c := newClient(t, ts.URL)
		code, body = c.do("POST", "/api/auth/login", map[string]string{"username": name, "password": "signing-key-pass-1"}, nil)
		expect(t, name+" 登录", code, 200, body)
		return c
	}
	settingsRole := newRole("signing-key-settings", "签名信任根（设置）", rbac.SettingsManage)
	connRole := newRole("signing-key-conn", "签名信任根（连接）", rbac.ConnectionsManage)
	settingsUser := newUser("signing-settings", []int64{memberID, settingsRole})
	connUser := newUser("signing-conn", []int64{memberID, connRole})
	memberUser := newUser("signing-member", []int64{memberID})
	for what, c := range map[string]*client{"settings.manage": settingsUser, "connections.manage": connUser} {
		code, body := c.do("GET", "/api/admin/signing-keys", nil, nil)
		expect(t, what+" 能看信任根", code, 200, body)
	}
	code, body = memberUser.do("GET", "/api/admin/signing-keys", nil, nil)
	expect(t, "员工不能看信任根", code, 403, body)
	code, body = memberUser.do("POST", "/api/admin/signing-keys", map[string]any{"public_key": pemOf(pub2)}, nil)
	expect(t, "员工不能加信任根", code, 403, body)
}
