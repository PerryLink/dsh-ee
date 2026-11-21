package oidc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// 这些测试覆盖 OIDC 出站请求的重定向策略：发现、换令牌、拉签名公钥都由管理员配置的
// issuer 触发，但重定向目标是对方服务器当场给的。跨 scheme/host/port 跟过去，等于把
// token 端点的 client secret（POST body）或至少把「只访问这个 issuer」的预期绕过去。

// oidcJSON 写一段 JSON 响应。
func oidcJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// TestOIDCDiscoverRejectsCrossOriginRedirect：发现配置 302 到另一台主机时不能跟。
// 目标故意返回一份 issuer 与管理员填写完全一致、端点齐全的「合法」发现配置——证明拦住
// 它的是重定向策略，而不是后面的 issuer 一致性校验。
func TestOIDCDiscoverRejectsCrossOriginRedirect(t *testing.T) {
	var originURL atomic.Value
	var destHits atomic.Int32
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destHits.Add(1)
		issuer, _ := originURL.Load().(string)
		oidcJSON(w, map[string]string{
			"issuer":                 issuer,
			"authorization_endpoint": issuer + "/authorize",
			"token_endpoint":         issuer + "/token",
			"jwks_uri":               issuer + "/jwks",
		})
	}))
	defer dest.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL+r.URL.Path, http.StatusFound)
	}))
	defer origin.Close()
	originURL.Store(origin.URL)

	_, err := NewClient(nil).Discover(context.Background(), origin.URL)
	if err == nil {
		t.Fatal("跨 origin 重定向被跟随：发现配置来自另一台主机，Redirect 未受限")
	}
	if !strings.Contains(err.Error(), "重定向") {
		t.Fatalf("错误应说明重定向被拦下：%v", err)
	}
	if n := destHits.Load(); n != 0 {
		t.Fatalf("跨 origin 重定向目标被请求了 %d 次", n)
	}
}

// TestOIDCDiscoverFollowsSameOriginRedirect：issuer 尾斜杠/路径规范化这类同源 302 仍要跟随，
// 避免把常见 IdP 的部署方式一刀切拦掉。
func TestOIDCDiscoverFollowsSameOriginRedirect(t *testing.T) {
	var origin *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/realms/acme/.well-known/openid-configuration", http.StatusFound)
	})
	mux.HandleFunc("GET /realms/acme/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		oidcJSON(w, map[string]string{
			"issuer":                 origin.URL,
			"authorization_endpoint": origin.URL + "/realms/acme/auth",
			"token_endpoint":         origin.URL + "/realms/acme/token",
			"jwks_uri":               origin.URL + "/realms/acme/jwks",
		})
	})
	origin = httptest.NewServer(mux)
	defer origin.Close()

	p, err := NewClient(nil).Discover(context.Background(), origin.URL)
	if err != nil {
		t.Fatalf("同源重定向不应被拦：%v", err)
	}
	if p.TokenURL != origin.URL+"/realms/acme/token" {
		t.Fatalf("发现配置解析不对：%+v", p)
	}
}

// TestOIDCExchangeRejectsCrossOriginRedirect：token 端点 302 到别的主机时，client secret
// 已经 POST 给了配置的端点（这是预期的），但绝不能跟到重定向目标去。
func TestOIDCExchangeRejectsCrossOriginRedirect(t *testing.T) {
	var destHits atomic.Int32
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destHits.Add(1)
		oidcJSON(w, map[string]string{"id_token": "x.y.z", "access_token": "at"})
	}))
	defer dest.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL+"/token", http.StatusFound)
	}))
	defer origin.Close()

	cfg := &Config{ClientID: "dsh-ee", ClientSecret: "idp-secret"}
	p := &Provider{TokenURL: origin.URL + "/token"}
	_, err := NewClient(nil).Exchange(context.Background(), p, cfg, "https://app.example/callback", "code", "verifier", "nonce")
	if err == nil {
		t.Fatal("跨 origin 重定向被跟随：换令牌请求送到了另一台主机")
	}
	if !strings.Contains(err.Error(), "重定向") {
		t.Fatalf("错误应说明重定向被拦下：%v", err)
	}
	if n := destHits.Load(); n != 0 {
		t.Fatalf("跨 origin 重定向目标被请求了 %d 次", n)
	}
}

// TestOIDCJWKSRejectsCrossOriginRedirect：jwks_uri 302 到别的主机时不能跟过去拉公钥。
func TestOIDCJWKSRejectsCrossOriginRedirect(t *testing.T) {
	var destHits atomic.Int32
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destHits.Add(1)
		oidcJSON(w, map[string]any{"keys": []any{}})
	}))
	defer dest.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL+r.URL.Path, http.StatusFound)
	}))
	defer origin.Close()

	_, err := NewClient(nil).jwks(context.Background(), origin.URL+"/jwks", false)
	if err == nil {
		t.Fatal("跨 origin 重定向被跟随：签名公钥从另一台主机拉取")
	}
	if !strings.Contains(err.Error(), "重定向") {
		t.Fatalf("错误应说明重定向被拦下：%v", err)
	}
	if n := destHits.Load(); n != 0 {
		t.Fatalf("跨 origin 重定向目标被请求了 %d 次", n)
	}
}

// TestOIDCExchangeFollowsSameOriginRedirect：同源换路径（反向代理常见）仍要跟随，
// 证明限制的是跨来源，不是禁掉一切重定向。
func TestOIDCExchangeFollowsSameOriginRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		// 307 保留 POST；302 会被 Go 改成 GET，这跟重定向来源无关，别把测试写岔。
		http.Redirect(w, r, "/token2", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("POST /token2", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		oidcJSON(w, map[string]string{"error": "invalid_grant"})
	})
	origin := httptest.NewServer(mux)
	defer origin.Close()

	p := &Provider{TokenURL: origin.URL + "/token"}
	_, err := NewClient(nil).Exchange(context.Background(), p, &Config{ClientID: "dsh-ee"}, "https://app.example/callback", "code", "verifier", "nonce")
	if err == nil {
		t.Fatal("token2 返回 400，Exchange 不应成功")
	}
	if strings.Contains(err.Error(), "重定向") {
		t.Fatalf("同源重定向不应被拦：%v", err)
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("应跟随后拿到 token2 的错误，实际：%v", err)
	}
}

// TestOIDCNewClientRedirectPolicy：不修改调用方传入的 client（避免影响共享 client 的其它
// 请求），默认补上同来源重定向限制；调用方自带策略时保留。
func TestOIDCNewClientRedirectPolicy(t *testing.T) {
	injected := &http.Client{Timeout: 5}
	c := NewClient(injected)
	if injected.CheckRedirect != nil {
		t.Fatal("不应修改调用方传入的 client")
	}
	if c.HTTP == injected {
		t.Fatal("应复制一份 client 再改，避免改到调用方")
	}
	if c.HTTP.CheckRedirect == nil {
		t.Fatal("默认应带上跨来源重定向限制")
	}
	if c.HTTP.Timeout != 5 {
		t.Fatalf("调用方配置的 Timeout 丢了：%v", c.HTTP.Timeout)
	}

	// 调用方显式设置了自己的重定向策略：保留，不被覆盖。
	var sawRedirect atomic.Int32
	own := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		sawRedirect.Add(1)
		return http.ErrUseLastResponse
	}}
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "final")
	}))
	defer dest.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL+"/final", http.StatusFound)
	}))
	defer origin.Close()

	resp, err := NewClient(own).HTTP.Get(origin.URL)
	if err != nil {
		t.Fatalf("调用方自己的 CheckRedirect 返回 ErrUseLastResponse，不应报错：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound || sawRedirect.Load() != 1 {
		t.Fatalf("调用方自己的重定向策略应被保留：status=%d calls=%d", resp.StatusCode, sawRedirect.Load())
	}
}
