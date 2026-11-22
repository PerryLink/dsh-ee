package runtime

import (
	"context"
	"io"
	"time"
)

type Provider interface {
	Create(context.Context, SandboxSpec) (Sandbox, error)
}

type SandboxSpec struct {
	Image   string
	Env     map[string]string
	Timeout time.Duration
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
