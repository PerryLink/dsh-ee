package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	Preset = "dingtalk"
	Issuer = "https://login.dingtalk.com"
	api    = "https://api.dingtalk.com"
	oapi   = "https://oapi.dingtalk.com"
)

type Client struct {
	HTTP       *http.Client
	ID, Secret string
	tokenMu    sync.Mutex
	token      string
	expires    time.Time
}

type Employee struct {
	UserID string
	Name   string
}

func ExternalSubject(corpID, userID string) string {
	return Preset + ":" + corpID + ":" + userID
}

func (c *Client) AuthCodeURL(redirect, state, corpID string) string {
	q := url.Values{
		"response_type": {"code"},
		"client_id":     {c.ID},
		"redirect_uri":  {redirect},
		"scope":         {"openid corpid"},
		"state":         {state},
		"prompt":        {"consent"},
		"corpId":        {corpID},
	}
	return Issuer + "/oauth2/auth?" + q.Encode()
}

func (c *Client) call(ctx context.Context, step, method, endpoint, token string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("%s: invalid request", step)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("%s: invalid request", step)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("x-acs-dingtalk-access-token", token)
	}
	hc := *c.HTTP
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := hc.Do(req)
	if err != nil {
		// Do not report URLs containing an application access token.
		return fmt.Errorf("%s: request failed", step)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("%s: HTTP %d", step, res.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out); err != nil {
		return fmt.Errorf("%s: invalid response", step)
	}
	return nil
}

func (c *Client) AppToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if time.Now().Before(c.expires) {
		return c.token, nil
	}
	var out struct {
		Token   string `json:"accessToken"`
		Seconds int    `json:"expireIn"`
	}
	if err := c.call(ctx, "DingTalk app token", http.MethodPost, api+"/v1.0/oauth2/accessToken", "", map[string]string{
		"appKey": c.ID, "appSecret": c.Secret,
	}, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", errors.New("DingTalk app token missing")
	}
	c.token = out.Token
	c.expires = time.Now().Add(time.Duration(max(0, out.Seconds-60)) * time.Second)
	return out.Token, nil
}

func (c *Client) Exchange(ctx context.Context, code, corpID string) (Employee, error) {
	if code == "" {
		return Employee{}, errors.New("DingTalk authorization code missing")
	}
	var grant struct {
		Token  string `json:"accessToken"`
		CorpID string `json:"corpId"`
	}
	if err := c.call(ctx, "DingTalk user token", http.MethodPost, api+"/v1.0/oauth2/userAccessToken", "", map[string]string{
		"clientId": c.ID, "clientSecret": c.Secret, "code": code, "grantType": "authorization_code",
	}, &grant); err != nil {
		return Employee{}, err
	}
	if grant.Token == "" || grant.CorpID != corpID || corpID == "" {
		return Employee{}, errors.New("DingTalk authorization did not select the configured organization")
	}
	var profile struct {
		UnionID string `json:"unionId"`
		Nick    string `json:"nick"`
	}
	if err := c.call(ctx, "DingTalk user profile", http.MethodGet, api+"/v1.0/contact/users/me", grant.Token, nil, &profile); err != nil {
		return Employee{}, err
	}
	if profile.UnionID == "" {
		return Employee{}, errors.New("DingTalk profile missing unionId")
	}
	appToken, err := c.AppToken(ctx)
	if err != nil {
		return Employee{}, err
	}
	var member struct {
		Code   json.Number `json:"errcode"`
		Result struct {
			ContactType json.Number `json:"contact_type"`
			UserID      string      `json:"userid"`
		} `json:"result"`
	}
	endpoint := oapi + "/topapi/user/getbyunionid?" + url.Values{"access_token": {appToken}}.Encode()
	if err := c.call(ctx, "DingTalk employee lookup", http.MethodPost, endpoint, "", map[string]string{"unionid": profile.UnionID}, &member); err != nil {
		return Employee{}, err
	}
	if member.Code != "0" || member.Result.ContactType != "0" || strings.TrimSpace(member.Result.UserID) == "" ||
		len(ExternalSubject(corpID, member.Result.UserID)) > 512 {
		return Employee{}, errors.New("DingTalk account is not an employee of the configured organization")
	}
	return Employee{UserID: member.Result.UserID, Name: profile.Nick}, nil
}
