package api

import (
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/extension"
)

// 签名策略（docs/plugins.md §9.3）：默认不阻断；配了强制验证签名时，没签名、验不过都当作检查不通过。
func TestSignatureProblem(t *testing.T) {
	unsigned := &extension.SignatureStatus{Status: extension.SignatureUnsigned, Message: "没有 signature.json：这个扩展没有签名"}
	invalid := &extension.SignatureStatus{Status: extension.SignatureInvalid, KeyID: "0123456789abcdef", Message: "签名和内容对不上：可能被改过"}
	valid := &extension.SignatureStatus{Status: extension.SignatureValid, KeyID: "0123456789abcdef", Message: "签名者 0123456789abcdef 在受信任的公钥里"}

	// 默认「警告后允许」：unsigned / invalid 都不产生错误，只在安装计划里显示状态
	for _, st := range []*extension.SignatureStatus{unsigned, invalid} {
		if d := signatureProblem(false, st); d != nil {
			t.Fatalf("默认不阻断，实际报了 %+v", d)
		}
	}
	if d := signatureProblem(false, nil); d != nil {
		t.Fatalf("没有签名状态时不应报错：%+v", d)
	}

	// 强制验证签名：fail closed
	for _, tc := range []struct {
		name string
		st   *extension.SignatureStatus
		want string
	}{
		{"没签名", unsigned, "没有 signature.json"},
		{"签名无效", invalid, "签名和内容对不上"},
		{"没有状态", nil, "这个扩展没有签名"},
	} {
		d := signatureProblem(true, tc.st)
		if d == nil {
			t.Fatalf("%s：强制签名时应报错", tc.name)
		}
		if d.Code != "signature.required" || d.Severity != extension.SevError || d.Where != extension.SignatureFile {
			t.Fatalf("%s：诊断的位置不对：%+v", tc.name, d)
		}
		if !strings.Contains(d.Message, tc.want) || !strings.Contains(d.Message, "DSH_EE_EXTENSION_REQUIRE_SIGNATURE=1") || d.Fix == "" {
			t.Fatalf("%s：错误里要说清楚原因和改法：%+v", tc.name, d)
		}
	}

	// 强制验证签名，但签名有效：放行
	if d := signatureProblem(true, valid); d != nil {
		t.Fatalf("签名有效时不应报错：%+v", d)
	}
}
