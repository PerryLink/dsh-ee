package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// 扩展签名的信任根（migrations/0032_trusted_signing_keys.sql、docs/plugin-signing.md §3）：
// 换下、撤销都保留记录，供验签区分历史签名和能不能新装。
func TestTrustedSigningKeys(t *testing.T) {
	st := openTokenTestStore(t)
	ctx := context.Background()
	uid := int64(7)
	base := TrustedSigningKey{
		KeyID: "aabbccddeeff0011", PublicKey: "-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----",
		Purpose: "extension", Note: "测试钥匙", CreatedBy: &uid,
	}
	k := base
	if err := st.CreateTrustedSigningKey(ctx, &k); err != nil {
		t.Fatal(err)
	}
	if k.CreatedAt.IsZero() || k.ValidFrom.IsZero() {
		t.Fatalf("创建时间和生效时间要填上：%+v", k)
	}
	// 同一个 key id 不能再加：撤销过的钥匙也不能重新加回来
	if err := st.CreateTrustedSigningKey(ctx, &base); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("重复的 key id 应是 ErrDuplicate：%v", err)
	}
	if got, err := st.TrustedSigningKey(ctx, k.KeyID); err != nil || got.Note != "测试钥匙" || got.CreatedBy == nil || *got.CreatedBy != uid || got.RetiredAt != nil || got.RevokedAt != nil {
		t.Fatalf("取回来的记录不对：%+v %v", got, err)
	}
	if _, err := st.TrustedSigningKey(ctx, "0000000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的 key id 应是 ErrNotFound：%v", err)
	}

	// 换下：只改一次，重复换下幂等
	at := time.Now().UTC()
	if changed, err := st.RetireTrustedSigningKey(ctx, k.KeyID, at); err != nil || !changed {
		t.Fatalf("换下：%v %v", changed, err)
	}
	if changed, err := st.RetireTrustedSigningKey(ctx, k.KeyID, at.Add(time.Minute)); err != nil || changed {
		t.Fatalf("重复换下应幂等：%v %v", changed, err)
	}
	if got, _ := st.TrustedSigningKey(ctx, k.KeyID); got.RetiredAt == nil || got.RevokedAt != nil {
		t.Fatalf("换下只设 retired_at：%+v", got)
	}

	// 撤销：换下过的还能撤销，撤销时间记上
	if changed, err := st.RevokeTrustedSigningKey(ctx, k.KeyID, at.Add(2*time.Minute)); err != nil || !changed {
		t.Fatalf("撤销：%v %v", changed, err)
	}
	if changed, err := st.RevokeTrustedSigningKey(ctx, k.KeyID, at.Add(3*time.Minute)); err != nil || changed {
		t.Fatalf("重复撤销应幂等：%v %v", changed, err)
	}
	// 撤销之后不能再换下
	if changed, err := st.RetireTrustedSigningKey(ctx, k.KeyID, at); err != nil || changed {
		t.Fatalf("撤销后不应再换下：%v %v", changed, err)
	}
	if got, _ := st.TrustedSigningKey(ctx, k.KeyID); got.RevokedAt == nil || got.RetiredAt == nil {
		t.Fatalf("撤销保留换下时间：%+v", got)
	}

	// 列表包括历史记录
	list, err := st.ListTrustedSigningKeys(ctx)
	if err != nil || len(list) != 1 || list[0].KeyID != k.KeyID || list[0].RevokedAt == nil {
		t.Fatalf("列表：%+v %v", list, err)
	}
}
