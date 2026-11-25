package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// 待发附件配额以前是「先查合计、再写库」两步：并发上传会同时读到「没超」，各自再插一条，
// 20 个 30 MB 的上传能把 500 MB 的上限冲到 510 MB。现在配额检查和写入在同一个事务里，
// 靠锁 users 行把同一员工的并发上传串起来：只有 16 个（合计 480 MB）成功。
func TestRuntimeChatUploadQuotaConcurrent(t *testing.T) {
	st := openTokenTestStore(t)
	ctx := context.Background()
	owner := tokenTestUser(t, st, false).ID
	const (
		uploadBytes = 30 << 20
		pendingMax  = 500 << 20
		uploads     = 20
	)
	wantSucceeded := int32(pendingMax / uploadBytes) // 16 个 30 MB 刚好不超过 500 MB

	var succeeded, rejected int32
	var failures []error
	var mu sync.Mutex
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < uploads; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			u := &RuntimeChatUpload{
				OwnerID: owner, ConversationID: 1, Name: fmt.Sprintf("并发-%d.txt", i),
				Size: uploadBytes, Format: "txt", StorageKey: fmt.Sprintf("concurrent-%d", i),
			}
			err := st.CreateRuntimeChatUploadWithQuota(ctx, u, pendingMax)
			switch {
			case err == nil:
				if u.ID == 0 {
					mu.Lock()
					failures = append(failures, fmt.Errorf("上传 %d 成功但没有回填 ID", i))
					mu.Unlock()
				}
				atomic.AddInt32(&succeeded, 1)
			case errors.Is(err, ErrRuntimeChatUploadQuota):
				if u.ID != 0 {
					mu.Lock()
					failures = append(failures, fmt.Errorf("上传 %d 被拒却回填了 ID %d", i, u.ID))
					mu.Unlock()
				}
				atomic.AddInt32(&rejected, 1)
			default:
				mu.Lock()
				failures = append(failures, fmt.Errorf("上传 %d: %w", i, err))
				mu.Unlock()
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for _, err := range failures {
		t.Error(err)
	}
	if got := atomic.LoadInt32(&succeeded); got != wantSucceeded {
		t.Fatalf("并发上传成功 %d 个，期望 %d 个（每个 %d 字节，上限 %d）", got, wantSucceeded, int64(uploadBytes), int64(pendingMax))
	}
	if got := atomic.LoadInt32(&rejected); got != uploads-wantSucceeded {
		t.Fatalf("被配额拒绝 %d 个，期望 %d 个", got, uploads-wantSucceeded)
	}
	total, err := st.RuntimeChatPendingUploadBytes(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(wantSucceeded) * uploadBytes; total != want {
		t.Fatalf("待发合计 %d 字节，期望 %d", total, want)
	}
	if total > pendingMax {
		t.Fatalf("并发上传后待发合计 %d 字节，超过上限 %d", total, int64(pendingMax))
	}
}

// 配额只算本人还没发出的附件：已发出的（task_id 非空）和别人的都不占额度；
// 正好等于上限放行，多一个字节就拒绝，被拒绝时不写库、不回填 ID。
func TestRuntimeChatUploadQuotaScopeAndBoundary(t *testing.T) {
	st := openTokenTestStore(t)
	ctx := context.Background()
	owner := tokenTestUser(t, st, false).ID
	other := tokenTestUser(t, st, true).ID
	const pendingMax = 200

	// 本人 150 字节待发（100 原件 + 50 正文）
	if err := st.CreateRuntimeChatUpload(ctx, &RuntimeChatUpload{OwnerID: owner, ConversationID: 1, Name: "pending.txt", Size: 100, Format: "txt", TextBytes: 50, StorageKey: "pending"}); err != nil {
		t.Fatal(err)
	}
	// 已经随消息发出去的：不占额度
	if _, err := st.DB().ExecContext(ctx, `INSERT INTO runtime_chat_uploads (owner_id, conversation_id, task_id, name, size, format, text_bytes, storage_key, created_at)
		VALUES (?,1,77,'sent.txt',4000,'txt',4000,'sent',?)`, owner, now()); err != nil {
		t.Fatal(err)
	}
	// 别人的待发附件：不占本人的额度
	if err := st.CreateRuntimeChatUpload(ctx, &RuntimeChatUpload{OwnerID: other, ConversationID: 1, Name: "other.txt", Size: 2000, Format: "txt", StorageKey: "other"}); err != nil {
		t.Fatal(err)
	}
	if n, err := st.RuntimeChatPendingUploadBytes(ctx, owner); err != nil || n != 150 {
		t.Fatalf("本人待发合计应为 150，得到 %d（%v）", n, err)
	}

	// 100 + 50 + 50 = 200 字节，正好到上限：放行
	if err := st.CreateRuntimeChatUploadWithQuota(ctx, &RuntimeChatUpload{OwnerID: owner, ConversationID: 1, Name: "fit.txt", Size: 50, Format: "txt", StorageKey: "fit"}, pendingMax); err != nil {
		t.Fatalf("正好等于上限应放行：%v", err)
	}
	// 再多 1 字节：拒绝，并且不写库
	over := &RuntimeChatUpload{OwnerID: owner, ConversationID: 1, Name: "over.txt", Size: 1, Format: "txt", StorageKey: "over"}
	if err := st.CreateRuntimeChatUploadWithQuota(ctx, over, pendingMax); !errors.Is(err, ErrRuntimeChatUploadQuota) {
		t.Fatalf("超过上限应返回 ErrRuntimeChatUploadQuota，得到 %v", err)
	}
	if over.ID != 0 {
		t.Fatalf("被拒绝的上传不应回填 ID：%d", over.ID)
	}
	var n int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_chat_uploads WHERE owner_id=? AND task_id IS NULL`, owner).Scan(&n); err != nil || n != 2 {
		t.Fatalf("被拒绝的上传不应写库：count=%d err=%v", n, err)
	}
	// 员工不存在：不写，返回 ErrNotFound
	if err := st.CreateRuntimeChatUploadWithQuota(ctx, &RuntimeChatUpload{OwnerID: 1 << 40, ConversationID: 1, Name: "x.txt", Size: 1, Format: "txt", StorageKey: "nobody"}, pendingMax); !errors.Is(err, ErrNotFound) {
		t.Fatalf("员工不存在应返回 ErrNotFound，得到 %v", err)
	}
}
