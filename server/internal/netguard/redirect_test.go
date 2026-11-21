package netguard

import (
	"net/http"
	"strings"
	"testing"
)

func TestSameOrigin(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"http://a.example/x", "http://a.example/y", true},
		{"http://a.example", "http://a.example:80/x", true},
		{"https://a.example", "https://a.example:443/x", true},
		{"http://A.Example", "http://a.example", true},
		{"https://a.example", "http://a.example", false},
		{"https://a.example:8443", "https://a.example", false},
		{"http://a.example", "http://b.example", false},
		{"http://a.example", "::bad-url", false},
	}
	for _, c := range cases {
		if got := SameOrigin(c.a, c.b); got != c.want {
			t.Errorf("SameOrigin(%q, %q) = %v，want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestSameOriginRedirect(t *testing.T) {
	newReq := func(rawURL string) *http.Request {
		req, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		return req
	}

	initial := newReq("https://origin.example/-/ping")

	// 同来源换路径：放行。
	if err := SameOriginRedirect(newReq("https://origin.example/ping2"), []*http.Request{initial}); err != nil {
		t.Fatalf("同来源重定向不应被拦：%v", err)
	}
	// 跨 host / 跨端口 / 跨 scheme：拒绝，并给出可读错误。
	for _, target := range []string{
		"https://other.example/ping",
		"https://origin.example:8443/ping",
		"http://origin.example/ping",
	} {
		err := SameOriginRedirect(newReq(target), []*http.Request{initial})
		if err == nil {
			t.Fatalf("跳到 %s 应被拒绝", target)
		}
		if !strings.Contains(err.Error(), "重定向") || !strings.Contains(err.Error(), "另一个地址") {
			t.Fatalf("跳到 %s 的错误不可读：%v", target, err)
		}
	}
	// 跳数上限与空 via（理论上不会由 net/http 传空，防御性处理）。
	if err := SameOriginRedirect(newReq("https://origin.example/x"), make([]*http.Request, 5)); err == nil {
		t.Fatal("超过 5 跳应被拒绝")
	}
	if err := SameOriginRedirect(initial, nil); err != nil {
		t.Fatalf("via 为空不应报错：%v", err)
	}
}
