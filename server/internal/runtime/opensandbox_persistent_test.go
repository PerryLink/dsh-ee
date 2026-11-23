package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeLifecycle 模拟 OpenSandbox 生命周期 API 的暂停 / 恢复状态机：pause 和 resume 都是异步的，
// 先进入 Pausing / Resuming，下一次 GET 才落到 Paused / Running。
type fakeLifecycle struct {
	mu      sync.Mutex
	state   string
	created map[string]any
	calls   []string
}

func (f *fakeLifecycle) handler(t *testing.T, execd string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/sandboxes":
			if err := json.NewDecoder(r.Body).Decode(&f.created); err != nil {
				t.Error(err)
			}
			f.state = "Running"
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"id":"bot-box","status":{"state":"Pending"}}`)
		case "GET /v1/sandboxes/bot-box":
			if f.state == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			current := f.state
			switch f.state {
			case "Pausing":
				f.state = "Paused"
			case "Resuming":
				f.state = "Running"
			}
			_, _ = fmt.Fprintf(w, `{"id":"bot-box","status":{"state":%q}}`, current)
		case "POST /v1/sandboxes/bot-box/pause":
			if f.state != "Running" {
				w.WriteHeader(http.StatusConflict)
				return
			}
			f.state = "Pausing"
			w.WriteHeader(http.StatusAccepted)
		case "POST /v1/sandboxes/bot-box/resume":
			if f.state != "Paused" {
				w.WriteHeader(http.StatusConflict)
				return
			}
			f.state = "Resuming"
			w.WriteHeader(http.StatusAccepted)
		case "GET /v1/sandboxes/bot-box/endpoints/44772":
			if f.state != "Running" {
				t.Errorf("endpoint requested while %s", f.state)
			}
			_, _ = fmt.Fprintf(w, `{"endpoint":"%s/sandbox"}`, strings.TrimPrefix(execd, "http://"))
		case "DELETE /v1/sandboxes/bot-box":
			f.state = ""
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func TestOpenSandboxHibernateAndWake(t *testing.T) {
	execd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer execd.Close()
	f := &fakeLifecycle{}
	server := httptest.NewServer(f.handler(t, execd.URL))
	defer server.Close()
	p, err := NewOpenSandbox(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	box, err := p.CreatePersistent(ctx, SandboxSpec{Image: "img", DiskQuotaBytes: 2 << 30})
	if err != nil {
		t.Fatal(err)
	}
	if box.ID() != "bot-box" {
		t.Fatalf("id = %q", box.ID())
	}
	if _, ok := f.created["timeout"]; ok {
		t.Fatal("a persistent sandbox must not carry an OpenSandbox TTL")
	}
	if _, ok := f.created["env"]; ok {
		t.Fatal("a persistent sandbox must not be created with environment variables")
	}
	limits, ok := f.created["resourceLimits"].(map[string]any)
	if !ok || limits["disk"] != "2147483648" {
		t.Fatalf("provider must request the native disk quota in bytes: %v", limits)
	}
	if err := p.Hibernate(ctx, "bot-box"); err != nil {
		t.Fatal(err)
	}
	if f.state != "Paused" {
		t.Fatalf("state after hibernate = %s", f.state)
	}
	// 已经休眠时再休眠是空操作。
	if err := p.Hibernate(ctx, "bot-box"); err != nil {
		t.Fatal(err)
	}
	woke, err := p.Wake(ctx, "bot-box")
	if err != nil {
		t.Fatal(err)
	}
	if f.state != "Running" || woke.ID() != "bot-box" {
		t.Fatalf("state after wake = %s", f.state)
	}
	// 运行中的电脑直接连上，不发 resume。
	before := len(f.calls)
	if _, err := p.Wake(ctx, "bot-box"); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls[before:] {
		if strings.HasSuffix(c, "/resume") {
			t.Fatal("resume sent to a running sandbox")
		}
	}
	if err := p.Remove(ctx, "bot-box"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Wake(ctx, "bot-box"); !errors.Is(err, ErrSandboxGone) {
		t.Fatalf("wake after removal = %v, want ErrSandboxGone", err)
	}
	if err := p.Hibernate(ctx, "bot-box"); !errors.Is(err, ErrSandboxGone) {
		t.Fatalf("hibernate after removal = %v, want ErrSandboxGone", err)
	}
	if err := p.Remove(ctx, "bot-box"); err != nil {
		t.Fatalf("removing a missing sandbox: %v", err)
	}
}

func TestOpenSandboxWakeTerminated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"bot-box","status":{"state":"Terminated"}}`)
	}))
	defer server.Close()
	p, _ := NewOpenSandbox(server.URL, "key")
	if _, err := p.Wake(context.Background(), "bot-box"); !errors.Is(err, ErrSandboxGone) {
		t.Fatalf("wake terminated = %v", err)
	}
}
