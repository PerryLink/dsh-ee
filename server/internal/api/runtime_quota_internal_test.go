package api

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/runtime"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// countingRuntime 记录 Create 次数，用来断言额度预检失败时没有分配沙箱。
type countingRuntime struct {
	memoryRuntime
	creates int
}

func (c *countingRuntime) Create(ctx context.Context, spec runtime.SandboxSpec) (runtime.Sandbox, error) {
	c.creates++
	return c.memoryRuntime.Create(ctx, spec)
}

// runQuotaTask 给新员工设置月度额度（limit 为 nil 表示不限）和本月已用量，然后跑一次云任务。
func runQuotaTask(t *testing.T, limit *int64, used int) (*store.RuntimeTask, *countingRuntime) {
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
	u := &store.User{Username: "quota-owner", DisplayName: "Quota Owner"}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserRoles(ctx, u.ID, []int64{role.ID}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserQuota(ctx, u.ID, store.UserQuotaPatch{MonthlyTokenLimit: limit}); err != nil {
		t.Fatal(err)
	}
	if used > 0 {
		if err := st.RecordUsage(ctx, store.Usage{UserID: u.ID, Model: "company-chat", TotalTokens: used, StatusCode: 200}); err != nil {
			t.Fatal(err)
		}
	}
	rt := &countingRuntime{}
	rt.run = func(runtime.Command) error { return nil }
	s := New(st, nil, Options{DataDir: t.TempDir(), Runtime: runtime.Settings{
		Provider: rt, Image: "unused-image", BaseURL: "https://gateway.example", Model: "company-chat",
		Command: []string{"dsh", "--patch", "{patch}", "{prompt}"},
	}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	bot := &store.RuntimeBot{ID: 1, OwnerID: u.ID, RunAsID: u.ID, Prompt: "Daily summary", ReadOnly: true}
	if _, err := s.enqueueCloudBot(ctx, bot, "2026-11-22T09:00"); err != nil {
		t.Fatal(err)
	}
	task, err := st.ClaimRuntimeTask(ctx, runtimeAttempts, 45*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	s.runCloudClaim(ctx, task)
	got, err := st.RuntimeTask(ctx, task.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got, rt
}

func TestCloudTaskQuotaPreflightFailsTerminallyWithoutSandbox(t *testing.T) {
	zero, one, ten := int64(0), int64(1), int64(10)
	for _, tc := range []struct {
		name  string
		limit *int64
		used  int
		want  string
	}{
		{"unallocated", &zero, 0, "unallocated"},
		{"exhausted exactly", &ten, 10, "exhausted"},
		{"exhausted over", &one, 5, "exhausted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, rt := runQuotaTask(t, tc.limit, tc.used)
			if got.Status != store.RuntimeFailed || !strings.Contains(got.Error, tc.want) {
				t.Fatalf("want terminal failure mentioning %q, got status %q error %q", tc.want, got.Status, got.Error)
			}
			if got.Attempts != 1 {
				t.Fatalf("quota failure must not be retried, attempts=%d", got.Attempts)
			}
			if rt.creates != 0 {
				t.Fatalf("sandbox allocated %d times despite missing quota", rt.creates)
			}
		})
	}
}

func TestCloudTaskQuotaPreflightAllowsUnlimitedAndRemaining(t *testing.T) {
	ten := int64(10)
	for name, tc := range map[string]struct {
		limit *int64
		used  int
	}{"unlimited": {nil, 1000}, "remaining": {&ten, 9}} {
		t.Run(name, func(t *testing.T) {
			got, rt := runQuotaTask(t, tc.limit, tc.used)
			if got.Status != store.RuntimeSucceeded || rt.creates != 1 {
				t.Fatalf("status %q error %q creates %d", got.Status, got.Error, rt.creates)
			}
		})
	}
}
