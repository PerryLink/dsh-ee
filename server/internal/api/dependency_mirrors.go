package api

import (
	"encoding/base64"
	"net/url"
	"strings"

	"github.com/hikariming/dsh-ee/server/internal/store"
)

func validateDependencyMirrors(v *store.DependencyMirrors) string {
	for _, m := range []*store.DependencyMirror{&v.PyPI, &v.Maven} {
		m.URL, m.Username = strings.TrimSpace(m.URL), strings.TrimSpace(m.Username)
		if m.URL == "" {
			*m = store.DependencyMirror{}
			continue
		}
		u, err := url.Parse(m.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return "依赖镜像地址必须是无内嵌账号密码的完整 HTTP(S) 地址"
		}
		if strings.ContainsAny(m.Username+m.Secret, "\r\n\x00") {
			return "依赖镜像凭据不能包含换行符或空字符"
		}
		if (m.Secret != "" || m.SecretEnc != "") && m.Username == "" {
			return "依赖镜像密码或令牌需要用户名"
		}
	}
	return ""
}

func (s *Server) sealDependencyMirrors(v *store.DependencyMirrors) error {
	for _, m := range []*store.DependencyMirror{&v.PyPI, &v.Maven} {
		if m.Secret != "" {
			sealed, err := s.box.Seal([]byte(m.Secret))
			if err != nil {
				return err
			}
			m.SecretEnc = base64.StdEncoding.EncodeToString(sealed)
		}
		m.Secret = ""
		m.HasSecret = m.SecretEnc != ""
	}
	return nil
}

type deliveredDependencyMirror struct {
	URL      string `json:"url"`
	Username string `json:"username,omitempty"`
	Secret   string `json:"secret,omitempty"`
}

func (s *Server) dependencyMirrors(v store.DependencyMirrors) (map[string]deliveredDependencyMirror, error) {
	out := map[string]deliveredDependencyMirror{}
	for name, m := range map[string]store.DependencyMirror{"pypi": v.PyPI, "maven": v.Maven} {
		if m.URL == "" {
			continue
		}
		d := deliveredDependencyMirror{URL: m.URL, Username: m.Username}
		if m.SecretEnc != "" {
			sealed, err := base64.StdEncoding.DecodeString(m.SecretEnc)
			if err != nil {
				return nil, err
			}
			plain, err := s.box.Open(sealed)
			if err != nil {
				return nil, err
			}
			d.Secret = string(plain)
		}
		out[name] = d
	}
	return out, nil
}
