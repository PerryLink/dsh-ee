package extension

import (
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// newSigningKey 生成一把测试用的签名密钥。
func newSigningKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

// signExample 给仓库里的声明式示例签名，返回带 signature.json 的文件。
func signExample(t *testing.T, priv ed25519.PrivateKey) map[string][]byte {
	t.Helper()
	res := CheckFiles(exampleFiles(t))
	if !res.OK {
		t.Fatalf("示例应通过检查：%+v", res.Results)
	}
	sig, raw, err := SignExtension([]byte(exampleFiles(t)[ManifestFile]), res.OpenAPIDoc, priv)
	if err != nil {
		t.Fatal(err)
	}
	if sig.Identity != res.Identity || sig.Digest != res.Digest {
		t.Fatalf("签名里的身份和摘要要和检查结果一致：%+v / %s %s", sig, res.Identity, res.Digest)
	}
	files := exampleFiles(t)
	files[SignatureFile] = raw
	return files
}

func TestSignAndVerify(t *testing.T) {
	pub, priv := newSigningKey(t)
	files := signExample(t, priv)
	got := VerifyFiles(files, []ed25519.PublicKey{pub})
	if got.Status != SignatureValid || got.KeyID != KeyID(pub) {
		t.Fatalf("签名应通过：%+v", got)
	}
	// 目录和 fs.FS 两种读法
	dir := t.TempDir()
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := VerifyDir(dir, []ed25519.PublicKey{pub}); got.Status != SignatureValid {
		t.Fatalf("VerifyDir：%+v", got)
	}
	fsys := fstest.MapFS{}
	for name, b := range files {
		fsys[name] = &fstest.MapFile{Data: b}
	}
	if got := Verify(fsys, []ed25519.PublicKey{pub}); got.Status != SignatureValid {
		t.Fatalf("Verify：%+v", got)
	}
}

func TestVerifyUnsigned(t *testing.T) {
	pub, _ := newSigningKey(t)
	got := VerifyFiles(exampleFiles(t), []ed25519.PublicKey{pub})
	if got.Status != SignatureUnsigned || got.Message == "" {
		t.Fatalf("没有签名时是 unsigned：%+v", got)
	}
}

func TestVerifyTampered(t *testing.T) {
	pub, priv := newSigningKey(t)
	// 改接口文档一个字节：摘要变了，签名对不上
	files := signExample(t, priv)
	files["openapi.yaml"] = append(files["openapi.yaml"], ' ')
	if got := VerifyFiles(files, []ed25519.PublicKey{pub}); got.Status != SignatureInvalid || got.Message == "" {
		t.Fatalf("改了接口文档应验不过：%+v", got)
	}
	// 改清单：身份或摘要都会变
	files = signExample(t, priv)
	files[ManifestFile] = []byte(string(files[ManifestFile]) + " ")
	if got := VerifyFiles(files, []ed25519.PublicKey{pub}); got.Status != SignatureInvalid {
		t.Fatalf("改了清单应验不过：%+v", got)
	}
}

// 换了扩展身份（把签名挪给另一个扩展）也验不过：签名把身份和摘要绑在一起。
func TestVerifyMovedToAnotherExtension(t *testing.T) {
	pub, priv := newSigningKey(t)
	signed := signExample(t, priv)
	raw, _ := json.Marshal(good())
	other := map[string][]byte{ManifestFile: raw, "openapi.json": []byte(goodDoc), SignatureFile: signed[SignatureFile]}
	if got := VerifyFiles(other, []ed25519.PublicKey{pub}); got.Status != SignatureInvalid {
		t.Fatalf("别的扩展的签名不该通过：%+v", got)
	}
}

func TestVerifyTrust(t *testing.T) {
	pub, priv := newSigningKey(t)
	files := signExample(t, priv)
	// 签名本身是对的，但不是这把钥匙签的
	otherPub, _ := newSigningKey(t)
	got := VerifyFiles(files, []ed25519.PublicKey{otherPub})
	if got.Status != SignatureInvalid || got.KeyID != KeyID(pub) {
		t.Fatalf("签名者不在受信任的公钥里应是 invalid：%+v", got)
	}
	// 一把受信任的公钥都没配：签了名也不能当 valid
	if got := VerifyFiles(files, nil); got.Status != SignatureInvalid {
		t.Fatalf("没有受信任的公钥时应是 invalid：%+v", got)
	}
}

func TestVerifyMalformedSignature(t *testing.T) {
	pub, _ := newSigningKey(t)
	cases := map[string][]byte{
		"不是 JSON":     []byte("not json"),
		"缺少字段":        []byte(`{"version":1,"alg":"ed25519"}`),
		"版本不认识":       []byte(`{"version":2,"alg":"ed25519","key_id":"x","identity":"a/b@1.0.0","digest":"d","signature":"s"}`),
		"算法不认识":       []byte(`{"version":1,"alg":"rsa","key_id":"x","identity":"a/b@1.0.0","digest":"d","signature":"s"}`),
		"签名不是 base64": []byte(`{"version":1,"alg":"ed25519","key_id":"` + KeyID(pub) + `","identity":"dsh-ee/demo-itsm@0.1.0","digest":"x","signature":"!!!"}`),
	}
	for what, raw := range cases {
		files := exampleFiles(t)
		files[SignatureFile] = raw
		if got := VerifyFiles(files, []ed25519.PublicKey{pub}); got.Status != SignatureInvalid || got.Message == "" {
			t.Fatalf("%s：应是 invalid 并说明原因，实际 %+v", what, got)
		}
	}
}

// 签名文件里的 digest 被改（哪怕签名本身是合法的）也验不过：digest 要等于实际内容算出来的。
func TestVerifyDigestMismatch(t *testing.T) {
	pub, priv := newSigningKey(t)
	res := CheckFiles(exampleFiles(t))
	_, raw, err := SignExtension(exampleFiles(t)[ManifestFile], res.OpenAPIDoc, priv)
	if err != nil {
		t.Fatal(err)
	}
	var sig Signature
	if err := json.Unmarshal(raw, &sig); err != nil {
		t.Fatal(err)
	}
	sig.Digest = Digest([]byte("x"), nil)
	files := exampleFiles(t)
	files[SignatureFile], _ = json.Marshal(sig)
	if got := VerifyFiles(files, []ed25519.PublicKey{pub}); got.Status != SignatureInvalid {
		t.Fatalf("摘要对不上应是 invalid：%+v", got)
	}
}

func TestKeyPEM(t *testing.T) {
	pub, priv := newSigningKey(t)
	privPEM, err := MarshalPrivateKeyPEM(priv)
	if err != nil {
		t.Fatal(err)
	}
	pubPEM, err := MarshalPublicKeyPEM(pub)
	if err != nil {
		t.Fatal(err)
	}
	gotPriv, err := ParsePrivateKeyPEM(privPEM)
	if err != nil || !gotPriv.Equal(priv) {
		t.Fatalf("私钥 PEM 读回来不对：%v", err)
	}
	// 公钥文件：一把公钥
	keys, err := ParsePublicKeysPEM(pubPEM)
	if err != nil || len(keys) != 1 || KeyID(keys[0]) != KeyID(pub) {
		t.Fatalf("公钥 PEM：%v %v", keys, err)
	}
	// 一个文件里放两把（也允许直接拿私钥文件当公钥用）
	otherPub, otherPriv := newSigningKey(t)
	otherPEM, _ := MarshalPrivateKeyPEM(otherPriv)
	both := append(append([]byte{}, pubPEM...), otherPEM...)
	keys, err = ParsePublicKeysPEM(both)
	if err != nil || len(keys) != 2 || KeyID(keys[1]) != KeyID(otherPub) {
		t.Fatalf("多把公钥：%v %v", keys, err)
	}
	// 私钥文件直接用
	keys, err = ParsePublicKeysPEM(privPEM)
	if err != nil || len(keys) != 1 || KeyID(keys[0]) != KeyID(pub) {
		t.Fatalf("从私钥取公钥：%v %v", keys, err)
	}
	// LoadPublicKeys / LoadPrivateKey
	dir := t.TempDir()
	privFile, pubFile := filepath.Join(dir, "sign.key"), filepath.Join(dir, "sign.pub")
	if err := os.WriteFile(privFile, privPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pubFile, pubPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if k, err := LoadPrivateKey(privFile); err != nil || !k.Equal(priv) {
		t.Fatalf("LoadPrivateKey：%v", err)
	}
	if ks, err := LoadPublicKeys(pubFile); err != nil || len(ks) != 1 {
		t.Fatalf("LoadPublicKeys：%v", err)
	}
	// 不是 PEM、或者不是 Ed25519：给出能看懂的错误
	if _, err := ParsePublicKeysPEM([]byte("hello")); err == nil {
		t.Fatal("不是 PEM 应报错")
	}
	if _, err := ParsePrivateKeyPEM([]byte("hello")); err == nil {
		t.Fatal("没有私钥应报错")
	}
}

// 签名对象带了身份：同一份内容换个身份，签出来的 payload 不一样。
func TestSignedPayload(t *testing.T) {
	if string(SignedPayload("a/b@1.0.0", "abc")) != "a/b@1.0.0:abc" {
		t.Fatal("签名对象应是 身份:摘要")
	}
	if string(SignedPayload("a/b@1.0.0", "abc")) == string(SignedPayload("a/b@1.0.0", "abd")) {
		t.Fatal("摘要不同，签名对象应不同")
	}
}

// 清单里没有 id / version 时不能签，免得签出一个身份不明的包。
func TestSignNeedsIdentity(t *testing.T) {
	_, priv := newSigningKey(t)
	if _, _, err := SignExtension([]byte(`{"version":"1.0.0"}`), nil, priv); err == nil {
		t.Fatal("没有 id 应报错")
	}
	if _, _, err := SignExtension([]byte(`not json`), nil, priv); err == nil {
		t.Fatal("清单不是 JSON 应报错")
	}
	if _, _, err := SignExtension(exampleFiles(t)[ManifestFile], nil, ed25519.PrivateKey("short")); err == nil {
		t.Fatal("长度不对的私钥应报错")
	}
}

// 受信任公钥的状态（docs/plugin-signing.md §3）：换下的仍认历史签名，撤销的标 revoked 且不能新装。
func TestVerifyKeyStates(t *testing.T) {
	pub, priv := newSigningKey(t)
	files := signExample(t, priv)
	cases := []struct {
		name     string
		state    string
		want     string
		wantKey  string
		contains string
	}{
		{"空状态当正在使用", "", SignatureValid, KeyStateActive, "在受信任的公钥里"},
		{"正在使用", KeyStateActive, SignatureValid, KeyStateActive, "在受信任的公钥里"},
		{"已换下", KeyStateRetired, SignatureValid, KeyStateRetired, "已换下"},
		{"已撤销", KeyStateRevoked, SignatureRevoked, KeyStateRevoked, "已撤销"},
	}
	for _, tc := range cases {
		got := VerifyFilesWithKeys(files, []TrustedKey{{Public: pub, State: tc.state}})
		if got.Status != tc.want || got.KeyState != tc.wantKey || got.KeyID != KeyID(pub) || !strings.Contains(got.Message, tc.contains) {
			t.Fatalf("%s：签名状态 %+v，期望 %s / %s / %q", tc.name, got, tc.want, tc.wantKey, tc.contains)
		}
	}
	// 撤销之后就算签名本身对，也不能当成有效
	if got := VerifyFilesWithKeys(files, []TrustedKey{{Public: pub, State: KeyStateRevoked}}); got.Status == SignatureValid {
		t.Fatalf("撤销的公钥不该给出 valid：%+v", got)
	}
	// 撤销的公钥 + 内容被改：还是 invalid（改过比撤销更严重）
	tampered := signExample(t, priv)
	tampered["openapi.yaml"] = append(tampered["openapi.yaml"], ' ')
	if got := VerifyFilesWithKeys(tampered, []TrustedKey{{Public: pub, State: KeyStateRevoked}}); got.Status != SignatureInvalid {
		t.Fatalf("内容被改应是 invalid：%+v", got)
	}
}

func TestParsePublicKeyPEM(t *testing.T) {
	pub, priv := newSigningKey(t)
	pubPEM, _ := MarshalPublicKeyPEM(pub)
	if k, err := ParsePublicKeyPEM(pubPEM); err != nil || !k.Equal(pub) {
		t.Fatalf("读公钥：%v", err)
	}
	privPEM, _ := MarshalPrivateKeyPEM(priv)
	if _, err := ParsePublicKeyPEM(privPEM); err == nil || !strings.Contains(err.Error(), "不要贴私钥") {
		t.Fatalf("私钥不该当公钥收：%v", err)
	}
	if _, err := ParsePublicKeyPEM([]byte("not pem")); err == nil {
		t.Fatal("不是 PEM 应报错")
	}
}
