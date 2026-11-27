package store

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

// 审计 target 列是 VARCHAR(255)。Conan 的对象路径合法长度可达约 459 字符
// （conans/<name>/<version>/<user>/<channel>/revisions/<40hex>/packages/<40hex>/…），
// 直接截断会让「前 255 字符相同、只有尾部不同」的两条路径在审计里完全一样，
// 事后无法区分是谁。这里要求截断后仍能区分不同 target，且短 target 原样保存。
func TestInsertAuditKeepsLongTargetsDistinct(t *testing.T) {
	st := openTokenTestStore(t)
	ctx := context.Background()

	// audit_logs.target 是 VARCHAR(255)，这里按列定义断言上限，不改产品常量。
	const targetMaxRunes = 255

	// 前 250 字符相同、总长 459 的两条 target，只有最后一位不同。
	common := strings.Repeat("a", 250)
	first := common + strings.Repeat("b", 209)
	second := common + strings.Repeat("b", 208) + "c"
	if len(first) != 459 || len(second) != 459 {
		t.Fatalf("用例 target 长度不对：%d / %d", len(first), len(second))
	}
	short := "conan/cpp/conans/lib/1.0/_/_/revisions/abc/files/conanfile.py"

	insert := func(action, target string) int64 {
		t.Helper()
		a := &AuditLog{Action: action, Target: target, Via: "test"}
		if err := st.InsertAudit(ctx, a); err != nil {
			t.Fatalf("InsertAudit(%s): %v", action, err)
		}
		return a.ID
	}
	readTarget := func(id int64) string {
		t.Helper()
		var got string
		if err := st.DB().QueryRowContext(ctx, "SELECT target FROM audit_logs WHERE id = ?", id).Scan(&got); err != nil {
			t.Fatalf("读回审计 target：%v", err)
		}
		return got
	}

	idFirst := insert("test.long_target_first", first)
	idSecond := insert("test.long_target_second", second)
	gotFirst, gotSecond := readTarget(idFirst), readTarget(idSecond)
	for name, got := range map[string]string{"first": gotFirst, "second": gotSecond} {
		if n := utf8.RuneCountInString(got); n > targetMaxRunes {
			t.Fatalf("%s 存库长度 %d 超过 target 列上限 %d", name, n, targetMaxRunes)
		}
		if !utf8.ValidString(got) {
			t.Fatalf("%s 存库值不是合法 UTF-8：%q", name, got)
		}
	}
	if gotFirst == gotSecond {
		t.Fatalf("两条只有尾部不同的 459 字符 target 存库后无法区分：%q", gotFirst)
	}
	if !strings.HasPrefix(gotFirst, strings.Repeat("a", 200)) {
		t.Fatalf("截断后应保留前缀：%q", gotFirst)
	}

	// 短 target 必须原样保存（兼容既有数据与查询）。
	if got := readTarget(insert("test.short_target", short)); got != short {
		t.Fatalf("短 target 应原样保存：got %q want %q", got, short)
	}
}
