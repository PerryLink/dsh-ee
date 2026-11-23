---
slug: bot-approval
category: harness
title: "云端机器人的写操作审批"
title_en: "Write approval for cloud bots"
summary: "每个云端机器人的写操作审批模式：只读、逐个审批（对话里的审批卡）、按规则自动审批、完全放行，以及每次写操作的审计记录。"
summary_en: "Per-bot write approval for cloud bots: read-only, approve each write with an in-chat card, rule-based auto-approval, or full access, with an audit entry for every write."
tags: [DSH, OpenSandbox, 审批, 审计]
updated_at: 2026-11-22
status: draft
since: 2026-11-22
lang: zh-CN
---

## 四种模式

云端常驻机器人（[cloud-runtime.md](./cloud-runtime.md)）默认只读。管理员在 **系统设置 → 云端常驻机器人** 里给每个机器人选一个写操作审批模式：

| 模式 | 取值 | 行为 |
| --- | --- | --- |
| 只读 | `read_only` | 默认值，与旧行为一致。写工具不下发给沙箱，调用一律拦下。 |
| 逐个审批 | `manual` | 机器人每次要做写操作，员工在对话里看到一张审批卡（企业系统、操作、参数），点「批准」才执行，点「拒绝」不执行。 |
| 自动审批 | `auto` | 命中自动审批规则的写操作直接执行；其余照逐个审批出卡。 |
| 完全放行 | `full` | 不再审批，写操作直接执行。开启时必须在弹窗里勾选并确认风险；对话页和管理端持续显示红色「完全放行」标记。 |

管理员还可以勾选「允许员工在对话页自己切换审批模式」。允许后，机器人所有者本人可以在 `/bot-chat` 顶部切换模式（切到完全放行同样要确认风险）；自动审批规则始终只由管理员设置。

## 自动审批规则

每行一条通配模式，匹配工具全名 `<连接>__<工具>`（与 `/mcp` 上的工具名一致），`*` 匹配任意字符：

- `wiki__create_page`：一个工具；
- `wiki__*`：一个连接的全部写操作；
- `*__create_*`：各系统的「新建」类操作。

单独的 `*`（或只由 `*`、`?`、`_` 组成的模式）会被拒绝：要全部放行请用完全放行模式，它有风险确认和持续标记。规则最多 50 条。

## 审批卡怎么工作

逐个审批复用写操作确认（[plugins.md §7.5](https://github.com/hikariming/dsh-ee/blob/main/docs/plugins.md#75-写操作要员工确认)，`write_confirmations` 表）：

1. 沙箱里的 DSH 调 `/mcp` 的写工具。服务端按这一轮的短期凭证找回任务和机器人，现读审批模式（管理员改成只读立即生效）。
2. 需要审批时建一条带 `runtime_task_id` 的待确认记录，这次 `/mcp` 调用停在服务端等待，最长 15 分钟且不超过这一轮任务的剩余时间。
3. 对话页在这一轮「正在回复」时轮询 `GET /api/me/confirmations`，把带同一任务 ID 的记录显示成审批卡；员工用 `POST /api/me/confirmations/{id}` `{"approve":true|false}` 决定（同一接口也服务于 `/confirm/{id}` 确认页）。
4. 批准后服务端用掉这次确认并执行，机器人拿到结果接着回复；拒绝、过期或这一轮被取消都不执行，模型收到「不要重试、告诉员工」的说明。等不到决定时确认立即过期，之后再点批准也不会执行。

定时运行（没有对话页）的机器人在逐个审批模式下，审批卡出现在员工的 `/confirm` 确认页；任务最长 5 分钟，来不及批准的写操作不执行。

## 审计

每次写操作都写一条审计，目标是 `connection:<连接>`，操作人是运行身份（机器人所有者本人），带上任务和机器人：

| 动作 | 何时 | 关键字段 |
| --- | --- | --- |
| `runtime.write` | 写操作被执行 | `mode`、`approved_by`（`user` / `rule` / `full`）、`approver_id`（人工批准时）、`rule`（规则自动批准时）、`confirmation_id`、`tool`、`args_sha256`、`outcome`（`ok` / `error`） |
| `runtime.write.blocked` | 没有执行 | `mode`、`reason`（`read_only` / `rejected` / `not_approved_in_time` / `args_too_large` / `approval_unusable` / `expired`） |
| `write.confirm` | 员工在审批卡上点了批准或拒绝 | `approve`、`tool`、`args_sha256` |
| `runtime.bot.approval` | 审批模式被修改 | `from`、`to`、`by`（`admin` / `owner`）、`risk_confirmed` |

管理端保存机器人时的 `runtime.bot.save` 也带上 `approval_mode`、`approval_rules` 和 `approval_employee_editable`。

## 凭证边界

写操作不签发新的、更大的凭证。沙箱里始终只有这一轮的短期令牌：只有 `models`、`connections` 范围，随任务超时过期、任务结束即吊销。写操作由 dsh-ee 服务端按员工本人的连接权限代为执行，连接凭据、共享凭据和管理员凭据都不进沙箱。审批模式只决定服务端放不放行，不改变令牌本身的权限。

## 接口

| 接口 | 说明 |
| --- | --- |
| `POST/PUT /api/admin/runtime/bots[/{id}]` | 多了 `approval_mode`、`approval_rules`、`approval_employee_editable`；从其他模式切到 `full` 时必须带 `"confirm_full_access": true`，否则 400 `runtime_approval.risk_unconfirmed` |
| `GET /api/runtime/chat/bots` | 每个机器人多了 `approval_mode` 和 `approval_editable` |
| `PUT /api/runtime/chat/bots/{id}/approval` | 所有者本人切换模式 `{"approval_mode":"manual","confirm_full_access":false}`；管理员没允许时 403 `runtime_approval.locked` |
| `GET /api/me/confirmations` | 云端机器人发起的确认带 `runtime_task_id` |

错误码：`runtime_approval.invalid_mode`、`runtime_approval.invalid_rules`、`runtime_approval.risk_unconfirmed`、`runtime_approval.locked`。
