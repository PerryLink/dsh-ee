package api

import (
	"encoding/json"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/store"
)

func TestApplyExtensionIgnoresAppsMissingAtSync(t *testing.T) {
	manifest := `{
  "id": "acme/oa", "version": "1.0.0", "name": "OA", "kind": "declarative",
  "openapi": "openapi.yaml", "servers": ["https://oa.acme.internal"],
  "apps": ["apps/hr.yaml"],
  "tools": [{"name": "list_forms", "readOnly": true, "keep": ["id"]}]
}`
	doc := `{"openapi":"3.0.0","info":{"title":"OA","version":"1"},"servers":[{"url":"https://oa.acme.internal"}],"paths":{"/forms":{"get":{"operationId":"list_forms","responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"string"},"internal_note":{"type":"string"}}}}}}}}}}}`
	sp := &assetSpec{Type: "openapi", OpenAPI: doc, Extension: &connExtension{Manifest: manifest}}
	tools := []*store.ConnectionTool{{Name: "list_forms", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	if err := applyExtension(sp, tools); err != nil {
		t.Fatalf("apps 在同步时缺失不应让整条连接失败：%v", err)
	}
	if !tools[0].ReadOnly || !tools[0].Enabled {
		t.Fatalf("工具只读/启用状态不对：%+v", tools[0])
	}
}
