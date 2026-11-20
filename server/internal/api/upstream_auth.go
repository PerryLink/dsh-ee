package api

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/hikariming/dsh-ee/server/internal/registry"
)

func (s *Server) upstreamCredentials(ctx context.Context) (string, string, error) {
	set, err := s.appSettings(ctx)
	if err != nil {
		return "", "", err
	}
	base := registry.UpstreamBase(set)
	auth := set.Upstream.Auth
	if set.Upstream.Registry != "custom" || base == "" || (auth.Type != "bearer" && auth.Type != "basic") {
		return "", "", nil
	}
	sealed, err := base64.StdEncoding.DecodeString(auth.SecretEnc)
	if err != nil {
		return "", "", fmt.Errorf("解密上游仓库凭据失败")
	}
	plain, err := s.box.Open(sealed)
	if err != nil {
		return "", "", fmt.Errorf("解密上游仓库凭据失败，请检查主密钥")
	}
	if auth.Type == "basic" {
		return base, "Basic " + base64.StdEncoding.EncodeToString([]byte(auth.Username+":"+string(plain))), nil
	}
	return base, "Bearer " + string(plain), nil
}
