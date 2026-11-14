package api_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/extension"
	"github.com/hikariming/dsh-ee/server/internal/secret"
)

// 扩展升级回滚：装过的版本留在 asset_versions 里，回滚把历史快照当作一次重新安装，
// 复用安装检查和差异计算，按当前信任根重新验签，先取工具再写库（失败时当前版本不动）。

// rbOpenAPI 是回滚测试用的接口文档；withCreate 为 true 时多一个写操作。
func rbOpenAPI(withCreate bool) string {
	doc := `openapi: 3.0.3
info: { title: Tickets, version: "1" }
paths:
  /api/tickets:
    get:
      operationId: list_tickets
      summary: List tickets
      responses:
        "200":
          description: ok
`
	if withCreate {
		doc += `    post:
      operationId: create_ticket
      summary: Open a ticket
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                title: { type: string }
      responses: { "201": { description: created } }
`
	}
	return doc
}

// rbManifest 是回滚测试用的声明式扩展清单。
func rbManifest(version string, withCreate bool) string {
	create := ""
	if withCreate {
		create = `, { "name": "create_ticket", "description": "建工单" }`
	}
	return fmt.Sprintf(`{
  "id": "acme/itsm", "version": %q, "name": "工单", "description": "查工单、建工单", "kind": "declarative",
  "openapi": "openapi.yaml", "servers": ["https://itsm.acme.internal"],
  "credential": { "mode": "shared" },
  "tools": [ { "name": "list_tickets", "readOnly": true, "description": "按状态列出工单" }%s ]
}`, version, create)
}

// rbPackage 组装上传格式：清单 + 接口文档。
func rbPackage(version string, withCreate bool) map[string]any {
	return map[string]any{"files": map[string]string{
		"dsh-ee.extension.json": rbManifest(version, withCreate),
		"openapi.yaml":          rbOpenAPI(withCreate),
	}}
}

type rbHistoryEntry struct {
	AssetVersion int    `json:"asset_version"`
	Extension    string `json:"extension"`
	Version      string `json:"version"`
	Digest       string `json:"digest"`
	Signature    string `json:"signature"`
	SignatureKey string `json:"signature_key"`
	InstalledBy  string `json:"installed_by"`
	InstalledAt  string `json:"installed_at"`
	Current      bool   `json:"current"`
	CanRollback  bool   `json:"can_rollback"`
}

type rbHistoryResp struct {
	Asset struct {
		ID   int64
		Slug string
	}
	ExtensionID    string           `json:"extension_id"`
	CurrentVersion int              `json:"current_version"`
	Versions       []rbHistoryEntry `json:"versions"`
}

type rbRollbackResp struct {
	Version      int `json:"version"`
	Tools        int `json:"tools"`
	RolledBackTo struct {
		AssetVersion int    `json:"asset_version"`
		Extension    string `json:"extension"`
	} `json:"rolled_back_to"`
	RolledBackFrom struct {
		Extension string `json:"extension"`
	} `json:"rolled_back_from"`
}

func TestDeclarativeExtensionRollback(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secret.NewBox(key)
	srv := api.New(st, box, api.Options{DataDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "管理员登录", code, 200, body)

	history := func(t *testing.T, path string) rbHistoryResp {
		t.Helper()
		code, body := admin.do("GET", path, nil, nil)
		expect(t, "安装历史 "+path, code, 200, body)
		return decodeAs[rbHistoryResp](t, body)
	}

	// 1. 装 1.0.0，再升级到 1.1.0（多一个写操作工具）
	code, body = admin.do("POST", "/api/admin/extensions/install", rbPackage("1.0.0", false), nil)
	expect(t, "安装扩展", code, 201, body)
	inst := decodeAs[struct {
		Tools int
		Asset struct {
			ID   int64
			Slug string
		}
	}](t, body)
	if inst.Tools != 1 || inst.Asset.Slug != "itsm" {
		t.Fatalf("安装结果不对：%s", body)
	}
	assetID := inst.Asset.ID
	code, body = admin.do("POST", "/api/admin/extensions/install", rbPackage("1.1.0", true), nil)
	expect(t, "升级扩展", code, 201, body)
	up := decodeAs[struct {
		Version      int
		Tools        int
		UpgradedFrom string `json:"upgraded_from"`
	}](t, body)
	if up.Version != 2 || up.UpgradedFrom != "1.0.0" || up.Tools != 2 {
		t.Fatalf("升级结果不对：%s", body)
	}

	// 2. 安装历史：两条，当前是 1.1.0，1.0.0 可回滚；按扩展 id（URL 编码）也能查
	h := history(t, "/api/admin/extensions/itsm/history")
	if h.Asset.ID != assetID || h.ExtensionID != "acme/itsm" || h.CurrentVersion != 2 || len(h.Versions) != 2 {
		t.Fatalf("安装历史不对：%+v", h)
	}
	if !h.Versions[0].Current || h.Versions[0].Extension != "acme/itsm@1.1.0" || h.Versions[0].InstalledBy != "admin" {
		t.Fatalf("当前版本不对：%+v", h.Versions[0])
	}
	if h.Versions[1].Extension != "acme/itsm@1.0.0" || !h.Versions[1].CanRollback || h.Versions[1].Digest == "" {
		t.Fatalf("可回滚的历史版本不对：%+v", h.Versions[1])
	}
	byID := history(t, "/api/admin/extensions/acme%2Fitsm/history")
	if byID.Asset.ID != assetID || len(byID.Versions) != 2 {
		t.Fatalf("按扩展 id 查历史不对：%+v", byID)
	}

	// 3. 不确认不执行：返回回滚计划和差异（要移除升级时新增的工具）
	code, body = admin.do("POST", "/api/admin/extensions/itsm/rollback", map[string]any{}, nil)
	expect(t, "没确认的回滚", code, 409, body)
	preview := decodeAs[struct {
		Error string `json:"error"`
		Plan  struct {
			From    rbHistoryEntry `json:"from"`
			To      rbHistoryEntry `json:"to"`
			Changes []string       `json:"changes"`
		} `json:"plan"`
	}](t, body)
	if preview.Plan.From.Extension != "acme/itsm@1.1.0" || preview.Plan.To.Extension != "acme/itsm@1.0.0" {
		t.Fatalf("回滚计划方向不对：%s", body)
	}
	if !strings.Contains(strings.Join(preview.Plan.Changes, "；"), "去掉了工具 create_ticket") {
		t.Fatalf("回滚计划应列出工具变化：%s", body)
	}

	// 4. 确认后回到上一版：线上版本换成 1.0.0，工具按历史快照重建
	code, body = admin.do("POST", "/api/admin/extensions/itsm/rollback", map[string]any{"confirm": true}, nil)
	expect(t, "确认回滚", code, 200, body)
	res := decodeAs[rbRollbackResp](t, body)
	if res.Version != 3 || res.Tools != 1 || res.RolledBackTo.AssetVersion != 1 ||
		res.RolledBackTo.Extension != "acme/itsm@1.0.0" || res.RolledBackFrom.Extension != "acme/itsm@1.1.0" {
		t.Fatalf("回滚结果不对：%s", body)
	}
	code, body = admin.do("GET", fmt.Sprintf("/api/hub/%d", assetID), nil, nil)
	expect(t, "回滚后的连接详情", code, 200, body)
	detail := decodeAs[struct {
		Asset struct {
			Spec struct {
				Extension struct {
					ID      string
					Version string
					Digest  string
				}
				OpenAPI string
			}
		}
		Tools []struct {
			Name     string
			ReadOnly bool `json:"read_only"`
			Enabled  bool
		}
	}](t, body)
	if detail.Asset.Spec.Extension.Version != "1.0.0" || detail.Asset.Spec.OpenAPI != rbOpenAPI(false) {
		t.Fatalf("线上版本没回到 1.0.0：%s", body)
	}
	if len(detail.Tools) != 1 || detail.Tools[0].Name != "list_tickets" || !detail.Tools[0].ReadOnly {
		t.Fatalf("回滚后应只剩 list_tickets：%s", body)
	}

	// 5. 回滚也进历史：现在是 1.0.0，之前两版都还能再回滚（可以再滚回 1.1.0）
	h = history(t, "/api/admin/extensions/itsm/history")
	if len(h.Versions) != 3 || h.Versions[0].AssetVersion != 3 || h.Versions[0].Extension != "acme/itsm@1.0.0" {
		t.Fatalf("回滚后历史不对：%+v", h.Versions)
	}
	code, body = admin.do("POST", "/api/admin/extensions/itsm/rollback", map[string]any{"version": 2, "confirm": true}, nil)
	expect(t, "回滚到指定版本（滚回去升级）", code, 200, body)
	again := decodeAs[rbRollbackResp](t, body)
	if again.Version != 4 || again.Tools != 2 || again.RolledBackTo.Extension != "acme/itsm@1.1.0" {
		t.Fatalf("指定版本回滚结果不对：%s", body)
	}

	// 6. 审计：两次回滚都记 extension.rollback，带前后版本
	code, body = admin.do("GET", "/api/admin/audit?action=extension.rollback&limit=10", nil, nil)
	expect(t, "审计日志", code, 200, body)
	logs := decodeAs[struct {
		Logs []struct {
			Action string
			Target string
			Detail map[string]any
		}
	}](t, body)
	if len(logs.Logs) != 2 || logs.Logs[0].Action != "extension.rollback" || logs.Logs[0].Target != "connection:itsm" {
		t.Fatalf("回滚审计不对：%s", body)
	}
	if !strings.Contains(fmt.Sprint(logs.Logs[0].Detail["from"]), "acme/itsm@1.0.0") ||
		!strings.Contains(fmt.Sprint(logs.Logs[0].Detail["to"]), "acme/itsm@1.1.0") {
		t.Fatalf("审计应带前后版本：%s", body)
	}

	// 7. 非法目标：不存在的版本号、当前版本、不存在的连接
	code, body = admin.do("POST", "/api/admin/extensions/itsm/rollback", map[string]any{"version": 99}, nil)
	expect(t, "不存在的版本", code, 400, body)
	code, body = admin.do("POST", "/api/admin/extensions/itsm/rollback", map[string]any{"version": 4}, nil)
	expect(t, "回滚到当前版本", code, 409, body)
	code, body = admin.do("POST", "/api/admin/extensions/nope/rollback", map[string]any{"confirm": true}, nil)
	expect(t, "不存在的连接", code, 404, body)
	code, body = admin.do("GET", "/api/admin/extensions/nope/history", nil, nil)
	expect(t, "不存在的连接历史", code, 404, body)

	// 8. 没有历史：只装过一版的扩展不能回滚
	solo := map[string]any{"files": map[string]string{
		"dsh-ee.extension.json": `{
  "id": "acme/solo", "version": "1.0.0", "name": "只有一版", "kind": "declarative",
  "openapi": "openapi.yaml", "servers": ["https://solo.acme.internal"],
  "tools": [ { "name": "list_tickets", "readOnly": true } ]
}`,
		"openapi.yaml": rbOpenAPI(false),
	}}
	code, body = admin.do("POST", "/api/admin/extensions/install", solo, nil)
	expect(t, "安装只有一版的扩展", code, 201, body)
	soloHistory := history(t, "/api/admin/extensions/solo/history")
	if len(soloHistory.Versions) != 1 || !soloHistory.Versions[0].Current {
		t.Fatalf("只有一版的历史不对：%+v", soloHistory.Versions)
	}
	code, body = admin.do("POST", "/api/admin/extensions/solo/rollback", map[string]any{"confirm": true}, nil)
	expect(t, "没有历史可回滚", code, 400, body)

	// 9. 权限：普通员工不能看历史、不能回滚
	code, body = admin.do("POST", "/api/admin/users", map[string]any{"username": "bob", "password": "bob-password-123", "display_name": "Bob"}, nil)
	expect(t, "建员工", code, 201, body)
	bob := newClient(t, ts.URL)
	code, body = bob.do("POST", "/api/auth/login", map[string]string{"username": "bob", "password": "bob-password-123"}, nil)
	expect(t, "员工登录", code, 200, body)
	code, body = bob.do("GET", "/api/admin/extensions/itsm/history", nil, nil)
	expect(t, "员工看历史", code, 403, body)
	code, body = bob.do("POST", "/api/admin/extensions/itsm/rollback", map[string]any{"confirm": true}, nil)
	expect(t, "员工回滚", code, 403, body)
}

// 服务扩展回滚：按历史版本重新取工具要连扩展的 /mcp；连不上时回滚不执行，当前版本和工具都不动。
func TestServiceExtensionRollbackFailureKeepsCurrent(t *testing.T) {
	f := newSvcFixture(t)
	version := "1.0.0"
	ext := newFakeServiceExt(t, f.ts.URL, func(self string) string {
		return wikiManifest(version, `{ "mode": "ee_jwt" }`, "")(self)
	})
	admin := f.admin

	// 装 1.0.0，再升级到 1.1.0
	code, body := admin.do("POST", "/api/admin/extensions/install",
		map[string]any{"service_url": ext.ts.URL, "config": map[string]any{"tenant": "acme", "api_key": "k-secret-123"}}, nil)
	expect(t, "安装服务扩展", code, 201, body)
	inst := decodeAs[struct {
		Tools int
		Asset struct{ ID int64 }
	}](t, body)
	if inst.Tools != 2 {
		t.Fatalf("服务扩展工具数不对：%s", body)
	}
	version = "1.1.0"
	code, body = admin.do("POST", "/api/admin/extensions/install",
		map[string]any{"service_url": ext.ts.URL, "config": map[string]any{"tenant": "acme"}}, nil)
	expect(t, "升级服务扩展", code, 201, body)
	up := decodeAs[struct {
		Version int
		Tools   int
	}](t, body)
	if up.Version != 2 || up.Tools != 2 {
		t.Fatalf("升级结果不对：%s", body)
	}

	// 历史里能看到两版；配置密文和明文都不进历史响应
	code, body = admin.do("GET", "/api/admin/extensions/wiki/history", nil, nil)
	expect(t, "服务扩展历史", code, 200, body)
	if !strings.Contains(string(body), "acme/wiki@1.0.0") || strings.Contains(string(body), "k-secret-123") || strings.Contains(string(body), "$enc") {
		t.Fatalf("历史响应不对（不能带配置密文和明文）：%s", body)
	}
	hist := decodeAs[struct {
		Versions []struct {
			AssetVersion int  `json:"asset_version"`
			Current      bool `json:"current"`
			CanRollback  bool `json:"can_rollback"`
		}
	}](t, body)
	if len(hist.Versions) != 2 || !hist.Versions[0].Current || !hist.Versions[1].CanRollback {
		t.Fatalf("服务扩展历史条目不对：%s", body)
	}

	// 扩展服务挂了：回滚要先按历史版本取工具，失败时还没写库
	ext.down = true
	code, body = admin.do("POST", "/api/admin/extensions/wiki/rollback", map[string]any{"confirm": true}, nil)
	expect(t, "扩展不可用时回滚", code, 502, body)
	if !strings.Contains(string(body), "当前版本没有改动") {
		t.Fatalf("应说明当前版本没动：%s", body)
	}

	// 当前版本、工具和历史都没有被破坏
	code, body = admin.do("GET", fmt.Sprintf("/api/hub/%d", inst.Asset.ID), nil, nil)
	expect(t, "失败后的连接详情", code, 200, body)
	detail := decodeAs[struct {
		Asset struct {
			Spec struct {
				Extension struct{ Version string }
			}
		}
		Tools []struct{ Name string }
	}](t, body)
	if detail.Asset.Spec.Extension.Version != "1.1.0" || len(detail.Tools) != 2 {
		t.Fatalf("失败的回滚不该改动当前版本：%s", body)
	}
	code, body = admin.do("GET", "/api/admin/extensions/wiki/history", nil, nil)
	expect(t, "失败后的历史", code, 200, body)
	if n := len(decodeAs[struct{ Versions []any }](t, body).Versions); n != 2 {
		t.Fatalf("失败的回滚不该留下历史：%d 条", n)
	}

	// 恢复后再回滚：线上版本换成 1.0.0，工具按历史快照重建
	ext.down = false
	code, body = admin.do("POST", "/api/admin/extensions/wiki/rollback", map[string]any{"confirm": true}, nil)
	expect(t, "扩展恢复后回滚", code, 200, body)
	res := decodeAs[struct {
		Version      int `json:"version"`
		Tools        int `json:"tools"`
		RolledBackTo struct {
			AssetVersion int    `json:"asset_version"`
			Extension    string `json:"extension"`
		} `json:"rolled_back_to"`
	}](t, body)
	if res.Version != 3 || res.Tools != 2 || res.RolledBackTo.AssetVersion != 1 || res.RolledBackTo.Extension != "acme/wiki@1.0.0" {
		t.Fatalf("服务扩展回滚结果不对：%s", body)
	}
	code, body = admin.do("GET", fmt.Sprintf("/api/hub/%d", inst.Asset.ID), nil, nil)
	expect(t, "回滚后的服务扩展详情", code, 200, body)
	if !strings.Contains(string(body), `"version":"1.0.0"`) {
		t.Fatalf("线上版本没回到 1.0.0：%s", body)
	}
}

// rbSignedPackage 是带签名的上传包；签名对象是清单 + 接口文档。
func rbSignedPackage(t *testing.T, version string, withCreate bool, priv ed25519.PrivateKey) map[string]any {
	t.Helper()
	manifest, doc := rbManifest(version, withCreate), rbOpenAPI(withCreate)
	_, raw, err := extension.SignExtension([]byte(manifest), []byte(doc), priv)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"files": map[string]string{
		"dsh-ee.extension.json": manifest,
		"openapi.yaml":          doc,
		"signature.json":        string(raw),
	}}
}

// 回滚按当前信任根重新验签：撤销的签名不能回滚；没存签名材料的旧版本在默认策略下可回滚但要提示。
func TestExtensionRollbackSignaturePolicy(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secret.NewBox(key)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.New(st, box, api.Options{DataDir: t.TempDir(), ExtensionTrustedKeys: []ed25519.PublicKey{pub}}, logger)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "管理员登录", code, 200, body)

	// 信任根落库，后面才能撤销；环境变量里的只读公钥不能撤销
	pemText, err := extension.MarshalPublicKeyPEM(pub)
	if err != nil {
		t.Fatal(err)
	}
	code, body = admin.do("POST", "/api/admin/signing-keys", map[string]string{"public_key": string(pemText)}, nil)
	expect(t, "加受信任公钥", code, 201, body)

	// 装签名版 1.0.0，再升级到 1.1.0
	code, body = admin.do("POST", "/api/admin/extensions/install", rbSignedPackage(t, "1.0.0", false, priv), nil)
	expect(t, "安装签名扩展", code, 201, body)
	inst := decodeAs[struct{ Asset struct{ ID int64 } }](t, body)
	code, body = admin.do("POST", "/api/admin/extensions/install", rbSignedPackage(t, "1.1.0", true, priv), nil)
	expect(t, "升级签名扩展", code, 201, body)

	// 历史里记下安装时的签名状态
	code, body = admin.do("GET", "/api/admin/extensions/itsm/history", nil, nil)
	expect(t, "签名扩展历史", code, 200, body)
	hist := decodeAs[rbHistoryResp](t, body)
	if len(hist.Versions) != 2 || hist.Versions[0].Signature != "valid" || hist.Versions[0].SignatureKey != extension.KeyID(pub) {
		t.Fatalf("历史里应带签名状态：%s", body)
	}

	// 公钥还在时能回滚
	code, body = admin.do("POST", "/api/admin/extensions/itsm/rollback", map[string]any{"confirm": true}, nil)
	expect(t, "验签通过时回滚", code, 200, body)

	// 撤销签名公钥后再回滚到签名版：按现在的信任根复核，被拦下，线上版本不动
	keyID := extension.KeyID(pub)
	code, body = admin.do("POST", "/api/admin/signing-keys/"+keyID+"/revoke", nil, nil)
	expect(t, "撤销公钥", code, 200, body)
	code, body = admin.do("POST", "/api/admin/extensions/itsm/rollback", map[string]any{"version": 2, "confirm": true}, nil)
	expect(t, "撤销后回滚签名版", code, 400, body)
	if !strings.Contains(string(body), "已撤销") {
		t.Fatalf("应说明签名者已撤销：%s", body)
	}
	code, body = admin.do("GET", fmt.Sprintf("/api/hub/%d", inst.Asset.ID), nil, nil)
	expect(t, "撤销后连接详情", code, 200, body)
	if !strings.Contains(string(body), `"version":"1.0.0"`) {
		t.Fatalf("被拦下的回滚不该改动线上版本：%s", body)
	}

	// 模拟本改动之前装的版本：快照里没有 signature，回滚时无法复核签名，默认策略下仍可回滚但要提示
	old, err := st.GetAssetVersion(ctx, inst.Asset.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]json.RawMessage
	if err := json.Unmarshal(old.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	var ext map[string]json.RawMessage
	if err := json.Unmarshal(spec["extension"], &ext); err != nil {
		t.Fatal(err)
	}
	delete(ext, "signature")
	delete(ext, "signature_status")
	extRaw, _ := json.Marshal(ext)
	spec["extension"] = extRaw
	specRaw, _ := json.Marshal(spec)
	if _, err := st.DB().ExecContext(ctx, "UPDATE asset_versions SET spec = ? WHERE asset_id = ? AND version = 1", string(specRaw), inst.Asset.ID); err != nil {
		t.Fatal(err)
	}
	code, body = admin.do("POST", "/api/admin/extensions/itsm/rollback", map[string]any{"version": 1, "confirm": true}, nil)
	expect(t, "旧历史版本回滚", code, 200, body)
	res := decodeAs[struct {
		Warnings []string `json:"warnings"`
	}](t, body)
	if !strings.Contains(strings.Join(res.Warnings, "；"), "没有保存签名文件") {
		t.Fatalf("旧历史版本应提示无法复核签名：%s", body)
	}
	code, body = admin.do("GET", "/api/admin/extensions/itsm/history", nil, nil)
	expect(t, "旧历史回滚后的历史", code, 200, body)
	if !strings.Contains(string(body), `"signature":"unknown"`) {
		t.Fatalf("没存签名材料的版本应显示 unknown：%s", body)
	}

	// 强制验签（DSH_EE_EXTENSION_REQUIRE_SIGNATURE=1）时，没存签名材料的旧版本不能回滚（fail closed）
	strict := api.New(st, box, api.Options{DataDir: t.TempDir(), ExtensionTrustedKeys: []ed25519.PublicKey{pub}, ExtensionRequireSignature: true}, logger)
	strictTS := httptest.NewServer(strict.Handler())
	defer strictTS.Close()
	strictAdmin := newClient(t, strictTS.URL)
	code, body = strictAdmin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "强制验签实例登录", code, 200, body)
	code, body = strictAdmin.do("GET", "/api/admin/extensions/itsm/history", nil, nil)
	expect(t, "强制验签实例的历史", code, 200, body)
	if !strings.Contains(string(body), `"can_rollback":false`) {
		t.Fatalf("强制验签时旧版本不该标成可回滚：%s", body)
	}
	code, body = strictAdmin.do("POST", "/api/admin/extensions/itsm/rollback", map[string]any{"version": 1, "confirm": true}, nil)
	expect(t, "强制验签时回滚旧版本", code, 400, body)
	if !strings.Contains(string(body), "没有保存签名文件") {
		t.Fatalf("应说明旧版本没有签名材料：%s", body)
	}
}
