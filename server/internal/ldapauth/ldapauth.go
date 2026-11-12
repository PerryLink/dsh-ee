// Package ldapauth 用企业目录（OpenLDAP、Active Directory 等）认证用户、读取资料与所属组。
//
// 流程：用服务账号绑定 → 按过滤条件找到唯一的用户条目 → 读组 → 用用户 DN 和密码再绑定一次验证密码。
// 服务账号可以留空（匿名搜索），但用户密码永远不能为空：多数目录把空密码绑定当成「未认证绑定」并返回成功。
package ldapauth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// Config 是目录连接与属性映射。BindPassword 不参与 JSON，由调用方加密保存。
type Config struct {
	Enabled bool `json:"enabled"`
	// 组织同步带字段（docs/access-control.md §6.1）要额外读的属性；由字段同步设置决定，不单独保存
	ExtraAttrs []string `json:"-"`
	// ldap://host:389 或 ldaps://host:636
	URL                string `json:"url"`
	StartTLS           bool   `json:"startTls"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify"`
	// 自签或企业 CA 的 PEM 证书，为空时用系统根证书
	CACert       string `json:"caCert"`
	BindDN       string `json:"bindDn"`
	BindPassword string `json:"-"`

	BaseDN string `json:"baseDn"`
	// 用 {username} 代表登录名（会按 RFC 4515 转义）
	UserFilter      string `json:"userFilter"`
	UsernameAttr    string `json:"usernameAttr"`
	DisplayNameAttr string `json:"displayNameAttr"`
	DepartmentAttr  string `json:"departmentAttr"`
	EmailAttr       string `json:"emailAttr"`

	// memberOf：读用户条目的 memberOf 属性（AD、开了 memberof overlay 的 OpenLDAP）
	// search：在 GroupBaseDN 下按 GroupFilter 搜组，{dn} 为用户 DN、{username} 为登录名
	// none：不读组
	GroupMode     string `json:"groupMode"`
	GroupBaseDN   string `json:"groupBaseDn"`
	GroupFilter   string `json:"groupFilter"`
	GroupNameAttr string `json:"groupNameAttr"`

	// 目录组 → dsh-ee 角色。组可以写完整 DN，也可以只写组名（CN）。
	RoleMappings []RoleMapping `json:"roleMappings"`
	// 目录里有、dsh-ee 还没有的账号首次登录时自动创建；关闭后只有管理员预先建好的 LDAP 账号能登录
	AutoCreate bool `json:"autoCreate"`
	// 定期核对目录，账号被删除或在 AD 里被禁用时，在 dsh-ee 里同步禁用
	SyncDisable bool `json:"syncDisable"`

	TimeoutSeconds int `json:"timeoutSeconds"`
}

type RoleMapping struct {
	Group  string `json:"group"`
	RoleID int64  `json:"roleId"`
}

// Defaults 是 OpenLDAP 风格的默认值；AD 用户可以在页面上一键换成 AD 预设。
func Defaults() *Config {
	return &Config{
		UserFilter:      "(&(objectClass=person)(uid={username}))",
		UsernameAttr:    "uid",
		DisplayNameAttr: "cn",
		DepartmentAttr:  "ou",
		EmailAttr:       "mail",
		GroupMode:       "memberOf",
		GroupFilter:     "(|(member={dn})(uniqueMember={dn})(memberUid={username}))",
		GroupNameAttr:   "cn",
		RoleMappings:    []RoleMapping{},
		AutoCreate:      true,
		SyncDisable:     true,
		TimeoutSeconds:  10,
	}
}

// Validate 检查并规范化配置；返回给管理员看的错误信息。
func (c *Config) Validate() string {
	c.URL = strings.TrimSpace(c.URL)
	c.BaseDN = strings.TrimSpace(c.BaseDN)
	c.BindDN = strings.TrimSpace(c.BindDN)
	c.UserFilter = strings.TrimSpace(c.UserFilter)
	c.GroupBaseDN = strings.TrimSpace(c.GroupBaseDN)
	c.GroupFilter = strings.TrimSpace(c.GroupFilter)
	for _, p := range []*string{&c.UsernameAttr, &c.DisplayNameAttr, &c.DepartmentAttr, &c.EmailAttr, &c.GroupNameAttr} {
		*p = strings.TrimSpace(*p)
	}
	if c.RoleMappings == nil {
		c.RoleMappings = []RoleMapping{}
	}
	if c.TimeoutSeconds <= 0 {
		c.TimeoutSeconds = 10
	}
	if c.TimeoutSeconds > 60 {
		return "超时不能超过 60 秒"
	}
	if c.URL == "" && !c.Enabled {
		return ""
	}
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "ldap" && u.Scheme != "ldaps") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") {
		return "目录地址应形如 ldap://dc.acme.local:389 或 ldaps://dc.acme.local:636"
	}
	if u.Scheme == "ldaps" && c.StartTLS {
		return "ldaps:// 已经是加密连接，不需要再开 StartTLS"
	}
	if c.CACert != "" && !x509.NewCertPool().AppendCertsFromPEM([]byte(c.CACert)) {
		return "CA 证书不是合法的 PEM"
	}
	if _, err := ldap.ParseDN(c.BaseDN); err != nil || c.BaseDN == "" {
		return "搜索起点（Base DN）不是合法的 DN，例如 dc=acme,dc=local"
	}
	if c.BindDN != "" {
		if _, err := ldap.ParseDN(c.BindDN); err != nil {
			// AD 也接受 user@domain 形式的绑定名
			if !strings.Contains(c.BindDN, "@") {
				return "服务账号应是 DN（cn=svc,dc=acme,dc=local）或 user@domain"
			}
		}
	}
	if !strings.Contains(c.UserFilter, "{username}") {
		return "用户过滤条件里要有 {username}"
	}
	if _, err := ldap.CompileFilter(strings.ReplaceAll(c.UserFilter, "{username}", "x")); err != nil {
		return "用户过滤条件不是合法的 LDAP 过滤器：" + err.Error()
	}
	if c.UsernameAttr == "" {
		return "用户名属性不能为空"
	}
	switch c.GroupMode {
	case "memberOf", "none":
	case "search":
		if _, err := ldap.ParseDN(c.GroupBaseDN); err != nil || c.GroupBaseDN == "" {
			return "按搜索读组时，组的搜索起点必须是合法的 DN"
		}
		f := strings.NewReplacer("{dn}", "x", "{username}", "x").Replace(c.GroupFilter)
		if _, err := ldap.CompileFilter(f); err != nil {
			return "组过滤条件不是合法的 LDAP 过滤器：" + err.Error()
		}
		if c.GroupNameAttr == "" {
			c.GroupNameAttr = "cn"
		}
	default:
		return "读组方式只能是 memberOf / search / none"
	}
	for i := range c.RoleMappings {
		c.RoleMappings[i].Group = strings.TrimSpace(c.RoleMappings[i].Group)
		if c.RoleMappings[i].Group == "" || c.RoleMappings[i].RoleID <= 0 {
			return "角色映射里每一行都要填组和角色"
		}
	}
	return ""
}

// Entry 是目录里的一个用户。
type Entry struct {
	DN          string   `json:"dn"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Department  string   `json:"department"`
	Email       string   `json:"email"`
	Groups      []string `json:"groups"`
	// AD 的 userAccountControl 带「账号已禁用」位
	Disabled bool `json:"disabled"`
	// 组织同步要带的额外属性（Config.ExtraAttrs），属性名 → 值
	Attrs map[string][]string `json:"attrs,omitempty"`
}

var (
	// ErrInvalidCredentials 用户不存在、密码错误或为空。对外统一成「用户名或密码错误」。
	ErrInvalidCredentials = errors.New("用户名或密码错误")
	ErrUserNotFound       = errors.New("目录里找不到这个用户")
	ErrAmbiguous          = errors.New("过滤条件匹配到多个用户，请把用户过滤条件写得更精确")
)

// Conn 是一条已用服务账号绑定的连接。
type Conn struct {
	cfg  *Config
	l    *ldap.Conn
	stop func() bool
}

func (c *Config) timeout() time.Duration { return time.Duration(c.TimeoutSeconds) * time.Second }

func (c *Config) tlsConfig() (*tls.Config, error) {
	u, err := url.Parse(c.URL)
	if err != nil {
		return nil, err
	}
	host, _, err := net.SplitHostPort(u.Host)
	if err != nil {
		host = u.Host
	}
	tc := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.InsecureSkipVerify}
	if c.CACert != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(c.CACert)) {
			return nil, errors.New("CA 证书不是合法的 PEM")
		}
		tc.RootCAs = pool
	}
	return tc, nil
}

// Dial 连接目录并用服务账号绑定（服务账号为空时不绑定，按匿名搜索）。
func (c *Config) Dial(ctx context.Context) (*Conn, error) {
	tc, err := c.tlsConfig()
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{Timeout: c.timeout()}
	l, err := ldap.DialURL(c.URL, ldap.DialWithDialer(d), ldap.DialWithTLSConfig(tc))
	if err != nil {
		return nil, fmt.Errorf("连接目录失败：%w", err)
	}
	l.SetTimeout(c.timeout())
	// ctx 取消时直接断开连接，正在进行的操作随之返回
	conn := &Conn{cfg: c, l: l, stop: context.AfterFunc(ctx, func() { l.Close() })}
	if c.StartTLS {
		if err := l.StartTLS(tc); err != nil {
			conn.Close()
			return nil, fmt.Errorf("StartTLS 失败：%w", err)
		}
	}
	if err := conn.bindService(); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func (cn *Conn) Close() {
	cn.stop()
	cn.l.Close()
}

func (cn *Conn) bindService() error {
	if cn.cfg.BindDN == "" {
		return nil
	}
	if cn.cfg.BindPassword == "" {
		return errors.New("服务账号的密码为空")
	}
	if err := cn.l.Bind(cn.cfg.BindDN, cn.cfg.BindPassword); err != nil {
		return fmt.Errorf("服务账号绑定失败：%w", err)
	}
	return nil
}

func (c *Config) attrs() []string {
	out := []string{"dn", c.UsernameAttr, "userAccountControl"}
	out = append(out, c.ExtraAttrs...)
	for _, a := range []string{c.DisplayNameAttr, c.DepartmentAttr, c.EmailAttr} {
		if a != "" {
			out = append(out, a)
		}
	}
	if c.GroupMode == "memberOf" {
		out = append(out, "memberOf")
	}
	return out
}

// Lookup 按登录名找用户条目并读组。找不到返回 ErrUserNotFound。
func (cn *Conn) Lookup(username string) (*Entry, error) {
	c := cn.cfg
	filter := strings.ReplaceAll(c.UserFilter, "{username}", ldap.EscapeFilter(username))
	res, err := cn.l.Search(ldap.NewSearchRequest(c.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases,
		2, c.TimeoutSeconds, false, filter, c.attrs(), nil))
	if err != nil && !ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) {
		// 搜索起点不存在是配置错误，不能当成「用户不存在」：目录同步会据此禁用账号
		if ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) {
			return nil, fmt.Errorf("搜索起点 %s 在目录里不存在：%w", c.BaseDN, err)
		}
		return nil, fmt.Errorf("搜索用户失败：%w", err)
	}
	if res == nil || len(res.Entries) == 0 {
		return nil, ErrUserNotFound
	}
	if len(res.Entries) > 1 {
		return nil, ErrAmbiguous
	}
	e := res.Entries[0]
	out := &Entry{
		DN:          e.DN,
		Username:    e.GetEqualFoldAttributeValue(c.UsernameAttr),
		DisplayName: attr(e, c.DisplayNameAttr),
		Department:  attr(e, c.DepartmentAttr),
		Email:       attr(e, c.EmailAttr),
		Groups:      []string{},
	}
	for _, a := range c.ExtraAttrs {
		if vs := e.GetEqualFoldAttributeValues(a); len(vs) > 0 {
			if out.Attrs == nil {
				out.Attrs = map[string][]string{}
			}
			out.Attrs[a] = vs
		}
	}
	if uac := e.GetEqualFoldAttributeValue("userAccountControl"); uac != "" {
		var n int64
		if _, err := fmt.Sscan(uac, &n); err == nil && n&2 != 0 {
			out.Disabled = true
		}
	}
	switch c.GroupMode {
	case "memberOf":
		out.Groups = append(out.Groups, e.GetEqualFoldAttributeValues("memberOf")...)
	case "search":
		f := strings.NewReplacer("{dn}", ldap.EscapeFilter(e.DN), "{username}", ldap.EscapeFilter(out.Username)).Replace(c.GroupFilter)
		gr, err := cn.l.Search(ldap.NewSearchRequest(c.GroupBaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases,
			1000, c.TimeoutSeconds, false, f, []string{"dn", c.GroupNameAttr}, nil))
		if err != nil && !ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) {
			return nil, fmt.Errorf("搜索用户所属组失败：%w", err)
		}
		if gr != nil {
			for _, g := range gr.Entries {
				out.Groups = append(out.Groups, g.DN)
			}
		}
	}
	return out, nil
}

func attr(e *ldap.Entry, name string) string {
	if name == "" {
		return ""
	}
	return strings.TrimSpace(e.GetEqualFoldAttributeValue(name))
}

// Verify 用用户 DN 与密码绑定一次验证密码，然后换回服务账号（同一连接还要继续查别人）。
func (cn *Conn) Verify(dn, password string) error {
	if password == "" {
		return ErrInvalidCredentials
	}
	err := cn.l.Bind(dn, password)
	if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
		return ErrInvalidCredentials
	}
	if err != nil {
		return fmt.Errorf("验证密码失败：%w", err)
	}
	return cn.bindService()
}

// Authenticate 验证登录名与密码，返回目录里的用户。
func (c *Config) Authenticate(ctx context.Context, username, password string) (*Entry, error) {
	if username == "" || password == "" {
		return nil, ErrInvalidCredentials
	}
	cn, err := c.Dial(ctx)
	if err != nil {
		return nil, err
	}
	defer cn.Close()
	e, err := cn.Lookup(username)
	if errors.Is(err, ErrUserNotFound) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if e.Disabled {
		return nil, ErrInvalidCredentials
	}
	if err := cn.Verify(e.DN, password); err != nil {
		return nil, err
	}
	return e, nil
}

// MatchRoles 返回用户所属组映射到的角色 id（去重）。组按完整 DN 或组名（第一段 RDN 的值）匹配，不区分大小写。
func (c *Config) MatchRoles(groups []string) []int64 {
	var out []int64
	for _, m := range c.RoleMappings {
		for _, g := range groups {
			if GroupMatches(m.Group, g) && !slices.Contains(out, m.RoleID) {
				out = append(out, m.RoleID)
				break
			}
		}
	}
	return out
}

// ManagedRoles 是出现在映射里的角色：这些角色由目录决定，登录时按组增删；其余角色保留管理员手动分配的。
func (c *Config) ManagedRoles() []int64 {
	var out []int64
	for _, m := range c.RoleMappings {
		if !slices.Contains(out, m.RoleID) {
			out = append(out, m.RoleID)
		}
	}
	return out
}

// GroupMatches 判断映射里写的组（DN 或组名）是否就是 groupDN。
func GroupMatches(want, groupDN string) bool {
	if strings.EqualFold(strings.TrimSpace(want), strings.TrimSpace(groupDN)) {
		return true
	}
	gd, err := ldap.ParseDN(groupDN)
	if err != nil || len(gd.RDNs) == 0 {
		return false
	}
	if wd, err := ldap.ParseDN(want); err == nil && len(wd.RDNs) > 1 {
		return wd.EqualFold(gd)
	}
	for _, a := range gd.RDNs[0].Attributes {
		if strings.EqualFold(a.Value, strings.TrimSpace(want)) {
			return true
		}
	}
	return false
}
