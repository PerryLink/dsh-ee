package runtime

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/secret"
)

// TestLiveOpenSandboxHibernateAndWake 在真实的 OpenSandbox 上验证常驻电脑：写文件、休眠（pause）、
// 唤醒（resume）后文件还在。需要 DSH_EE_TEST_OPENSANDBOX_URL / _KEY，可用 DSH_EE_TEST_OPENSANDBOX_IMAGE 换镜像。
func TestLiveOpenSandboxHibernateAndWake(t *testing.T) {
	base := os.Getenv("DSH_EE_TEST_OPENSANDBOX_URL")
	if base == "" {
		t.Skip("DSH_EE_TEST_OPENSANDBOX_URL is not set")
	}
	image := os.Getenv("DSH_EE_TEST_OPENSANDBOX_IMAGE")
	if image == "" {
		image = "alpine:3.22"
	}
	p, err := NewOpenSandbox(base, os.Getenv("DSH_EE_TEST_OPENSANDBOX_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	creationKey := secret.Token()
	box, err := p.CreatePersistent(ctx, SandboxSpec{Image: image, CreationKey: creationKey})
	if err != nil {
		t.Fatal(err)
	}
	id := box.ID()
	t.Cleanup(func() { _ = p.Remove(context.Background(), id) })
	if found, err := p.FindPersistent(ctx, creationKey); err != nil || found != id {
		t.Fatalf("find sandbox by creation key: %q, %v", found, err)
	}
	run := func(s Sandbox, args ...string) Result {
		t.Helper()
		r, err := s.Exec(ctx, Command{Args: args, Timeout: 30 * time.Second})
		if err != nil || r.ExitCode != 0 {
			t.Fatalf("%v: %+v %v", args, r, err)
		}
		return r
	}
	run(box, "sh", "-c", "mkdir -p /tmp/dsh-ee-work && echo 'quarter goal 42' > /tmp/dsh-ee-work/note.txt")
	start := time.Now()
	if err := p.Hibernate(ctx, id); err != nil {
		t.Fatalf("hibernate: %v", err)
	}
	t.Logf("hibernate took %s", time.Since(start).Round(time.Millisecond))
	inst, _ := p.instance(id)
	if state, err := inst.state(ctx); err != nil || state != "Paused" {
		t.Fatalf("state after hibernate = %q, %v", state, err)
	}
	start = time.Now()
	woke, err := p.Wake(ctx, id)
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	t.Logf("wake took %s", time.Since(start).Round(time.Millisecond))
	if got := run(woke, "cat", "/tmp/dsh-ee-work/note.txt"); strings.TrimSpace(got.Stdout) != "quarter goal 42" {
		t.Fatalf("file after wake = %q", got.Stdout)
	}
	f, err := woke.ReadFile(ctx, "/tmp/dsh-ee-work/note.txt")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(f)
	f.Close()
	if strings.TrimSpace(string(b)) != "quarter goal 42" {
		t.Fatalf("download after wake = %q", b)
	}
	if du := run(woke, "du", "-sk", "/tmp/dsh-ee-work"); !strings.Contains(du.Stdout, "/tmp/dsh-ee-work") {
		t.Fatalf("du output = %q", du.Stdout)
	}
	if err := p.Remove(ctx, id); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	if _, err := p.Wake(ctx, id); err != ErrSandboxGone {
		t.Logf("wake after removal = %v (OpenSandbox may still report Stopping)", err)
	}
}
