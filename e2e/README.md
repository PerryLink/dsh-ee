# 浏览器 E2E

```bash
cd e2e
npm ci

# 连一个已经启动好的 dsh-ee（旧用法，适合本地调试）
ADMIN_PW=e2e-admin-password-1 \
E2E_BASE=http://127.0.0.1:8080 \
CHROME=/usr/bin/google-chrome \
node run.mjs

# 只跑一个测试
E2E_BASE=... ADMIN_PW=... node run.mjs 10-tokens
```

## 隔离模式

CI 用隔离模式：每个测试文件启动一个全新的 `dsh-ee`、独立数据库、独立数据目录，不再依赖测试顺序。

需要：

```bash
E2E_MANAGED=1
E2E_BIN=/path/to/dsh-ee
E2E_DSN='root:@tcp(127.0.0.1:4000)/'
ADMIN_PW=...
```

`E2E_BASE` 决定监听端口（默认 `http://127.0.0.1:8080`）。测试结束后会停止服务端并删除临时数据目录；如果本机有 `mysql` 客户端，也会 drop 测试库（`E2E_KEEP_DB=1` 可保留）。

## 飞书机器人对话本地演示（40）

`40-feishu-bot-chat.mjs` 不需要真实飞书租户或 DSH 二进制。它在本机启动带 OIDC、通讯录和回复捕获的假飞书，浏览器操作管理开关、飞书登录与聊天预览；通过 `tsx` 加载真正的 `FeishuChatRelay`、`HttpClient`、`McpBridge` 与 `registerApproval`。只有 DSH 宿主和模型是确定性模拟，模型经员工的 DSH 类型令牌、真实审批钩子与桥接器调用 `/mcp` 的 `ee_feishu`，不关闭服务端写操作确认，也不直接伪造聊天回复。

```bash
# 仓库根目录；也可按构建文档使用已嵌入最新前端的本地二进制
make build
cd e2e && npm ci
E2E_MANAGED=1 E2E_BIN="$PWD/../dist/dsh-ee" \
  E2E_DSN='root:@tcp(127.0.0.1:4000)/' \
  ADMIN_PW=e2e-admin-password-1 CHROME=/path/to/chrome \
  E2E_RECORD=1 node run.mjs 40-feishu-bot-chat
```

依赖运行中的 TiDB、`dsh-plugin` 的开发依赖（`make build` 已安装，单独构建服务端时先运行 `cd dsh-plugin && npm ci`），以及能显示中文的字体。首次录屏前，在 `e2e/` 下运行 `node node_modules/playwright-core/cli.js install ffmpeg` 安装 Playwright 视频编码器。测试会真实等待约 60 秒，让停止轮询后的在线状态自然过期，不改服务端时钟或数据库。

默认输出位于 `e2e/shots/`（不入库）：三张 `feishu-bot-*.png`、`feishu-bot-video/*.webm` 与 `feishu-bot-recording.json`。JSON 记录原片路径和可在演示 GIF 中跳过的在线状态等待时间；原片保留完整等待。`E2E_SHOTS` / `E2E_RECORD_DIR` 可覆盖目录。预览页明确标注「本地验证」，这些产物不是生产飞书租户或真实 DSH / 模型的验收记录。
