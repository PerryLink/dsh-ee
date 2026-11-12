package sdk

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ee_jwt 的验签：只认 RS256，按 kid 在 dsh-ee 公开的 JWKS 里找公钥，再查 exp / nbf / aud（和可选的 iss）。
// 和 dsh-ee 服务端 internal/eejwt 的 Verify 是同一套规则。

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type keySet struct {
	url string
	hc  *http.Client

	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	fetched time.Time
}

// 不认识的 kid 会触发重新取 JWKS（dsh-ee 轮换了密钥），但最多每 10 秒一次，免得被乱填的 kid 打爆。
const jwksMinInterval = 10 * time.Second

func (ks *keySet) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if k := ks.keys[kid]; k != nil {
		return k, nil
	}
	if !ks.fetched.IsZero() && time.Since(ks.fetched) < jwksMinInterval {
		return nil, fmt.Errorf("JWKS 里没有 kid 为 %q 的公钥", kid)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ks.url, nil)
	if err != nil {
		return nil, err
	}
	hc := ks.hc
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("取 dsh-ee 的 JWKS（%s）失败：%w", ks.url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("取 dsh-ee 的 JWKS（%s）失败：HTTP %d", ks.url, resp.StatusCode)
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, fmt.Errorf("dsh-ee 的 JWKS 读不了：%w", err)
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(n) == 0 || len(e) == 0 || len(e) > 4 {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	ks.keys, ks.fetched = keys, time.Now()
	if k := keys[kid]; k != nil {
		return k, nil
	}
	return nil, fmt.Errorf("JWKS 里没有 kid 为 %q 的公钥", kid)
}

// verify 校验令牌并返回声明。
func (ks *keySet) verify(ctx context.Context, token, audience, issuer string, now time.Time) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("令牌格式不对")
	}
	var head struct{ Alg, Kid string }
	if raw, err := base64.RawURLEncoding.DecodeString(parts[0]); err != nil || json.Unmarshal(raw, &head) != nil {
		return nil, errors.New("令牌头解析失败")
	}
	if head.Alg != "RS256" {
		return nil, fmt.Errorf("不接受签名算法 %q", head.Alg)
	}
	pub, err := ks.key(ctx, head.Kid)
	if err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("签名解析失败")
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		return nil, errors.New("签名不对")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("令牌内容解析失败")
	}
	claims := map[string]any{}
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil, errors.New("令牌内容解析失败")
	}
	exp, ok := claims["exp"].(float64)
	if !ok || !now.Before(time.Unix(int64(exp), 0)) {
		return nil, errors.New("令牌已过期")
	}
	if nbf, ok := claims["nbf"].(float64); ok && now.Before(time.Unix(int64(nbf), 0).Add(-time.Minute)) {
		return nil, errors.New("令牌还没生效")
	}
	if issuer != "" && claims["iss"] != issuer {
		return nil, fmt.Errorf("签发方不对：%v", claims["iss"])
	}
	if claims["aud"] != audience {
		return nil, fmt.Errorf("这个令牌不是签给 %s 的", audience)
	}
	return claims, nil
}
