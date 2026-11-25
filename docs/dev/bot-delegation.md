---
slug: bot-delegation
category: harness
title: "员工电脑与机器人协作"
title_en: "Employee computers and bot delegation"
summary: "一个员工一台云电脑，他的机器人共用工作目录；机器人之间可以派任务、取回结果、新建助手，带防循环上限和完整审计。"
summary_en: "One cloud computer per employee shared by their bots; bots can delegate tasks to each other, get results back and create helpers, with loop limits and full audit."
tags: [云端机器人, OpenSandbox, 机器人对话, 派任务, 审计]
updated_at: 2026-11-23
status: draft
since: 2026-11-22
lang: zh-CN
---

## 一个员工一台电脑

[云端常驻机器人](/docs/cloud-runtime)的常驻云电脑按**所有者**分配，不再是每个机器人一台：

| 所有者 | 谁用 | 面板上的名字 |
| --- | --- | --- |
| 员工（`owner_kind=user`） | 这位员工的所有个人机器人：定时 / 手动任务和他本人的网页对话 | 我的电脑 |
| 团队（`owner_kind=team`，部门） | 共享给这个部门的团队对话；不放在任何个人的电脑上 | 团队电脑 |

电脑里的目录：

| 路径 | 用途 |
| --- | --- |
| `/tmp/dsh-ee-work/bots/<机器人 ID>` | 每个机器人自己的工作目录，也是它运行时的当前目录 |
| `/tmp/dsh-ee-work/shared` | 同一台电脑上所有机器人都能读写的共享目录：机器人 A 写的文件，机器人 B 在这里读到 |
| `/tmp/dsh-ee-home/bots/<机器人 ID>` | 每个机器人自己的 DSH 主目录（会话、缓存），互不干扰 |
| `/tmp/dsh-ee-work/bots/<机器人 ID>/output` | 这一轮的产物（相对路径 `output/`），每轮开始时清空，结束时收进文件库并从电脑上删掉 |

休眠 / 唤醒、数据库租约、每轮短期凭证、磁盘上限和管理员回收都和以前一样，只是作用在所有者的电脑上。员工在「动态 → 我的电脑」里手动休眠或重置时，影响他的所有机器人；管理员在「系统设置 → 云端常驻机器人」看到每台员工电脑和团队电脑。

### 并发：排队，不同时执行

同一台电脑同一时间只跑一轮，其余排队（沿用数据库租约）。我们没有选择让多个 run 同时 exec，原因是：

- **凭证**：每轮签一张短期令牌，只放在这一轮命令的环境变量里，这一轮结束、释放电脑之前吊销。团队电脑上相邻两轮可能是两个人（每轮都以发消息的人运行），如果同时执行，一个人的令牌会和另一个人的进程同在一台机器上。排队后电脑里任何时候最多只有一个人的令牌，下一轮（不论是谁）开始时上一轮的令牌已经失效。
- **文件**：共享目录不会被两轮同时写；休眠、重置、回收的判断不用改。

代价是同一位员工的机器人一个接一个地跑。每位员工排队或运行中的云任务上限仍是 5 个。

### 从「每个机器人一台」迁移

迁移 `0060_runtime_owner_computers` 把 `runtime_bot_computers` 的记录合并成每个员工一行（保留的那台带上 0057 记下的网关主机名）：

- 每位员工保留**最近使用**的那台电脑（`last_used_at` 最大，并列时机器人 ID 大的），原来的文件留在 `/tmp/dsh-ee-work` 根目录下，这位员工的所有机器人都能读到；
- 其余几台记进 `runtime_computer_orphans`，回收器在下一次巡检时删掉它们（里面的文件随之删除，审计 `runtime.computer.reclaim`，`reason=merged_per_owner`）；新建到一半中断、只有 `creation_key` 的记录也进回收队列，回收器先按 `creation_key` 向 OpenSandbox 找到沙箱再删，找不到就是从没建成；
- 还没有电脑的记录直接丢弃；所有租约清空。

上线前如果某台要被合并掉的电脑里有重要文件，请先让对应机器人把文件写进产物目录收走，或者在迁移前手动复制。迁移前须停止仍在使用旧表的实例、等待运行中的任务结束：`0061_drop_legacy_runtime_bot_computers` 会删除旧表。

## 机器人之间派任务

云端机器人在一轮里通过 `/mcp` 上的 `bots__*` 工具和同一位员工的其他机器人协作。认证用的就是这一轮的短期令牌，服务端按令牌找回是哪一轮、哪个机器人在调用（`runtime_tasks.credential_token_id`）。

| 工具 | 作用 |
| --- | --- |
| `bots__list` | 同一位员工、现在可以对话的其他机器人 |
| `bots__send` | 给其中一个派任务（`kind: "task"`，有结果后交回发起方）或发消息（`kind: "message"`，不追加回合） |
| `bots__result` | 查一次委派的状态和结果 |
| `bots__create` | 替员工新建一个机器人（例如临时助手），只在非只读模式下提供，按调用方的写操作审批模式放行 |

### 一次委派怎么走

1. 机器人 A 调 `bots__send`，服务端只记下一条 `pending`。A 应该把需要的文件放进 `/tmp/dsh-ee-work/shared`，然后结束这一轮。
2. A 这一轮**成功结束、释放电脑之后**，服务端在机器人 B 的对话里入队一轮（就像员工给 B 发了一条消息，界面上标着「来自 A 的委派」）。之所以等 A 结束，是因为 A 和 B 在同一台电脑上排队：A 如果占着电脑等 B，B 永远拿不到电脑。A 这一轮失败或要重试时，它记下的委派会被取消。
3. B 在同一台电脑上读到共享目录里的文件，完成后它的回复就是结果。
4. A 这一轮派出的 `task` 委派全部有结果（成功、失败或没派出去）后，服务端在 A 的对话里追加一轮，把结果交给 A 接着处理。这一轮在界面上显示成「委派的结果回来了」卡片，不显示成员工说的话。
5. A 的对话里，派出的那一轮下面有委派卡片：派给谁、状态、结果，点「打开 B 的对话」跳过去。

定时 / 手动任务里派出的委派没有对话可以交回，结果留在 B 的对话、卡片和 `bots__result` 里。

### 权限边界

- 只能派给同一位员工本人、以他身份运行、开着网页对话的机器人；别人的机器人一律当作不存在（`bots__list` 看不到，`bots__send` 被拒并审计 `runtime.delegation.reject`）。
- 目标那一轮以同一位员工运行，同样只读、同样受他的模型额度和 5 个排队 / 运行上限约束；队列满时委派记成失败。
- 团队对话的轮次现在不能派任务；团队共享上线后再开放团队内部的委派，同样不能跨团队。
- 只有云端机器人这一轮的短期令牌能用 `bots__*`，员工自己的接入令牌和第三方客户端拿不到这些工具。

### 防循环

| 规则 | 上限 | 超出时 |
| --- | --- | --- |
| 派任务的层数（员工的一条消息或一次定时运行是第 0 层） | 3 层 | 拒绝，`reason=depth` |
| 一轮最多派出 | 3 个 | 拒绝，`reason=fanout` |
| 同一个根引出的整棵委派树（包括结果回来后的追加轮） | 8 个 | 拒绝，`reason=total` |
| 目标已经在这条链上（包括派给自己、派回给派给自己的机器人） | — | 拒绝，`reason=cycle` |
| 同一轮给同一个机器人两个待派任务 | — | 拒绝，`reason=duplicate` |

被截断时写审计 `runtime.delegation.truncate`，工具结果会告诉机器人改为自己完成或回报员工。

### 新建机器人

`bots__create` 按[写操作审批模式](./bot-approval.md)放行：只读拒绝，完全放行直接执行，自动审批只在命中规则时直接执行。需要逐个审批时，对话里出现审批卡，工具也会返回 `/confirm/{id}` 链接；员工点同意后，机器人用相同参数再调一次，一次同意只能建一次。新机器人归同一位员工、以他身份运行、只读、开着网页对话、不开定时，和他的其他机器人共用一台电脑。机器人替一位员工建的机器人（仍然存在的）最多 5 个，到上限时直接拒绝，不再请员工确认。

机器人替员工新建的机器人不能由所有者共享给部门（见[团队共享机器人](./bot-team-sharing.md)）：要共享给团队，由管理员另行处理。

## 审计

| 动作 | 记录 |
| --- | --- |
| `runtime.delegation.create` | 谁派给谁：`from_bot`、`to_bot`、`run`（发起的那一轮）、`kind`、`delegation`、`depth`、`root_run` |
| `runtime.delegation.reject` / `.truncate` | 被拒或被防循环截断：`reason` |
| `runtime.delegation.dispatch` | 在目标对话里入队：`parent_run`、`run`（目标那一轮）、`conversation` |
| `runtime.delegation.finish` | 目标那一轮结束：`status` |
| `runtime.delegation.fail` / `.cancel` | 没派出去（目标忙、队列满、不可用）或发起轮失败 |
| `runtime.delegation.followup` / `.followup_skip` | 结果交回发起方的那一轮，或没能追加的原因 |
| `runtime.bot.create_request` / `.create_by_bot` / `.create_reject` | 新建机器人的请求、建成、因上限被拒 |
| `runtime.computer.*` | 电脑的休眠、重置、回收，目标写成 `computer:<id>:user:<员工 ID>` 或 `computer:<id>:team:<部门>` |

## 接口

| 接口 | 说明 |
| --- | --- |
| `GET /api/runtime/chat/conversations/{id}` | 多了 `delegations`：这一页轮次相关的委派（`parent_task_id` / `child_task_id` / `followup_task_id` 指向哪一轮） |
| `GET /api/runtime/chat/bots/{id}/profile` | `computer` 多了 `owner_kind`、`department`、`shared_bots` |
| `GET /api/admin/runtime/computers` | 每项是一台电脑：`id`、`owner_kind`、`owner_id` / `owner_name` 或 `department`、最近使用的 `bot_id` / `bot_name` |
| `POST /api/admin/runtime/computers/{id}/hibernate`、`DELETE /api/admin/runtime/computers/{id}` | `{id}` 现在是电脑 ID（以前是机器人 ID） |
