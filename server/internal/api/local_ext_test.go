package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// 端侧扩展：写操作前要员工在网页上确认，一次同意只能执行一次；调用摘要记进审计。
func TestLocalExtensionConfirmAndAudit(t *testing.T) {
	f := newSvcFixture(t)
	alice, aliceTok := f.user("alice")
	_, bobTok := f.user("bob")

	confirm := func(d dshClient, body map[string]any) map[string]any {
		t.Helper()
		code, raw, _ := d.do("POST", "/api/local/confirm", body, nil)
		if code != 200 {
			t.Fatalf("确认接口：HTTP %d %s", code, raw)
		}
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return out
	}
	req := map[string]any{"extension": "acme/legacy-oa", "version": "0.1.0", "tool": "submit_leave", "args": map[string]any{"days": 2}}

	// 不合法的扩展名直接拒
	if code, raw, _ := aliceTok.do("POST", "/api/local/confirm", map[string]any{"extension": "../x", "tool": "t"}, nil); code != 400 {
		t.Fatalf("扩展名不对应 400：%d %s", code, raw)
	}

	first := confirm(aliceTok, req)
	if first["approved"] != false || !strings.Contains(fmt.Sprint(first["url"]), "/confirm/") {
		t.Fatalf("第一次应要求确认：%v", first)
	}
	id := int64(first["id"].(float64))
	// 同样的参数再问：还是同一条
	if again := confirm(aliceTok, req); int64(again["id"].(float64)) != id {
		t.Fatalf("没确认前重复问应给同一条：%v", again)
	}
	// 确认页能看出是哪个端侧扩展
	code, body := alice.do("GET", fmt.Sprintf("/api/me/confirmations/%d", id), nil, nil)
	expect(t, "看确认", code, 200, body)
	if !strings.Contains(string(body), "端侧扩展 acme/legacy-oa") {
		t.Fatalf("确认页应写明端侧扩展：%s", body)
	}
	// 别人不能替 alice 确认，也用不了她的同意
	code, body = alice.do("POST", fmt.Sprintf("/api/me/confirmations/%d", id), map[string]any{"approve": true}, nil)
	expect(t, "alice 同意", code, 200, body)
	if other := confirm(bobTok, req); other["approved"] != false {
		t.Fatalf("bob 不能用 alice 的同意：%v", other)
	}
	if ok := confirm(aliceTok, req); ok["approved"] != true {
		t.Fatalf("同意后再调应放行：%v", ok)
	}
	if used := confirm(aliceTok, req); used["approved"] != false {
		t.Fatalf("一次同意只能用一次：%v", used)
	}
	// 换了参数要重新确认
	req2 := map[string]any{"extension": "acme/legacy-oa", "tool": "submit_leave", "args": map[string]any{"days": 3}}
	if changed := confirm(aliceTok, req2); changed["approved"] != false {
		t.Fatalf("参数变了要重新确认：%v", changed)
	}
	// DSH 自己有审批卡：默认 non_dsh 时不再确认；切到 always 之后也要确认
	req3 := map[string]any{"extension": "acme/legacy-oa", "tool": "submit_leave", "args": map[string]any{"days": 9}, "harness": "dsh"}
	if dsh := confirm(aliceTok, req3); dsh["approved"] != true {
		t.Fatalf("DSH 里默认不用服务端再确认：%v", dsh)
	}
	code, body = f.admin.do("PUT", "/api/admin/write-confirm", map[string]string{"mode": "always"}, nil)
	expect(t, "改成一律确认", code, 200, body)
	if dsh := confirm(aliceTok, req3); dsh["approved"] != false {
		t.Fatalf("always 时 DSH 里也要确认：%v", dsh)
	}

	// 回报调用摘要
	if code, raw, _ := aliceTok.do("POST", "/api/local/audit", map[string]any{"extension": "acme/legacy-oa", "version": "0.1.0", "tool": "list_forms"}, nil); code != 400 {
		t.Fatalf("没带 ok 应 400：%d %s", code, raw)
	}
	if code, raw, _ := aliceTok.do("POST", "/api/local/audit", map[string]any{"extension": "acme/legacy-oa", "version": "0.1.0", "tool": "list_forms", "ok": true, "duration_ms": 42}, nil); code != 204 {
		t.Fatalf("回报：%d %s", code, raw)
	}
	code, body = f.admin.do("GET", "/api/admin/audit?action=local.call", nil, nil)
	expect(t, "查审计", code, 200, body)
	if !strings.Contains(string(body), "local:acme/legacy-oa") || !strings.Contains(string(body), "list_forms") {
		t.Fatalf("审计里应有这次本机调用：%s", body)
	}
}
