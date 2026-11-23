package runtime

import (
	"context"
	"errors"
	"io"
	"time"
)

type Provider interface {
	Create(context.Context, SandboxSpec) (Sandbox, error)
}

// ErrSandboxGone 表示一台常驻沙箱已经不存在（被删、过期或进入终态）。调用方应当新建一台，
// 并把这当作“电脑被重置”：原来的文件已经没有了。
var ErrSandboxGone = errors.New("sandbox no longer exists")

// PersistentProvider 是能让沙箱跨任务保留的 provider：空闲时休眠（暂停，文件系统保留），
// 下一轮唤醒后接着用。没有实现它的 provider 按“每轮新建、跑完销毁”运行。
//
// 常驻沙箱里不放任何凭证：创建时不带环境变量，每轮的短期凭证只通过 Exec 的环境变量传入，
// 这一轮结束即吊销。
type PersistentProvider interface {
	Provider
	// CreatePersistent 新建一台不会因超时自动删除的沙箱（spec.Timeout 被忽略）。
	CreatePersistent(context.Context, SandboxSpec) (PersistentSandbox, error)
	// Wake 连接到一台已有的沙箱：休眠中的先恢复，运行中的直接连上。不存在时返回 ErrSandboxGone。
	Wake(ctx context.Context, id string) (PersistentSandbox, error)
	// Hibernate 让沙箱休眠，等到确实进入休眠才返回。不存在时返回 ErrSandboxGone。
	Hibernate(ctx context.Context, id string) error
	// Remove 删除沙箱；已经不存在不算错误。
	Remove(ctx context.Context, id string) error
}

// PersistentSandbox 是一台常驻沙箱的连接。Destroy 会删除整台电脑，常驻用法下只有“重置 / 回收”才调用。
type PersistentSandbox interface {
	Sandbox
	ID() string
}

type SandboxSpec struct {
	Image          string
	Env            map[string]string
	Timeout        time.Duration
	AllowedHosts   []string
	DiskQuotaBytes int64
}

type Sandbox interface {
	Exec(context.Context, Command) (Result, error)
	WriteFile(context.Context, string, []byte) error
	ListFiles(context.Context, string) ([]File, error)
	ReadFile(context.Context, string) (io.ReadCloser, error)
	Destroy(context.Context) error
}

type Command struct {
	Args    []string
	Dir     string
	Env     map[string]string
	Timeout time.Duration

	// OnStdout receives stdout chunks in order during Exec, before any capture limit.
	OnStdout func(string)
}

type Result struct {
	Stdout, Stderr string
	ExitCode       int
}

type File struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}
