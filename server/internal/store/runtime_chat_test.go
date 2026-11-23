package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// 0054：旧版同一位员工对同一个机器人的多段对话并成一条线程，轮次、时间、产物一个不丢；
// 之后 (owner, bot) 唯一，合并来的旧轮次在下一次发消息时恰好并入滚动记忆一次。
func TestRuntimeChatThreadMigration(t *testing.T) {
	s, _ := openTokenTestStoreDSN(t)
	ctx := context.Background()
	db := s.DB()
	owner := tokenTestUser(t, s, false).ID
	other := tokenTestUser(t, s, true).ID
	exec := func(q string, args ...any) int64 {
		t.Helper()
		res, err := db.ExecContext(ctx, q, args...)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	scan := func(q string, args []any, dest ...any) {
		t.Helper()
		if err := db.QueryRowContext(ctx, q, args...).Scan(dest...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		scan(q, args, &n)
		return n
	}
	bot := func(ownerID int64, name string) int64 {
		t.Helper()
		on := true
		b := &RuntimeBot{OwnerID: ownerID, RunAsID: ownerID, Name: name, Prompt: "persona", Schedule: "0 9 * * *", ReadOnly: true, DeliveryTarget: "feishu_owner"}
		if err := s.SaveRuntimeBot(ctx, b, &on); err != nil {
			t.Fatal(err)
		}
		return b.ID
	}
	base := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	conversation := func(ownerID, botID int64, title string, created, updated time.Time) int64 {
		t.Helper()
		return exec(`INSERT INTO runtime_chat_conversations (owner_id, bot_id, title, created_at, updated_at) VALUES (?,?,?,?,?)`,
			ownerID, botID, title, created, updated)
	}
	turn := func(ownerID, conversationID int64, n int, status string) int64 {
		t.Helper()
		at := base.Add(time.Duration(n) * time.Minute)
		return exec(`INSERT INTO runtime_tasks (owner_id, run_as_id, bot_id, prompt, status, idempotency_key, timeout_seconds, error, summary,
			created_at, updated_at, chat_conversation_id, chat_content) VALUES (?,?,NULL,?,?,?,?,'',?,?,?,?,?)`,
			ownerID, ownerID, "prompt", status, fmt.Sprintf("chat:%d:k%d", conversationID, n), 30, fmt.Sprintf("reply %d", n), at, at,
			conversationID, fmt.Sprintf("question %d", n))
	}

	body, err := migrationFS.ReadFile("migrations/0054_runtime_chat_threads.sql")
	if err != nil {
		t.Fatal(err)
	}
	var data []string
	unique := ""
	for _, stmt := range splitStatements(string(body)) {
		switch {
		case strings.Contains(stmt, "ADD UNIQUE KEY uq_runtime_chat_owner_bot (owner_id, bot_id)"):
			unique = stmt
		case strings.HasPrefix(stmt, "ALTER TABLE"):
			// TiDB 等库一条 ALTER 只做一件事。
			if strings.Count(stmt, " ADD ") != 1 {
				t.Fatalf("each ALTER must make exactly one change: %s", stmt)
			}
		default:
			data = append(data, stmt)
		}
	}
	if unique == "" || len(data) != 6 {
		t.Fatalf("0054 shape changed: unique=%q, %d data statements", unique, len(data))
	}

	// 回到迁移前的形态：没有唯一索引，同一对 (owner, bot) 有三段对话，轮次交错。
	exec(`ALTER TABLE runtime_chat_conversations DROP INDEX uq_runtime_chat_owner_bot`)
	botA, botB, botC := bot(owner, "A"), bot(owner, "B"), bot(other, "C")
	first := conversation(owner, botA, "", base.Add(2*time.Hour), base.Add(3*time.Hour))
	second := conversation(owner, botA, "Earliest titled", base, base.Add(time.Hour))
	third := conversation(owner, botA, "Later titled", base.Add(4*time.Hour), base.Add(9*time.Hour))
	solo := conversation(owner, botB, "Solo", base, base)
	foreign := conversation(other, botC, "Foreign", base, base)
	var succeeded []int64
	for i := 0; i < 13; i++ {
		succeeded = append(succeeded, turn(owner, []int64{second, first, third}[i%3], i, RuntimeSucceeded))
	}
	failed := turn(owner, third, 13, RuntimeFailed)
	soloTurn := turn(owner, solo, 14, RuntimeSucceeded)
	foreignTurn := turn(other, foreign, 15, RuntimeSucceeded)
	exec(`INSERT INTO runtime_artifacts (task_id, name, storage_key, size_bytes, created_at) VALUES (?,?,?,?,?)`, succeeded[0], "report.txt", "key-1", 7, base)
	exec(`INSERT INTO runtime_task_events (task_id, status, attempt, created_at) VALUES (?,?,?,?)`, succeeded[0], RuntimeSucceeded, 1, base)

	// 数据语句可以重跑：第二遍什么都不改。
	for pass := 0; pass < 2; pass++ {
		for _, stmt := range data {
			exec(stmt)
		}
	}
	exec(unique)

	var kept int64
	var title string
	var created, updated time.Time
	scan(`SELECT id, title, created_at, updated_at FROM runtime_chat_conversations WHERE owner_id=? AND bot_id=?`, []any{owner, botA}, &kept, &title, &created, &updated)
	if kept != first || title != "Earliest titled" || !created.Equal(base) || !updated.Equal(base.Add(9*time.Hour)) {
		t.Fatalf("canonical thread: id %d (want %d), title %q, created %v, updated %v", kept, first, title, created, updated)
	}
	if n := count(`SELECT COUNT(*) FROM runtime_chat_conversations`); n != 3 {
		t.Fatalf("threads after merge: %d, want 3", n)
	}
	if n := count(`SELECT COUNT(*) FROM runtime_tasks WHERE chat_conversation_id=?`, first); n != 14 {
		t.Fatalf("canonical thread has %d turns, want 14", n)
	}
	if n := count(`SELECT COUNT(*) FROM runtime_tasks WHERE chat_conversation_id IN (?,?)`, second, third); n != 0 {
		t.Fatalf("%d turns still point at merged threads", n)
	}
	if n := count(`SELECT COUNT(*) FROM runtime_tasks`); n != 16 {
		t.Fatalf("tasks after merge: %d, want 16", n)
	}
	if n := count(`SELECT COUNT(*) FROM runtime_chat_thread_merges WHERE canonical_id=? AND owner_id=? AND bot_id=?`, first, owner, botA); n != 2 {
		t.Fatalf("merge archive rows: %d, want 2", n)
	}
	scan(`SELECT title FROM runtime_chat_thread_merges WHERE old_id=?`, []any{third}, &title)
	if title != "Later titled" {
		t.Fatalf("archived title: %q", title)
	}
	for conversationID, taskID := range map[int64]int64{solo: soloTurn, foreign: foreignTurn} {
		if n := count(`SELECT COUNT(*) FROM runtime_tasks t JOIN runtime_chat_conversations c ON c.id=t.chat_conversation_id AND c.owner_id=t.owner_id
			WHERE t.id=? AND c.id=?`, taskID, conversationID); n != 1 {
			t.Fatalf("untouched thread %d lost turn %d", conversationID, taskID)
		}
	}
	if artifacts, err := s.RuntimeArtifacts(ctx, succeeded[0], owner); err != nil || len(artifacts) != 1 || artifacts[0].Name != "report.txt" {
		t.Fatalf("artifact after reparenting: %+v, %v", artifacts, err)
	}
	if artifacts, err := s.RuntimeArtifacts(ctx, succeeded[0], other); err != nil || len(artifacts) != 0 {
		t.Fatalf("cross-owner artifact after reparenting: %+v, %v", artifacts, err)
	}
	if events, err := s.RuntimeTaskEvents(ctx, succeeded[0], owner); err != nil || len(events) != 1 {
		t.Fatalf("events after reparenting: %+v, %v", events, err)
	}

	// 旧对话 ID 只对本人解析到保留线程；直接 ID 不受影响，别人拿新旧 ID 都查不到。
	for _, id := range []int64{first, second, third} {
		if c, err := s.RuntimeChatConversation(ctx, id, owner); err != nil || c.ID != first || c.BotID != botA {
			t.Fatalf("thread %d resolves to %+v, %v; want canonical %d", id, c, err, first)
		}
		if _, err := s.RuntimeChatConversation(ctx, id, other); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-owner thread %d: %v", id, err)
		}
	}
	if c, err := s.RuntimeChatConversation(ctx, solo, owner); err != nil || c.ID != solo {
		t.Fatalf("untouched thread: %+v, %v", c, err)
	}
	if c, err := s.RuntimeChatConversation(ctx, foreign, other); err != nil || c.ID != foreign {
		t.Fatalf("other owner's own thread: %+v, %v", c, err)
	}

	// 存档完整：翻页能走到第一轮，顺序就是任务 ID，原话、回复和时间都在。
	var all []RuntimeChatTurn
	for before, pages := int64(0), 0; ; pages++ {
		page, next, err := s.RuntimeChatTurns(ctx, first, owner, before, 5)
		if err != nil || len(page) == 0 || len(page) > 5 || pages > 3 {
			t.Fatalf("page before %d: %d turns, %v", before, len(page), err)
		}
		all = append(append([]RuntimeChatTurn{}, page...), all...)
		if next == 0 {
			break
		}
		if next != page[0].ID {
			t.Fatalf("next_before %d, want the oldest returned turn %d", next, page[0].ID)
		}
		before = next
	}
	if len(all) != 14 || all[13].ID != failed || all[13].Status != RuntimeFailed {
		t.Fatalf("archive has %d turns: %+v", len(all), all)
	}
	for i, id := range succeeded {
		if all[i].ID != id || all[i].Content != fmt.Sprintf("question %d", i) || all[i].Summary != fmt.Sprintf("reply %d", i) ||
			!all[i].CreatedAt.Equal(base.Add(time.Duration(i)*time.Minute)) {
			t.Fatalf("archived turn %d: %+v", i, all[i])
		}
	}
	if page, next, err := s.RuntimeChatTurns(ctx, first, other, 0, 5); err != nil || len(page) != 0 || next != 0 {
		t.Fatalf("cross-owner history: %+v, %d, %v", page, next, err)
	}

	// 唯一索引 + 幂等的取或建。
	if _, err := db.ExecContext(ctx, `INSERT INTO runtime_chat_conversations (owner_id, bot_id, title, created_at, updated_at) VALUES (?,?,'',?,?)`,
		owner, botA, base, base); !isMySQLErr(err, mysqlErrDuplicateKey) {
		t.Fatalf("duplicate thread insert: %v", err)
	}
	for i := 0; i < 2; i++ {
		if c, err := s.EnsureRuntimeChatConversation(ctx, owner, botA); err != nil || c.ID != first {
			t.Fatalf("ensure existing thread: %+v, %v", c, err)
		}
	}
	for name, pair := range map[string][2]int64{"other owner": {other, botA}, "foreign bot": {owner, botC}} {
		if _, err := s.EnsureRuntimeChatConversation(ctx, pair[0], pair[1]); !errors.Is(err, ErrNotFound) {
			t.Fatalf("ensure for %s: %v", name, err)
		}
	}

	// 合并来的 13 轮从未并入过记忆（水位 0）：下一次发消息时，窗口之外的最早 3 轮按任务 ID 顺序各并入一次。
	var folded []int64
	var window []int64
	keep := func(n int) int { return n }
	plan := RuntimeChatPlanner{
		Fold: func(memory string, turn RuntimeChatTurn) string {
			folded = append(folded, turn.ID)
			return memory + fmt.Sprintf("%d,", turn.ID)
		},
	}
	plan.Prompt = func(_ *RuntimeBot, memory string, recent []RuntimeChatTurn) (string, string, int) {
		window = window[:0]
		for _, turn := range recent {
			window = append(window, turn.ID)
		}
		kept := keep(len(recent))
		for _, turn := range recent[:len(recent)-kept] {
			memory = plan.Fold(memory, turn)
		}
		return "prompt", memory, kept
	}
	state := func() (string, int64) {
		t.Helper()
		var memory string
		var watermark int64
		scan(`SELECT COALESCE(context_summary,''), context_task_id FROM runtime_chat_conversations WHERE id=?`, []any{first}, &memory, &watermark)
		return memory, watermark
	}
	message := func(key string) RuntimeChatMessage {
		return RuntimeChatMessage{OwnerID: owner, ConversationID: first, Content: "new " + key, Title: "new",
			IdempotencyKey: fmt.Sprintf("chat:%d:%s", first, key), TimeoutSeconds: 30}
	}
	if memory, watermark := state(); memory != "" || watermark != 0 {
		t.Fatalf("merged thread starts with memory %q at %d", memory, watermark)
	}
	if _, err := s.EnqueueRuntimeChatTurn(ctx, RuntimeChatMessage{OwnerID: other, ConversationID: first, Content: "x", IdempotencyKey: "chat:x", TimeoutSeconds: 30}, plan); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner enqueue: %v", err)
	}
	// 合并前的轮次带着旧对话 ID 的幂等键：改挂后在保留线程上按原键重发，重放原来那一轮，不新建、不并入记忆；
	// 同一个键换了内容是冲突，别人用这个键查不到线程。
	legacy := RuntimeChatMessage{OwnerID: owner, ConversationID: first, Content: "question 0", Title: "new",
		IdempotencyKey: fmt.Sprintf("chat:%d:k0", second), TimeoutSeconds: 30}
	if replay, err := s.EnqueueRuntimeChatTurn(ctx, legacy, plan); err != nil || replay.ID != succeeded[0] || replay.Status != RuntimeSucceeded || replay.Summary != "reply 0" {
		t.Fatalf("reparented idempotency replay: %+v, %v", replay, err)
	}
	legacy.Content = "another question"
	if _, err := s.EnqueueRuntimeChatTurn(ctx, legacy, plan); !errors.Is(err, ErrConflict) {
		t.Fatalf("reparented key with other content: %v", err)
	}
	legacy.OwnerID = other
	if _, err := s.EnqueueRuntimeChatTurn(ctx, legacy, plan); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner reparented key: %v", err)
	}
	if memory, watermark := state(); len(folded) != 0 || memory != "" || watermark != 0 ||
		count(`SELECT COUNT(*) FROM runtime_tasks WHERE chat_conversation_id=?`, first) != 14 {
		t.Fatalf("legacy replay touched the thread: folded %v, memory %q, watermark %d", folded, memory, watermark)
	}
	one, err := s.EnqueueRuntimeChatTurn(ctx, message("m1"), plan)
	if err != nil {
		t.Fatal(err)
	}
	wantMemory := fmt.Sprintf("%d,%d,%d,", succeeded[0], succeeded[1], succeeded[2])
	if memory, watermark := state(); !reflect.DeepEqual(folded, succeeded[:3]) || !reflect.DeepEqual(window, succeeded[3:]) ||
		memory != wantMemory || watermark != succeeded[2] {
		t.Fatalf("legacy backfill: folded %v, window %v, memory %q, watermark %d", folded, window, memory, watermark)
	}
	// 重放和被拒绝的消息都不再并入。
	if replay, err := s.EnqueueRuntimeChatTurn(ctx, message("m1"), plan); err != nil || replay.ID != one.ID {
		t.Fatalf("replay: %+v, %v", replay, err)
	}
	if _, err := s.EnqueueRuntimeChatTurn(ctx, message("m2"), plan); !errors.Is(err, ErrRuntimeChatBusy) {
		t.Fatalf("second active turn: %v", err)
	}
	if memory, watermark := state(); len(folded) != 3 || memory != wantMemory || watermark != succeeded[2] {
		t.Fatalf("replay or busy changed the memory: folded %v, memory %q, watermark %d", folded, memory, watermark)
	}
	// 下一轮：窗口往前挪一轮；提示词只放得下 8 轮时，放不下的 2 轮也并入，水位跟到最后并入的那一轮。
	exec(`UPDATE runtime_tasks SET status=?, summary='done' WHERE id=?`, RuntimeSucceeded, one.ID)
	keep = func(n int) int { return n - 2 }
	if _, err := s.EnqueueRuntimeChatTurn(ctx, message("m2"), plan); err != nil {
		t.Fatal(err)
	}
	wantWindow := append(append([]int64{}, succeeded[4:]...), one.ID)
	if memory, watermark := state(); !reflect.DeepEqual(folded, succeeded[:6]) || !reflect.DeepEqual(window, wantWindow) ||
		memory != wantMemory+fmt.Sprintf("%d,%d,%d,", succeeded[3], succeeded[4], succeeded[5]) || watermark != succeeded[5] {
		t.Fatalf("rolling memory: folded %v, window %v, memory %q, watermark %d", folded, window, memory, watermark)
	}
	// 压缩只动线程上的摘录，存档一轮没少。
	if n := count(`SELECT COUNT(*) FROM runtime_tasks WHERE chat_conversation_id=?`, first); n != 16 {
		t.Fatalf("archive after roll-up: %d turns, want 16", n)
	}
}
