package store

import (
	"context"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/secret"
)

// API 客户端注册（client_credentials）的库层：创建 / 按 client_id 取 / 列出（带 owner 信息）/
// 记最近使用 / 禁用幂等。需要数据库（DSH_EE_TEST_DSN）。
func TestAPIClientsStore(t *testing.T) {
	st := openTokenTestStore(t)
	ctx := context.Background()
	owner := &User{Username: "client-store-owner", Department: "研发效能部", PasswordHash: "x"}
	if err := st.CreateUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	c := &APIClient{
		ClientID: "dshc_store_test", SecretHash: secret.Hash("dshs_store_secret"), Name: "ci-store",
		OwnerUserID: owner.ID, Scopes: []string{"models", "hub"}, ClientKind: ClientKindThirdParty, CreatedBy: owner.ID,
	}
	if err := st.CreateAPIClient(ctx, c); err != nil {
		t.Fatal(err)
	}
	if c.ID == 0 || c.CreatedAt.IsZero() {
		t.Fatalf("创建后应回填 id / created_at：%+v", c)
	}

	got, err := st.APIClientByClientID(ctx, c.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "ci-store" || got.OwnerUserID != owner.ID || got.OwnerUsername != owner.Username ||
		got.OwnerDepartment != owner.Department || got.ClientKind != ClientKindThirdParty ||
		len(got.Scopes) != 2 || got.Scopes[0] != "models" || got.Scopes[1] != "hub" {
		t.Fatalf("按 client_id 取回的字段不对：%+v", got)
	}
	if got.DisabledAt != nil || got.LastUsedAt != nil {
		t.Fatalf("新客户端不应有禁用 / 使用时间：%+v", got)
	}
	if _, err := st.APIClientByClientID(ctx, "dshc_missing"); err != ErrNotFound {
		t.Fatalf("不存在的 client_id 应返回 ErrNotFound：%v", err)
	}

	list, err := st.ListAPIClients(ctx)
	if err != nil || len(list) != 1 || list[0].ClientID != c.ClientID {
		t.Fatalf("列表不对：%v %+v", err, list)
	}

	if err := st.TouchAPIClient(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = st.APIClientByClientID(ctx, c.ClientID)
	if got.LastUsedAt == nil {
		t.Fatal("成功签发后应更新 last_used_at")
	}

	changed, err := st.DisableAPIClient(ctx, c.ClientID)
	if err != nil || !changed {
		t.Fatalf("第一次禁用应生效：changed=%v err=%v", changed, err)
	}
	changed, err = st.DisableAPIClient(ctx, c.ClientID)
	if err != nil || changed {
		t.Fatalf("重复禁用应幂等：changed=%v err=%v", changed, err)
	}
	got, _ = st.APIClientByClientID(ctx, c.ClientID)
	if got.DisabledAt == nil {
		t.Fatal("禁用后应有 disabled_at")
	}
}

// 没有 scopes 的客户端落一个空 JSON 数组而不是 NULL；读回来仍是空数组。
func TestAPIClientsStoreEmptyScopes(t *testing.T) {
	st := openTokenTestStore(t)
	ctx := context.Background()
	owner := &User{Username: "client-store-empty", PasswordHash: "x"}
	if err := st.CreateUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	c := &APIClient{ClientID: "dshc_store_empty", SecretHash: secret.Hash("x"), Name: "empty", OwnerUserID: owner.ID}
	if err := st.CreateAPIClient(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err := st.APIClientByClientID(ctx, c.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Scopes == nil || len(got.Scopes) != 0 {
		t.Fatalf("空 scopes 应读回空数组：%+v", got.Scopes)
	}
}
