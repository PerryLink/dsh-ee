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

func TestComputerPolicy(t *testing.T) {
	p, err := loadComputerPolicy(func(string) string { return "" })
	if err != nil || !p.Persistent || p.IdleTimeout != DefaultComputerIdle || p.DiskLimitBytes != DefaultComputerDiskMB<<20 || p.Retention != DefaultComputerRetention {
		t.Fatalf("defaults = %+v, %v", p, err)
	}
	env := map[string]string{"DSH_EE_RUNTIME_PERSISTENT": "off", "DSH_EE_RUNTIME_COMPUTER_IDLE": "5m", "DSH_EE_RUNTIME_COMPUTER_DISK_MB": "512", "DSH_EE_RUNTIME_COMPUTER_RETENTION": "0"}
	p, err = loadComputerPolicy(func(k string) string { return env[k] })
	if err != nil || p.Persistent || p.IdleTimeout.Minutes() != 5 || p.DiskLimitBytes != 512<<20 || p.Retention != 0 {
		t.Fatalf("configured = %+v, %v", p, err)
	}
	for k, v := range map[string]string{"DSH_EE_RUNTIME_PERSISTENT": "maybe", "DSH_EE_RUNTIME_COMPUTER_IDLE": "10s", "DSH_EE_RUNTIME_COMPUTER_DISK_MB": "1", "DSH_EE_RUNTIME_COMPUTER_RETENTION": "5m"} {
		if _, err := loadComputerPolicy(func(key string) string {
			if key == k {
				return v
			}
			return ""
		}); err == nil {
			t.Fatalf("%s=%s accepted", k, v)
		}
	}
}
