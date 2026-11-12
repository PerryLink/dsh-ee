package sdk

import (
	"context"
	"net/http"
	"time"
)

// OAuth 钩子（docs/plugins.md §7.3）：源系统的授权流程不标准时实现它。标准的 OAuth 2.0 授权码流程
// 不用写代码，在清单的 OAuth 里填授权地址、令牌地址和 scope，由 dsh-ee 自己完成。
// dsh-ee 负责回调地址、state 校验、按人保存令牌、到期前调 Refresh；refresh token 和 client secret 不会出现在工具调用里。
type OAuth struct {
	// 员工点「授权」时要跳去的地址。state 要原样带上，redirectURI 是 dsh-ee 的回调地址
	AuthorizeURL func(ctx context.Context, c *Call, redirectURI, state string) (string, error)
	// 用回调带回来的参数（code 等）换令牌
	Exchange func(ctx context.Context, c *Call, redirectURI string, params map[string]string) (*Tokens, error)
	// 刷新；不支持刷新可以不实现，到期后员工要重新授权
	Refresh func(ctx context.Context, c *Call, refreshToken string) (*Tokens, error)
	// 员工离职、取消授权、卸载扩展时撤销；可以不实现
	Revoke func(ctx context.Context, c *Call, tokens *Tokens) error
}

// Tokens 是换到的令牌。
type Tokens struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
	// 显示在「我的连接」里的账号名，比如源系统里的用户名
	Account string `json:"account,omitempty"`
}

// OAuth 注册 OAuth 钩子。
func (e *Extension) OAuth(o OAuth) *Extension {
	if o.AuthorizeURL == nil || o.Exchange == nil {
		panic("sdk: OAuth 至少要实现 AuthorizeURL 和 Exchange")
	}
	e.oauth = &o
	return e
}

func (e *Extension) handleOAuth(w http.ResponseWriter, r *http.Request, c *Call, name string) {
	o := e.oauth
	missing := func() { writeError(w, http.StatusNotFound, CodeNotImplemented, "这个扩展没有实现 oauth/"+name) }
	if o == nil {
		missing()
		return
	}
	ctx := r.Context()
	var req struct {
		RedirectURI  string            `json:"redirectUri"`
		State        string            `json:"state"`
		Params       map[string]string `json:"params"`
		RefreshToken string            `json:"refreshToken"`
		Tokens       *Tokens           `json:"tokens"`
	}
	if !readBody(w, r, &req) {
		return
	}
	switch name {
	case "authorize-url":
		u, err := o.AuthorizeURL(ctx, c, req.RedirectURI, req.State)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"url": u})
	case "exchange":
		t, err := o.Exchange(ctx, c, req.RedirectURI, req.Params)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, t)
	case "refresh":
		if o.Refresh == nil {
			missing()
			return
		}
		t, err := o.Refresh(ctx, c, req.RefreshToken)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, t)
	case "revoke":
		if o.Revoke == nil {
			missing()
			return
		}
		if err := o.Revoke(ctx, c, req.Tokens); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		missing()
	}
}
