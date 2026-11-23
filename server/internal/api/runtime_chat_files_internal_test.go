package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"path"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/runtime"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// workspaceRuntime 模拟一台真实的云端电脑：DSH 按自己的工作目录解析相对路径写文件，
// 列目录时返回各级子目录和文件的绝对路径（OpenSandbox /directories/list 的形状）。
// writes 是这一轮机器人要写的文件（相对或绝对路径 → 内容）。
type workspaceRuntime struct {
	files  map[string]string
	writes map[string]string
	dirs   []string // 每次执行 DSH 时的工作目录
	prompt string   // 最近一次交给 DSH 的提示词
}

func (w *workspaceRuntime) Create(context.Context, runtime.SandboxSpec) (runtime.Sandbox, error) {
	w.files = map[string]string{}
	return w, nil
}

func (w *workspaceRuntime) Exec(_ context.Context, c runtime.Command) (runtime.Result, error) {
	if c.Args[0] == "mkdir" {
		return runtime.Result{}, nil
	}
	w.dirs = append(w.dirs, c.Dir)
	w.prompt = c.Args[len(c.Args)-1]
	for name, content := range w.writes {
		if !path.IsAbs(name) {
			name = path.Join(c.Dir, name)
		}
		w.files[name] = content
	}
	if c.OnStdout != nil {
		c.OnStdout(`{"type":"final","sessionId":"test","reason":{"kind":"completed"},"text":"已经写好了明日待办.md"}` + "\n")
	}
	return runtime.Result{}, nil
}

func (w *workspaceRuntime) WriteFile(_ context.Context, name string, value []byte) error {
	w.files[name] = string(value)
	return nil
}

func (w *workspaceRuntime) ListFiles(_ context.Context, dir string) ([]runtime.File, error) {
	seen := map[string]runtime.File{}
	for name, content := range w.files {
		rel, ok := strings.CutPrefix(name, dir+"/")
		if !ok {
			continue
		}
		parts := strings.Split(rel, "/")
		for i := 1; i < len(parts) && i <= runtime.ListDepth; i++ {
			d := dir + "/" + strings.Join(parts[:i], "/")
			seen[d] = runtime.File{Path: d, Type: "directory"}
		}
		if len(parts) <= runtime.ListDepth {
			seen[name] = runtime.File{Path: name, Type: "file", Size: int64(len(content))}
		}
	}
	out := make([]runtime.File, 0, len(seen))
	for _, f := range seen {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (w *workspaceRuntime) ReadFile(_ context.Context, name string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(w.files[name])), nil
}

func (w *workspaceRuntime) Destroy(context.Context) error { return nil }

// 对话里机器人生成的文件要进机器人资料的「文件库」，可以下载；只有线程所有者本人看得到，
// 别的员工和管理员都拿不到。定时任务的产物也在同一个文件库里。
func TestRuntimeChatFilesReachLibrary(t *testing.T) {
	c, employee, admin := newRuntimeChatTest(t)
	ctx := context.Background()
	ws := &workspaceRuntime{writes: map[string]string{
		"明日待办.md":                          "# 明日待办\n- 九点站会\n",
		"reports/周报.md":                    "本周进展",
		runtimeOutputDir + "/notes.txt":    "absolute path",
		".dsh/session.json":                "tool state",
		"/tmp/elsewhere.txt":               "outside the output directory",
		"a/b/c/too-deep.txt":               "deeper than the listing",
		runtimeOutputDir + "/.hidden-note": "hidden",
	}}
	c.s.opts.Runtime.Provider = ws
	ownerID, owner := employee("files-owner")
	otherID, other := employee("files-other")
	on := true
	const chat = "/api/runtime/chat"
	bot := decodeRuntimeChat[store.RuntimeBot](t, "create bot", admin("POST", runtimeBotsTestPath, runtimeChatBotBody(t, ownerID, "Planner", false, &on)), http.StatusOK)
	foreign := decodeRuntimeChat[store.RuntimeBot](t, "create foreign bot", admin("POST", runtimeBotsTestPath, runtimeChatBotBody(t, otherID, "Planner", false, &on)), http.StatusOK)
	profilePath := func(id int64) string { return chat + "/bots/" + strconv.FormatInt(id, 10) + "/profile" }

	thread := decodeRuntimeChat[store.RuntimeChatConversation](t, "open thread", owner("POST", chat+"/conversations", fmt.Sprintf(`{"bot_id":%d}`, bot.ID)), http.StatusOK)
	body, _ := json.Marshal(map[string]string{"content": "帮我写一份明日待办", "idempotency_key": "todo"})
	turn := decodeRuntimeChat[store.RuntimeChatTurn](t, "send", owner("POST", chat+"/conversations/"+strconv.FormatInt(thread.ID, 10)+"/messages", string(body)), http.StatusAccepted)
	c.runNext()
	prompt := ws.prompt
	if len(ws.dirs) != 1 || ws.dirs[0] != runtimeOutputDir {
		t.Fatalf("DSH must run with the output directory as its working directory (its sandbox workspace root): %v", ws.dirs)
	}
	if !strings.Contains(prompt, "current working directory is "+runtimeOutputDir) || !strings.Contains(prompt, "file library") {
		t.Fatalf("chat prompt does not explain where to save files: %q", prompt)
	}

	profile := decodeRuntimeChat[store.RuntimeChatBotProfile](t, "owner profile", owner("GET", profilePath(bot.ID), ""), http.StatusOK)
	got := map[string]store.RuntimeChatArtifact{}
	for _, a := range profile.Artifacts {
		if a.Source != "chat" || a.TaskID != turn.ID {
			t.Fatalf("artifact not attributed to the chat turn: %+v", a)
		}
		got[a.Name] = a
	}
	want := map[string]string{"明日待办.md": "# 明日待办\n- 九点站会\n", "notes.txt": "absolute path", "reports/周报.md": "本周进展"}
	if len(got) != len(want) {
		t.Fatalf("library has %v, want exactly %v", profile.Artifacts, want)
	}
	for name, content := range want {
		a, ok := got[name]
		if !ok || a.Size != int64(len(content)) {
			t.Fatalf("library entry %q: %+v (all: %+v)", name, a, profile.Artifacts)
		}
		download := fmt.Sprintf("/api/runtime/tasks/%d/artifacts/%d", a.TaskID, a.ID)
		rec := owner("GET", download, "")
		if rec.Code != http.StatusOK || rec.Body.String() != content {
			t.Fatalf("owner download %q: HTTP %d %q", name, rec.Code, rec.Body.String())
		}
		// 子目录里的文件下载时只用最后一段做文件名。
		if kind, params, err := mime.ParseMediaType(rec.Header().Get("Content-Disposition")); err != nil || kind != "attachment" || params["filename"] != path.Base(name) {
			t.Fatalf("download %q Content-Disposition: %q", name, rec.Header().Get("Content-Disposition"))
		}
		for who, client := range map[string]func(string, string, string) *httptest.ResponseRecorder{"other employee": other, "admin": admin} {
			if rec := client("GET", download, ""); rec.Code != http.StatusNotFound {
				t.Fatalf("%s downloaded %q: HTTP %d", who, name, rec.Code)
			}
		}
	}
	for who, client := range map[string]func(string, string, string) *httptest.ResponseRecorder{"other employee": other, "admin": admin} {
		expectRuntimeChatCode(t, who+" reads the library", client("GET", profilePath(bot.ID), ""), http.StatusNotFound, codeRuntimeChatBotUnavailable)
	}
	if p := decodeRuntimeChat[store.RuntimeChatBotProfile](t, "other owner's own bot", other("GET", profilePath(foreign.ID), ""), http.StatusOK); len(p.Artifacts) != 0 {
		t.Fatalf("another employee's library shows the owner's files: %+v", p.Artifacts)
	}

	// 定时任务的产物进同一个文件库，标成 task。
	ws.writes = map[string]string{"日报.md": "定时生成"}
	scheduled, err := c.s.enqueueCloudBot(ctx, &bot, "2026-11-23T09:00")
	if err != nil {
		t.Fatal(err)
	}
	c.runNext()
	profile = decodeRuntimeChat[store.RuntimeChatBotProfile](t, "profile after a scheduled run", owner("GET", profilePath(bot.ID), ""), http.StatusOK)
	if len(profile.Artifacts) != 4 || profile.Artifacts[0].TaskID != scheduled.ID || profile.Artifacts[0].Source != "task" || profile.Artifacts[0].Name != "日报.md" {
		t.Fatalf("scheduled artifact missing from the library: %+v", profile.Artifacts)
	}
	if rec := admin("GET", fmt.Sprintf("/api/runtime/tasks/%d/artifacts/%d", scheduled.ID, profile.Artifacts[0].ID), ""); rec.Code != http.StatusOK {
		// 定时任务本来就在管理员的任务视图里；这里只确认文件库没有改变它的可见范围。
		t.Fatalf("admin task artifact: HTTP %d", rec.Code)
	}
	if rec := other("GET", fmt.Sprintf("/api/runtime/tasks/%d/artifacts/%d", scheduled.ID, profile.Artifacts[0].ID), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("other employee downloaded a scheduled artifact: HTTP %d", rec.Code)
	}
}

func TestRuntimeArtifactName(t *testing.T) {
	for remote, want := range map[string]string{
		runtimeOutputDir + "/明日待办.md":       "明日待办.md",
		runtimeOutputDir + "/reports/周报.md": "reports/周报.md",
		runtimeOutputDir + "/a/b/c.txt":     "a/b/c.txt",
		runtimeOutputDir + "/a/b/c/d.txt":   "",
		runtimeOutputDir + "/../etc/passwd": "",
		runtimeOutputDir + "//x.txt":        "",
		runtimeOutputDir:                    "",
		runtimeOutputDir + "-other/x.txt":   "",
		"/tmp/x.txt":                        "",
		runtimeOutputDir + "/bad\nname.txt": "",
		runtimeOutputDir + "/bad\\name.txt": "",
		runtimeOutputDir + "/" + "\xff.txt": "",
	} {
		got, ok := runtimeArtifactName(remote, runtimeOutputDir)
		if got != want || ok != (want != "") {
			t.Errorf("runtimeArtifactName(%q) = %q, %v; want %q", remote, got, ok, want)
		}
	}
}
