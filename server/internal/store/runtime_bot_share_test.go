package store

import (
	"context"
	"errors"
	"testing"
)

// 机器人替员工新建的机器人（runtime_bot_origins 里有记录）不能由所有者共享给部门（只读审计 C2）：
// 它的提示词由机器人 / 模型控制，共享后同部门同事会以本人权限执行所有者写的提示词，超出「共享不放大权限」的边界。
// 管理员共享、取消共享、普通机器人都不受影响。
func TestRuntimeBotShareExcludesBotCreatedBots(t *testing.T) {
	st := openTokenTestStore(t)
	ctx := context.Background()
	owner := tokenTestUser(t, st, false)
	department := owner.Department
	webChat := true

	// 普通机器人：所有者以本人身份运行、只读、开着网页对话，满足共享条件。
	normal := &RuntimeBot{OwnerID: owner.ID, RunAsID: owner.ID, Name: "normal", Prompt: "persona", Schedule: "0 9 * * *",
		ReadOnly: true, DeliveryTarget: "feishu_owner"}
	if err := st.SaveRuntimeBot(ctx, normal, &webChat); err != nil {
		t.Fatal(err)
	}
	// 机器人替所有者新建的机器人：CreateRuntimeBotByBot 会同时写 runtime_bot_origins。
	created := &RuntimeBot{OwnerID: owner.ID, Name: "created", Prompt: "prompt from a bot", Schedule: "0 9 * * *"}
	if err := st.CreateRuntimeBotByBot(ctx, created, normal.ID, 1); err != nil {
		t.Fatal(err)
	}

	// 普通机器人：所有者共享成功。
	previous, err := st.SetRuntimeBotShare(ctx, normal.ID, owner.ID, department)
	if err != nil || previous != "" {
		t.Fatalf("share a regular bot: previous=%q err=%v", previous, err)
	}
	// 自建机器人：所有者共享被拒（沿用现有错误语义 ErrNotFound），库里也不会写入共享部门。
	if previous, err = st.SetRuntimeBotShare(ctx, created.ID, owner.ID, department); !errors.Is(err, ErrNotFound) || previous != "" {
		t.Fatalf("owner sharing a bot created by a bot must be rejected: previous=%q err=%v", previous, err)
	}
	if bot, err := st.RuntimeBot(ctx, created.ID); err != nil || bot.SharedDepartment != "" {
		t.Fatalf("rejected share must not change the bot: %+v, %v", bot, err)
	}
	// 管理员（ownerID=0）不受影响：可以把自建机器人共享给部门。
	if previous, err = st.SetRuntimeBotShare(ctx, created.ID, 0, department); err != nil || previous != "" {
		t.Fatalf("admin share a bot created by a bot: previous=%q err=%v", previous, err)
	}
	// 取消共享不受影响：所有者仍能把自己（管理员共享出去的）自建机器人收回。
	if previous, err = st.SetRuntimeBotShare(ctx, created.ID, owner.ID, ""); err != nil || previous != department {
		t.Fatalf("owner unshare a bot created by a bot: previous=%q err=%v", previous, err)
	}
	// 普通机器人也能取消共享。
	if previous, err = st.SetRuntimeBotShare(ctx, normal.ID, owner.ID, ""); err != nil || previous != department {
		t.Fatalf("owner unshare a regular bot: previous=%q err=%v", previous, err)
	}
}
