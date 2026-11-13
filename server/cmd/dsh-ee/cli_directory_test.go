package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/directory"
	"github.com/hikariming/dsh-ee/server/internal/extension"
)

// 企业目录的本地签名 / 验签（docs/plugin-signing.md §8）：目录整体 Ed25519 签名，
// 私钥只在企业本地；验签可以用 --key 或和服务端同名的 DSH_EE_EXTENSION_TRUSTED_KEYS。

func writeDirTestFile(t *testing.T, dir string, doc *directory.Document) string {
	t.Helper()
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, directory.FileName)
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func testDirDoc() *directory.Document {
	return &directory.Document{
		Format:      directory.FormatVersion,
		DirectoryID: "acme-prod",
		Title:       extension.Text{ZH: "ACME 企业扩展目录"},
		GeneratedAt: "2026-11-13T08:00:00Z",
		Issuer:      directory.Issuer{Name: "ACME 平台组"},
		Entries: []directory.Entry{
			{
				Kind: directory.KindExtension, Name: "acme/keyring-itsm", Version: "1.0.0", Channel: directory.ChannelStable,
				Digest: strings.Repeat("a", 64),
				Source: directory.Source{Type: directory.SourceTarball, URL: "https://cdn.example.com/keyring-1.0.0.zip"},
			},
			{
				Kind: directory.KindPlugin, Name: "@acme/oa-plugin", Version: "2.0.0", Channel: directory.ChannelBeta,
				Digest: "sha512-" + strings.Repeat("A", 86) + "==",
				Source: directory.Source{Type: directory.SourceNPM},
			},
		},
	}
}

func dirTestKeys(t *testing.T, dir string) (string, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privPEM, err := extension.MarshalPrivateKeyPEM(priv)
	if err != nil {
		t.Fatal(err)
	}
	pubPEM, err := extension.MarshalPublicKeyPEM(pub)
	if err != nil {
		t.Fatal(err)
	}
	privPath := filepath.Join(dir, "directory.key")
	pubPath := filepath.Join(dir, "directory.pub")
	if err := os.WriteFile(privPath, privPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pubPath, pubPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	return privPath, pubPath
}

func TestCLIDirectorySignVerify(t *testing.T) {
	dir := t.TempDir()
	privPath, pubPath := dirTestKeys(t, dir)
	path := writeDirTestFile(t, dir, testDirDoc())
	t.Setenv(trustedKeysEnv, "")
	t.Setenv(directoryHostsEnv, "")

	// 没签名：verify 退出码 1
	_, code, out, errOut := cli("directory", "verify", path, "--key", pubPath)
	if code != 1 || !strings.Contains(out, "unsigned") {
		t.Fatalf("没签名时应退出码 1 并说明：%d\n%s\n%s", code, out, errOut)
	}

	// 签名：原地写回，输出 key id / digest
	_, code, out, errOut = cli("directory", "sign", path, "--key", privPath, "--json")
	if code != 0 {
		t.Fatalf("sign 应成功：%d\n%s\n%s", code, out, errOut)
	}
	var signed struct {
		File        string `json:"file"`
		DirectoryID string `json:"directory_id"`
		Entries     int    `json:"entries"`
		KeyID       string `json:"key_id"`
		Digest      string `json:"digest"`
	}
	if err := json.Unmarshal([]byte(out), &signed); err != nil || signed.File != path || signed.DirectoryID != "acme-prod" || signed.Entries != 2 || len(signed.KeyID) != 16 || len(signed.Digest) != 64 {
		t.Fatalf("sign --json 输出不对：%v\n%s", err, out)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := directory.Parse(raw)
	if err != nil || doc.Signature == nil || doc.Signature.KeyID != signed.KeyID {
		t.Fatalf("签名没写回文件：%v %+v", err, doc)
	}

	// 用公钥验签通过
	_, code, out, errOut = cli("directory", "verify", path, "--key", pubPath)
	if code != 0 || !strings.Contains(out, "通过：acme-prod") || !strings.Contains(out, "valid") {
		t.Fatalf("verify 应通过：%d\n%s\n%s", code, out, errOut)
	}
	// --json 输出机器可读结果
	_, code, out, _ = cli("directory", "verify", path, "--key", pubPath, "--json")
	if code != 0 {
		t.Fatalf("verify --json 应通过：%d\n%s", code, out)
	}
	var verified struct {
		OK        bool `json:"ok"`
		Signature struct {
			Status string `json:"status"`
			KeyID  string `json:"key_id"`
		} `json:"signature"`
	}
	if err := json.Unmarshal([]byte(out), &verified); err != nil || !verified.OK || verified.Signature.Status != directory.SignatureValid || verified.Signature.KeyID != signed.KeyID {
		t.Fatalf("verify --json 输出不对：%v\n%s", err, out)
	}

	// 拿别的公钥验：不受信任，退出码 1
	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPEM, _ := extension.MarshalPublicKeyPEM(otherPub)
	otherPath := filepath.Join(dir, "other.pub")
	if err := os.WriteFile(otherPath, otherPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, code, _, _ := cli("directory", "verify", path, "--key", otherPath); code != 1 {
		t.Fatalf("未知公钥应退出码 1：%d", code)
	}
	// 不配信任根也是 1
	if _, code, _, _ := cli("directory", "verify", path); code != 1 {
		t.Fatalf("没配公钥应退出码 1：%d", code)
	}
	// 用和服务端同名的环境变量给受信任公钥：通过
	t.Setenv(trustedKeysEnv, pubPath)
	if _, code, _, errOut := cli("directory", "verify", path); code != 0 {
		t.Fatalf("DSH_EE_EXTENSION_TRUSTED_KEYS 应能验签：%d %s", code, errOut)
	}
	t.Setenv(trustedKeysEnv, "")

	// 签完再改内容（保留签名）：退出码 1
	tamperPath := writeDirTestFile(t, dir, testDirDoc())
	if _, code, _, _ := cli("directory", "sign", tamperPath, "--key", privPath); code != 0 {
		t.Fatal("签 tamperPath 应成功")
	}
	tamperRaw, err := os.ReadFile(tamperPath)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(tamperRaw), "ACME 企业扩展目录", "被篡改的标题", 1)
	if err := os.WriteFile(tamperPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, code, _, _ := cli("directory", "verify", tamperPath, "--key", pubPath); code != 1 {
		t.Fatalf("篡改后应验签失败：%d", code)
	}

	// 目录不合法：不签名，文件保持原样
	broken := testDirDoc()
	broken.Entries[0].Digest = "abc"
	brokenPath := writeDirTestFile(t, dir, broken)
	_, code, _, errOut = cli("directory", "sign", brokenPath, "--key", privPath)
	if code != 1 || !strings.Contains(errOut, "不签名") || !strings.Contains(errOut, "entry.digest") {
		t.Fatalf("非法目录应拒绝签名：%d\n%s", code, errOut)
	}
	if raw, _ := os.ReadFile(brokenPath); strings.Contains(string(raw), `"signature"`) {
		t.Fatal("拒绝签名时不应写 signature")
	}

	// tarball 内网主机：默认拒绝；--tarball-hosts 放行
	internal := testDirDoc()
	internal.Entries[0].Source.URL = "https://objects.acme.internal/keyring.zip"
	internalPath := writeDirTestFile(t, dir, internal)
	if _, code, _, _ := cli("directory", "sign", internalPath, "--key", privPath); code != 1 {
		t.Fatalf("默认应拒绝内网 tarball：%d", code)
	}
	if _, code, _, errOut := cli("directory", "sign", internalPath, "--key", privPath, "--tarball-hosts", "objects.acme.internal"); code != 0 {
		t.Fatalf("白名单后应能签名：%d %s", code, errOut)
	}
	// 环境变量的白名单同样生效（sign 之后 verify 也用）
	t.Setenv(directoryHostsEnv, "*.acme.internal")
	if _, code, _, _ := cli("directory", "verify", internalPath, "--key", pubPath); code != 0 {
		t.Fatalf("环境变量白名单应能验签：%d", code)
	}
	t.Setenv(directoryHostsEnv, "")
	if _, code, _, _ := cli("directory", "verify", internalPath, "--key", pubPath); code != 1 {
		t.Fatalf("没白名单时内网地址应验不过（条目校验失败）：%d", code)
	}

	// 用法
	if _, code, _, _ := cli("directory"); code != 2 {
		t.Fatalf("没有子命令应返回 2：%d", code)
	}
	if _, code, _, _ := cli("directory", "sign"); code != 2 {
		t.Fatalf("缺参数应返回 2：%d", code)
	}
	if _, code, out, _ := cli("directory", "help"); code != 0 || !strings.Contains(out, "directory sign") {
		t.Fatalf("help 应列出用法：%d\n%s", code, out)
	}
	if _, code, out, _ := cli("help"); code != 0 || !strings.Contains(out, "dsh-ee directory sign") {
		t.Fatalf("顶层 help 应列出 directory：%d\n%s", code, out)
	}
}

func TestCLIDirectoryRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	privPath, _ := dirTestKeys(t, dir)
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, code, _, errOut := cli("directory", "sign", bad, "--key", privPath); code != 1 || !strings.Contains(errOut, "目录不对") {
		t.Fatalf("坏 JSON 应拒绝：%d %q", code, errOut)
	}
	unknown := writeDirTestFile(t, dir, testDirDoc())
	raw, _ := os.ReadFile(unknown)
	if err := os.WriteFile(unknown, append(raw[:len(raw)-2], []byte(",\n  \"extra\": 1\n}\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, code, _, errOut := cli("directory", "verify", unknown); code != 1 || !strings.Contains(errOut, "目录不对") {
		t.Fatalf("未知字段应拒绝：%d %q", code, errOut)
	}
	if _, code, _, _ := cli("directory", "nope"); code != 2 {
		t.Fatalf("未知子命令应返回 2：%d", code)
	}
}
