package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestDshReleaseStore(t *testing.T) {
	st := openTokenTestStore(t)
	ctx := context.Background()
	first := &DshRelease{Target: "mac-arm64", Kind: "zip", Version: "1.0.0", ReleasedAt: now(), File: "dsh-1.zip", Size: 100, SHA512: "aGFzaA==", UpstreamURL: "https://download.deepseek.com/dsh-1.zip", State: ReleaseAvailable}
	if err := st.CreateDshRelease(ctx, first); err != nil {
		t.Fatal(err)
	}
	duplicate := *first
	duplicate.ID = 0
	if err := st.CreateDshRelease(ctx, &duplicate); err == nil {
		t.Fatal("相同 target、kind、version 应触发唯一键冲突")
	}
	second := &DshRelease{Target: first.Target, Kind: first.Kind, Version: "1.0.1", ReleasedAt: now(), File: "dsh-2.zip", Size: 200, SHA512: "aGFzaA==", UpstreamURL: "https://download.deepseek.com/dsh-2.zip", State: ReleaseMirrored, LocalPath: "/tmp/dsh-2.zip"}
	if err := st.CreateDshRelease(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDshReleaseRecommended(ctx, first.ID); !errors.Is(err, ErrReleaseNotMirrored) {
		t.Fatalf("未下载版本不能推荐：%v", err)
	}
	if err := st.UpdateDshReleaseState(ctx, first.ID, ReleaseMirrored, "/tmp/dsh-1.zip", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDshReleaseRecommended(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDshReleaseRecommended(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	items, err := st.ListDshReleases(ctx, first.Target)
	if err != nil || len(items) != 2 {
		t.Fatalf("版本列表：%d %v", len(items), err)
	}
	if !items[0].Recommended || items[0].ID != second.ID {
		t.Fatalf("推荐版本应排在最前：%+v", items[0])
	}
	old, err := st.DshReleaseByID(ctx, first.ID)
	if err != nil || old.Recommended {
		t.Fatalf("设置新推荐时应清除旧推荐：%+v %v", old, err)
	}
	if err := st.ResetDshRelease(ctx, second.ID); !errors.Is(err, ErrReleaseRecommended) {
		t.Fatalf("推荐版本不能删除：%v", err)
	}
	if err := st.ResetDshRelease(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	old, err = st.DshReleaseByID(ctx, first.ID)
	if err != nil || old.State != ReleaseRemoved || old.LocalPath != "" || old.MirroredAt != nil {
		t.Fatalf("删除本地文件后应变为 removed：%+v %v", old, err)
	}
}

// 推荐是每个平台（target）一个，不分安装包种类：Mac 推荐 dmg 会清掉同平台 zip 的推荐，
// Windows 推荐 exe 新版本会清掉旧版本的推荐（旧版本随后可以删），别的平台不受影响。
func TestDshReleaseRecommendOnePerPlatform(t *testing.T) {
	st := openTokenTestStore(t)
	ctx := context.Background()
	mk := func(target, kind, version, file string) *DshRelease {
		r := &DshRelease{Target: target, Kind: kind, Version: version, ReleasedAt: now(), File: file, Size: 1, SHA512: "aGFzaA==",
			UpstreamURL: "https://download.deepseek.com/" + file, State: ReleaseMirrored, LocalPath: "/tmp/" + file}
		if err := st.CreateDshRelease(ctx, r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	recommend := func(r *DshRelease) {
		t.Helper()
		if err := st.SetDshReleaseRecommended(ctx, r.ID); err != nil {
			t.Fatal(err)
		}
	}
	recommendedOf := func(target string) []string {
		t.Helper()
		items, err := st.ListDshReleases(ctx, target)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, it := range items {
			if it.Recommended {
				out = append(out, it.Kind+"@"+it.Version)
			}
		}
		return out
	}

	macZip, macDmg := mk("mac-arm64", "zip", "1.1.0", "dsh.zip"), mk("mac-arm64", "dmg", "1.1.0", "dsh.dmg")
	winV1, winV2 := mk("win-x64", "exe", "1.0.0", "dsh-1.exe"), mk("win-x64", "exe", "1.1.0", "dsh-2.exe")
	intelZip := mk("mac-x64", "zip", "1.0.0", "dsh-x64.zip")

	recommend(intelZip)
	recommend(macZip)
	recommend(winV1)

	recommend(macDmg)
	if got := recommendedOf("mac-arm64"); len(got) != 1 || got[0] != "dmg@1.1.0" {
		t.Fatalf("推荐 Mac 的 dmg 应清掉同平台 zip 的推荐：%v", got)
	}
	if got, err := st.DshReleaseByID(ctx, macZip.ID); err != nil || got.Recommended {
		t.Fatalf("zip 不应再是推荐：%+v %v", got, err)
	}

	recommend(winV2)
	if got := recommendedOf("win-x64"); len(got) != 1 || got[0] != "exe@1.1.0" {
		t.Fatalf("Windows 推荐 v2 应清掉 v1 的推荐：%v", got)
	}
	if err := st.ResetDshRelease(ctx, winV1.ID); err != nil {
		t.Fatalf("不再推荐的旧版本应能删除：%v", err)
	}

	if got := recommendedOf("mac-x64"); len(got) != 1 || got[0] != "zip@1.0.0" {
		t.Fatalf("别的平台的推荐不受影响：%v", got)
	}
}

// 同一平台的两个推荐并发执行时不能死锁，结束后这个平台只剩一个推荐。
func TestDshReleaseRecommendConcurrent(t *testing.T) {
	st := openTokenTestStore(t)
	ctx := context.Background()
	var ids []int64
	for i, kind := range []string{"zip", "dmg", "zip", "dmg"} {
		file := fmt.Sprintf("dsh-%d.%s", i, kind)
		r := &DshRelease{Target: "mac-arm64", Kind: kind, Version: fmt.Sprintf("1.%d.0", i), ReleasedAt: now(), File: file, Size: 1, SHA512: "aGFzaA==",
			UpstreamURL: "https://download.deepseek.com/" + file, State: ReleaseMirrored, LocalPath: "/tmp/" + file}
		if err := st.CreateDshRelease(ctx, r); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for round := 0; round < 10; round++ {
		for _, id := range ids {
			wg.Add(1)
			go func(id int64) {
				defer wg.Done()
				errs <- st.SetDshReleaseRecommended(ctx, id)
			}(id)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("并发推荐失败（可能死锁）：%v", err)
		}
	}
	items, err := st.ListDshReleases(ctx, "mac-arm64")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, it := range items {
		if it.Recommended {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("并发推荐之后平台上应只有一个推荐，实际 %d 个", n)
	}
}
