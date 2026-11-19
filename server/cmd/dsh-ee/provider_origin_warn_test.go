package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// fakeProviderOriginCounter 让启动告警逻辑不用起数据库/服务就能测。
type fakeProviderOriginCounter struct {
	n     int
	names []string
	err   error
}

func (f fakeProviderOriginCounter) CountProvidersMissingKeyOrigin(context.Context) (int, []string, error) {
	return f.n, f.names, f.err
}

func bufferLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

// 启动告警：只有存在「有 key 但 api_key_origin 为空」的历史上游时打一条 Warn，
// 带数量与名称（不含 key）；没有记录时不打；查询失败只 Warn、不阻断启动。
func TestProviderWarnMissingKeyOrigin(t *testing.T) {
	t.Run("有历史记录", func(t *testing.T) {
		log, buf := bufferLogger()
		warnProvidersMissingKeyOrigin(context.Background(), fakeProviderOriginCounter{
			n: 2, names: []string{"旧上游 A", "旧上游 B"},
		}, log)
		out := buf.String()
		if !strings.Contains(out, "level=WARN") {
			t.Fatalf("应打 Warn：%s", out)
		}
		if !strings.Contains(out, "count=2") {
			t.Fatalf("告警应带数量：%s", out)
		}
		for _, name := range []string{"旧上游 A", "旧上游 B"} {
			if !strings.Contains(out, name) {
				t.Fatalf("告警应带上游名称 %q：%s", name, out)
			}
		}
		if n := strings.Count(out, "缺少 API Key 绑定 origin"); n != 1 {
			t.Fatalf("每次启动只应告警一条，实际 %d 条：%s", n, out)
		}
		if strings.Contains(out, "api_key_enc") || strings.Contains(out, "Bearer") {
			t.Fatalf("告警不得带 key/密文：%s", out)
		}
	})

	t.Run("没有历史记录", func(t *testing.T) {
		log, buf := bufferLogger()
		warnProvidersMissingKeyOrigin(context.Background(), fakeProviderOriginCounter{}, log)
		if out := buf.String(); out != "" {
			t.Fatalf("没有历史记录时不应打日志：%s", out)
		}
	})

	t.Run("查询失败不阻断启动", func(t *testing.T) {
		log, buf := bufferLogger()
		warnProvidersMissingKeyOrigin(context.Background(), fakeProviderOriginCounter{err: errors.New("db down")}, log)
		out := buf.String()
		if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "db down") {
			t.Fatalf("查询失败应打一条 Warn 并带原因：%s", out)
		}
	})
}
