# capabilities / commitments / labels：作者声明面（可机器核的部分）

> 上游是 **权限与标注规范**（上游版本 `v1.0.0-rc.1`，日期 2026-10-31，CC BY 4.0）。
> 本页面向 dsh-ee 插件作者，说明 `package.json` 里 `dsh.capabilities`、`dsh.commitments`、`dsh.labels` 三个声明面：怎么写、checker 怎么读、哪些只是声明（不能当门控）。

```text
Source: https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/60-权限与标注规范.md@19c410bd261d942c360386334d4841543a646579
License: CC BY 4.0 (https://creativecommons.org/licenses/by/4.0/)
© 2026 PerryLink
Changes:
- 精简为作者声明面摘要：保留三类划分、`dsh.capabilities` 15 个 ID、`dsh.commitments` 9 条、`dsh.labels` 5 类与禁用标签、字段/检查/结果上限映射
- 删除上游内部研究引用（20/01/10/41/70/43 等）、宿主源码路径与行号、检查器 A1–A14 逐条编号、星标/市场收录等第三方元数据
- 删除安装期完整提示模板、免责声明模板与“下一步建议”；只保留与声明面直接相关的短提示原则
- 新增「怎么落到 package.json」「dsh-ee 边界」两节（dsh-ee 侧新增文字，非上游）
- 字段示例（JSONC）来自上游规范文本（CC BY 4.0），只说明字段形状；未复制任何 Apache-2.0 代码、脚本或 schema
- 保留上游版本号与日期；字段规则以上游固定 commit 为准
Date: 2026-11-12
Modified version: dsh-ee docs
非官方·非认证·非安全审计 / Not official, not a certification, not a security audit.
```

## 一、先分清三类声明：A 可机械验证、B 只能自述、C 强制标注

DSH 的 `ctx.*` seam 不是权限边界：宿主源码自述 fs-sandbox 的 fence 是 trusted code 里的策略检查、不是内核边界；插件管理器工具在 workspace 沙箱之外运行；扩展 runner 的 sandbox “不是 containment”。所以「声明面」不能当成权限门控，也不能互相替代：

| 类 | 字段 | 能做什么 | 不能做什么 |
|---|---|---|---|
| A. 可机械验证 | `dsh.capabilities` | 装载期声明一致性检查、UI 披露、市场筛选 | 运行时门控、阻止 `import node:fs` |
| B. 只能声明 + 背书 | `dsh.commitments` | 让作者对 9 条行为作出可引用的承诺 | 证明承诺为真 |
| C. 强制标注 | `dsh.labels` | 把高风险类别显式化、安装期强提示 | 检测隐藏行为 |

一句话：**A 是“你用了什么”，B 是“你承诺不做什么”，C 是“你属于哪类高风险”。** 三者都写进 manifest，但只有 A 能被 L0 机械复查。

> **重要现状**：这三个字段是本标准扩展，上游固定 commit 的宿主**不读取**它们；checker 与市场可以读，宿主采纳前只做披露、不做门控。安装期提示由读取 manifest 的市场/客户端渲染，不是宿主权限系统。不要对用户宣称“声明了就等于被限制”。

## 二、`dsh.capabilities`（A 类）

### 2.1 字段写法

```jsonc
{
  "dsh": {
    "manifestVersion": 1,
    "capabilitiesVersion": 1,
    "capabilities": ["tools:register", "network:egress", "sessions:read"]
  }
}
```

- `capabilitiesVersion`：当前只允许 `1`；缺失按 `1` 处理（过渡期）。
- `capabilities`：字符串数组，ID 必须来自下面闭集；未知 ID → C-11.1 FAIL。
- 数组不得重复；**空数组表示“明确不声明任何能力”**（C-11.1 WARN，不是 PASS）。
- 字段缺失：C-11 整体 WARN；字段存在但漏报高信号触发 → FAIL。
- 声明了但静态未观测到：WARN（不 FAIL，避免动态调用误杀）。

### 2.2 能力 ID 闭集（15 个）

| ID | 静态触发信号 | 说明 |
|---|---|---|
| `fs:read` | `ctx.fs` 读方法 | 读文件 |
| `fs:write` | `ctx.fs` 写方法 | 写文件 |
| `shell` | `ctx.shell` / `ctx.bash` | 执行 shell |
| `subprocess` | `ctx.subprocess` | 子进程 / PTY |
| `network:egress` | `ctx.web` / 裸 `fetch` | 出站网络 |
| `network:ingress` | `ctx.webServer` / `ctx.webhookRuntime` | 入站路由 / webhook |
| `credentials:read` | `ctx.credentials.resolve` | 读单个凭据 |
| `credentials:list` | `ctx.credentials.listRecords` | 枚举全部凭据 |
| `credentials:provider` | 实现 credentials seam / 读 backing file | 凭据提供方 |
| `llm:adapter` | `ctx.llm` 注册 adapter | 模型 adapter |
| `sessions:read` | `ctx.sessions` / `sessionQuery` | 读会话内容 |
| `system-prompt:write` | `ctx.systemPrompt` 注册 section/context/投影 | 改系统提示 |
| `tools:register` | `ctx.tools.register` | 注册工具 |
| `remote:expose` | `@Remote` / `@RemoteScope` | 对外 Remote 方法 |
| `plugin:install` | `ctx.pluginManager.installBundle/removeBundle` | 自安装 / 卸载插件 |

> 为什么能力 ID 数与检查项数不是一一对应：`credentials` 只有一条检查，但需要 `read` / `list` / `provider` 三个 ID 才能区分“读一个”和“枚举全部”。

### 2.3 常见漏报（会 FAIL 的高信号触发）

| 源码里出现 | 应声明 |
|---|---|
| `ctx.pluginManager.installBundle(...)` | `plugin:install` |
| `@Remote` / `@RemoteScope` | `remote:expose` |
| `ctx.webServer` / `ctx.webhookRuntime` 路由 | `network:ingress` |
| `ctx.credentials.resolve(...)` | `credentials:read` |
| `ctx.credentials.listRecords(...)` | `credentials:list` |
| 实现 credentials seam / 直接读 backing file | `credentials:provider` |
| `ctx.llm` 注册 adapter | `llm:adapter` |
| `ctx.systemPrompt` 注册 section/context/投影 | `system-prompt:write` |
| `ctx.tools.register(...)` | `tools:register` |
| `ctx.sessions` / `sessionQuery` | `sessions:read` |
| `ctx.fs` 读 / 写 | `fs:read` / `fs:write` |
| `ctx.shell` / `ctx.bash` | `shell` |
| `ctx.subprocess` | `subprocess` |
| `ctx.web` / 裸 `fetch` | `network:egress` |

### 2.4 checker 行为（对齐 DSC-11）

- 字段存在且合法：逐个 ID 校验；非法 ID → FAIL。
- 高信号触发而对应 ID 缺失 → FAIL。
- 过度声明（声明了但静态没看到）→ WARN。
- 字段缺失 → C-11 整体 WARN，不做触发映射。
- 宿主当前不读该字段；所以它是**披露**，不是“权限被限制”的证明。

## 三、`dsh.commitments`（B 类）

### 3.1 字段写法

```jsonc
{
  "dsh": {
    "commitmentsVersion": 1,
    "commitments": {
      "no-session-upload": true,
      "no-third-party-proxy": true,
      "no-credential-enumeration": true,
      "no-silent-prompt-rewrite": false,
      "no-adapter-bypass": true,
      "no-webhook-auto-session": true,
      "no-security-seam-replacement": true,
      "third-party-endpoint-disclosure": true,
      "no-danger-full-access": true
    },
    "disclosure": {
      "endpoints": ["https://api.example.com"],
      "retention": "none",
      "systemPrompt": "unchanged",
      "sessionLog": "read-only"
    }
  }
}
```

- 9 个键**全部必填**（`true` / `false`）；缺失 = 未声明，UI 显示“未声明”，不默认成 `true`。
- `false` 不是“违规”，是作者**明确不承诺**；高风险类别应配合 `dsh.labels` 与 `disclosure`。
- `disclosure` 是承诺的补充：端点、留存、是否改系统提示、是否读会话日志。

### 3.2 九条承诺

| 键 | 承诺 | 为什么只能声明 | 关联披露 |
|---|---|---|---|
| B1 `no-session-upload` | 不上传会话数据 | 无出口网络策略；`ctx.web` 与裸 `fetch` 等价开放 | `disclosure.endpoints` / `retention` |
| B2 `no-third-party-proxy` | 不代理第三方 API / 不转发到未配置端点 | “第三方 API”无法静态枚举 | `disclosure.endpoints` |
| B3 `no-credential-enumeration` | 不 `listRecords()` 枚举全部凭据、不 `resolve()` 无关 ref | `listRecords()` 是一等 API，无调用方鉴权 | `capabilities` 的 `credentials:list` |
| B4 `no-silent-prompt-rewrite` | 不静默改写 system prompt / 工具 schema / 消息投影 | 是合法扩展点，无法区分恶意与正当 | `disclosure.systemPrompt` |
| B5 `no-adapter-bypass` | 不注册 LLM adapter 之外挂监听全部模型流量 | `ctx.llm` 注册无审批 | `capabilities` 的 `llm:adapter` |
| B6 `no-webhook-auto-session` | 不在 webhook rule 中建入站自动化 Session | webhook rule 本身就是 trusted code | `capabilities` 的 `network:ingress` |
| B7 `no-security-seam-replacement` | 不替换安全相关 seam 的 provider（sandbox/sandboxPolicy/fs/approval/remoteAccess/cuaAccess） | “everything is replaceable”是产品特性 | `disclosure` + 人工 |
| B8 `no-danger-full-access` | 不利用 `danger-full-access` / `--add-dir` 把行为放到 workspace 之外 | 用户可选 preset 关闭沙箱 | `disclosure` |
| B9 `third-party-endpoint-disclosure` | 第三方端点/留存必须披露 | 外发行为不可 proof-flow-bound | `disclosure.endpoints` / `retention` |

**背书链**：B 类承诺的可信度只能来自来源三方之一（`@deepseek-ai` 组织归属 / npm publisher / 社区 index maintainer）+ A 类静态结果的签名存档。**不要把 star 数或“市场收录”当背书。**

### 3.3 checker 行为（对齐 DSC-12）

- B 类**不做 FAIL**；只做“缺失/未声明”WARN 与 UI 披露。
- 矛盾示例：`capabilities` 含 `credentials:list` 但 `commitments.no-credential-enumeration: true` → WARN（不是 FAIL，可能是 UI 管理工具）。
- `disclosure.endpoints` 为空但声明了 `network:egress` → WARN。
- `disclosure.retention` 缺失且声明了 `network:egress` → WARN。

## 四、`dsh.labels`（C 类）

### 4.1 字段写法

```jsonc
{
  "dsh": {
    "labelsVersion": 1,
    "labels": ["prompt-modification", "third-party-model-provider"],
    "usageScope": "authorized-targets-only",
    "upstream": "https://github.com/upstream/dsh-purge",
    "mirrorOf": "@upstream/dsh-purge@1.4.2"
  }
}
```

- `labels`：字符串数组，ID 来自下面闭集；未知/禁用标签 → FAIL。
- `usageScope`：`red-team` 必填（建议 `authorized-targets-only` / `lab-only` / `public-demo`）。
- `upstream` / `mirrorOf`：`mirror` 必填；`upstream` 必须是 https URL 且不等于自身。
- 字段缺失：C-13 整体 WARN；字段存在但触发缺标签 → FAIL。

### 4.2 五类标签

| ID | 类别 | 触发 / 判定 | 必填披露 | 安装提示（短） |
|---|---|---|---|---|
| `prompt-modification` | 破限 / 提示注入 | `capabilities` 含 `system-prompt:write` | `disclosure.systemPrompt` | 会改写系统提示 / 可能绕过安全策略 |
| `red-team` | 红队 / 安全研究 | **仅自声明**，无机械触发 | `usageScope` | 仅限你拥有或已获授权的目标 |
| `third-party-model-provider` | 免费 Key / 第三方模型 | `llm:adapter` + `network:egress` | `disclosure.endpoints` / `retention` | 请求将发往第三方端点；留存见披露 |
| `remote-access` | 远程访问 / 暴露面 | `network:ingress` | `disclosure.listen` / 认证方式 | 会开放入站访问；确认认证与网络边界 |
| `mirror` | 镜像 / 套壳 / 分叉 | `dsh.upstream` 存在 | `upstream` + `mirrorOf` | 此为镜像/分叉；上游…；版本可能落后 |

**禁用标签**（出现即 FAIL）：`official`、`verified`、`certified`、`secure`、`audited`。这些词会被读成官方/安全背书。

### 4.3 checker 行为（对齐 DSC-13）

- `labels` 字段存在：逐个校验；非法/禁用 → FAIL。
- `capabilities` 存在：按触发规则要求对应标签；缺标签 → FAIL。
- `capabilities` 缺失：C-13.2 不可评估，整体 WARN；仍检查禁用标签。
- `upstream`：https + 非自指；`mirror` 标签缺失 → FAIL。
- `red-team` 缺 `usageScope` → WARN；**未声明的红队无法识别**（已知盲区，不是检查器能修的）。

## 五、字段 ↔ 检查 ↔ 结果上限

| 字段 | 检查项 | 结果上限 |
|---|---|---|
| `dsh.capabilities` | DSC-11（C-11） | 字段缺失 WARN；存在但漏报 FAIL |
| `dsh.commitments` | DSC-12（C-12）的披露面 | 缺失 WARN；矛盾 WARN |
| `dsh.labels` | DSC-13（C-13） | 字段缺失 WARN；触发缺标签/禁用词 FAIL |
| `dsh.upstream` / `mirrorOf` | DSC-13.3 | 缺失/自指 FAIL |
| `dsh.usageScope` | DSC-13.5 | 缺失 WARN |

## 六、怎么落到 package.json

最小组合示例（字段名以上游规范为准；示例中的能力/标签要换成你自己的真实声明）：

```jsonc
{
  "name": "@acme/dsh-hello",
  "version": "0.1.0",
  "license": "Apache-2.0",
  "dsh": {
    "manifestVersion": 1,
    "capabilitiesVersion": 1,
    "capabilities": ["tools:register", "network:egress"],
    "commitmentsVersion": 1,
    "commitments": {
      "no-session-upload": true,
      "no-third-party-proxy": true,
      "no-credential-enumeration": true,
      "no-silent-prompt-rewrite": true,
      "no-adapter-bypass": true,
      "no-webhook-auto-session": true,
      "no-security-seam-replacement": true,
      "third-party-endpoint-disclosure": true,
      "no-danger-full-access": true
    },
    "disclosure": {
      "endpoints": ["https://api.example.com"],
      "retention": "none",
      "systemPrompt": "unchanged",
      "sessionLog": "read-only"
    },
    "labelsVersion": 1
  }
}
```

写完后用固定版本 checker 看这三项：

```sh
npx dsh-standards@1.6.9 check . --json > report.json
# 在 checks[] 里找 DSC-11 / DSC-12 / DSC-13；结论以 coverage 与 exit code 为准
```

- `DSC-11/12/13` 的 WARN 需要逐条解释（例如“未声明能力”“承诺矛盾”“端点为空”）；
- FAIL 要修（非法 ID、禁用标签、高信号漏报）；
- 不要把 `WARN` 当 PASS，也不要把“声明了”当“已验证”。

## 七、dsh-ee 边界

- 这三个字段是**标准扩展**，dsh-ee 的发布预检（`POST /api/me/packages/check`）是另一套检查，**不评估也不强制**这些声明面；两者不要互相换算。
- dsh-ee 不把这些声明当权限门控、审核准入或安全结论；不在页面上展示“能力认证/标签等级”。
- dsh-ee 服务端不执行 checker；上面的命令是作者本机用。
- 字段缺失/矛盾时，dsh-ee 预检不会替你补；请按上游规范自行声明，并在审核说明里解释高风险能力（如 `network:egress`、`system-prompt:write`）。

## 上游原文与相关页

- 上游原文：[60-权限与标注规范.md@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/60-权限与标注规范.md)
- 条款定义：[31-DS-C条款.md@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/31-DS-C条款.md)（C-11/C-12/C-13）
- 本目录：[DS-C 条款索引](./ds-c-clause-index.md)、[checker 命令与报告字段](./checker-cli.md)、[证据边界与常见误读](./evidence-and-misreadings.md)、[作者自检清单](./author-checklist.md)

**非官方 · 非认证 · 非安全审计。**
