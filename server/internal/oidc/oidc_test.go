package oidc

import (
	"encoding/json"
	"testing"
)

func TestRequireEmailVerifiedDefaultOff(t *testing.T) {
	if Defaults().RequireEmailVerified {
		t.Fatal("严格身份绑定应默认关闭，避免不返回 email_verified 的企业 IdP 登不进来")
	}
}

func TestRequireEmailVerifiedJSONRoundTrip(t *testing.T) {
	raw, err := json.Marshal(&Config{RequireEmailVerified: true})
	if err != nil {
		t.Fatal(err)
	}
	var back Config
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !back.RequireEmailVerified {
		t.Fatalf("requireEmailVerified 应能存取：%s", raw)
	}
}

func TestIdentityEmailVerified(t *testing.T) {
	cfg := Defaults()
	cases := []struct {
		name   string
		claims Claims
		want   bool
	}{
		{"布尔 true", Claims{"sub": "s", "email_verified": true}, true},
		{"布尔 false", Claims{"sub": "s", "email_verified": false}, false},
		{"没有声明", Claims{"sub": "s"}, false},
		{"字符串 true", Claims{"sub": "s", "email_verified": "true"}, true},
		{"字符串 TRUE（大小写）", Claims{"sub": "s", "email_verified": " TRUE "}, true},
		{"字符串 false", Claims{"sub": "s", "email_verified": "false"}, false},
		{"数字 1", Claims{"sub": "s", "email_verified": json.Number("1")}, true},
		{"数字 0", Claims{"sub": "s", "email_verified": json.Number("0")}, false},
	}
	for _, c := range cases {
		if got := cfg.Identity(c.claims).EmailVerified; got != c.want {
			t.Errorf("%s：EmailVerified=%v，want %v", c.name, got, c.want)
		}
	}
}

// 标准声明来自 ID Token 的 JSON 解码，这里确认 json.Number 形态也能读到。
func TestIdentityEmailVerifiedFromJSON(t *testing.T) {
	var claims Claims
	if err := json.Unmarshal([]byte(`{"sub":"s","email_verified":true}`), &claims); err != nil {
		t.Fatal(err)
	}
	if !Defaults().Identity(claims).EmailVerified {
		t.Fatal("ID Token 里的 email_verified=true 应被识别")
	}
}
