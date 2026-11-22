package runtime

import (
	"slices"
	"testing"
)

func TestDefaultRuntimeCommandLauncherFlagOrder(t *testing.T) {
	env := map[string]string{
		"DSH_EE_RUNTIME_PROVIDER":    "opensandbox",
		"DSH_EE_RUNTIME_API_KEY":     "test",
		"DSH_EE_RUNTIME_URL":         "http://sandbox.example",
		"DSH_EE_RUNTIME_IMAGE":       "test-image",
		"DSH_EE_RUNTIME_GATEWAY_URL": "https://gateway.example",
		"DSH_EE_RUNTIME_MODEL":       "test-model",
	}
	settings, err := LoadSettings(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"dsh", "--profile", "headless", "--patch", "{patch}", "--json", "{prompt}"}
	if !slices.Equal(settings.Command, want) {
		t.Fatalf("default command = %q, want %q", settings.Command, want)
	}
}
