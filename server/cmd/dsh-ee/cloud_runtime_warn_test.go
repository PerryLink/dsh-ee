package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeCloudRuntimeFootprint struct {
	computers, orphans int
	err                error
}

func (f fakeCloudRuntimeFootprint) CountCloudRuntimeFootprint(context.Context) (int, int, error) {
	return f.computers, f.orphans, f.err
}

// 启动告警：只在「provider 未配置 + 库里还有云电脑/待回收沙箱」时打一条 Warn；
// 配置了 provider、或没有遗留记录时不打；查询失败只 Warn、不阻断启动。
func TestWarnCloudRuntimeWithoutProvider(t *testing.T) {
	t.Run("有遗留记录", func(t *testing.T) {
		log, buf := bufferLogger()
		warnCloudRuntimeWithoutProvider(context.Background(), fakeCloudRuntimeFootprint{computers: 2, orphans: 1}, log, false)
		out := buf.String()
		if !strings.Contains(out, "level=WARN") {
			t.Fatalf("应打 Warn：%s", out)
		}
		if !strings.Contains(out, "computers=2") || !strings.Contains(out, "orphans=1") {
			t.Fatalf("告警应带数量：%s", out)
		}
		if n := strings.Count(out, "云 runtime 未配置"); n != 1 {
			t.Fatalf("每次启动只应告警一条，实际 %d 条：%s", n, out)
		}
	})
	t.Run("provider 已配置", func(t *testing.T) {
		log, buf := bufferLogger()
		warnCloudRuntimeWithoutProvider(context.Background(), fakeCloudRuntimeFootprint{computers: 2, orphans: 1}, log, true)
		if out := buf.String(); out != "" {
			t.Fatalf("配置了 provider 时不应告警：%s", out)
		}
	})
	t.Run("没有遗留记录", func(t *testing.T) {
		log, buf := bufferLogger()
		warnCloudRuntimeWithoutProvider(context.Background(), fakeCloudRuntimeFootprint{}, log, false)
		if out := buf.String(); out != "" {
			t.Fatalf("没有遗留记录时不应告警：%s", out)
		}
	})
	t.Run("查询失败不阻断启动", func(t *testing.T) {
		log, buf := bufferLogger()
		warnCloudRuntimeWithoutProvider(context.Background(), fakeCloudRuntimeFootprint{err: errors.New("db down")}, log, false)
		out := buf.String()
		if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "db down") {
			t.Fatalf("查询失败应打一条 Warn 并带原因：%s", out)
		}
	})
}
