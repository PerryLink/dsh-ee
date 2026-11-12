package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/config"
	"github.com/hikariming/dsh-ee/server/internal/extension"
)

// copyExampleDir 把仓库里的声明式示例复制到临时目录：签名会写 signature.json，不能动仓库里的文件。
func copyExampleDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{extension.ManifestFile, "openapi.yaml"} {
		raw, err := os.ReadFile(filepath.Join(exampleDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

type keygenOut struct {
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
	KeyID      string `json:"key_id"`
}

func keygen(t *testing.T, dir string) keygenOut {
	t.Helper()
	priv := filepath.Join(dir, "test-sign.key")
	_, code, out, errOut := cli("extension", "keygen", priv, "--json")
	if code != 0 {
		t.Fatalf("keygen 应成功：%d\n%s\n%s", code, out, errOut)
	}
	var got keygenOut
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.PrivateKey != priv || got.PublicKey == "" || len(got.KeyID) != 16 {
		t.Fatalf("keygen --json 输出不对：%v\n%s", err, out)
	}
	if st, err := os.Stat(priv); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("私钥要是 0600：%v %v", st, err)
	}
	return got
}

func TestCLIKeygenSignVerify(t *testing.T) {
	dir := copyExampleDir(t)
	keys := keygen(t, dir)
	if keys.PublicKey != filepath.Join(dir, "test-sign.pub") {
		t.Fatalf("默认公钥文件应是同名的 .pub：%s", keys.PublicKey)
	}

	// 还没签名：verify 退出码 1，说清楚是没有签名
	_, code, _, errOut := cli("extension", "verify", dir, "--key", keys.PublicKey)
	if code != 1 || !strings.Contains(errOut, "没有签名") {
		t.Fatalf("没签名时 verify 应是 1 并说明：%d %q", code, errOut)
	}

	// 签名：给身份、摘要和签名者；文件写在扩展目录的 signature.json
	_, code, out, errOut := cli("extension", "sign", dir, "--key", keys.PrivateKey, "--json")
	if code != 0 {
		t.Fatalf("sign 应成功：%d\n%s\n%s", code, out, errOut)
	}
	var signed struct {
		File     string `json:"file"`
		Identity string `json:"identity"`
		Digest   string `json:"digest"`
		KeyID    string `json:"key_id"`
	}
	if err := json.Unmarshal([]byte(out), &signed); err != nil || signed.Identity != "dsh-ee/demo-itsm@0.1.0" || len(signed.Digest) != 64 || signed.KeyID != keys.KeyID || signed.File != filepath.Join(dir, extension.SignatureFile) {
		t.Fatalf("sign --json 输出不对：%v\n%s", err, out)
	}
	raw, err := os.ReadFile(filepath.Join(dir, extension.SignatureFile))
	if err != nil {
		t.Fatal(err)
	}
	var sig extension.Signature
	if err := json.Unmarshal(raw, &sig); err != nil || sig.Alg != "ed25519" || sig.Version != 1 || sig.Identity != signed.Identity || sig.Digest != signed.Digest {
		t.Fatalf("signature.json 不对：%v %+v", err, sig)
	}

	// 验签：--key 给公钥，或从环境变量拿受信任的公钥
	_, code, out, _ = cli("extension", "verify", dir, "--key", keys.PublicKey)
	if code != 0 || !strings.Contains(out, "签名有效") {
		t.Fatalf("签过名应通过：%d\n%s", code, out)
	}
	t.Setenv(trustedKeysEnv, keys.PublicKey)
	_, code, out, _ = cli("extension", "verify", dir, "--json")
	if code != 0 || !strings.Contains(out, `"status": "valid"`) {
		t.Fatalf("用环境变量给公钥也应通过：%d\n%s", code, out)
	}

	// 把打包也串起来：签过名的包里有 signature.json，管理员上传后安装计划能核对
	zipPath := filepath.Join(t.TempDir(), "signed.zip")
	_, code, out, _ = cli("extension", "pack", dir, "-o", zipPath, "--json")
	if code != 0 {
		t.Fatalf("pack 应成功：%d\n%s", code, out)
	}
	pkg, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	files, err := extension.ReadZip(pkg)
	if err != nil || files[extension.SignatureFile] == nil {
		t.Fatalf("签过名的包里要有 signature.json：%v", err)
	}
	if got := extension.VerifyFiles(files, nil); got.Status != extension.SignatureInvalid {
		t.Fatalf("包里签名在、没配受信任公钥时是 invalid：%+v", got)
	}

	// 篡改内容：验签退出码 1
	doc := filepath.Join(dir, "openapi.yaml")
	b, _ := os.ReadFile(doc)
	if err := os.WriteFile(doc, append(b, ' '), 0o644); err != nil {
		t.Fatal(err)
	}
	_, code, out, _ = cli("extension", "verify", dir, "--key", keys.PublicKey, "--json")
	if code != 1 || !strings.Contains(out, `"status": "invalid"`) {
		t.Fatalf("改了内容应验不过：%d\n%s", code, out)
	}
}

func TestCLIKeygenRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	keygen(t, dir)
	_, code, _, errOut := cli("extension", "keygen", filepath.Join(dir, "test-sign.key"), "--json")
	if code != 1 || !strings.Contains(errOut, "已经存在") {
		t.Fatalf("不覆盖已有私钥：%d %q", code, errOut)
	}
}

func TestCLISignRefusesBrokenExtension(t *testing.T) {
	dir := t.TempDir()
	// 只有清单、没有接口文档：check 不通过，不签名
	raw, err := os.ReadFile(filepath.Join(exampleDir, extension.ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, extension.ManifestFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	keys := keygen(t, dir)
	_, code, _, errOut := cli("extension", "sign", dir, "--key", keys.PrivateKey)
	if code != 1 || !strings.Contains(errOut, "检查没通过，不签名") {
		t.Fatalf("检查不通过时不签名：%d %q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, extension.SignatureFile)); !os.IsNotExist(err) {
		t.Fatal("检查不通过时不该写 signature.json")
	}
}

func TestCLISignVerifyUsage(t *testing.T) {
	dir := copyExampleDir(t)
	// 用法不对：退出码 2，说明怎么用
	for _, args := range [][]string{
		{"extension", "sign"},
		{"extension", "sign", dir},          // 没给 --key
		{"extension", "sign", dir, "--key"}, // --key 后面没值
		{"extension", "verify"},
		{"extension", "verify", dir},                                   // 没给公钥，也没设环境变量
		{"extension", "keygen"},                                        // 没给文件
		{"extension", "keygen", filepath.Join(dir, "k"), "--nope", ""}, // 不认识的参数
	} {
		os.Unsetenv(trustedKeysEnv)
		if _, code, _, errOut := cli(args...); code != 2 || !strings.Contains(errOut, "用法") {
			t.Fatalf("%v：应是退出码 2 并给用法，实际 %d %q", args, code, errOut)
		}
	}
	// 签名者的公钥文件读不了：也是用法问题
	if _, code, _, errOut := cli("extension", "verify", dir, "--key", filepath.Join(dir, "nope.pub")); code != 2 || !strings.Contains(errOut, "读受信任的公钥") {
		t.Fatalf("公钥文件不存在：%d %q", code, errOut)
	}
	// help 里要有新命令
	if _, code, out, _ := cli("help"); code != 0 || !strings.Contains(out, "extension sign") || !strings.Contains(out, "extension verify") || !strings.Contains(out, "extension keygen") {
		t.Fatalf("help 应列出 sign / verify / keygen：%d\n%s", code, out)
	}
}

// 启动时的签名策略校验：强制签名必须同时给受信任的公钥，否则 dsh-ee 不启动。
func TestExtensionTrustedKeys(t *testing.T) {
	if _, err := extensionTrustedKeys(&config.Config{ExtensionRequireSignature: true}); err == nil || !strings.Contains(err.Error(), "DSH_EE_EXTENSION_TRUSTED_KEYS") {
		t.Fatalf("强制签名却没给公钥应报错：%v", err)
	}
	if keys, err := extensionTrustedKeys(&config.Config{}); err != nil || keys != nil {
		t.Fatalf("没配签名策略时没有公钥：%v %v", keys, err)
	}
	dir := t.TempDir()
	keys := keygen(t, dir)
	got, err := extensionTrustedKeys(&config.Config{ExtensionTrustedKeys: keys.PublicKey, ExtensionRequireSignature: true})
	if err != nil || len(got) != 1 || extension.KeyID(got[0]) != keys.KeyID {
		t.Fatalf("读受信任公钥：%v %v", got, err)
	}
	bad := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(bad, []byte("not a key"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := extensionTrustedKeys(&config.Config{ExtensionTrustedKeys: bad}); err == nil || !strings.Contains(err.Error(), "DSH_EE_EXTENSION_TRUSTED_KEYS") {
		t.Fatalf("公钥文件不对应报错并指出环境变量：%v", err)
	}
}
