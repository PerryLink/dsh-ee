package store

import (
	"context"
	"testing"
	"time"
)

// 审计 C1：飞书聊天队列过去只把过期行标成 status=expired、从不删除，队列表无界增长
// （连未绑定账号的入站记录也留着）。PurgeFeishuChats 必须按创建时间整行删除超过
// 保留期的行，窗口内的行保留。
func TestFeishuChatPurge(t *testing.T) {
	st := openTokenTestStore(t)
	ctx := context.Background()
	seed := func(key FeishuChatKey, messageID, text string, age time.Duration) int64 {
		t.Helper()
		if _, err := st.EnqueueFeishuChat(ctx, key, messageID, "oc-chat", text); err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := st.DB().QueryRowContext(ctx, "SELECT id FROM feishu_chat_messages WHERE message_id=?", messageID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if age > 0 {
			if _, err := st.DB().ExecContext(ctx, "UPDATE feishu_chat_messages SET created_at=? WHERE id=?", now().Add(-age), id); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	exists := func(id int64) bool {
		t.Helper()
		var found bool
		if err := st.DB().QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM feishu_chat_messages WHERE id=?)", id).Scan(&found); err != nil {
			t.Fatal(err)
		}
		return found
	}
	bound := FeishuChatKey{AppScope: "scope", UserID: 7, SenderID: "ou-user"}
	unbound := FeishuChatKey{AppScope: "scope", SenderID: "ou-stranger"}
	old := seed(bound, "msg-old", "两小时前的消息", 2*time.Hour)
	oldUnbound := seed(unbound, "msg-old-unbound", "", 2*time.Hour)
	fresh := seed(bound, "msg-fresh", "刚收到的消息", 0)
	recent := seed(bound, "msg-recent", "半小时前的消息", 30*time.Minute)

	n, err := st.PurgeFeishuChats(ctx, FeishuChatLifetime)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("应删除 2 行超过保留期的消息，实际删除 %d 行", n)
	}
	if exists(old) || exists(oldUnbound) {
		t.Fatal("超过保留期的行必须整行删除，而不是只把 status 标成 expired")
	}
	if !exists(fresh) || !exists(recent) {
		t.Fatal("保留期内的行不应被删除")
	}
	// 迁移 0065 给 created_at 建了索引：周期 DELETE 按时间范围删，不做全表扫描。
	var indexes int
	if err := st.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='feishu_chat_messages' AND column_name='created_at'").Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if indexes == 0 {
		t.Fatal("feishu_chat_messages 缺少 created_at 索引（迁移 0065）")
	}
}
