---
slug: cloud-runtime
category: harness
title: "云端常驻机器人试点"
title_en: "Cloud resident bot pilot"
summary: "用 OpenSandbox 定时执行只读 DSH 任务、保存文件产物，并通过飞书私聊交付。"
summary_en: "Run scheduled read-only DSH tasks in OpenSandbox, save artifacts, and deliver results in Feishu."
tags: [DSH, OpenSandbox, 飞书]
updated_at: 2026-11-22
status: draft
since: 2026-11-22
lang: zh-CN
---

## 开启试点

dsh-ee 保存机器人定义、队列、状态事件和文件；执行在 OpenSandbox 的独立短期沙箱中。本机 DSH 可以关机。默认不开启，也不改变已有飞书/钉钉的本机聊天中继。

| 环境变量 | 说明 |
| --- | --- |
| `DSH_EE_RUNTIME_PROVIDER` | `opensandbox`；空值或 `off` 关闭 |
| `DSH_EE_RUNTIME_URL` | OpenSandbox 生命周期 API 的服务根地址 |
| `DSH_EE_RUNTIME_API_KEY` | 独立的 OpenSandbox API key，不是员工令牌 |
| `DSH_EE_RUNTIME_IMAGE` | 预构建的可信 Linux 镜像，建议按 digest 固定。应包含 DSH CLI（如 `@deepseek-ai/dsh@0.2.2-rc.5`，自带同版本 MCP 客户端）和 Node.js 24.9.0 或以上 |
| `DSH_EE_RUNTIME_GATEWAY_URL` | 沙箱内可访问的 dsh-ee origin；生产用 HTTPS。执行器在此调用 `/v1/chat/completions` 和 `/mcp` |
| `DSH_EE_RUNTIME_MODEL` | 网关对外模型名，员工本身也须有使用权限 |
| `DSH_EE_RUNTIME_COMMAND` | 可选 JSON argv，默认 `["dsh","--profile","headless","--patch","{patch}","--json","{prompt}"]`，支持独立参数占位符 `{patch}`、`{prompt}`、`{output}`。DSH 启动参数 `--patch` 必须放在 `--json` 之前；带 `--json` 时，最近运行只显示最终回复；自定义纯文本命令应只将摘要写到 stdout |

部署时让 OpenSandbox 强制执行出站策略（Docker bridge、`dns+nft`，或等效的网络隔离），只放行沙箱到企业网关；不要把 sandbox/execd 端口公开。使用专用网关 origin，不与其他服务或同主机的其他开放端口混用。运行前在目标镜像验证 headless profile 能加载配置和 MCP 客户端。

用仓库内的 Dockerfile 构建镜像：`docker build -t dsh-ee-runtime:pilot server/internal/runtime`。将镜像推到 OpenSandbox 能访问的企业仓库，或加载到单机 OpenSandbox 的 Docker Engine。

管理端 **系统设置 → 云端常驻机器人** 中创建机器人：选择有网关权限的非管理员员工（填写 ID），配置任务提示词和 UTC 每日 cron（例如 `0 1 * * *` 是北京时间每天 09:00），打开定时运行。也可立即运行并在下方刷新状态、下载文件。调度仅在服务在线时检查当前分钟，不补跑错过的时间。运行身份的月度 token 额度必须是不限（留空）或大于 0 且本月未用完：新建用户的额度默认是 0（未分配），网关会对它返回 429 `monthly_quota_exceeded`。任务开始时、分配沙箱和短期凭证之前，服务按与网关相同的规则检查该额度；未分配或已用完会直接以说明原因的终态失败结束，不重试、不创建沙箱（读库出错仍会重试）。这只是提前提示，不预留额度，网关仍会逐请求校验。测试飞书交付前，需配置飞书通知应用、员工飞书身份绑定及员工能打开的 dsh-ee 公网访问地址。

## 任务 API

登录后可用 `POST /api/runtime/tasks` 提交 `{"prompt":"生成今日摘要","idempotency_key":"daily-2026-11-22","timeout_seconds":120}`。`GET /api/runtime/tasks` 列出最近任务；`GET /api/runtime/tasks/{id}` 查状态，`GET /api/runtime/tasks/{id}/events` 查状态变更，`POST /api/runtime/tasks/{id}/cancel` 取消。用 `GET /api/runtime/tasks/{id}/artifacts` 查文件，`GET /api/runtime/tasks/{id}/artifacts/{artifact}` 下载。员工只能查看自己的任务。有系统设置权限的管理员可以查看普通任务，但网页对话和使用常驻电脑的任务结果（含回复、错误详情及产物）只对所有者开放。

任务最长 5 分钟，单次运行的令牌只在该员工权限范围内、只对指定模型的网关请求和只读 MCP 调用生效，并在退出时吊销。临时电脑的工作目录和产物目录都是 `/tmp/dsh-ee-output`；常驻电脑的产物目录是工作目录下的 `output/`。DSH 的 shell 工具在 bwrap 下会把 `/tmp` 换成临时文件系统，只把工作区根挂回真实目录，所以产物目录必须在工作区内。

只收集产物目录及其两层以内子目录里的普通文件（不含隐藏文件和隐藏目录；最多 8 个、每个不超过 8 MiB），子目录里的文件以相对路径命名，例如 `reports/周报.md`。超限或无效的文件会记入服务端日志。文件在 `DSH_EE_DATA_DIR/runtime-artifacts`。网页对话（`/bot-chat`）右侧的「文件库」列出这个机器人在对话和定时任务里生成的文件，可以预览和下载，只有机器人的所属员工本人看得到。队列支持多个实例抢占，但多实例必须共享该产物目录；试点未实现对象存储、自动保留期限、Feishu 重试、补跑以及持久 Agent 记忆。不支持写工具或 E2B。

## 每个机器人一台常驻云电脑

OpenSandbox 下，每个机器人有自己的一台云电脑（一个常驻沙箱）：机器人的定时 / 手动任务和网页对话都在这台电脑里跑。一轮结束后电脑保持开机；空闲超过闲置超时后自动休眠（OpenSandbox `POST /sandboxes/{id}/pause`），下一次对话或定时任务时唤醒（`resume`）。工作目录 `/tmp/dsh-ee-work`（DSH 的当前目录）和 DSH 主目录 `/tmp/dsh-ee-home` 里的文件都会保留，工作目录下的 `output/` 除外：其中的文件收成这一轮的产物，不带入下轮。常驻沙箱创建时不带 `timeout`，由 dsh-ee 自己负责休眠和回收。

凭证不跟着电脑走：电脑创建时不带任何环境变量，每轮仍签发新的短期令牌，只通过这一轮执行命令的环境变量传入，这一轮结束、释放电脑之前就吊销。若吊销失败，则等令牌失效后才能再次使用或休眠电脑。

创建电脑前先保存创建标识；创建响应丢失或进程退出后，下一轮或空闲回收器会用它找回沙箱。更换网关主机名时，OpenSandbox 会在下一轮执行前替换该电脑的出站白名单，保留文件且不再放行旧地址。升级前请确认目标版本支持 `PUT /sandboxes/{id}/networkpolicy`；更新失败时任务不会签发新凭证，也不会删除原电脑。

同一台电脑同一时间只给一个任务或一次操作用（数据库租约，多实例也成立）。电脑正被占用时，每次尝试最多等 90 秒，仍被占用就按任务重试规则处理，不会改用没有原有文件的临时电脑。不属于任何机器人的任务（`POST /api/runtime/tasks`）一直使用临时电脑。电脑在沙箱服务那边已经不存在时（被外部删除或过期），下一轮自动新建一台，原来的文件就没有了。

| 环境变量 | 说明 |
| --- | --- |
| `DSH_EE_RUNTIME_PERSISTENT` | `on`（默认）或 `off`。`off` 时所有任务都每轮新建、跑完销毁；以前留下的电脑空闲后仍会被休眠和按保留期回收 |
| `DSH_EE_RUNTIME_COMPUTER_IDLE` | 闲置超时，Go 时长格式，`1m` 到 `24h`，默认 `15m` |
| `DSH_EE_RUNTIME_COMPUTER_DISK_MB` | 每台电脑工作目录加 DSH 主目录的磁盘用量阈值（MB），默认 `2048`。每轮结束时用 `du` 统计；超过后新任务直接失败（不重试），重置电脑后恢复。不是文件系统写入的硬上限 |
| `DSH_EE_RUNTIME_COMPUTER_NATIVE_DISK_QUOTA` | `on` 时在创建电脑时也向 OpenSandbox 请求上述大小的原生磁盘配额，`off`（默认）时只检查用量阈值。需要支持 `resourceLimits.disk` 的 OpenSandbox 及宿主配置（例如支持可写层配额的 Docker 存储驱动）；Kubernetes 对应临时存储限额，超量可能驱逐 Pod。启用或更改配额后，既有电脑需重新创建才生效 |
| `DSH_EE_RUNTIME_COMPUTER_RETENTION` | 休眠多久后自动回收（删除整台电脑），默认 `720h`（30 天），`0` 表示不自动回收 |

员工在 **机器人对话** 右侧面板的 **动态 → 云端电脑** 里看到电脑的真实状态（工作中、唤醒中、运行中、正在休眠、休眠中、未开机）和磁盘用量，可以手动 **休眠** 或 **重置**（删除整台电脑和其中的文件，已经收进文件库的产物不受影响）。对应接口是 `POST /api/runtime/chat/bots/{id}/computer/hibernate` 和 `.../computer/reset`，只对机器人的所有者本人开放。管理员在 **系统设置 → 云端常驻机器人** 下方分页看到电脑的状态、占用和磁盘用量，可以休眠或回收：`GET /api/admin/runtime/computers`（若有 `next_before`，用 `?before=<next_before>` 取下一页）、`POST /api/admin/runtime/computers/{id}/hibernate`、`DELETE /api/admin/runtime/computers/{id}`。电脑正在跑任务时手动操作返回 409 `runtime_computer.busy`。

Provider 接口：`runtime.PersistentProvider` 在 `Provider` 之外加了 `CreatePersistent`、`Wake`、`CheckPersistent`、`Hibernate`、`Remove`。没有实现它的 provider（或 `DSH_EE_RUNTIME_PERSISTENT=off`）退回每轮新建；员工面板显示“每轮新开”，手动操作返回 409 `runtime_computer.unsupported`。关闭常驻但仍使用支持常驻的 provider 时，管理员仍可休眠或回收已有电脑。

OpenSandbox 的暂停 / 恢复取决于它的运行时后端。上线前先在目标部署上验证：创建一个沙箱写入文件，`pause` 后状态变为 `Paused`，`resume` 后文件还在。暂停失败时电脑保持开机并在下一个闲置周期重试，日志里会有“处理空闲机器人电脑失败”。
