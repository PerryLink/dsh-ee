package extension

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// 扩展的签名（docs/plugins.md §9.3）：作者用私钥给清单（声明式扩展连同接口文档）签一个
// detached 签名，放在扩展目录的 signature.json 里；安装时 dsh-ee 用受信任的公钥核对。
//
// 签名对象是 "<id>@<version>:<digest>"：身份和摘要绑在一起，换身份、改内容都验不过，
// 也不能把别的扩展的签名挪过来用。digest 用的就是 Digest（清单和接口文档的 sha256），
// 和安装计划里显示的、升级时判断内容有没有变的是同一个口径。
//
// 私钥只在作者本地：dsh-ee 不生成、不托管、不入库，只保存受信任的公钥。

// SignatureFile 是扩展的签名文件名，和清单放在同一个目录。
const SignatureFile = "signature.json"

// 签名格式：目前只有一种，版本没变、算法只有 Ed25519。
const (
	signatureVersion = 1
	SignatureAlg     = "ed25519"
)

// 签名状态。安装计划里要分开显示，不能合并成一个「可信」。
const (
	// SignatureUnsigned 没有 signature.json。
	SignatureUnsigned = "unsigned"
	// SignatureValid 签名对得上，签名者也在受信任的公钥里（公钥可能是 active 或 retired）。
	SignatureValid = "valid"
	// SignatureInvalid 有签名，但格式不对、内容被改过、或者签名者不受信任。
	SignatureInvalid = "invalid"
	// SignatureRevoked 签名对得上，但签名者的公钥已撤销：历史签名仍能核对，但不能用这个签名
	// 新装或升级扩展（docs/plugin-signing.md §3 的信任根生命周期）。
	SignatureRevoked = "revoked"
)

// 受信任公钥的状态。
const (
	// KeyStateActive 正在使用，可以用来验新装和新签名。
	KeyStateActive = "active"
	// KeyStateRetired 已换下：不再用于新签名，但历史签名仍认（换钥匙时保留一段时间的旧公钥）。
	KeyStateRetired = "retired"
	// KeyStateRevoked 已撤销：等于不信任；历史签名标注 revoked，新装 / 升级拒绝。
	KeyStateRevoked = "revoked"
)

// TrustedKey 是验签时可用的一把公钥及其状态。
// 命令行 `--key`、DSH_EE_EXTENSION_TRUSTED_KEYS 里的公钥都是 active；库里的记录可能是
// retired / revoked，用来区分「历史签名」和「还能不能新装」。
type TrustedKey struct {
	Public ed25519.PublicKey
	// active / retired / revoked；空当 active
	State string
}

// keyStateOf 取公钥状态，空当正在使用。
func keyStateOf(k TrustedKey) string {
	if k.State == "" {
		return KeyStateActive
	}
	return k.State
}

// Signature 是 signature.json 的内容。
type Signature struct {
	Version int `json:"version"`
	// 算法，目前只有 ed25519
	Alg string `json:"alg"`
	// 签名者的 key id：公钥的 sha256 前 16 位十六进制
	KeyID string `json:"key_id"`
	// 扩展身份 作者/名字@版本
	Identity string `json:"identity"`
	// 清单（声明式扩展连同接口文档）的 sha256，即 Digest 的结果
	Digest string `json:"digest"`
	// 对 SignedPayload(Identity, Digest) 的 Ed25519 签名，base64
	Signature string `json:"signature"`
}

// SignatureStatus 是安装计划、`extension verify` 里显示的签名状态。
type SignatureStatus struct {
	// unsigned / valid / invalid
	Status string `json:"status"`
	// 签名文件里写的 key id（有的话）
	KeyID string `json:"key_id,omitempty"`
	// 签名文件里写的扩展身份（有的话）
	Identity string `json:"identity,omitempty"`
	// 签名者的公钥状态：active / retired / revoked；签名对得上、公钥在受信任列表里时才有
	KeyState string `json:"key_state,omitempty"`
	// 给人看的一句话：为什么是这个状态、怎么处理
	Message string `json:"message,omitempty"`
}

// unsigned 没有签名。
func unsignedStatus(msg string) *SignatureStatus {
	return &SignatureStatus{Status: SignatureUnsigned, Message: msg}
}

// invalidStatus 签名没通过。
func invalidStatus(msg string) *SignatureStatus {
	return &SignatureStatus{Status: SignatureInvalid, Message: msg}
}

// invalidStatusOf 签名没通过，但能看出是谁签的（给管理员排查用）。
func invalidStatusOf(sig *Signature, msg string) *SignatureStatus {
	st := invalidStatus(msg)
	st.KeyID, st.Identity = sig.KeyID, sig.Identity
	return st
}

// SignedPayload 是真正被签的字节。用身份和摘要拼起来，而不是直接签 signature.json：
// 签名文件里的字段（key_id、alg 之类）可以改而不破坏签名，也不会把签名挪到别的扩展上去。
func SignedPayload(identity, digest string) []byte {
	return []byte(identity + ":" + digest)
}

// KeyID 返回公钥的 key id：sha256 前 16 位十六进制。
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])[:16]
}

// SignExtension 给清单（声明式扩展连同接口文档）签名，返回 signature.json 的内容。
// 私钥只在调用方手里；dsh-ee 不保存它。
func SignExtension(manifest, openapi []byte, priv ed25519.PrivateKey) (*Signature, []byte, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, nil, errors.New("私钥不对：要一个 Ed25519 私钥")
	}
	m, err := ParseManifest(manifest)
	if err != nil {
		return nil, nil, err
	}
	if m.ID == "" || m.Version == "" {
		return nil, nil, errors.New("清单里缺少 id 或 version，没法签名")
	}
	identity := m.Identity()
	digest := Digest(manifest, openapi)
	pub := priv.Public().(ed25519.PublicKey)
	sig := &Signature{
		Version:   signatureVersion,
		Alg:       SignatureAlg,
		KeyID:     KeyID(pub),
		Identity:  identity,
		Digest:    digest,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, SignedPayload(identity, digest))),
	}
	raw, err := json.MarshalIndent(sig, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return sig, append(raw, '\n'), nil
}

// ActiveKeys 把一组公钥都当成 active（正在使用），给命令行和 DSH_EE_EXTENSION_TRUSTED_KEYS 用。
func ActiveKeys(pubs []ed25519.PublicKey) []TrustedKey {
	out := make([]TrustedKey, 0, len(pubs))
	for _, p := range pubs {
		out = append(out, TrustedKey{Public: p})
	}
	return out
}

// VerifyFiles 核对一个扩展的签名。trusted 是受信任的公钥（都当成 active）；没有配置时，
// 签了名的扩展也只会是 invalid（签名者不受信任），而不是 valid。
func VerifyFiles(files map[string][]byte, trusted []ed25519.PublicKey) *SignatureStatus {
	return VerifyFilesWithKeys(files, ActiveKeys(trusted))
}

// Verify 核对一个扩展目录的签名，给 `dsh-ee extension verify` 用。
func Verify(files fs.FS, trusted []ed25519.PublicKey) *SignatureStatus {
	return VerifyWithKeys(files, ActiveKeys(trusted))
}

// VerifyDir 读取扩展目录做验签。
func VerifyDir(dir string, trusted []ed25519.PublicKey) *SignatureStatus {
	return VerifyDirWithKeys(dir, ActiveKeys(trusted))
}

// VerifyFilesWithKeys 核对一个扩展的签名，公钥带状态（active / retired / revoked）。
func VerifyFilesWithKeys(files map[string][]byte, trusted []TrustedKey) *SignatureStatus {
	read := func(name string) ([]byte, bool) {
		b, ok := files[name]
		return b, ok
	}
	return verify(read, trusted)
}

// VerifyWithKeys 核对一个扩展目录的签名，公钥带状态。
func VerifyWithKeys(files fs.FS, trusted []TrustedKey) *SignatureStatus {
	read := func(name string) ([]byte, bool) {
		b, err := fs.ReadFile(files, name)
		return b, err == nil
	}
	return verify(read, trusted)
}

// VerifyDirWithKeys 读取扩展目录做验签，公钥带状态。
func VerifyDirWithKeys(dir string, trusted []TrustedKey) *SignatureStatus {
	read := func(name string) ([]byte, bool) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		return b, err == nil
	}
	return verify(read, trusted)
}

// verify 是验签的公共部分。read 返回扩展根目录下的一个文件。
func verify(read func(name string) ([]byte, bool), trusted []TrustedKey) *SignatureStatus {
	raw, ok := read(SignatureFile)
	if !ok {
		return unsignedStatus("没有 " + SignatureFile + "：这个扩展没有签名")
	}
	manifestRaw, ok := read(ManifestFile)
	if !ok {
		return invalidStatus("有签名，但找不到 " + ManifestFile + "，没法核对")
	}
	m, err := ParseManifest(manifestRaw)
	if err != nil {
		return invalidStatus("有签名，但清单读不了：" + err.Error())
	}
	var openapi []byte
	if m.OpenAPI != "" {
		if b, ok := read(cleanRelPath(m.OpenAPI)); ok {
			openapi = b
		}
	}
	sig, reason := parseSignature(raw)
	if reason != "" {
		return invalidStatus(reason)
	}
	if sig.Identity != m.Identity() {
		return invalidStatusOf(sig, fmt.Sprintf("签名是给 %s 的，这份清单是 %s", sig.Identity, m.Identity()))
	}
	if want := Digest(manifestRaw, openapi); sig.Digest != want {
		return invalidStatusOf(sig, "签名对不上清单或接口文档：内容可能被改过")
	}
	got, err := base64.StdEncoding.DecodeString(sig.Signature)
	if err != nil || len(got) != ed25519.SignatureSize {
		return invalidStatusOf(sig, "签名不是合法的 base64 或长度不对")
	}
	byID := map[string]TrustedKey{}
	for _, k := range trusted {
		if len(k.Public) != ed25519.PublicKeySize {
			continue
		}
		if id := KeyID(k.Public); byID[id].Public == nil {
			byID[id] = TrustedKey{Public: k.Public, State: keyStateOf(k)}
		}
	}
	key, ok := byID[sig.KeyID]
	if !ok {
		if len(byID) == 0 {
			return invalidStatusOf(sig, "签名者 "+sig.KeyID+" 不受信任：dsh-ee 还没有配置受信任的签名公钥")
		}
		return invalidStatusOf(sig, "签名者 "+sig.KeyID+" 不在受信任的公钥里")
	}
	if !ed25519.Verify(key.Public, SignedPayload(sig.Identity, sig.Digest), got) {
		return invalidStatusOf(sig, "签名和内容对不上：可能被改过")
	}
	switch key.State {
	case KeyStateRevoked:
		return &SignatureStatus{Status: SignatureRevoked, KeyID: sig.KeyID, Identity: sig.Identity,
			KeyState: KeyStateRevoked, Message: "签名者的公钥 " + sig.KeyID + " 已撤销：历史签名仍能核对，但不能用它新装或升级扩展"}
	case KeyStateRetired:
		return &SignatureStatus{Status: SignatureValid, KeyID: sig.KeyID, Identity: sig.Identity, KeyState: KeyStateRetired,
			Message: "签名者 " + sig.KeyID + " 的公钥已换下，仍认这个签名；新签名请让作者换用新公钥"}
	}
	return &SignatureStatus{Status: SignatureValid, KeyID: sig.KeyID, Identity: sig.Identity, KeyState: KeyStateActive,
		Message: "签名者 " + sig.KeyID + " 在受信任的公钥里"}
}

// parseSignature 读 signature.json 并检查格式；格式不对时返回给人看的原因。
func parseSignature(raw []byte) (*Signature, string) {
	var sig Signature
	if err := json.Unmarshal(raw, &sig); err != nil {
		return nil, "signature.json 不是合法的 JSON：" + err.Error()
	}
	switch {
	case sig.Version != signatureVersion:
		return nil, fmt.Sprintf("不认识的签名版本 %d：dsh-ee 现在只认 %d", sig.Version, signatureVersion)
	case sig.Alg != SignatureAlg:
		return nil, "不认识的签名算法「" + sig.Alg + "」：dsh-ee 只认 " + SignatureAlg
	case sig.KeyID == "" || sig.Identity == "" || sig.Digest == "" || sig.Signature == "":
		return nil, "signature.json 缺少 key_id、identity、digest 或 signature 字段"
	}
	return &sig, ""
}

// LoadPublicKeys 从 PEM 文件读受信任的公钥。文件里可以有多把，公钥、私钥（从中取公钥）、
// 证书都可以，方便直接拿签名用的私钥文件来核对。
func LoadPublicKeys(path string) ([]ed25519.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	keys, err := ParsePublicKeysPEM(raw)
	if err != nil {
		return nil, fmt.Errorf("%s：%w", path, err)
	}
	return keys, nil
}

// ParsePublicKeyPEM 解析 PEM 里的一把 Ed25519 公钥（只接受公钥或证书，不接受私钥）。
// 管理员在 API 里新增受信任公钥时用：私钥不该贴进来。
func ParsePublicKeyPEM(raw []byte) (ed25519.PublicKey, error) {
	for rest := raw; len(rest) > 0; {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = next
		switch block.Type {
		case "PUBLIC KEY":
			k, err := x509.ParsePKIXPublicKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("读公钥：%w", err)
			}
			if p, ok := k.(ed25519.PublicKey); ok {
				return p, nil
			}
		case "CERTIFICATE":
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("读证书：%w", err)
			}
			if p, ok := cert.PublicKey.(ed25519.PublicKey); ok {
				return p, nil
			}
		}
	}
	return nil, errors.New("文件里没有 Ed25519 公钥（PEM 格式的 PUBLIC KEY 或 CERTIFICATE）；不要贴私钥")
}

// ParsePublicKeysPEM 解析 PEM 格式的公钥（可以有多块，公钥 / 私钥 / 证书都行）。
func ParsePublicKeysPEM(raw []byte) ([]ed25519.PublicKey, error) {
	var out []ed25519.PublicKey
	seen := map[string]bool{}
	for rest := raw; len(rest) > 0; {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = next
		var pub ed25519.PublicKey
		switch block.Type {
		case "PUBLIC KEY":
			k, err := x509.ParsePKIXPublicKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("读公钥：%w", err)
			}
			if p, ok := k.(ed25519.PublicKey); ok {
				pub = p
			}
		case "CERTIFICATE":
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("读证书：%w", err)
			}
			if p, ok := cert.PublicKey.(ed25519.PublicKey); ok {
				pub = p
			}
		case "PRIVATE KEY":
			k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("读私钥：%w", err)
			}
			if p, ok := k.(ed25519.PrivateKey); ok {
				pub = p.Public().(ed25519.PublicKey)
			}
		}
		if pub == nil {
			continue
		}
		if id := KeyID(pub); !seen[id] {
			seen[id] = true
			out = append(out, pub)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("文件里没有 Ed25519 公钥（PEM 格式）")
	}
	return out, nil
}

// LoadPrivateKey 从 PEM 文件读签名用的 Ed25519 私钥。
func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParsePrivateKeyPEM(raw)
}

// ParsePrivateKeyPEM 解析 PEM 格式的 Ed25519 私钥（PKCS#8）。
func ParsePrivateKeyPEM(raw []byte) (ed25519.PrivateKey, error) {
	for rest := raw; len(rest) > 0; {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = next
		if block.Type != "PRIVATE KEY" {
			continue
		}
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("读私钥：%w", err)
		}
		p, ok := k.(ed25519.PrivateKey)
		if !ok {
			return nil, errors.New("这是一把私钥，但不是 Ed25519 的")
		}
		return p, nil
	}
	return nil, errors.New("文件里没有 Ed25519 私钥（PEM 格式，PKCS#8）；可以用 dsh-ee extension keygen 生成")
}

// GenerateKey 生成一把签名用的 Ed25519 密钥。只有命令行 keygen 用；dsh-ee 服务端不生成、不保存私钥。
func GenerateKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// MarshalPrivateKeyPEM 把私钥写成 PEM（PKCS#8）。
func MarshalPrivateKeyPEM(priv ed25519.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// MarshalPublicKeyPEM 把公钥写成 PEM（PKIX）。
func MarshalPublicKeyPEM(pub ed25519.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}
