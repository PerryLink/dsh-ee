package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/secret"
)

// ephemeralTestStores 在一个随机库里打开两个独立连接池，模拟两个 dsh-ee 实例共用同一个数据库。
func ephemeralTestStores(t *testing.T) (*Store, *Store) {
	t.Helper()
	base := os.Getenv("DSH_EE_TEST_DSN")
	if base == "" {
		t.Skip("未设置 DSH_EE_TEST_DSN，跳过数据库集成测试")
	}
	ctx := context.Background()
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	name := "dsh_ee_test_eph_" + hex.EncodeToString(suffix)
	dsn := strings.Replace(base, ")/", ")/"+name, 1)
	st1, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st1.DB().ExecContext(context.Background(), "DROP DATABASE `"+name+"`")
		st1.Close()
	})
	if err := st1.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	// 迁移可重复执行；第二个实例连同一个库，不再迁移（线上由先启动的实例迁移）。
	if err := st1.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	st2, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st2.Close() })
	return st1, st2
}

// jsonEqual 按语义比较两段 JSON（数据库会把 JSON 列规范化，不能按字节比）。
func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("解析 JSON %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("解析 JSON %s: %v", b, err)
	}
	return reflect.DeepEqual(x, y)
}

// newEphemeralTestState 造一条还没过期、payload 是简单 JSON 的状态。
func newEphemeralTestState(kind string, ttl time.Duration, ownerKey string, fields map[string]any) EphemeralState {
	if fields == nil {
		fields = map[string]any{"value": "x"}
	}
	payload, _ := json.Marshal(fields)
	return EphemeralState{
		Kind:      kind,
		StateHash: secret.Hash(secret.Token()),
		OwnerKey:  ownerKey,
		Payload:   payload,
		ExpiresAt: time.Now().UTC().Add(ttl),
	}
}

// 基本读写：Get 不消费、Consume 只能成功一次、错误 kind/过期/重复写入的错误语义。
func TestEphemeralStateStore(t *testing.T) {
	st, _ := ephemeralTestStores(t)
	ctx := context.Background()

	s := newEphemeralTestState("test_basic", time.Minute, "", map[string]any{"user_id": 42, "next": "/go"})
	if err := st.PutEphemeralState(ctx, s); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// 库里只存 sha256，不存调用方的随机值本身。
	if len(s.StateHash) != 64 {
		t.Fatalf("state_hash 应该是 64 位 sha256：%q", s.StateHash)
	}
	var stored string
	var payload []byte
	if err := st.DB().QueryRowContext(ctx,
		`SELECT state_hash, payload FROM ephemeral_states WHERE kind = ? AND state_hash = ?`,
		s.Kind, s.StateHash).Scan(&stored, &payload); err != nil {
		t.Fatalf("读库：%v", err)
	}
	if stored != s.StateHash || !json.Valid(payload) {
		t.Fatalf("落库内容不对：hash=%q payload=%s", stored, payload)
	}

	got, err := st.GetEphemeralState(ctx, s.Kind, s.StateHash)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// MySQL/TiDB 会把 JSON 列规范化（键顺序/空白会变），按语义比较。
	if !jsonEqual(t, got.Payload, s.Payload) || got.OwnerKey != "" || got.ConsumedAt != nil {
		t.Fatalf("Get 返回不对：%+v", got)
	}
	// Get 不消费：还能再读一次。
	if _, err := st.GetEphemeralState(ctx, s.Kind, s.StateHash); err != nil {
		t.Fatalf("第二次 Get: %v", err)
	}

	consumed, err := st.ConsumeEphemeralState(ctx, s.Kind, s.StateHash)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if consumed.ConsumedAt == nil || !jsonEqual(t, consumed.Payload, s.Payload) {
		t.Fatalf("Consume 返回不对：%+v", consumed)
	}
	if _, err := st.ConsumeEphemeralState(ctx, s.Kind, s.StateHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("第二次 Consume 应返回 ErrNotFound，得到 %v", err)
	}
	if _, err := st.GetEphemeralState(ctx, s.Kind, s.StateHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("消费后 Get 应返回 ErrNotFound，得到 %v", err)
	}

	// 错误 kind：同一个 hash 在别的 kind 下不存在。
	if _, err := st.ConsumeEphemeralState(ctx, "test_other_kind", s.StateHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("错误 kind 应返回 ErrNotFound，得到 %v", err)
	}

	// 过期：读取和消费都明确返回 expired。
	expired := newEphemeralTestState("test_basic", -time.Minute, "", nil)
	if err := st.PutEphemeralState(ctx, expired); err != nil {
		t.Fatalf("Put 过期状态: %v", err)
	}
	if _, err := st.GetEphemeralState(ctx, expired.Kind, expired.StateHash); !errors.Is(err, ErrEphemeralStateExpired) {
		t.Fatalf("过期状态 Get 应返回 ErrEphemeralStateExpired，得到 %v", err)
	}
	if _, err := st.ConsumeEphemeralState(ctx, expired.Kind, expired.StateHash); !errors.Is(err, ErrEphemeralStateExpired) {
		t.Fatalf("过期状态 Consume 应返回 ErrEphemeralStateExpired，得到 %v", err)
	}

	// 重复写入同一个 (kind, hash)：返回 ErrEphemeralStateExists，不覆盖原状态。
	dup := newEphemeralTestState("test_basic", time.Minute, "", map[string]any{"value": "new"})
	if err := st.PutEphemeralState(ctx, dup); err != nil {
		t.Fatal(err)
	}
	if err := st.PutEphemeralState(ctx, dup); !errors.Is(err, ErrEphemeralStateExists) {
		t.Fatalf("重复 Put 应返回 ErrEphemeralStateExists，得到 %v", err)
	}

	// 参数校验：坏哈希/空 payload 直接拒绝。
	if _, err := st.GetEphemeralState(ctx, "test_basic", "short"); err == nil {
		t.Fatal("长度不对的哈希应报错")
	}
	if err := st.PutEphemeralState(ctx, EphemeralState{Kind: "test_basic", StateHash: secret.Hash("x"), ExpiresAt: time.Now().Add(time.Minute)}); err == nil {
		t.Fatal("空 payload 应报错")
	}
}

// 更新 payload（接入码 attach 设备登录）：有效状态能改，消费/过期后返回 false。
func TestEphemeralStateUpdatePayload(t *testing.T) {
	st, _ := ephemeralTestStores(t)
	ctx := context.Background()

	s := newEphemeralTestState("test_payload", time.Minute, "user:1", map[string]any{"user_code": ""})
	if err := st.ReplaceEphemeralState(ctx, s); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	next, _ := json.Marshal(map[string]any{"user_code": "ABCD-EFGH"})
	if ok, err := st.UpdateEphemeralPayload(ctx, s.Kind, s.StateHash, next); err != nil || !ok {
		t.Fatalf("UpdatePayload: ok=%v err=%v", ok, err)
	}
	got, err := st.GetEphemeralState(ctx, s.Kind, s.StateHash)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	_ = json.Unmarshal(got.Payload, &decoded)
	if decoded["user_code"] != "ABCD-EFGH" {
		t.Fatalf("payload 没更新：%s", got.Payload)
	}
	// payload 完全一样时 affected rows 是 0，但仍应报告 true。
	if ok, err := st.UpdateEphemeralPayload(ctx, s.Kind, s.StateHash, next); err != nil || !ok {
		t.Fatalf("相同 payload 的 UpdatePayload: ok=%v err=%v", ok, err)
	}
	// 不存在的状态返回 false，不报错。
	missing := newEphemeralTestState("test_payload", time.Minute, "", nil)
	if ok, err := st.UpdateEphemeralPayload(ctx, missing.Kind, missing.StateHash, next); err != nil || ok {
		t.Fatalf("不存在的状态 UpdatePayload: ok=%v err=%v", ok, err)
	}
	// 消费后不能再改。
	if _, err := st.ConsumeEphemeralState(ctx, s.Kind, s.StateHash); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.UpdateEphemeralPayload(ctx, s.Kind, s.StateHash, next); err != nil || ok {
		t.Fatalf("已消费的状态 UpdatePayload: ok=%v err=%v", ok, err)
	}
	// 过期后不能再改。
	expired := newEphemeralTestState("test_payload", -time.Minute, "user:2", nil)
	if err := st.ReplaceEphemeralState(ctx, expired); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.UpdateEphemeralPayload(ctx, expired.Kind, expired.StateHash, next); err != nil || ok {
		t.Fatalf("过期状态 UpdatePayload: ok=%v err=%v", ok, err)
	}
}

// 按属主替换：同一个属主只保留一条，旧状态（旧接入码）立即失效。
func TestEphemeralStateReplaceByOwner(t *testing.T) {
	st, other := ephemeralTestStores(t)
	ctx := context.Background()

	first := newEphemeralTestState("test_owner", time.Minute, "user:7", map[string]any{"gen": 1})
	if err := st.ReplaceEphemeralState(ctx, first); err != nil {
		t.Fatalf("第一次 Replace: %v", err)
	}
	second := newEphemeralTestState("test_owner", time.Minute, "user:7", map[string]any{"gen": 2})
	// B 实例替换：A 实例写的旧状态必须立刻消失。
	if err := other.ReplaceEphemeralState(ctx, second); err != nil {
		t.Fatalf("第二次 Replace: %v", err)
	}
	if _, err := st.GetEphemeralState(ctx, first.Kind, first.StateHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("旧状态应已被替换掉，得到 %v", err)
	}
	if _, err := st.GetEphemeralState(ctx, second.Kind, second.StateHash); err != nil {
		t.Fatalf("新状态应可读：%v", err)
	}
	// 不同属主互不影响。
	otherOwner := newEphemeralTestState("test_owner", time.Minute, "user:8", map[string]any{"gen": 3})
	if err := st.ReplaceEphemeralState(ctx, otherOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetEphemeralState(ctx, second.Kind, second.StateHash); err != nil {
		t.Fatalf("不同属主的替换不应影响已有效果：%v", err)
	}
	// 没有属主的状态不能走 Replace（否则会误删同 kind 全部状态）。
	noOwner := newEphemeralTestState("test_owner", time.Minute, "", nil)
	if err := st.ReplaceEphemeralState(ctx, noOwner); err == nil {
		t.Fatal("没有 owner_key 的状态不能按属主替换")
	}
}

// 两个实例并发消费同一个状态：只有一个成功。
func TestEphemeralStateConsumeOnceAcrossInstances(t *testing.T) {
	st1, st2 := ephemeralTestStores(t)
	ctx := context.Background()

	// A 实例写入、B 实例消费：跨实例可用。
	s := newEphemeralTestState("test_multi", time.Minute, "", map[string]any{"who": "a"})
	if err := st1.PutEphemeralState(ctx, s); err != nil {
		t.Fatal(err)
	}
	if _, err := st2.ConsumeEphemeralState(ctx, s.Kind, s.StateHash); err != nil {
		t.Fatalf("B 实例消费 A 实例写的状态失败：%v", err)
	}

	// 同一个状态被两个实例并发消费：只能成功一次。
	race := newEphemeralTestState("test_multi", time.Minute, "", map[string]any{"who": "race"})
	if err := st1.PutEphemeralState(ctx, race); err != nil {
		t.Fatal(err)
	}
	stores := []*Store{st1, st2}
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = stores[i%2].ConsumeEphemeralState(ctx, race.Kind, race.StateHash)
		}(i)
	}
	wg.Wait()
	success := 0
	for i, err := range errs {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrNotFound):
			// 其余必须都报「无效/已被使用」，不能是数据库错误。
		default:
			t.Fatalf("并发消费返回意外错误（goroutine %d）：%v", i, err)
		}
	}
	if success != 1 {
		t.Fatalf("同一个状态并发消费只能成功一次，实际成功 %d 次", success)
	}
	var consumed int
	if err := st1.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM ephemeral_states WHERE kind = ? AND state_hash = ? AND consumed_at IS NOT NULL`,
		race.Kind, race.StateHash).Scan(&consumed); err != nil || consumed != 1 {
		t.Fatalf("consumed_at 应该只写一次：%d %v", consumed, err)
	}
}

// 两个实例同时给同一个属主换新状态：最终只留一条，旧的全部失效。
func TestEphemeralStateReplaceAcrossInstances(t *testing.T) {
	st1, st2 := ephemeralTestStores(t)
	ctx := context.Background()

	stores := []*Store{st1, st2}
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	states := make([]EphemeralState, n)
	for i := 0; i < n; i++ {
		states[i] = newEphemeralTestState("test_owner_race", time.Minute, "user:9", map[string]any{"gen": i})
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = stores[i%2].ReplaceEphemeralState(ctx, states[i])
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil && !errors.Is(err, ErrEphemeralStateExists) {
			t.Fatalf("并发替换返回意外错误（goroutine %d）：%v", i, err)
		}
	}
	// 无论哪个实例赢，库里这个属主只能有一条，且只有一个旧状态仍可用。
	var rows int
	if err := st1.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM ephemeral_states WHERE kind = ? AND owner_key = ?`,
		"test_owner_race", "user:9").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("同一个属主最终只能有一条状态，实际 %d 条", rows)
	}
	alive := 0
	for _, s := range states {
		if _, err := st1.GetEphemeralState(ctx, s.Kind, s.StateHash); err == nil {
			alive++
		}
	}
	if alive != 1 {
		t.Fatalf("并发替换后应该只有最后一个状态有效，实际 %d 个", alive)
	}
}

// 清理：只删过期的，未过期的保留，重复执行幂等。
func TestEphemeralStatePurge(t *testing.T) {
	st, _ := ephemeralTestStores(t)
	ctx := context.Background()

	expired1 := newEphemeralTestState("test_purge", -time.Minute, "user:1", nil)
	expired2 := newEphemeralTestState("test_purge", -time.Hour, "", nil)
	alive := newEphemeralTestState("test_purge", time.Minute, "", nil)
	for _, s := range []EphemeralState{expired1, expired2, alive} {
		if err := st.PutEphemeralState(ctx, s); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	n, err := st.PurgeExpiredEphemeralStates(ctx)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if n != 2 {
		t.Fatalf("应该清掉 2 条过期状态，实际 %d", n)
	}
	for _, s := range []EphemeralState{expired1, expired2} {
		if _, err := st.GetEphemeralState(ctx, s.Kind, s.StateHash); !errors.Is(err, ErrNotFound) {
			t.Fatalf("过期状态应已删除，得到 %v", err)
		}
	}
	if _, err := st.GetEphemeralState(ctx, alive.Kind, alive.StateHash); err != nil {
		t.Fatalf("未过期状态不应被清理：%v", err)
	}
	if n, err := st.PurgeExpiredEphemeralStates(ctx); err != nil || n != 0 {
		t.Fatalf("重复清理应幂等：n=%d err=%v", n, err)
	}
	// 已消费但未过期的状态随过期一起删（不影响空间回收语义）。
	consumed := newEphemeralTestState("test_purge", time.Minute, "", nil)
	if err := st.PutEphemeralState(ctx, consumed); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ConsumeEphemeralState(ctx, consumed.Kind, consumed.StateHash); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, "UPDATE ephemeral_states SET expires_at = ? WHERE kind = ? AND state_hash = ?",
		time.Now().UTC().Add(-time.Second), consumed.Kind, consumed.StateHash); err != nil {
		t.Fatal(err)
	}
	if n, err := st.PurgeExpiredEphemeralStates(ctx); err != nil || n != 1 {
		t.Fatalf("已消费状态过期后应被清理：n=%d err=%v", n, err)
	}
}
