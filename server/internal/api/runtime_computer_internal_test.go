package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/runtime"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// fakeComputers 是一个支持休眠 / 唤醒的假 provider：每台电脑有自己的文件系统，休眠中的电脑拒绝执行命令，
// 所以文件能在下一轮读到，只可能是因为同一台电脑被唤醒了。
type fakeComputers struct {
	mu                 sync.Mutex
	boxes              map[string]*fakeBox
	next               int
	creates            int
	ephemeral          int
	wakes              int
	hibernates         int
	disk               int64 // du 报告的 KB
	diskQuota          int64
	tokens             []string
	check              func(token string) // 每轮 DSH 运行时检查凭证
	loseCreateResponse bool
	hideCreatedBox     bool
	allowedHost        string
}

type fakeBox struct {
	owner       *fakeComputers
	id          string
	paused      bool
	files       map[string]string
	env         map[string]string
	creationKey string
}

func (f *fakeComputers) newBox(env map[string]string) *fakeBox {
	f.next++
	b := &fakeBox{owner: f, id: "box-" + strconv.Itoa(f.next), files: map[string]string{}, env: env}
	if f.boxes == nil {
		f.boxes = map[string]*fakeBox{}
	}
	f.boxes[b.id] = b
	return b
}

func (f *fakeComputers) Create(_ context.Context, spec runtime.SandboxSpec) (runtime.Sandbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ephemeral++
	return f.newBox(spec.Env), nil
}

func (f *fakeComputers) CreatePersistent(_ context.Context, spec runtime.SandboxSpec) (runtime.PersistentSandbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates++
	f.diskQuota = spec.DiskQuotaBytes
	b := f.newBox(spec.Env)
	b.creationKey = spec.CreationKey
	f.allowedHost = spec.AllowedHosts[0]
	if f.loseCreateResponse {
		return nil, errors.New("lost create response")
	}
	return b, nil
}

func (f *fakeComputers) FindPersistent(_ context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hideCreatedBox {
		return "", runtime.ErrSandboxGone
	}
	for _, b := range f.boxes {
		if b.creationKey == key {
			return b.id, nil
		}
	}
	return "", runtime.ErrSandboxGone
}

func (f *fakeComputers) ReplaceAllowedHosts(_ context.Context, id string, hosts []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.boxes[id]; !ok {
		return runtime.ErrSandboxGone
	}
	f.allowedHost = hosts[0]
	return nil
}

func (f *fakeComputers) CheckPersistent(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.boxes[id]; !ok {
		return runtime.ErrSandboxGone
	}
	return nil
}

func (f *fakeComputers) Wake(_ context.Context, id string) (runtime.PersistentSandbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.boxes[id]
	if !ok {
		return nil, runtime.ErrSandboxGone
	}
	if b.paused {
		b.paused = false
		f.wakes++
	}
	return b, nil
}

func (f *fakeComputers) Hibernate(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.boxes[id]
	if !ok {
		return runtime.ErrSandboxGone
	}
	if !b.paused {
		f.hibernates++
	}
	b.paused = true
	return nil
}

func (f *fakeComputers) Remove(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.boxes, id)
	return nil
}

func (b *fakeBox) ID() string { return b.id }

func (b *fakeBox) Exec(_ context.Context, c runtime.Command) (runtime.Result, error) {
	b.owner.mu.Lock()
	if b.paused || b.owner.boxes[b.id] != b {
		b.owner.mu.Unlock()
		return runtime.Result{}, errors.New("sandbox is not running")
	}
	b.owner.mu.Unlock()
	switch c.Args[0] {
	case "rm":
		for name := range b.files {
			if strings.HasPrefix(name, c.Args[2]+"/") {
				delete(b.files, name)
			}
		}
		return runtime.Result{}, nil
	case "mkdir":
		return runtime.Result{}, nil
	case "du":
		return runtime.Result{Stdout: strconv.FormatInt(b.owner.disk, 10) + "\t" + c.Args[2] + "\n0\t" + c.Args[3] + "\n"}, nil
	}
	// 模拟 DSH：命令是 fake-dsh {prompt}；提示词里的 WRITE:<text> 写进工作目录，READ 读出来作为回复。
	token := c.Env["DSH_EE_RUNTIME_TOKEN"]
	b.owner.mu.Lock()
	b.owner.tokens = append(b.owner.tokens, token)
	check := b.owner.check
	b.owner.mu.Unlock()
	if check != nil {
		check(token)
	}
	prompt := c.Args[len(c.Args)-1]
	note := path.Join(c.Dir, "note.txt")
	switch {
	case strings.Contains(prompt, "WRITE:"):
		text, _, _ := strings.Cut(prompt[strings.Index(prompt, "WRITE:")+6:], "\n")
		b.files[note] = strings.TrimSpace(text)
		b.files[c.Env["DSH_EE_OUTPUT_DIR"]+"/reports/summary.txt"] = "report"
		return runtime.Result{Stdout: "saved"}, nil
	case strings.Contains(prompt, "READ"):
		if v, ok := b.files[note]; ok {
			return runtime.Result{Stdout: "note: " + v}, nil
		}
		return runtime.Result{Stdout: "no note"}, nil
	}
	return runtime.Result{Stdout: "ok"}, nil
}

func (b *fakeBox) WriteFile(_ context.Context, name string, data []byte) error {
	b.files[name] = string(data)
	return nil
}

func (b *fakeBox) ListFiles(_ context.Context, dir string) ([]runtime.File, error) {
	var files []runtime.File
	for name, data := range b.files {
		if strings.HasPrefix(name, dir+"/") {
			files = append(files, runtime.File{Path: name, Type: "file", Size: int64(len(data))})
		}
	}
	return files, nil
}

func (b *fakeBox) ReadFile(_ context.Context, name string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(b.files[name])), nil
}

func (b *fakeBox) Destroy(ctx context.Context) error { return b.owner.Remove(ctx, b.id) }

type computerTest struct {
	t        *testing.T
	s        *Server
	bot      *store.RuntimeBot
	employee int64
	owner    func(string, string, string) *httptest.ResponseRecorder
	admin    func(string, string, string) *httptest.ResponseRecorder
}

func newComputerTest(t *testing.T, provider runtime.Provider, policy runtime.ComputerPolicy) *computerTest {
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
	u := &store.User{Username: "computer-owner", DisplayName: "Computer Owner"}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserRoles(ctx, u.ID, []int64{role.ID}); err != nil {
		t.Fatal(err)
	}
	s := New(st, nil, Options{DataDir: t.TempDir(), Runtime: runtime.Settings{
		Provider: provider, Image: "img", BaseURL: "https://gateway.example", Model: "company-chat",
		Command: []string{"fake-dsh", "{prompt}"}, Computer: policy,
	}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	on := true
	bot := &store.RuntimeBot{OwnerID: u.ID, RunAsID: u.ID, Name: "Notes", Prompt: "placeholder", Schedule: "0 9 * * *", ReadOnly: true, DeliveryTarget: runtimeFeishuOwner}
	if err := st.SaveRuntimeBot(ctx, bot, &on); err != nil {
		t.Fatal(err)
	}
	return &computerTest{t: t, s: s, bot: bot, employee: u.ID, owner: runtimeTestClient(t, s, u.ID), admin: runtimeTestClient(t, s, admin.ID)}
}

// run 让机器人跑一轮手动任务，返回结束后的任务。
func (c *computerTest) run(prompt string) *store.RuntimeTask {
	c.t.Helper()
	ctx := context.Background()
	b := *c.bot
	b.Prompt = prompt
	if _, err := c.s.enqueueCloudBot(ctx, &b, ""); err != nil {
		c.t.Fatal(err)
	}
	task, err := c.s.store.ClaimRuntimeTask(ctx, runtimeAttempts, 45*time.Second)
	if err != nil {
		c.t.Fatal(err)
	}
	c.s.runCloudClaim(ctx, task)
	got, err := c.s.store.RuntimeTask(ctx, task.ID, c.employee)
	if err != nil {
		c.t.Fatal(err)
	}
	return got
}

func (c *computerTest) profile() runtimeComputerView {
	c.t.Helper()
	rec := c.owner("GET", "/api/runtime/chat/bots/"+strconv.FormatInt(c.bot.ID, 10)+"/profile", "")
	var out struct {
		Computer runtimeComputerView `json:"computer"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		c.t.Fatalf("profile: HTTP %d %s", rec.Code, rec.Body.String())
	}
	return out.Computer
}

func (c *computerTest) tokenValid(token string) bool {
	_, err := c.s.resolveBearer(context.WithValue(context.Background(), runtimeTokenRouteKey{}, true), token, "")
	return err == nil
}

func TestBotComputerSurvivesHibernateAndWake(t *testing.T) {
	f := &fakeComputers{disk: 8}
	c := newComputerTest(t, f, runtime.ComputerPolicy{Persistent: true, IdleTimeout: time.Millisecond, DiskLimitBytes: 1 << 20})
	f.check = func(token string) {
		if !c.tokenValid(token) {
			t.Error("the per-run credential must be valid while the run executes")
		}
	}
	if v := c.profile(); !v.Persistent || v.State != store.RuntimeComputerNone {
		t.Fatalf("initial computer = %+v", v)
	}

	first := c.run("WRITE:季度目标是 42")
	if first.Status != store.RuntimeSucceeded || first.Summary != "saved" {
		t.Fatalf("first run: %+v", first)
	}
	files, err := c.s.store.RuntimeArtifacts(context.Background(), first.ID, c.employee)
	if err != nil || len(files) != 1 || files[0].Name != "reports/summary.txt" {
		t.Fatalf("first run artifacts = %+v, %v", files, err)
	}
	taskPath := fmt.Sprintf("/api/runtime/tasks/%d", first.ID)
	for _, suffix := range []string{"", "/events", "/artifacts", fmt.Sprintf("/artifacts/%d", files[0].ID)} {
		endpoint := taskPath + suffix
		if rec := c.admin("GET", endpoint, ""); rec.Code != http.StatusNotFound {
			t.Errorf("admin can access persistent result %s: HTTP %d", endpoint, rec.Code)
		}
		if rec := c.owner("GET", endpoint, ""); rec.Code != http.StatusOK {
			t.Errorf("owner cannot access persistent result %s: HTTP %d", endpoint, rec.Code)
		}
	}
	runs := decodeRuntimeChat[[]store.RuntimeTask](t, "admin status list", c.admin("GET", "/api/runtime/tasks", ""), http.StatusOK)
	if len(runs) != 1 || runs[0].ID != first.ID || runs[0].Status != store.RuntimeSucceeded ||
		!runs[0].PrivateResult || runs[0].Summary != "" || runs[0].Error != "" {
		t.Fatalf("admin task list must show status without private results: %+v", runs)
	}
	if f.creates != 1 || f.ephemeral != 0 || len(f.boxes) != 1 {
		t.Fatalf("first run should create one persistent computer: creates=%d ephemeral=%d", f.creates, f.ephemeral)
	}
	if f.diskQuota != 0 {
		t.Fatalf("native disk quota must be opt-in, got %d", f.diskQuota)
	}
	if v := c.profile(); v.State != store.RuntimeComputerRunning || v.Busy || v.DiskBytes != 8<<10 {
		t.Fatalf("computer after first run = %+v", v)
	}
	for _, b := range f.boxes {
		if len(b.env) != 0 {
			t.Fatal("persistent computer created with environment variables")
		}
		if _, ok := b.files[runtimePersistentOutputDir+"/reports/summary.txt"]; ok {
			t.Fatal("collected deliverables should not count against persistent disk usage")
		}
	}

	// 空闲超时后回收器把电脑休眠；休眠时上一轮的凭证已经吊销。
	time.Sleep(5 * time.Millisecond)
	c.s.reapCloudComputers(context.Background())
	if f.hibernates != 1 {
		t.Fatalf("idle computer was not hibernated (hibernates=%d)", f.hibernates)
	}
	if v := c.profile(); v.State != store.RuntimeComputerHibernated || v.HibernatedAt == nil {
		t.Fatalf("computer after idle = %+v", v)
	}
	if c.tokenValid(f.tokens[0]) {
		t.Fatal("a hibernated computer must not leave a valid credential behind")
	}

	second := c.run("READ the note")
	if second.Status != store.RuntimeSucceeded || second.Summary != "note: 季度目标是 42" {
		t.Fatalf("second run did not see the file from the first run: %+v", second)
	}
	if f.creates != 1 || f.wakes != 1 {
		t.Fatalf("second run should wake the same computer: creates=%d wakes=%d", f.creates, f.wakes)
	}
	if len(f.tokens) != 2 || f.tokens[0] == f.tokens[1] {
		t.Fatal("every run must get a fresh credential")
	}
	for _, token := range f.tokens {
		if c.tokenValid(token) {
			t.Fatal("credential still valid after its run finished")
		}
	}

	// 手动休眠，再重置：重置后文件没了，下一轮是新电脑。
	base := "/api/runtime/chat/bots/" + strconv.FormatInt(c.bot.ID, 10) + "/computer/"
	cookie := secret.Token()
	if err := c.s.store.CreateSession(context.Background(), secret.Hash(cookie), c.employee, time.Hour); err != nil {
		t.Fatal(err)
	}
	for _, fetchSite := range []string{"same-site", ""} {
		req := httptest.NewRequest("POST", "https://app.example"+base+"reset", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		req.Header.Set("Origin", "https://other.example")
		req.Header.Set("Sec-Fetch-Site", fetchSite)
		rec := httptest.NewRecorder()
		c.s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden || len(f.boxes) != 1 {
			t.Fatalf("cross-origin reset (%q): HTTP %d, %d computers", fetchSite, rec.Code, len(f.boxes))
		}
	}
	if rec := c.owner("POST", base+"hibernate", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"state":"hibernated"`) {
		t.Fatalf("manual hibernate: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.owner("POST", base+"reset", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"state":"none"`) {
		t.Fatalf("reset: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if len(f.boxes) != 0 {
		t.Fatal("reset did not remove the computer")
	}
	if third := c.run("READ the note"); third.Summary != "no note" || f.creates != 2 {
		t.Fatalf("run after reset: %+v creates=%d", third, f.creates)
	}
	if rec := c.owner("POST", base+"reboot", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown action: HTTP %d", rec.Code)
	}
}

func TestBotComputerBusyAndAdmin(t *testing.T) {
	f := &fakeComputers{}
	c := newComputerTest(t, f, runtime.ComputerPolicy{Persistent: true, IdleTimeout: time.Hour, DiskLimitBytes: 1 << 20})
	ctx := context.Background()
	c.run("WRITE:hello")

	// 电脑被别的任务占着：手动操作返回 busy，新任务等不到就重试。
	if _, err := c.s.store.AcquireRuntimeBotComputer(ctx, c.bot.ID, "task:other", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if rec := c.owner("POST", "/api/runtime/chat/bots/"+strconv.FormatInt(c.bot.ID, 10)+"/computer/hibernate", ""); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), codeRuntimeComputerBusy) {
		t.Fatalf("hibernate busy computer: HTTP %d %s", rec.Code, rec.Body.String())
	}
	wait := runtimeComputerWait
	runtimeComputerWait = 0
	defer func() { runtimeComputerWait = wait }()
	if got := c.run("READ"); got.Status != store.RuntimeQueued || f.ephemeral != 0 {
		t.Fatalf("busy computer should requeue the task without a temporary computer: %+v ephemeral=%d", got, f.ephemeral)
	}
	if err := c.s.store.ReleaseRuntimeBotComputer(ctx, c.bot.ID, "task:other"); err != nil {
		t.Fatal(err)
	}
	retry, err := c.s.store.ClaimRuntimeTask(ctx, runtimeAttempts, 45*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.s.runCloudClaim(ctx, retry)
	if got, err := c.s.store.RuntimeTask(ctx, retry.ID, c.employee); err != nil || got.Status != store.RuntimeSucceeded || got.Summary != "note: hello" || f.creates != 1 {
		t.Fatalf("retry should read from the original computer: %+v, %v, creates=%d", got, err, f.creates)
	}

	// 管理员能看到并回收。
	rec := c.admin("GET", "/api/admin/runtime/computers", "")
	var list struct {
		Persistent bool                   `json:"persistent"`
		Computers  []runtimeAdminComputer `json:"computers"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil || !list.Persistent || len(list.Computers) != 1 ||
		list.Computers[0].BotID != c.bot.ID || list.Computers[0].BotName != "Notes" || list.Computers[0].State != store.RuntimeComputerRunning {
		t.Fatalf("admin list: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.owner("GET", "/api/admin/runtime/computers", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("employee reading admin list: HTTP %d", rec.Code)
	}
	path := "/api/admin/runtime/computers/" + strconv.FormatInt(c.bot.ID, 10)
	if rec := c.admin("POST", path+"/hibernate", ""); rec.Code != http.StatusOK || f.hibernates != 1 {
		t.Fatalf("admin hibernate: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.admin("DELETE", path, ""); rec.Code != http.StatusOK || len(f.boxes) != 0 {
		t.Fatalf("admin reclaim: HTTP %d %s (boxes=%d)", rec.Code, rec.Body.String(), len(f.boxes))
	}
	if rec := c.admin("DELETE", "/api/admin/runtime/computers/999999", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("reclaim missing: HTTP %d", rec.Code)
	}
}

func TestBotComputerRetainsLeaseUntilCredentialExpires(t *testing.T) {
	f := &fakeComputers{}
	c := newComputerTest(t, f, runtime.ComputerPolicy{Persistent: true, IdleTimeout: time.Hour, DiskLimitBytes: 1 << 30, NativeDiskQuota: true})
	task := &store.RuntimeTask{ID: 123, BotID: &c.bot.ID, OwnerID: c.employee, RunAsID: c.employee, TimeoutSeconds: 300}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	box, err := c.s.openCloudComputer(ctx, task, runtime.SandboxSpec{Image: "img", AllowedHosts: []string{"gateway.example"}})
	if err != nil {
		t.Fatal(err)
	}
	if f.diskQuota != 1<<30 {
		t.Fatalf("configured native disk quota was not passed to the computer: %d", f.diskQuota)
	}
	raw, revoke, err := c.s.cloudCredential(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	defer revoke()
	token, err := c.s.store.AccessTokenByHash(ctx, secret.Hash(raw))
	deadline, _ := ctx.Deadline()
	if err != nil || token.ExpiresAt == nil || token.ExpiresAt.After(deadline.Add(time.Millisecond)) {
		t.Fatal("credential should expire no later than the run's deadline")
	}

	box.close(false) // Revocation failed: the worker must leave the lease in place.
	if _, err := c.s.cloudComputerAction(ctx, c.bot.ID, computerHibernate, "test", nil); !errors.Is(err, store.ErrRuntimeComputerBusy) || f.hibernates != 0 {
		t.Fatalf("hibernation must wait for the credential to expire: %v", err)
	}
}

func TestBotComputerWorkerRetriesLostCreateResponse(t *testing.T) {
	f := &fakeComputers{loseCreateResponse: true}
	c := newComputerTest(t, f, runtime.ComputerPolicy{Persistent: true, IdleTimeout: time.Millisecond})
	ctx := context.Background()
	b := *c.bot
	b.Prompt = "WRITE:original computer"
	task, err := c.s.enqueueCloudBot(ctx, &b, "")
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		claim, err := c.s.store.ClaimRuntimeTask(ctx, runtimeAttempts, 45*time.Second)
		if err != nil || claim.ID != task.ID || claim.Attempts != attempt {
			t.Fatalf("attempt %d: %+v, %v", attempt, claim, err)
		}
		c.s.runCloudClaim(ctx, claim)
	}
	got, err := c.s.store.RuntimeTask(ctx, task.ID, c.employee)
	if err != nil || got.Status != store.RuntimeSucceeded || got.Attempts != 2 || f.creates != 1 {
		t.Fatalf("lost response didn't recover in the same task: %+v, %v, creates=%d", got, err, f.creates)
	}
}

func TestBotComputerDefersAmbiguousCreateRetryUntilLeaseExpires(t *testing.T) {
	f := &fakeComputers{loseCreateResponse: true, hideCreatedBox: true}
	c := newComputerTest(t, f, runtime.ComputerPolicy{Persistent: true, IdleTimeout: time.Millisecond})
	ctx := context.Background()
	b := *c.bot
	b.Prompt = "READ the note"
	original, err := c.s.enqueueCloudBot(ctx, &b, "")
	if err != nil {
		t.Fatal(err)
	}
	task, err := c.s.store.ClaimRuntimeTask(ctx, runtimeAttempts, 45*time.Second)
	if err != nil || task.ID != original.ID {
		t.Fatalf("initial attempt: %+v, %v", task, err)
	}
	c.s.runCloudClaim(ctx, task)
	pending, err := c.s.store.RuntimeBotComputer(ctx, c.bot.ID)
	if err != nil || pending.SandboxID != "" || pending.CreationKey == "" || !pending.Busy || f.creates != 1 {
		t.Fatalf("pending create is not recoverable: %+v, %v", pending, err)
	}
	if _, err := c.s.store.ClaimRuntimeTask(ctx, runtimeAttempts, 45*time.Second); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ambiguous create should not block the worker on an immediate retry: %v", err)
	}
	f.hideCreatedBox = false
	if _, err := c.s.store.DB().ExecContext(ctx, `UPDATE runtime_bot_computers SET lease_until=?, last_used_at=? WHERE bot_id=?`,
		time.Now().Add(-time.Second), time.Now().Add(-time.Second), c.bot.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.s.store.DB().ExecContext(ctx, `UPDATE runtime_tasks SET lease_until=? WHERE id=?`,
		time.Now().Add(-time.Second), task.ID); err != nil {
		t.Fatal(err)
	}
	for _, box := range f.boxes {
		box.files[runtimeWorkDir+"/note.txt"] = "original computer"
	}
	c.s.reapCloudComputers(ctx)
	if v := c.profile(); v.State != store.RuntimeComputerHibernated || f.hibernates != 1 {
		t.Fatalf("reaper did not recover and hibernate the computer: %+v", v)
	}
	c.s.opts.Runtime.BaseURL = "https://gateway-next.example"
	retry, err := c.s.store.ClaimRuntimeTask(ctx, runtimeAttempts, 45*time.Second)
	if err != nil || retry.ID != original.ID || retry.Attempts != 2 {
		t.Fatalf("retry after lease expired: %+v, %v", retry, err)
	}
	c.s.runCloudClaim(ctx, retry)
	if got, err := c.s.store.RuntimeTask(ctx, retry.ID, c.employee); err != nil || got.Summary != "note: original computer" ||
		f.creates != 1 || f.allowedHost != "gateway-next.example" {
		t.Fatalf("retry after gateway move: %+v, %v, creates=%d, gateway=%q", got, err, f.creates, f.allowedHost)
	}
}

func TestBotComputerDiskLimitAndRetention(t *testing.T) {
	f := &fakeComputers{disk: 4096} // 4 MB
	c := newComputerTest(t, f, runtime.ComputerPolicy{Persistent: true, IdleTimeout: time.Millisecond, DiskLimitBytes: 1 << 20, Retention: time.Millisecond})
	if got := c.run("WRITE:big"); got.Status != store.RuntimeSucceeded {
		t.Fatalf("first run: %+v", got)
	}
	got := c.run("READ")
	if got.Status != store.RuntimeFailed || !strings.Contains(got.Error, "disk limit") || got.Attempts != 1 {
		t.Fatalf("over-limit run should fail terminally: %+v", got)
	}
	// 休眠超过保留期的电脑会被回收。
	time.Sleep(5 * time.Millisecond)
	c.s.reapCloudComputers(context.Background())
	if f.hibernates != 1 {
		t.Fatalf("hibernates = %d", f.hibernates)
	}
	time.Sleep(5 * time.Millisecond)
	c.s.reapCloudComputers(context.Background())
	if len(f.boxes) != 0 {
		t.Fatal("computer past retention was not reclaimed")
	}
	if v := c.profile(); v.State != store.RuntimeComputerNone || v.DiskBytes != 0 {
		t.Fatalf("after reclaim = %+v", v)
	}
	f.disk = 4
	if got := c.run("READ"); got.Status != store.RuntimeSucceeded || f.creates != 2 {
		t.Fatalf("run after reclaim: %+v", got)
	}
}

func TestBotComputerReplacesMissingOverLimitSandbox(t *testing.T) {
	f := &fakeComputers{disk: 4096}
	c := newComputerTest(t, f, runtime.ComputerPolicy{Persistent: true, DiskLimitBytes: 1 << 20})
	if got := c.run("WRITE:big"); got.Status != store.RuntimeSucceeded {
		t.Fatalf("first run: %+v", got)
	}
	ctx := context.Background()
	if _, err := c.s.cloudComputerAction(ctx, c.bot.ID, computerHibernate, "test", nil); err != nil {
		t.Fatal(err)
	}
	if got := c.run("READ"); got.Status != store.RuntimeFailed || !strings.Contains(got.Error, "disk limit") || f.wakes != 0 {
		t.Fatalf("existing over-limit sandbox should be blocked without waking: %+v", got)
	}
	for id := range f.boxes {
		_ = f.Remove(ctx, id)
	}
	f.disk = 4
	if got := c.run("READ"); got.Status != store.RuntimeSucceeded || got.Summary != "no note" || f.creates != 2 {
		t.Fatalf("next run did not replace the missing sandbox: %+v", got)
	}
}

func TestBotComputerAdminInventoryPages(t *testing.T) {
	c := newComputerTest(t, &fakeComputers{}, runtime.ComputerPolicy{Persistent: true})
	ctx := context.Background()
	for id := int64(1); id <= 101; id++ {
		if _, err := c.s.store.DB().ExecContext(ctx, `INSERT INTO runtime_bot_computers (bot_id, sandbox_id, state, last_used_at, created_at, updated_at)
			VALUES (?, ?, ?, NOW(3), NOW(3), NOW(3))`, id, strconv.FormatInt(id, 10), store.RuntimeComputerHibernated); err != nil {
			t.Fatal(err)
		}
	}
	type page struct {
		Computers []runtimeAdminComputer `json:"computers"`
		Next      string                 `json:"next_before"`
	}
	const endpoint = "/api/admin/runtime/computers"
	first := decodeRuntimeChat[page](t, "inventory first page", c.admin("GET", endpoint, ""), http.StatusOK)
	if len(first.Computers) != 100 || first.Computers[0].BotID != 101 || first.Next != "2" {
		t.Fatalf("first page: %d computers, next=%q", len(first.Computers), first.Next)
	}
	second := decodeRuntimeChat[page](t, "inventory second page", c.admin("GET", endpoint+"?before="+first.Next, ""), http.StatusOK)
	if len(second.Computers) != 1 || second.Computers[0].BotID != 1 || second.Next != "" {
		t.Fatalf("second page: %+v", second)
	}
}

// 不支持休眠 / 唤醒的 provider（或关闭了常驻）退回每轮新建、跑完销毁，面板如实显示。
func TestBotComputerUnsupportedProviderFallsBack(t *testing.T) {
	m := &memoryRuntime{run: func(runtime.Command) error { return nil }}
	c := newComputerTest(t, m, runtime.ComputerPolicy{Persistent: true, IdleTimeout: time.Minute})
	if got := c.run("anything"); got.Status != store.RuntimeSucceeded || !m.destroyed {
		t.Fatalf("fallback run: %+v destroyed=%v", got, m.destroyed)
	}
	if v := c.profile(); v.Persistent || v.State != store.RuntimeComputerNone {
		t.Fatalf("profile for unsupported provider = %+v", v)
	}
	if _, err := c.s.store.RuntimeBotComputer(context.Background(), c.bot.ID); !notFound(err) {
		t.Fatalf("fallback must not record a computer: %v", err)
	}
	if rec := c.owner("POST", "/api/runtime/chat/bots/"+strconv.FormatInt(c.bot.ID, 10)+"/computer/hibernate", ""); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), codeRuntimeComputerUnsupported) {
		t.Fatalf("hibernate unsupported: HTTP %d %s", rec.Code, rec.Body.String())
	}

	f := &fakeComputers{}
	off := newComputerTest(t, f, runtime.ComputerPolicy{Persistent: false})
	off.run("WRITE:x")
	if f.creates != 0 || f.ephemeral != 1 || len(f.boxes) != 0 {
		t.Fatalf("persistent=off must create per run: creates=%d ephemeral=%d boxes=%d", f.creates, f.ephemeral, len(f.boxes))
	}
}

// TestLiveBotComputer 用真实的 OpenSandbox 跑完整的工作流：第一轮在电脑里写文件，回收器让它休眠，
// 第二轮唤醒后读出同一个文件。需要 DSH_EE_TEST_OPENSANDBOX_URL / _KEY 和数据库。
func TestLiveBotComputer(t *testing.T) {
	base := os.Getenv("DSH_EE_TEST_OPENSANDBOX_URL")
	if base == "" {
		t.Skip("DSH_EE_TEST_OPENSANDBOX_URL is not set")
	}
	provider, err := runtime.NewOpenSandbox(base, os.Getenv("DSH_EE_TEST_OPENSANDBOX_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	c := newComputerTest(t, provider, runtime.ComputerPolicy{Persistent: true, IdleTimeout: time.Millisecond, DiskLimitBytes: 64 << 20})
	c.s.opts.Runtime.Image = "alpine:3.22"
	c.s.opts.Runtime.Command = []string{"sh", "-ec", `test "$DSH_EE_OUTPUT_DIR" = "$PWD/output"
case "$1" in
  *WRITE*) echo 'quarter goal 42' > note.txt
           mkdir -p output/reports
           echo 'quarterly report' > output/reports/summary.txt
           echo saved ;;
  *) test ! -e output/reports/summary.txt
     cat note.txt ;;
esac`, "sh", "{prompt}"}
	t.Cleanup(func() {
		_, _ = c.s.cloudComputerAction(context.Background(), c.bot.ID, computerReset, "test", nil)
	})
	first := c.run("WRITE a note")
	if first.Status != store.RuntimeSucceeded || first.Summary != "saved" {
		t.Fatalf("first run: %+v", first)
	}
	files, err := c.s.store.RuntimeArtifacts(context.Background(), first.ID, c.employee)
	if err != nil || len(files) != 1 || files[0].Name != "reports/summary.txt" || files[0].Size != int64(len("quarterly report\n")) {
		t.Fatalf("first run deliverables: %+v, %v", files, err)
	}
	time.Sleep(5 * time.Millisecond)
	c.s.reapCloudComputers(context.Background())
	if v := c.profile(); v.State != store.RuntimeComputerHibernated {
		t.Fatalf("after idle: %+v", v)
	}
	c.s.opts.Runtime.BaseURL = "https://gateway-next.example"
	if got := c.run("READ the note"); got.Status != store.RuntimeSucceeded || got.Summary != "quarter goal 42" {
		t.Fatalf("second run: %+v", got)
	}
	if v := c.profile(); v.State != store.RuntimeComputerRunning || v.DiskBytes <= 0 {
		t.Fatalf("after second run: %+v", v)
	}
}
