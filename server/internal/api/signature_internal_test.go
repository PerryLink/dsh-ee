package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/extension"
	"github.com/hikariming/dsh-ee/server/internal/store"
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

	// 撤销过的公钥：不管有没有开强制签名，新装 / 升级都拒绝（历史签名标 revoked，但保留）
	revoked := &extension.SignatureStatus{Status: extension.SignatureRevoked, KeyID: "0123456789abcdef", KeyState: extension.KeyStateRevoked,
		Message: "签名者的公钥 0123456789abcdef 已撤销"}
	for _, require := range []bool{false, true} {
		d := signatureProblem(require, revoked)
		if d == nil || d.Code != "signature.revoked" || d.Severity != extension.SevError || !strings.Contains(d.Message, "已撤销") {
			t.Fatalf("撤销的公钥（require=%v）应报 signature.revoked：%+v", require, d)
		}
	}
}

// 信任根合并（docs/plugin-signing.md §3）：库里的状态优先于环境变量；未生效、坏记录跳过。
func TestMergeTrustedExtensionKeys(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
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
	now := time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC)
	t1 := now.Add(-time.Hour)
	t2 := now.Add(time.Hour)
	rows := []store.TrustedSigningKey{
		// 和 env 里是同一把：库里的状态优先
		{KeyID: extension.KeyID(pub), PublicKey: pemOf(pub), Purpose: "extension", ValidFrom: t1, RevokedAt: &t2},
		// 还没到生效时间
		{KeyID: extension.KeyID(otherPub), PublicKey: pemOf(otherPub), Purpose: "extension", ValidFrom: t2.Add(time.Hour)},
		// 不是 PEM 的记录跳过
		{KeyID: "0123456789abcdef", PublicKey: "not pem", Purpose: "extension", ValidFrom: t1},
		// 别的用途不参与扩展验签
		{KeyID: "fedcba9876543210", PublicKey: pemOf(pub), Purpose: "npm", ValidFrom: t1},
	}
	keys := mergeTrustedSigningKeys(rows, []ed25519.PublicKey{pub}, now, signingKeyPurposeExtension)
	if len(keys) != 1 || extension.KeyID(keys[0].Public) != extension.KeyID(pub) || keys[0].State != extension.KeyStateRevoked {
		t.Fatalf("库里的撤销状态应压过 env、其它记录跳过：%+v", keys)
	}
	// 没有库记录时 env 的钥匙是 active
	keys = mergeTrustedSigningKeys(nil, []ed25519.PublicKey{pub}, now, signingKeyPurposeExtension)
	if len(keys) != 1 || keys[0].State != extension.KeyStateActive {
		t.Fatalf("env 的钥匙应是 active：%+v", keys)
	}
	// 换下的状态会带到验签里
	rows[0].RevokedAt = nil
	rows[0].RetiredAt = &t2
	keys = mergeTrustedSigningKeys(rows[:1], nil, now, signingKeyPurposeExtension)
	if len(keys) != 1 || keys[0].State != extension.KeyStateRetired {
		t.Fatalf("换下的钥匙状态：%+v", keys)
	}
	// 企业目录验签：purpose=directory 和 extension 的钥匙都接受（复用 #134 的信任根）
	dirRow := store.TrustedSigningKey{KeyID: extension.KeyID(otherPub), PublicKey: pemOf(otherPub), Purpose: signingKeyPurposeDirectory, ValidFrom: t1}
	keys = mergeTrustedSigningKeys([]store.TrustedSigningKey{dirRow}, nil, now, signingKeyPurposeExtension, signingKeyPurposeDirectory)
	if len(keys) != 1 || keys[0].State != extension.KeyStateActive {
		t.Fatalf("目录用途的钥匙应参与目录验签：%+v", keys)
	}
	keys = mergeTrustedSigningKeys([]store.TrustedSigningKey{dirRow}, nil, now, signingKeyPurposeExtension)
	if len(keys) != 0 {
		t.Fatalf("扩展验签不该用 directory 用途的钥匙：%+v", keys)
	}
}
