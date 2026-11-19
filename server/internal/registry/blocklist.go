package registry

import (
	"strings"
	"sync/atomic"

	"github.com/hikariming/dsh-ee/server/internal/store"
)

// MaxBlocklist 是 dshfind 屏蔽名单的条目上限。
const MaxBlocklist = 500

const blockAuthorPrefix = "author:"

// NormalizeBlockEntry 校验并规整一条屏蔽规则：包名（dsh-foo、@x/foo）、整个作用域（@x/*）
// 或 dshfind 作者（author:<GitHub 用户名>）。不合法返回 false。
func NormalizeBlockEntry(raw string) (string, bool) {
	e := strings.TrimSpace(raw)
	if a, ok := strings.CutPrefix(e, blockAuthorPrefix); ok {
		a = strings.TrimSpace(a)
		if a == "" || len(a) > 100 || strings.ContainsAny(a, " \t\r\n/") {
			return "", false
		}
		return blockAuthorPrefix + strings.ToLower(a), true
	}
	if sc, ok := strings.CutSuffix(e, "/*"); ok {
		if !ValidScope(sc) {
			return "", false
		}
		return sc + "/*", true
	}
	if !ValidName(e) {
		return "", false
	}
	return e, true
}

// blockedBy 返回屏蔽了 name 的那条规则；没被屏蔽返回空串。
// 作者规则按 dshfind 目录里登记的作者匹配，只对目录里出现过的包生效。
func blockedBy(list []string, name, author string) string {
	author = strings.ToLower(author)
	for _, e := range list {
		switch {
		case strings.HasPrefix(e, blockAuthorPrefix):
			if author != "" && e[len(blockAuthorPrefix):] == author {
				return e
			}
		case strings.HasSuffix(e, "/*"):
			if strings.HasPrefix(name, strings.TrimSuffix(e, "*")) {
				return e
			}
		case e == name:
			return e
		}
	}
	return ""
}

// feedAuthors 是最近一次取到的 dshfind 目录里「包名 → 作者」，供作者规则在回源、同步时查。
type feedAuthors struct {
	v atomic.Pointer[map[string]string]
}

func (f *feedAuthors) get(name string) string {
	if m := f.v.Load(); m != nil {
		return (*m)[name]
	}
	return ""
}

func (f *feedAuthors) set(plugins []FeedPlugin) {
	m := make(map[string]string, len(plugins))
	for _, p := range plugins {
		if p.Author != "" {
			m[p.Name] = p.Author
		}
	}
	f.v.Store(&m)
}

// BlockedBy 返回屏蔽了这个包的规则（系统设置 upstream.blocklist）；空串表示没被屏蔽。
func (s *Service) BlockedBy(set *store.AppSettings, name string) string {
	if len(set.Upstream.Blocklist) == 0 {
		return ""
	}
	return blockedBy(set.Upstream.Blocklist, name, s.authors.get(name))
}

// FeedBlockedBy 和 BlockedBy 一样，但作者直接用目录条目里的。
func FeedBlockedBy(set *store.AppSettings, p FeedPlugin) string {
	return blockedBy(set.Upstream.Blocklist, p.Name, p.Author)
}
