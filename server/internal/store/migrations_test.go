package store

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"time"
)

// 迁移文件是按行尾分号切分后逐条执行的（Migrate 里用 splitStatements）；
// 这里不连数据库，先保证每个文件都能被完整解析成可执行语句，
// 并且 token client_kind 这次改动落在预期的表和列上。
func TestMigrationsParse(t *testing.T) {
	files, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("没有迁移文件")
	}
	for _, f := range files {
		body, err := migrationFS.ReadFile(f)
		if err != nil {
			t.Fatalf("读 %s: %v", f, err)
		}
		stmts := splitStatements(string(body))
		if len(stmts) == 0 {
			t.Fatalf("%s 解析出 0 条语句", f)
		}
		for _, stmt := range stmts {
			trimmed := strings.TrimSpace(stmt)
			if trimmed == "" {
				t.Fatalf("%s 解析出了空语句", f)
			}
			// 切分器不处理字符串里的分号；迁移里只允许注释和语句，不允许残留的注释行混进语句
			for _, line := range strings.Split(stmt, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "--") {
					t.Fatalf("%s 有注释混进了语句：%q", f, stmt)
				}
			}
		}
	}
}

func TestFeishuChatMigration(t *testing.T) {
	body, err := migrationFS.ReadFile("migrations/0045_feishu_chat.sql")
	if err != nil {
		t.Fatal(err)
	}
	stmts := splitStatements(string(body))
	if len(stmts) != 1 {
		t.Fatalf("expected one create statement, got %d", len(stmts))
	}
	for _, fragment := range []string{"CREATE TABLE feishu_chat_messages", "UNIQUE KEY uq_feishu_chat_message (message_id)", "KEY idx_feishu_chat_queue (user_id, status, id)", "delivered_at", "replied_at"} {
		if !strings.Contains(stmts[0], fragment) {
			t.Errorf("migration missing %s", fragment)
		}
	}
}

// 0032：令牌客户端类型与调用/确认记录里的令牌归属。
func TestTokenClientKindMigration(t *testing.T) {
	body, err := migrationFS.ReadFile("migrations/0032_token_client_kind.sql")
	if err != nil {
		t.Fatalf("读 0032 迁移: %v", err)
	}
	stmts := splitStatements(string(body))
	if len(stmts) == 0 {
		t.Fatal("0032 迁移没有可执行语句")
	}
	joined := strings.Join(stmts, "\n")
	for _, want := range []string{
		"ALTER TABLE access_tokens",
		"client_kind VARCHAR(16) NOT NULL DEFAULT 'dsh'",
		"ALTER TABLE connection_calls",
		"token_id BIGINT NULL",
		"ALTER TABLE write_confirmations",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("0032 迁移缺少 %q：\n%s", want, joined)
		}
	}
}

// 0036：多实例共享的一次性状态表（web link / 接入码 / OIDC state）。
func TestEphemeralStatesMigration(t *testing.T) {
	body, err := migrationFS.ReadFile("migrations/0036_ephemeral_states.sql")
	if err != nil {
		t.Fatalf("读 0036 迁移: %v", err)
	}
	stmts := splitStatements(string(body))
	if len(stmts) == 0 {
		t.Fatal("0036 迁移没有可执行语句")
	}
	joined := strings.Join(stmts, "\n")
	for _, want := range []string{
		"CREATE TABLE ephemeral_states",
		"kind        VARCHAR(32)  NOT NULL",
		"state_hash  CHAR(64)     NOT NULL",
		"owner_key   VARCHAR(191) NULL",
		"payload     JSON         NOT NULL",
		"expires_at  DATETIME(3)  NOT NULL",
		"created_at  DATETIME(3)  NOT NULL",
		"consumed_at DATETIME(3)  NULL",
		"PRIMARY KEY (kind, state_hash)",
		"UNIQUE KEY uk_ephemeral_states_owner (kind, owner_key)",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("0036 迁移缺少 %q：\n%s", want, joined)
		}
	}
}

// 0036：API 客户端注册（client_credentials）与审计来源加宽。
func TestAPIClientsMigration(t *testing.T) {
	body, err := migrationFS.ReadFile("migrations/0036_api_clients.sql")
	if err != nil {
		t.Fatalf("读 0036 迁移: %v", err)
	}
	joined := strings.Join(splitStatements(string(body)), "\n")
	for _, want := range []string{
		"CREATE TABLE api_clients",
		"client_id     VARCHAR(64)   NOT NULL",
		"secret_hash   CHAR(64)      NOT NULL",
		"owner_user_id BIGINT        NOT NULL",
		"scopes        VARCHAR(1024) NOT NULL",
		"client_kind   VARCHAR(16)   NOT NULL DEFAULT 'third_party'",
		"created_by    BIGINT        NOT NULL DEFAULT 0",
		"disabled_at   DATETIME(3)   NULL",
		"last_used_at  DATETIME(3)   NULL",
		"UNIQUE KEY uk_api_clients_client_id (client_id)",
		"ALTER TABLE audit_logs MODIFY COLUMN via VARCHAR(32)",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("0036 迁移缺少 %q：\n%s", want, joined)
		}
	}
}

// 0035：刷新令牌数据模型（docs/token-lifecycle.md §3）。
func TestTokenRefreshMigration(t *testing.T) {
	body, err := migrationFS.ReadFile("migrations/0035_token_refresh_tokens.sql")
	if err != nil {
		t.Fatalf("读 0035 迁移: %v", err)
	}
	stmts := splitStatements(string(body))
	if len(stmts) == 0 {
		t.Fatal("0035 迁移没有可执行语句")
	}
	joined := strings.Join(stmts, "\n")
	for _, want := range []string{
		"CREATE TABLE token_refresh_tokens",
		"family_id          CHAR(32)",
		"access_token_id    BIGINT",
		"user_id            BIGINT",
		"client_kind        VARCHAR(16)",
		"secret_hash        CHAR(64)",
		"issued_at          DATETIME(3)",
		"expires_at         DATETIME(3)",
		"rotated_at         DATETIME(3)",
		"revoked_at         DATETIME(3)",
		"reuse_detected_at  DATETIME(3)",
		"created_ip         VARCHAR(64)",
		"UNIQUE KEY uk_token_refresh_hash",
		"ALTER TABLE access_tokens",
		"family_id CHAR(32) NULL",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("0035 迁移缺少 %q：\n%s", want, joined)
		}
	}
}

// 0037：公开协议面（/v1 与 /mcp）的每令牌限流改为数据库共享固定窗口。
func TestSharedRateLimitsMigration(t *testing.T) {
	body, err := migrationFS.ReadFile("migrations/0037_shared_rate_limits.sql")
	if err != nil {
		t.Fatalf("读 0037 迁移: %v", err)
	}
	stmts := splitStatements(string(body))
	if len(stmts) == 0 {
		t.Fatal("0037 迁移没有可执行语句")
	}
	// 迁移里为了对齐列写了很多空格，断言前先压成单个空格。
	joined := strings.Join(strings.Fields(strings.Join(stmts, "\n")), " ")
	for _, want := range []string{
		"CREATE TABLE shared_rate_limits",
		"rate_key VARCHAR(191) NOT NULL",
		"window_start DATETIME(3) NOT NULL",
		"count INT NOT NULL DEFAULT 0",
		"PRIMARY KEY (rate_key, window_start)",
		"KEY idx_shared_rate_limits_window (window_start)",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("0037 迁移缺少 %q：\n%s", want, joined)
		}
	}
}

// 0038：模型能力声明列（思考模式开关与可用强度）。
func TestModelReasoningMigration(t *testing.T) {
	body, err := migrationFS.ReadFile("migrations/0038_model_reasoning.sql")
	if err != nil {
		t.Fatalf("读 0038 迁移: %v", err)
	}
	stmts := splitStatements(string(body))
	if len(stmts) == 0 {
		t.Fatal("0038 迁移没有可执行语句")
	}
	joined := strings.Join(stmts, "\n")
	for _, want := range []string{
		"ALTER TABLE models",
		"supports_reasoning TINYINT(1) NOT NULL DEFAULT 0",
		"reasoning_efforts VARCHAR(128) NOT NULL DEFAULT ''",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("0038 迁移缺少 %q：\n%s", want, joined)
		}
	}
}

// 0040：模型输入模态声明列（text / image / file，默认 text）。
func TestModelInputModalitiesMigration(t *testing.T) {
	body, err := migrationFS.ReadFile("migrations/0040_model_input_modalities.sql")
	if err != nil {
		t.Fatalf("读 0040 迁移: %v", err)
	}
	stmts := splitStatements(string(body))
	if len(stmts) == 0 {
		t.Fatal("0040 迁移没有可执行语句")
	}
	joined := strings.Join(stmts, "\n")
	for _, want := range []string{
		"ALTER TABLE models",
		"input_modalities VARCHAR(128) NOT NULL DEFAULT 'text'",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("0040 迁移缺少 %q：\n%s", want, joined)
		}
	}
}

// 0037：users 表加语言偏好列。空串 = 跟随组织默认语言，老账号不用回填；
// 迁移是可重复执行的加法 DDL（旧二进制不读这列也能继续跑）。
func TestUserLanguageMigration(t *testing.T) {
	body, err := migrationFS.ReadFile("migrations/0037_user_language.sql")
	if err != nil {
		t.Fatalf("读 0037 迁移: %v", err)
	}
	stmts := splitStatements(string(body))
	if len(stmts) != 1 {
		t.Fatalf("0037 迁移应只有一条 ALTER，实际 %d 条", len(stmts))
	}
	stmt := strings.Join(strings.Fields(stmts[0]), " ")
	for _, want := range []string{
		"ALTER TABLE users",
		"ADD COLUMN language VARCHAR(8) NOT NULL DEFAULT ''",
	} {
		if !strings.Contains(stmt, want) {
			t.Fatalf("0037 迁移缺少 %q：%s", want, stmt)
		}
	}
}

// 0042：providers 加 api_key_origin 列，记录每把 key 写入时绑定的上游 origin。
// 纯加法 DDL（旧二进制不读这列也能继续跑），空串 = 加列前的旧记录。
func TestProviderKeyOriginMigration(t *testing.T) {
	body, err := migrationFS.ReadFile("migrations/0042_provider_key_origin.sql")
	if err != nil {
		t.Fatalf("读 0042 迁移: %v", err)
	}
	stmts := splitStatements(string(body))
	if len(stmts) != 1 {
		t.Fatalf("0042 迁移应只有一条 ALTER，实际 %d 条", len(stmts))
	}
	stmt := strings.Join(strings.Fields(stmts[0]), " ")
	for _, want := range []string{
		"ALTER TABLE providers",
		"ADD COLUMN api_key_origin VARCHAR(255) NOT NULL DEFAULT ''",
	} {
		if !strings.Contains(stmt, want) {
			t.Fatalf("0042 迁移缺少 %q：%s", want, stmt)
		}
	}
}

// legacyScopeBackfillMigration 回填升级前遗留范围申请的快照标识（scope_requested_at）。
const legacyScopeBackfillMigration = "0064_backfill_legacy_scope_requested_at"

// 0064：升级前提交、还没被审批的范围申请没有快照标识（pending_scope 非空但
// scope_requested_at 为 NULL），审批人拿不到令牌，只能一直 400/409。
// 迁移用 updated_at 回填：申请写入时 pending_scope 与 scope_requested_at 是同一条 UPDATE
// 写的同一个时间点，两列都是 DATETIME(3)，精度一致。
func TestMigrationLegacyScopeBackfillSQL(t *testing.T) {
	body, err := migrationFS.ReadFile("migrations/" + legacyScopeBackfillMigration + ".sql")
	if err != nil {
		t.Fatalf("读 0064 迁移: %v", err)
	}
	stmts := splitStatements(string(body))
	if len(stmts) != 1 {
		t.Fatalf("0064 迁移应只有一条 UPDATE，实际 %d 条", len(stmts))
	}
	stmt := strings.Join(strings.Fields(stmts[0]), " ")
	for _, want := range []string{
		"UPDATE assets SET scope_requested_at = updated_at",
		"WHERE pending_scope IS NOT NULL",
		"pending_scope <> ''",
		"scope_requested_at IS NULL",
	} {
		if !strings.Contains(stmt, want) {
			t.Fatalf("0064 迁移缺少 %q：%s", want, stmt)
		}
	}
}

// 在真实库上走一遍升级路径：先造升级前遗留的数据，再把 0064 从 schema_migrations 摘掉
// 重新 Migrate（等价于「升级到含这条迁移的版本时库里已经有旧数据」）。
// 只回填仍在审且标识为 NULL 的行：已有标识的行、没有在审申请的行都不能被动。
func TestMigrationLegacyScopeBackfillApplies(t *testing.T) {
	st := openTokenTestStore(t)
	ctx := context.Background()

	// 遗留申请：在审，但没有快照标识
	legacyAt := time.Date(2026, 11, 1, 9, 30, 0, 456_000_000, time.UTC)
	legacy := scopeTestAsset(t, st, "legacy-scope-pending", Scope{Users: []int64{1}})
	setLegacyScopeRequest(t, st, legacy.ID, `{"departments":["销售部"]}`, legacyAt)

	// 已有标识的行：updated_at 与标识故意不同，回填不能覆盖标识
	existingAt := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	existing := scopeTestAsset(t, st, "legacy-scope-existing", Scope{Users: []int64{2}})
	if _, err := st.DB().ExecContext(ctx,
		"UPDATE assets SET pending_scope = ?, scope_requested_at = ?, updated_at = ? WHERE id = ?",
		`{"all":true}`, existingAt, existingAt.Add(time.Second), existing.ID); err != nil {
		t.Fatalf("造已有标识的申请：%v", err)
	}

	// 没有在审申请的行：不能凭空造出一个令牌
	noPendingAt := time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC)
	noPending := scopeTestAsset(t, st, "legacy-scope-none", Scope{Users: []int64{3}})
	if _, err := st.DB().ExecContext(ctx, "UPDATE assets SET updated_at = ? WHERE id = ?", noPendingAt, noPending.ID); err != nil {
		t.Fatalf("改无申请行的 updated_at：%v", err)
	}
	// pending_scope 为空串按「没有在审申请」处理（和 scanAsset 一致），不能回填
	emptyPending := scopeTestAsset(t, st, "legacy-scope-empty", Scope{Users: []int64{4}})
	if _, err := st.DB().ExecContext(ctx,
		"UPDATE assets SET pending_scope = '', scope_requested_at = NULL, updated_at = ? WHERE id = ?",
		noPendingAt.Add(time.Second), emptyPending.ID); err != nil {
		t.Fatalf("造 pending_scope 为空串的行：%v", err)
	}

	replayMigration(t, st, legacyScopeBackfillMigration)

	got, err := st.GetAsset(ctx, legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ScopeRequestedAt == nil || !got.ScopeRequestedAt.Equal(legacyAt) {
		t.Fatalf("遗留申请应回填 updated_at 作为快照标识：got %v want %v", got.ScopeRequestedAt, legacyAt)
	}
	if got.PendingScope == nil || len(got.PendingScope.Departments) != 1 || got.PendingScope.Departments[0] != "销售部" {
		t.Fatalf("回填不能改动申请本身：pending=%+v", got.PendingScope)
	}

	got, err = st.GetAsset(ctx, existing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ScopeRequestedAt == nil || !got.ScopeRequestedAt.Equal(existingAt) {
		t.Fatalf("已有标识不能被回填覆盖：got %v want %v", got.ScopeRequestedAt, existingAt)
	}

	for _, tc := range []struct {
		name string
		id   int64
	}{{"没有在审申请", noPending.ID}, {"pending_scope 为空串", emptyPending.ID}} {
		got, err := st.GetAsset(ctx, tc.id)
		if err != nil {
			t.Fatal(err)
		}
		if got.ScopeRequestedAt != nil {
			t.Fatalf("%s 的行不应回填：%+v", tc.name, got.ScopeRequestedAt)
		}
	}

	// 迁移可重复执行：再回填一次，所有行的标识都不变
	replayMigration(t, st, legacyScopeBackfillMigration)
	again, err := st.GetAsset(ctx, legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.ScopeRequestedAt == nil || !again.ScopeRequestedAt.Equal(legacyAt) {
		t.Fatalf("重复执行回填不应改变已回填的标识：got %v want %v", again.ScopeRequestedAt, legacyAt)
	}
	again, err = st.GetAsset(ctx, existing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.ScopeRequestedAt == nil || !again.ScopeRequestedAt.Equal(existingAt) {
		t.Fatalf("重复执行回填不应覆盖已有标识：got %v want %v", again.ScopeRequestedAt, existingAt)
	}
}

// setLegacyScopeRequest 造一条「升级前遗留」的范围申请：申请在审（pending_scope 非空），
// 但快照标识 scope_requested_at 还是 NULL。
func setLegacyScopeRequest(t *testing.T, st *Store, id int64, pendingScope string, updatedAt time.Time) {
	t.Helper()
	if _, err := st.DB().ExecContext(context.Background(),
		"UPDATE assets SET pending_scope = ?, scope_requested_at = NULL, updated_at = ? WHERE id = ?",
		pendingScope, updatedAt, id); err != nil {
		t.Fatalf("造遗留范围申请：%v", err)
	}
}

// replayMigration 把一条已登记的迁移从 schema_migrations 摘掉再 Migrate 一次。
// 测试库建好时迁移都跑过了，这样做等价于「升级到含这条迁移的版本时库里已经有旧数据」，
// 走的是生产升级同一条路径（Migrate 执行迁移文件里的 SQL 并重新登记版本）。
func replayMigration(t *testing.T, st *Store, version string) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.DB().ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = ?", version); err != nil {
		t.Fatalf("摘掉迁移登记 %s：%v", version, err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("重跑迁移 %s：%v", version, err)
	}
}
