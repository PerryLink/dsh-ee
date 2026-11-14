package api

import (
	"testing"
	"time"
)

// embedStatsDayStart 把「本地时区的今天零点」换算成 UTC：跨时区、跨 UTC 日界都要正确。
func TestEmbedStatsDayStart(t *testing.T) {
	now := time.Date(2026, 11, 13, 20, 10, 0, 0, time.UTC)
	cases := []struct {
		tz   int
		want time.Time
	}{
		{0, time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)},
		// 东八区此刻是 11-14 04:10，今天零点 = 11-13 16:00 UTC。
		{480, time.Date(2026, 11, 13, 16, 0, 0, 0, time.UTC)},
		// 西八区此刻是 11-13 12:10，今天零点 = 11-13 08:00 UTC。
		{-480, time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC)},
		// 东十四区已经是 11-14 10:10。
		{840, time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)},
		// 西十四区还是 11-13 06:10。
		{-840, time.Date(2026, 11, 13, 14, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		if got := embedStatsDayStart(now, c.tz); !got.Equal(c.want) {
			t.Errorf("tz=%d：得到 %s，期望 %s", c.tz, got, c.want)
		}
	}

	// 刚过 UTC 零点：UTC 口径的今天才刚开始，东八区已经是第二天上午。
	early := time.Date(2026, 11, 14, 0, 5, 0, 0, time.UTC)
	if got, want := embedStatsDayStart(early, 0), time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("UTC 日界：%s != %s", got, want)
	}
	if got, want := embedStatsDayStart(early, 480), time.Date(2026, 11, 13, 16, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("东八区跨 UTC 日界：%s != %s", got, want)
	}
}
