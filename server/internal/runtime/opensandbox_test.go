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
	"testing/iotest"
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
	var streamed strings.Builder
	result, err := sandbox.Exec(ctx, Command{Args: []string{"print", "a b", "x';y"}, Timeout: time.Second, OnStdout: func(chunk string) {
		streamed.WriteString(chunk)
	}})
	if err != nil || result.Stdout != "ok\n" || result.ExitCode != 0 {
		t.Fatalf("unexpected execution: %+v, %v", result, err)
	}
	if streamed.String() != result.Stdout {
		t.Fatalf("streamed stdout = %q, want %q", streamed.String(), result.Stdout)
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
	if _, err := parseCommandStream(strings.NewReader(`{"type":"stdout","text":"partial"}`), nil); err == nil {
		t.Fatal("an interrupted stream must not be reported as success")
	}
	result, err := parseCommandStream(strings.NewReader(`{"type":"error","error":{"exit_code":42}}`), nil)
	if err != nil || result.ExitCode != 42 {
		t.Fatalf("unexpected result: %+v, %v", result, err)
	}
}

func TestCommandStreamDecodesExecdExitError(t *testing.T) {
	stream := strings.NewReader(`{"type":"stderr","text":"diagnostic"}
{"type":"error","error":{"ename":"CommandExecError","evalue":"1","traceback":["exit status 1"]}}`)
	result, err := parseCommandStream(stream, nil)
	if err != nil || result.ExitCode != 1 || result.Stderr != "diagnostic" {
		t.Fatalf("unexpected result: %+v, %v", result, err)
	}
}

func TestCommandStreamReadsDSHRecordsWithoutNewlines(t *testing.T) {
	var final DSHFinalOutput
	stream := strings.NewReader(`{"type":"stdout","text":"{\"type\":\"session\",\"sessionId\":\"test\"}"}
{"type":"stdout","text":"{\"type\":\"text\",\"text\":\"progress\"}"}
{"type":"stdout","text":"{\"type\":\"final\",\"text\":\"report\"}"}
{"type":"execution_complete","exit_code":0}`)
	if _, err := parseCommandStream(stream, final.Add); err != nil {
		t.Fatal(err)
	}
	if reply := final.Reply(); reply != "report" {
		t.Fatalf("final reply = %q, want report", reply)
	}
}

func TestCommandStreamKeepsSplitDSHFinalAfterOutputLimit(t *testing.T) {
	encodeChunk := func(chunk string) string {
		encoded, err := json.Marshal(chunk)
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("data: {\"type\":\"stdout\",\"text\":%s}\n\n", encoded)
	}
	progress := `{"type":"thinking","text":"` + strings.Repeat("x", 8192) + "\"}\n"
	stream := strings.Repeat(encodeChunk(progress), 1+maxOutput/len(progress))
	stream += encodeChunk("{\"type\":\"final\",\"text\":\"earlier reply\"}\n")
	stream += encodeChunk(strings.Repeat("x", maxDSHRecord)) + encodeChunk("too long\n")
	stream += encodeChunk(`{"type":"fin`) + encodeChunk(`al","text":"报告已生成`) + encodeChunk("\"}\n")
	stream += encodeChunk("{\"type\":\"text\",\"text\":\"not the reply\"}\n")
	stream += "{\"type\":\"execution_complete\",\"exit_code\":0}\n"

	var final DSHFinalOutput
	result, err := parseCommandStream(iotest.HalfReader(strings.NewReader(stream)), final.Add)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Stdout) != maxOutput || strings.Contains(result.Stdout, "报告已生成") {
		t.Fatalf("raw stdout capture was not capped at %d bytes", maxOutput)
	}
	if got := final.Reply(); got != "报告已生成" {
		t.Fatalf("final reply = %q, want %q", got, "报告已生成")
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

// provider 控制面 TCP 连上却不回响应头时，请求必须在 ResponseHeaderTimeout 内失败：
// 修前用 http.DefaultTransport（没有 ResponseHeaderTimeout）会一直阻塞，调用它的回收器跟着停摆。
// 测试自己再包一层 ctx 超时，修前也只会等这么久而不是永久挂住。
func TestOpenSandboxResponseHeaderTimeout(t *testing.T) {
	old := openSandboxResponseHeaderTimeout
	openSandboxResponseHeaderTimeout = 100 * time.Millisecond
	defer func() { openSandboxResponseHeaderTimeout = old }()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		<-r.Context().Done() // 不写响应头，一直挂着，直到客户端放弃
	}))
	defer server.Close()

	p, err := NewOpenSandbox(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := p.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("client transport = %T, want *http.Transport", p.client.Transport)
	}
	if transport.ResponseHeaderTimeout != openSandboxResponseHeaderTimeout {
		t.Fatalf("ResponseHeaderTimeout = %v, want %v", transport.ResponseHeaderTimeout, openSandboxResponseHeaderTimeout)
	}
	if transport.DialContext == nil || transport.Proxy == nil ||
		transport.TLSHandshakeTimeout != 10*time.Second || transport.IdleConnTimeout != 90*time.Second {
		t.Fatalf("transport timeouts are incomplete: %+v", transport)
	}
	if p.client.Timeout != 0 {
		t.Fatalf("Client.Timeout = %v; it would cut Exec's SSE stream short", p.client.Timeout)
	}
	if p.client.CheckRedirect == nil {
		t.Fatal("CheckRedirect must be kept")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if _, err := p.FindPersistent(ctx, "creation-key"); err == nil {
		t.Fatal("a provider that never sends response headers must not look successful")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("request blocked for %v; ResponseHeaderTimeout was not applied", elapsed)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

// 分页接口一直回 hasNextPage 时 FindPersistent 必须有页数上限，不能无限翻下去。
func TestOpenSandboxFindPersistentPageLimit(t *testing.T) {
	old := openSandboxMaxPages
	openSandboxMaxPages = 3
	defer func() { openSandboxMaxPages = old }()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `{"items":[],"pagination":{"hasNextPage":true}}`)
	}))
	defer server.Close()
	p, err := NewOpenSandbox(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := p.FindPersistent(ctx, "creation-key"); err == nil {
		t.Fatal("an endless sandbox list must fail instead of paging forever")
	}
	if got := requests.Load(); got != int32(openSandboxMaxPages) {
		t.Fatalf("pages requested = %d, want %d", got, openSandboxMaxPages)
	}
}
