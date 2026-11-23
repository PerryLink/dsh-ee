package runtime

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

type Settings struct {
	Provider Provider
	Image    string
	BaseURL  string
	Model    string
	Command  []string
	// Computer 是每个机器人常驻电脑的资源上限；provider 不支持休眠 / 唤醒或 Persistent=false 时不起作用。
	Computer ComputerPolicy
}

// ComputerPolicy 控制每个机器人自己的常驻云电脑。
type ComputerPolicy struct {
	// Persistent 为 false 时所有任务都“每轮新建、跑完销毁”（DSH_EE_RUNTIME_PERSISTENT=off）。
	Persistent bool
	// IdleTimeout：最后一轮结束后空闲多久自动休眠。
	IdleTimeout time.Duration
	// DiskLimitBytes：工作目录和 DSH 主目录加起来的上限，超过后新任务拒绝运行，直到重置。
	DiskLimitBytes int64
	// NativeDiskQuota also requests a writable-layer / ephemeral-storage quota from OpenSandbox.
	NativeDiskQuota bool
	// Retention：休眠超过这么久的电脑自动回收（删除），0 表示不自动回收。
	Retention time.Duration
}

const (
	DefaultComputerIdle      = 15 * time.Minute
	DefaultComputerDiskMB    = 2048
	DefaultComputerRetention = 30 * 24 * time.Hour
)

func loadComputerPolicy(env func(string) string) (ComputerPolicy, error) {
	p := ComputerPolicy{Persistent: true, IdleTimeout: DefaultComputerIdle, DiskLimitBytes: DefaultComputerDiskMB << 20, Retention: DefaultComputerRetention}
	switch v := strings.TrimSpace(env("DSH_EE_RUNTIME_PERSISTENT")); v {
	case "", "on":
	case "off":
		p.Persistent = false
	default:
		return p, errors.New("DSH_EE_RUNTIME_PERSISTENT must be on or off")
	}
	if v := strings.TrimSpace(env("DSH_EE_RUNTIME_COMPUTER_IDLE")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Minute || d > 24*time.Hour {
			return p, errors.New("DSH_EE_RUNTIME_COMPUTER_IDLE must be a duration between 1m and 24h")
		}
		p.IdleTimeout = d
	}
	if v := strings.TrimSpace(env("DSH_EE_RUNTIME_COMPUTER_DISK_MB")); v != "" {
		mb, err := strconv.ParseInt(v, 10, 64)
		if err != nil || mb < 64 || mb > 1<<20 {
			return p, errors.New("DSH_EE_RUNTIME_COMPUTER_DISK_MB must be an integer between 64 and 1048576")
		}
		p.DiskLimitBytes = mb << 20
	}
	switch strings.TrimSpace(env("DSH_EE_RUNTIME_COMPUTER_NATIVE_DISK_QUOTA")) {
	case "", "off":
	case "on":
		p.NativeDiskQuota = true
	default:
		return p, errors.New("DSH_EE_RUNTIME_COMPUTER_NATIVE_DISK_QUOTA must be on or off")
	}
	if v := strings.TrimSpace(env("DSH_EE_RUNTIME_COMPUTER_RETENTION")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d != 0 && d < time.Hour {
			return p, errors.New("DSH_EE_RUNTIME_COMPUTER_RETENTION must be 0 or a duration of at least 1h")
		}
		p.Retention = d
	}
	return p, nil
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
	computer, err := loadComputerPolicy(env)
	if err != nil {
		return Settings{}, err
	}
	out := Settings{
		Computer: computer,
		Provider: p, Image: strings.TrimSpace(env("DSH_EE_RUNTIME_IMAGE")),
		BaseURL: strings.TrimRight(strings.TrimSpace(env("DSH_EE_RUNTIME_GATEWAY_URL")), "/"),
		Model:   strings.TrimSpace(env("DSH_EE_RUNTIME_MODEL")),
		Command: []string{"dsh", "--profile", "headless", "--patch", "{patch}", "--json", "{prompt}"},
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
