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

登录后可用 `POST /api/runtime/tasks` 提交 `{"prompt":"生成今日摘要","idempotency_key":"daily-2026-11-22","timeout_seconds":120}`。`GET /api/runtime/tasks` 列出最近任务；`GET /api/runtime/tasks/{id}` 查状态，`GET /api/runtime/tasks/{id}/events` 查状态变更，`POST /api/runtime/tasks/{id}/cancel` 取消。用 `GET /api/runtime/tasks/{id}/artifacts` 查文件，`GET /api/runtime/tasks/{id}/artifacts/{artifact}` 下载。员工只能查看自己的任务；有系统设置权限的管理员可以查看全部。

任务最长 5 分钟，单次运行的令牌只在该员工权限范围内、只对指定模型的网关请求和只读 MCP 调用生效，并在退出时吊销。只收集直接写入 `/tmp/dsh-ee-output` 的普通文件（最多 8 个、每个不超过 8 MiB）；文件在 `DSH_EE_DATA_DIR/runtime-artifacts`。队列支持多个实例抢占，但部署多个实例必须共享该产物目录；试点未实现对象存储、自动保留期限、Feishu 重试、补跑以及持久 Agent 记忆。不支持写工具、E2B 或在同一沙箱内保留下一次运行的状态。
