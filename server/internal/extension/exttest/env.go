// Package exttest 是写扩展时用的模拟环境和协议一致性测试（docs/agent-dev.md §4.2、§5.1）。
//
// 模拟环境是一台假的 dsh-ee：只做扩展会碰到的那部分——公开 JWKS、给几个预置的员工签 ee_jwt。
// 不需要数据库，命令行 `dsh-ee extension test / dev / call` 和 CI 用的都是它。
package exttest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/eejwt"
)

// User 是模拟环境里预置的一位员工。
type User struct {
	ID       string   `json:"id"`
	Username string   `json:"username"`
	Name     string   `json:"name"`
	Email    string   `json:"email"`
	Dept     string   `json:"dept"`
	Roles    []string `json:"roles"`
}

// Users 是预置的员工，覆盖几个部门和角色。场景测试里用 `as: <用户名>` 指定以谁的身份调用。
var Users = []User{
	{ID: "101", Username: "zhangsan", Name: "张三", Email: "zhangsan@example.com", Dept: "研发部", Roles: []string{"member"}},
	{ID: "102", Username: "lisi", Name: "李四", Email: "lisi@example.com", Dept: "财务部", Roles: []string{"member"}},
	{ID: "103", Username: "wangwu", Name: "王五", Email: "wangwu@example.com", Dept: "人事部", Roles: []string{"member"}},
	{ID: "1", Username: "admin", Name: "管理员", Email: "admin@example.com", Dept: "信息部", Roles: []string{"admin", "member"}},
}

// FindUser 按用户名找预置的员工。
func FindUser(name string) (User, bool) {
	for _, u := range Users {
		if u.Username == name {
			return u, true
		}
	}
	return User{}, false
}

// Env 是模拟的 dsh-ee。
type Env struct {
	// 对外的地址，扩展的 DSH_EE_URL 填它
	URL string
	key *eejwt.Key
	srv *http.Server
	ln  net.Listener
}

// DefaultListen 是模拟环境默认监听的地址。
const DefaultListen = "127.0.0.1:7788"

// devKey 取开发用的签名密钥。存在用户的缓存目录里，这样扩展不用重启，几次命令之间签出来的令牌它都认。
func devKey() (*eejwt.Key, error) {
	dir, err := os.UserCacheDir()
	if err == nil {
		path := filepath.Join(dir, "dsh-ee", "dev-key.der")
		if raw, err := os.ReadFile(path); err == nil {
			if k, err := eejwt.Parse(raw); err == nil {
				return k, nil
			}
		}
		k, err := eejwt.Generate()
		if err != nil {
			return nil, err
		}
		if der, err := k.Marshal(); err == nil && os.MkdirAll(filepath.Dir(path), 0o700) == nil {
			_ = os.WriteFile(path, der, 0o600)
		}
		return k, nil
	}
	return eejwt.Generate()
}

// Start 在 listen 上起模拟环境。
func Start(listen string) (*Env, error) {
	key, err := devKey()
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, fmt.Errorf("模拟环境起不来（%s 被占用了？换一个 --listen）：%w", listen, err)
	}
	e := &Env{URL: "http://" + ln.Addr().String(), key: key, ln: ln}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(eejwt.JWKS{Keys: []eejwt.JWK{key.Public()}})
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","version":"dev-mock"}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "这是 dsh-ee 的扩展开发模拟环境（不是真的 dsh-ee）。\n扩展启动时设 DSH_EE_URL=%s 。\n预置的员工：", e.URL)
		for _, u := range Users {
			fmt.Fprintf(w, "\n  %s（%s，%s）", u.Username, u.Name, u.Dept)
		}
		fmt.Fprintln(w)
	})
	e.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = e.srv.Serve(ln) }()
	return e, nil
}

// Close 停掉模拟环境。
func (e *Env) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = e.srv.Shutdown(ctx)
}

// Token 给一位员工签一个调 aud 这个扩展的令牌。
func (e *Env) Token(aud string, u User) string {
	now := time.Now()
	tok, _ := eejwt.Sign(e.key, map[string]any{
		"iss": e.URL, "aud": aud, "sub": u.ID, "preferred_username": u.Username, "name": u.Name, "email": u.Email,
		"dept": u.Dept, "roles": u.Roles, "iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(eejwt.Lifetime).Unix(),
	})
	return tok
}

// SystemToken 签一个没有员工的令牌（健康检查、同步、回调）。
func (e *Env) SystemToken(aud string) string {
	now := time.Now()
	tok, _ := eejwt.Sign(e.key, map[string]any{
		"iss": e.URL, "aud": aud, "sub": "system", "sys": true, "name": "dsh-ee",
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(eejwt.Lifetime).Unix(),
	})
	return tok
}

// badTokens 是几种应该被扩展拒绝的令牌。
func (e *Env) badTokens(aud string) (map[string]string, error) {
	other, err := eejwt.Generate()
	if err != nil {
		return nil, err
	}
	if len(Users) == 0 {
		return nil, errors.New("没有预置的员工")
	}
	u := Users[0]
	now := time.Now()
	claims := func(aud string, exp time.Time) map[string]any {
		return map[string]any{"iss": e.URL, "aud": aud, "sub": u.ID, "preferred_username": u.Username, "iat": now.Unix(), "exp": exp.Unix()}
	}
	expired, _ := eejwt.Sign(e.key, claims(aud, now.Add(-time.Minute)))
	wrongAud, _ := eejwt.Sign(e.key, claims(aud+"-other", now.Add(time.Minute)))
	// 用同一个 kid、另一把私钥签：签名对不上
	other.ID = e.key.ID
	forged, _ := eejwt.Sign(other, claims(aud, now.Add(time.Minute)))
	return map[string]string{"不带令牌": "", "令牌过期": expired, "aud 不对": wrongAud, "签名不对": forged}, nil
}
