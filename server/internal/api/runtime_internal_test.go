package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/runtime"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

type memoryRuntime struct {
	files     map[string]string
	run       func(runtime.Command) error
	final     string // 非空时替换 JSON 模式下默认的最终回复 "pilot summary"
	destroyed bool
}

func (m *memoryRuntime) Create(context.Context, runtime.SandboxSpec) (runtime.Sandbox, error) {
	m.files = map[string]string{}
	return m, nil
}

func (m *memoryRuntime) Exec(_ context.Context, c runtime.Command) (runtime.Result, error) {
	if c.Args[0] == "mkdir" || c.Args[0] == "rm" {
		return runtime.Result{}, nil
	}
	if err := m.run(c); err != nil {
		return runtime.Result{}, err
	}
	m.files[runtimeOutputDir+"/summary.txt"] = "pilot summary"
	if c.OnStdout != nil && m.final != "" {
		text, _ := json.Marshal(m.final)
		c.OnStdout(`{"type":"final","sessionId":"test","reason":{"kind":"completed"},"text":` + string(text) + "}\n")
		return runtime.Result{}, nil
	}
	if c.OnStdout != nil {
		c.OnStdout(`{"type":"session","sessionId":"test"}
{"type":"thinking","text":"Checking the request"}
{"type":"text","text":"I will query the enterprise source"}
{"type":"tool_result","content":"Intermediate tool output"}
{"type":"final","sessionId":"test","reason":{"kind":"completed"},"text":"pilot summary"}
`)
		return runtime.Result{}, nil
	}
	return runtime.Result{Stdout: "pilot summary"}, nil
}

func (m *memoryRuntime) WriteFile(_ context.Context, name string, value []byte) error {
	m.files[name] = string(value)
	return nil
}

func (m *memoryRuntime) ListFiles(context.Context, string) ([]runtime.File, error) {
	return []runtime.File{{Path: runtimeOutputDir + "/summary.txt", Type: "file", Size: 13}}, nil
}

func (m *memoryRuntime) ReadFile(_ context.Context, name string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(m.files[name])), nil
}

func (m *memoryRuntime) Destroy(context.Context) error {
	m.destroyed = true
	return nil
}

func TestCloudRuntimePilot(t *testing.T) {
	m := &memoryRuntime{}
	runRuntimePilot(t, m, "unused-image", []string{"dsh", "--profile", "headless", "--patch", "{patch}", "--json", "{prompt}"}, func(s *Server, employee int64) {
		m.run = func(c runtime.Command) error {
			if c.OnStdout == nil {
				t.Fatal("DSH JSON output must be read from the stdout stream")
			}
			token := c.Env["DSH_EE_RUNTIME_TOKEN"]
			patch := m.files[runtimePatchPath]
			if !strings.HasPrefix(token, runtimeTokenPrefix) || !strings.Contains(patch, "- insert:\n    - id: dsh-ee-runtime-mcp") ||
				!strings.Contains(patch, "https://gateway.example/mcp") {
				t.Fatal("missing per-run credential or gateway patch")
			}
			if _, err := s.resolveBearer(context.Background(), token, ""); err == nil {
				t.Fatal("runtime credential accepted outside the model gateway and MCP")
			}
			pr, err := s.resolveBearer(context.WithValue(context.Background(), runtimeTokenRouteKey{}, true), token, "")
			if err != nil || !pr.Runtime || pr.User.ID != employee || pr.AdminLive {
				t.Fatalf("unexpected runtime principal: %v", err)
			}
			req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ee_write","arguments":{}}}`))
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Tool unavailable to read-only cloud tasks") {
				t.Fatalf("read-only MCP rejection: HTTP %d", rec.Code)
			}
			return nil
		}
	})
	if !m.destroyed {
		t.Fatal("sandbox was not destroyed")
	}
}

func TestRuntimeSummary(t *testing.T) {
	const token = "ee_at_rt_test"
	for _, tc := range []struct {
		name, stdout, want string
		jsonOutput         bool
	}{
		{"missing final", `{"type":"text","text":"not an answer"}`, "", true},
		{"truncated final", "{\"type\":\"text\",\"text\":\"progress\"}\n{\"type\":\"final\",\"text\":\"partial", "", true},
		{"multiline final", `{"type":"final","text":"  报告已生成\n下载文件。  "}`, "报告已生成\n下载文件。", true},
		{"redacted final", `{"type":"final","text":"` + token + `"}`, "[redacted]", true},
		{"clipped final", `{"type":"final","text":"` + strings.Repeat("报", 2401) + `"}`, strings.Repeat("报", 2400) + "…", true},
		{"custom plain stdout", "  pilot summary\n", "pilot summary", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reply := tc.stdout
			if tc.jsonOutput {
				var final runtime.DSHFinalOutput
				final.Add(tc.stdout)
				reply = final.Reply()
			}
			if got := runtimeSummary(reply, token); got != tc.want {
				t.Fatalf("summary = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRuntimeExecError(t *testing.T) {
	const token = "ee_at_rt_test"
	withStderr := runtime.Result{ExitCode: 1, Stderr: "dsh: " + token}
	if got := runtimeExecError(withStderr, nil, "", token).Error(); got != "headless DSH exited with status 1: dsh: [redacted]" {
		t.Fatalf("stderr diagnostic = %q", got)
	}
	var final runtime.DSHFinalOutput
	final.Add(`{"type":"error","message":"unknown option '--patch'"}`)
	if got := runtimeExecError(runtime.Result{ExitCode: 1}, nil, final.Failure(), token).Error(); got != "headless DSH exited with status 1: unknown option '--patch'" {
		t.Fatalf("JSON diagnostic = %q", got)
	}
	if got := runtimeExecError(runtime.Result{}, errors.New("OpenSandbox POST returned HTTP 502"), "", token).Error(); got != "headless DSH execution failed: OpenSandbox POST returned HTTP 502" {
		t.Fatalf("exec diagnostic = %q", got)
	}
}

func TestLiveCloudRuntimePilot(t *testing.T) {
	base := os.Getenv("DSH_EE_TEST_OPENSANDBOX_URL")
	if base == "" {
		t.Skip("DSH_EE_TEST_OPENSANDBOX_URL is not set")
	}
	provider, err := runtime.NewOpenSandbox(base, os.Getenv("DSH_EE_TEST_OPENSANDBOX_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	runRuntimePilot(t, provider, "alpine:3.22", []string{"sh", "-c", `printf 'pilot summary' > "$DSH_EE_OUTPUT_DIR/summary.txt"; printf 'pilot summary'`}, nil)
}

func newRuntimeBotTest(t *testing.T) (*Server, *store.User, func(string, string, string) *httptest.ResponseRecorder) {
	t.Helper()
	st := openAuthenticateTestStore(t)
	ctx := context.Background()
	if _, err := Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	admin, err := st.GetUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	role, err := st.GetRoleByCode(ctx, rbac.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	employee := &store.User{Username: "runtime-owner", DisplayName: "Runtime Owner"}
	if err := st.CreateUser(ctx, employee); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserRoles(ctx, employee.ID, []int64{role.ID}); err != nil {
		t.Fatal(err)
	}
	s := New(st, nil, Options{DataDir: t.TempDir(), Runtime: runtime.Settings{Provider: &memoryRuntime{}}},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	return s, employee, runtimeTestClient(t, s, admin.ID)
}

func runtimeTestClient(t *testing.T, s *Server, id int64) func(string, string, string) *httptest.ResponseRecorder {
	t.Helper()
	token := secret.Token()
	if err := s.store.CreateSession(context.Background(), secret.Hash(token), id, time.Hour); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	return func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
}

const runtimeBotsTestPath = "/api/admin/runtime/bots"

func runtimeBotTestBody(t *testing.T, owner int64, enabled bool) string {
	t.Helper()
	body, err := json.Marshal(store.RuntimeBot{OwnerID: owner, RunAsID: owner, Name: "Daily summary",
		Prompt: "Summarize", Schedule: "0 9 * * *", ReadOnly: true, Enabled: enabled})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestRuntimeBotUpdateRejectsMalformedIDs(t *testing.T) {
	s, employee, request := newRuntimeBotTest(t)
	body := runtimeBotTestBody(t, employee.ID, true)
	for _, id := range []string{"0", "-1", "invalid"} {
		rec := request("PUT", runtimeBotsTestPath+"/"+id, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("update %q: HTTP %d, %s", id, rec.Code, rec.Body.String())
		}
	}
	bots, err := s.store.RuntimeBots(context.Background(), false)
	if err != nil || len(bots) != 0 {
		t.Fatalf("invalid update created a bot: %v (%v)", bots, err)
	}
	if rec := request("POST", runtimeBotsTestPath, body); rec.Code != http.StatusOK {
		t.Fatalf("create bot: HTTP %d, %s", rec.Code, rec.Body.String())
	}
}

func TestRuntimeBotCanRetireAfterEmployeeLosesEligibility(t *testing.T) {
	s, employee, request := newRuntimeBotTest(t)
	enabled := runtimeBotTestBody(t, employee.ID, true)
	rec := request("POST", runtimeBotsTestPath, enabled)
	var bot store.RuntimeBot
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &bot) != nil || !bot.Enabled {
		t.Fatalf("create enabled bot: HTTP %d, %s", rec.Code, rec.Body.String())
	}
	disabled := true
	ctx := context.Background()
	if err := s.store.UpdateUser(ctx, employee.ID, store.UserPatch{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	path := runtimeBotsTestPath + "/" + strconv.FormatInt(bot.ID, 10)
	rec = request("PUT", path, runtimeBotTestBody(t, employee.ID, false))
	if rec.Code != http.StatusOK {
		t.Fatalf("disable bot: HTTP %d, %s", rec.Code, rec.Body.String())
	}
	if rec := request("PUT", path, enabled); rec.Code != http.StatusNotFound {
		t.Fatalf("re-enable ineligible bot: HTTP %d, %s", rec.Code, rec.Body.String())
	}
	got, err := s.store.RuntimeBot(ctx, bot.ID)
	if err != nil || got.Enabled {
		t.Fatalf("bot was not retired: %v (%v)", got, err)
	}
}

func TestRuntimeTaskAdmissionLimits(t *testing.T) {
	s, employee, admin := newRuntimeBotTest(t)
	ctx := context.Background()
	request := runtimeTestClient(t, s, employee.ID)
	rec := admin("POST", runtimeBotsTestPath, runtimeBotTestBody(t, employee.ID, true))
	var bot store.RuntimeBot
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &bot) != nil {
		t.Fatalf("create bot: HTTP %d, %s", rec.Code, rec.Body.String())
	}
	runPath := runtimeBotsTestPath + "/" + strconv.FormatInt(bot.ID, 10) + "/run"
	if rec := admin("POST", runPath, ""); rec.Code != http.StatusAccepted {
		t.Fatalf("run bot: HTTP %d, %s", rec.Code, rec.Body.String())
	}
	const slot = "2026-11-22T09:00"
	if _, err := s.enqueueCloudBot(ctx, &bot, slot); !errors.Is(err, store.ErrRuntimeBotBusy) {
		t.Fatalf("scheduler did not skip queued bot: %v", err)
	}
	running, err := s.store.ClaimRuntimeTask(ctx, runtimeAttempts, 45*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.enqueueCloudBot(ctx, &bot, slot); !errors.Is(err, store.ErrRuntimeBotBusy) {
		t.Fatalf("scheduler did not skip running bot: %v", err)
	}
	const tasks = "/api/runtime/tasks"
	const same = `{"prompt":"Summary","idempotency_key":"same"}`
	for i := 1; i < store.RuntimeTaskQueueLimit; i++ {
		body := `{"prompt":"Summary"}`
		if i == 1 {
			body = same
		}
		if rec := request("POST", tasks, body); rec.Code != http.StatusAccepted {
			t.Fatalf("enqueue below limit: HTTP %d, %s", rec.Code, rec.Body.String())
		}
	}
	if rec := request("POST", tasks, same); rec.Code != http.StatusAccepted {
		t.Fatalf("idempotent replay at capacity: HTTP %d, %s", rec.Code, rec.Body.String())
	}
	if rec := request("POST", tasks, `{"prompt":"Overflow"}`); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("submit at capacity: HTTP %d, %s", rec.Code, rec.Body.String())
	}
	if rec := admin("POST", runPath, ""); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("bot run at capacity: HTTP %d, %s", rec.Code, rec.Body.String())
	}
	if err := s.store.FinishRuntimeTask(ctx, running, store.RuntimeSucceeded, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.enqueueCloudBot(ctx, &bot, slot); err != nil {
		t.Fatalf("scheduler could not enqueue after old run finished: %v", err)
	}
}

func runRuntimePilot(t *testing.T, provider runtime.Provider, image string, command []string, setup func(*Server, int64)) {
	t.Helper()
	st := openAuthenticateTestStore(t)
	ctx := context.Background()
	if _, err := Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	role, err := st.GetRoleByCode(ctx, rbac.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	s := New(st, nil, Options{DataDir: t.TempDir(), Runtime: runtime.Settings{
		Provider: provider, Image: image, BaseURL: "https://gateway.example", Model: "company-chat", Command: command,
	}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	client := func(name string) (int64, func(string, string, string) *httptest.ResponseRecorder) {
		u := &store.User{Username: name, DisplayName: name}
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		if err := st.SetUserRoles(ctx, u.ID, []int64{role.ID}); err != nil {
			t.Fatal(err)
		}
		token := secret.Token()
		if err := st.CreateSession(ctx, secret.Hash(token), u.ID, time.Hour); err != nil {
			t.Fatal(err)
		}
		h := s.Handler()
		return u.ID, func(method, path, body string) *httptest.ResponseRecorder {
			r := httptest.NewRequest(method, path, strings.NewReader(body))
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
			r.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			return rec
		}
	}
	employee, request := client("runtime-owner")
	_, other := client("runtime-unrelated")
	if setup != nil {
		setup(s, employee)
	}
	b := &store.RuntimeBot{ID: 1, OwnerID: employee, RunAsID: employee, Prompt: "Daily summary", ReadOnly: true}
	first, err := s.enqueueCloudBot(ctx, b, "2026-11-22T09:00")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.enqueueCloudBot(ctx, b, "2026-11-22T09:00")
	if err != nil || first.ID != second.ID {
		t.Fatalf("schedule slot not idempotent: %v", err)
	}
	task, err := st.ClaimRuntimeTask(ctx, runtimeAttempts, 45*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	s.runCloudClaim(ctx, task)
	got, err := st.RuntimeTask(ctx, task.ID, employee)
	if err != nil || got.Status != store.RuntimeSucceeded || got.Summary != "pilot summary" {
		t.Fatalf("task outcome: %+v, %v", got, err)
	}
	base := "/api/runtime/tasks/" + strconv.FormatInt(task.ID, 10)
	for _, suffix := range []string{"", "/events", "/artifacts"} {
		if rec := other("GET", base+suffix, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("cross-owner %q: %d", suffix, rec.Code)
		}
	}
	rec := request("GET", base+"/events", "")
	var events []store.RuntimeTaskEvent
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &events) != nil || len(events) != 3 ||
		events[0].Status != store.RuntimeQueued || events[1].Status != store.RuntimeRunning || events[2].Status != store.RuntimeSucceeded {
		t.Fatalf("task events: %d %s", rec.Code, rec.Body.String())
	}
	rec = request("GET", base+"/artifacts", "")
	var artifacts []store.RuntimeArtifact
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &artifacts) != nil || len(artifacts) != 1 {
		t.Fatalf("artifact list: %d %s", rec.Code, rec.Body.String())
	}
	rec = request("GET", base+"/artifacts/"+strconv.FormatInt(artifacts[0].ID, 10), "")
	if rec.Code != http.StatusOK || rec.Body.String() != "pilot summary" {
		t.Fatalf("artifact download: %d", rec.Code)
	}
}
