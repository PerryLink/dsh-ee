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
	Image        string
	Env          map[string]string
	Timeout      time.Duration
	AllowedHosts []string
}

type Sandbox interface {
	Exec(context.Context, Command) (Result, error)
	WriteFile(context.Context, string, []byte) error
	// ListFiles lists the entries under a directory, descendants included up to ListDepth levels
	// (1 is the directory's own children). Paths are absolute.
	ListFiles(context.Context, string) ([]File, error)
	ReadFile(context.Context, string) (io.ReadCloser, error)
	Destroy(context.Context) error
}

// ListDepth is how many directory levels ListFiles reports below the listed directory.
const ListDepth = 3

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
