package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenSandboxLifecycleAndExecd(t *testing.T) {
	var lifecycle, execd atomic.Int32
	data := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		execd.Add(1)
		if r.Header.Get("X-EXECD-ACCESS-TOKEN") != "execd" || r.Header.Get(openSandboxKey) != "" {
			t.Error("incorrect execd credentials")
		}
		if r.URL.Query().Has("path") && r.URL.Query().Get("path") != "/out/summary & notes.txt" && r.URL.Query().Get("path") != "/out" {
			t.Error("wrong file path")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /sandbox/ping":
			w.WriteHeader(http.StatusOK)
		case "POST /sandbox/command":
			var input struct {
				Command string
				Timeout int
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Command != `exec 'print' 'a b' 'x'"'"';y'` || input.Timeout != 1000 {
				t.Errorf("incorrect command: %+v (%v)", input, err)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"type\":\"stdout\",\"text\":\"ok\\n\"}\n\n{\"type\":\"execution_complete\",\"exit_code\":0}\n\n")
		case "POST /sandbox/files/upload":
			form, err := r.MultipartReader()
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			for _, want := range []string{"metadata", "file"} {
				part, err := form.NextPart()
				if err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				b, err := io.ReadAll(part)
				if err != nil || part.FormName() != want {
					t.Errorf("invalid multipart part %q: %v", want, err)
				}
				if want == "metadata" && (!strings.Contains(string(b), `"path":"/out/prompt.txt"`) || part.FileName() != "metadata.json") || want == "file" && string(b) != "prompt" {
					t.Errorf("incorrect multipart content: %s", b)
				}
			}
		case "GET /sandbox/directories/list":
			_, _ = io.WriteString(w, `[{"path":"/out/summary & notes.txt","type":"file","size":2}]`)
		case "GET /sandbox/files/download":
			_, _ = io.WriteString(w, "ok")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer data.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lifecycle.Add(1)
		if r.Header.Get(openSandboxKey) != "lifecycle" {
			t.Error("missing lifecycle credentials")
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/sandboxes":
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"id":"box","status":{"state":"Pending"}}`)
		case "GET /v1/sandboxes/box":
			_, _ = io.WriteString(w, `{"id":"box","status":{"state":"Running"}}`)
		case "GET /v1/sandboxes/box/endpoints/44772":
			_, _ = fmt.Fprintf(w, `{"endpoint":"%s/sandbox","headers":{"X-EXECD-ACCESS-TOKEN":"execd"}}`, strings.TrimPrefix(data.URL, "http://"))
		case "DELETE /v1/sandboxes/box":
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, err := NewOpenSandbox(server.URL, "lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	sandbox, err := p.Create(ctx, SandboxSpec{Image: "sandbox-image", Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := sandbox.WriteFile(ctx, "/out/prompt.txt", []byte("prompt")); err != nil {
		t.Fatal(err)
	}
	result, err := sandbox.Exec(ctx, Command{Args: []string{"print", "a b", "x';y"}, Timeout: time.Second})
	if err != nil || result.Stdout != "ok\n" || result.ExitCode != 0 {
		t.Fatalf("unexpected execution: %+v, %v", result, err)
	}
	files, err := sandbox.ListFiles(ctx, "/out")
	if err != nil || len(files) != 1 || files[0].Size != 2 || files[0].Type != "file" {
		t.Fatalf("unexpected files: %+v, %v", files, err)
	}
	file, err := sandbox.ReadFile(ctx, files[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(file)
	file.Close()
	if err != nil || string(b) != "ok" {
		t.Fatalf("unexpected file: %q, %v", b, err)
	}
	if err := sandbox.Destroy(ctx); err != nil {
		t.Fatal(err)
	}
	if lifecycle.Load() != 4 || execd.Load() != 5 {
		t.Errorf("lifecycle calls = %d, execd calls = %d", lifecycle.Load(), execd.Load())
	}
}

func TestCommandStreamRequiresTerminalEvent(t *testing.T) {
	if _, err := parseCommandStream(strings.NewReader(`{"type":"stdout","text":"partial"}`)); err == nil {
		t.Fatal("an interrupted stream must not be reported as success")
	}
	result, err := parseCommandStream(strings.NewReader(`{"type":"error","error":{"exit_code":42}}`))
	if err != nil || result.ExitCode != 42 {
		t.Fatalf("unexpected result: %+v, %v", result, err)
	}
}

func TestLiveOpenSandbox(t *testing.T) {
	base := os.Getenv("DSH_EE_TEST_OPENSANDBOX_URL")
	if base == "" {
		t.Skip("DSH_EE_TEST_OPENSANDBOX_URL is not set")
	}
	p, err := NewOpenSandbox(base, os.Getenv("DSH_EE_TEST_OPENSANDBOX_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	image := os.Getenv("DSH_EE_TEST_OPENSANDBOX_IMAGE")
	if image == "" {
		image = "alpine:3.22"
	}
	s, err := p.Create(ctx, SandboxSpec{Image: image, Timeout: 3 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := s.Destroy(ctx); err != nil {
			t.Error(err)
		}
	}()
	if err := s.WriteFile(ctx, "/tmp/dsh-ee-runtime-smoke.txt", []byte("sandbox smoke")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Exec(ctx, Command{Args: []string{"cat", "/tmp/dsh-ee-runtime-smoke.txt"}, Timeout: 10 * time.Second})
	if err != nil || got.ExitCode != 0 || got.Stdout != "sandbox smoke" {
		t.Fatalf("unexpected smoke command: %+v %v", got, err)
	}
}
