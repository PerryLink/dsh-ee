package runtime

import (
	"encoding/json"
	"errors"
	"strings"
)

type Settings struct {
	Provider Provider
	Image    string
	BaseURL  string
	Model    string
	Command  []string
}

func LoadSettings(env func(string) string) (Settings, error) {
	kind := strings.TrimSpace(env("DSH_EE_RUNTIME_PROVIDER"))
	if kind == "" || kind == "off" {
		return Settings{}, nil
	}
	if kind != "opensandbox" {
		return Settings{}, errors.New("DSH_EE_RUNTIME_PROVIDER must be opensandbox or off")
	}
	key := env("DSH_EE_RUNTIME_API_KEY")
	if key == "" {
		return Settings{}, errors.New("DSH_EE_RUNTIME_API_KEY is required")
	}
	p, err := NewOpenSandbox(env("DSH_EE_RUNTIME_URL"), key)
	if err != nil {
		return Settings{}, err
	}
	out := Settings{
		Provider: p, Image: strings.TrimSpace(env("DSH_EE_RUNTIME_IMAGE")),
		BaseURL: strings.TrimRight(strings.TrimSpace(env("DSH_EE_RUNTIME_GATEWAY_URL")), "/"),
		Model:   strings.TrimSpace(env("DSH_EE_RUNTIME_MODEL")),
		Command: []string{"dsh", "--profile", "headless", "--patch", "{patch}", "{prompt}"},
	}
	base, err := parseGatewayURL(out.BaseURL)
	if err != nil || out.Image == "" || out.Model == "" {
		return Settings{}, errors.New("DSH_EE_RUNTIME_IMAGE, DSH_EE_RUNTIME_GATEWAY_URL, and DSH_EE_RUNTIME_MODEL are required")
	}
	out.BaseURL = base
	if cmd := env("DSH_EE_RUNTIME_COMMAND"); cmd != "" {
		if err := json.Unmarshal([]byte(cmd), &out.Command); err != nil || len(out.Command) == 0 || out.Command[0] == "" {
			return Settings{}, errors.New("DSH_EE_RUNTIME_COMMAND must be a nonempty JSON argv array")
		}
	}
	return out, nil
}
