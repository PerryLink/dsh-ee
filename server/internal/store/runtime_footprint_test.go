package store

import (
	"context"
	"testing"
)

// 启动告警只统计数量：云电脑与待回收沙箱各一行时返回 1/1，清掉 orphan 后收敛为 1/0。
func TestCountCloudRuntimeFootprint(t *testing.T) {
	s, _ := openTokenTestStoreDSN(t)
	ctx := context.Background()
	if n1, n2, err := s.CountCloudRuntimeFootprint(ctx); err != nil || n1 != 0 || n2 != 0 {
		t.Fatalf("空库应为 0/0：%d %d %v", n1, n2, err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO runtime_computers (owner_kind, owner_user_id, owner_department, state, last_used_at, created_at, updated_at) VALUES ('user', 0, '', 'none', NOW(3), NOW(3), NOW(3))`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO runtime_computer_orphans (sandbox_id, creation_key, bot_id, reason, created_at) VALUES ('sbx-footprint-test', '', 0, 'merge', NOW(3))`); err != nil {
		t.Fatal(err)
	}
	n1, n2, err := s.CountCloudRuntimeFootprint(ctx)
	if err != nil || n1 != 1 || n2 != 1 {
		t.Fatalf("应为 1/1：%d %d %v", n1, n2, err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM runtime_computer_orphans WHERE sandbox_id='sbx-footprint-test'`); err != nil {
		t.Fatal(err)
	}
	if n1, n2, err = s.CountCloudRuntimeFootprint(ctx); err != nil || n1 != 1 || n2 != 0 {
		t.Fatalf("应为 1/0：%d %d %v", n1, n2, err)
	}
}
