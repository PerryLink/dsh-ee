package store

import (
	"context"
	"testing"
)

// 统计口径：api_key_origin 为空、且确实有 key（密文非空）的上游才算。
// 有绑定 origin 的、没有 key 的都不算；名称按 id 排序，且不返回 key/密文。
func TestProviderCountMissingKeyOrigin(t *testing.T) {
	st := openTokenTestStore(t) // 复用 tokens_test.go 的随机测试库 helper（同包）
	ctx := context.Background()
	create := func(name, origin string, key []byte) int64 {
		t.Helper()
		p := &Provider{
			Name: name, Vendor: "custom", BaseURL: "https://api.example.com/v1",
			APIKeyEnc: key, APIKeyHint: "…test", APIKeyOrigin: origin, Priority: 100, Enabled: true,
		}
		if err := st.CreateProvider(ctx, p); err != nil {
			t.Fatalf("建上游 %s: %v", name, err)
		}
		return p.ID
	}

	legacyA := create("旧上游 A", "", []byte("SECRET-CIPHERTEXT-A"))
	create("已绑定上游", "https://api.example.com", []byte("SECRET-CIPHERTEXT-B"))
	create("无 key 上游", "", []byte{})
	create("旧上游 B", "", []byte("SECRET-CIPHERTEXT-C"))

	n, names, err := st.CountProvidersMissingKeyOrigin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("应统计 2 条历史记录，实际 %d：%v", n, names)
	}
	if len(names) != 2 || names[0] != "旧上游 A" || names[1] != "旧上游 B" {
		t.Fatalf("名称应按 id 排序：%v", names)
	}

	// 重填 key 后补写 origin，记录即从统计里消失（管理员重填一次 key 即收敛）。
	origin := "https://api.example.com"
	if err := st.UpdateProvider(ctx, legacyA, ProviderPatch{APIKeyEnc: []byte("SECRET-CIPHERTEXT-A2"), APIKeyOrigin: &origin}); err != nil {
		t.Fatal(err)
	}
	n, names, err = st.CountProvidersMissingKeyOrigin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || len(names) != 1 || names[0] != "旧上游 B" {
		t.Fatalf("补写 origin 后应只剩 1 条，实际 %d：%v", n, names)
	}

	// 全部清理后返回 0 条，名称是空切片而不是 nil，方便调用方直接打印。
	if _, err := st.DB().ExecContext(ctx, "DELETE FROM providers"); err != nil {
		t.Fatal(err)
	}
	n, names, err = st.CountProvidersMissingKeyOrigin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || names == nil || len(names) != 0 {
		t.Fatalf("没有历史记录时应返回 0 与空切片，实际 %d：%v", n, names)
	}
}
