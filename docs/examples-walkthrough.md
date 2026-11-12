# DSH 插件样例走读（8 个）

> 状态：2026-11-12 按固定 tag 核对。本文只写**自己的导读**：定位、前置、安装、走读入口、改造练习、检查边界、来源许可。不复制上游 README/图片/推广内容，只链固定版本。
>
> **这批样例是 DSH 插件作者样例，不是 dsh-ee 扩展样例。** 它们展示的是 DSH/Cordis 的插件写法（工具、命令、服务、存储、策略、MCP 等）。dsh-ee 自己的扩展三种形态（声明式/服务/端侧）的样例在 `extensions/examples/` 与 `extensions/official/`，见 [extensions/README.md](../extensions/README.md) 与 [plugins.md](./plugins.md)。

## 怎么用这篇文档

1. **先看定位和前置**，挑一个和你要写的能力最接近的样例；每个样例的难度只表示需要的先验知识，不是评级。
2. **安装到 DSH profile**：命令统一是 `dsh plugin --profile <profile> add <包@固定版本>`；默认 profile 写 `web`。
3. **按「走读入口」打开固定 tag 的文件**：先读入参/返回值声明，再读实现，最后读测试。
4. **做「改造练习」**：每个样例都给了一个不改变样例主旨的小改动，改完自己跑测试。
5. **读「检查边界」**：样例证明的是「这种写法能跑」，不证明安全、生产就绪或符合 dsh-ee 的发布要求。

通用约定：

- **版本固定**：安装命令里的包版本 = 上游 tag（`v` 前缀去掉）。固定在 2026-11-12 核实过 tag 存在；上游继续发版不会改变这里的命令。
- **来源**：8 个仓库都在 `github.com/PerryLink/` 下，许可均为 Apache-2.0。只链仓库固定 tag 与文件，不复制正文；引用代码片段请按 Apache-2.0 保留上游版权与 NOTICE。
- **企业环境**：这些包来自公网 npm。如果 DSH profile 只指向 dsh-ee 的企业 npm 源，能否安装取决于源的收录模式（按需回源 / 只提供已预载 / 离线），需要管理员先确认这些包可用；这与 dsh-ee 的插件预检、审核是两件事。
- **dsh-ee 不执行这些样例的检查**：本文（以及后续可能上线的样例页）只做导读，不评分、不认证、不代表已审核。

## 总表

| # | 样例 | 固定版本 | 难度 | 看什么 | 来源 |
|---|---|---|---|---|---|
| 1 | `@perrylink/dsh-claim-check` | 0.1.2 | starter | 最小工具、确定性核对 | [PerryLink/dsh-claim-check](https://github.com/PerryLink/dsh-claim-check/tree/v0.1.2) |
| 2 | `dsh-translate` | 0.3.5 | starter | 手写 client bundle + 服务描述符 | [PerryLink/dsh-translate](https://github.com/PerryLink/dsh-translate/tree/v0.3.5) |
| 3 | `dsh-output-styles` | 0.8.3 | intermediate | 命令、system prompt 注入、渲染注册表 | [PerryLink/dsh-output-styles](https://github.com/PerryLink/dsh-output-styles/tree/v0.8.3) |
| 4 | `dsh-library` | 0.3.0 | intermediate | 服务、storage-domain、检索与引用 | [PerryLink/dsh-library](https://github.com/PerryLink/dsh-library/tree/v0.3.0) |
| 5 | `dsh-industry-research` | 0.3.25 | intermediate | 随包技能、多工具、合成 fixtures | [PerryLink/dsh-industry-research](https://github.com/PerryLink/dsh-industry-research/tree/v0.3.25) |
| 6 | `dsh-test-drive` | 0.3.25 | intermediate | 临时 DSH_HOME 冒烟、后台任务 | [PerryLink/dsh-test-drive](https://github.com/PerryLink/dsh-test-drive/tree/v0.3.25) |
| 7 | `dsh-permission-rules` | 0.7.21 | advanced | 策略中间件、规则测试、热重载 | [PerryLink/dsh-permission-rules](https://github.com/PerryLink/dsh-permission-rules/tree/v0.7.21) |
| 8 | `dsh-memento` | 0.5.30 | advanced | 服务 seam、审批门、SQLite、MCP | [PerryLink/dsh-memento](https://github.com/PerryLink/dsh-memento/tree/v0.5.30) |

---

## 1. dsh-claim-check（证据逐字核对）

### 定位

最小工具样例：给定原文段落和一条断言，用逐字证据（引文、数字、标签值、极性/强度）给出确定性的核对结论；改写过的说法只算「证据不足」，不会算「支持」。三个工具：`claim_check`、`claim_bind`、`claim_report`。零模型、零网络也能复算，适合作为第一个 DSH 插件。

### 前置

- Node `^22.19.0 || >=24`（`package.json` engines）；
- DSH `>=0.2.1-0 <0.3.0 || >=0.2.2-0 <0.3.0`；
- 准备一段原文与一条断言；不需要模型与网络；
- 样例依赖 `@perrylink/dsh-research-core@0.1.1`（同一次安装里带上）。

### 安装

```bash
dsh plugin --profile web add @perrylink/dsh-research-core@0.1.1 @perrylink/dsh-claim-check@0.1.2
```

### 走读入口

- 插件入口与依赖注入：[`src/index.ts`](https://github.com/PerryLink/dsh-claim-check/blob/v0.1.2/src/index.ts)（`inject: ['tools']`，注册三个工具）。
- 确定性核对： [`src/evaluate.ts`](https://github.com/PerryLink/dsh-claim-check/blob/v0.1.2/src/evaluate.ts)。
- 三个工具的参数/返回值：`src/tools/claim-check.ts`、`claim-bind.ts`、`claim-report.ts`（同上 tag 下的 `src/tools/`）。
- 报告格式： [`src/report.ts`](https://github.com/PerryLink/dsh-claim-check/blob/v0.1.2/src/report.ts)（Markdown 与 SARIF）。
- 组合声明： [`cordis.patch.yml`](https://github.com/PerryLink/dsh-claim-check/blob/v0.1.2/cordis.patch.yml)。
- 测试：`test/`（`evaluate.spec.ts`、`mount.spec.ts`、`runtime-tools.spec.ts`）；上游 README 的 Tools / Error codes 两节可对照。

### 改造练习

给 `evaluate` 增加一种新的「标签值」写法（例如「总计：1,234」）并在 `test/evaluate.spec.ts` 里先写失败用例，再改实现让它通过。要求：不引入模型、不访问网络，测试与原有断言全过。

### 检查边界

- 它证明「工具注册 + 确定性逻辑 + 报告」这条最小链路能跑；不证明核对结论在真实业务文本上一定正确（它只做逐字证据）。
- 不是 dsh-ee 的发布预检，也不是安全检查；不覆盖企业 npm 源、审核、权限。
- 样例的测试只覆盖样例自己的行为，不代表你的改动也正确。

### 来源许可

- 仓库：[PerryLink/dsh-claim-check @ v0.1.2](https://github.com/PerryLink/dsh-claim-check/tree/v0.1.2)；许可：[Apache-2.0](https://github.com/PerryLink/dsh-claim-check/blob/v0.1.2/LICENSE)；版权人：PerryLink contributors。
- 依赖 `@perrylink/dsh-research-core@0.1.1` 单独发布，安装命令已固定版本。

---

## 2. dsh-translate（全页翻译：客户端与服务）

### 定位

手写客户端插件样例：host 半边注册 `translatePage` 服务（状态 + 翻译），浏览器半边遍历页面文本、做占位符遮罩、只把显示层发给模型。展示「手写 client bundle + 服务描述符 + 两端契约」，以及「不把会话写进日志/磁盘」的隐私边界。

### 前置

- Node `^22.19.0 || >=24.0.0`；
- DSH `>=0.2.1-0 <0.3.0`；
- profile 里要有一个可用的模型提供方（翻译走模型）；
- 需要在 web profile 里看效果（client 半边是浏览器插件）。

### 安装

```bash
dsh plugin --profile web add dsh-translate@0.3.5
```

### 走读入口

- host 半边入口： [`index.mjs`](https://github.com/PerryLink/dsh-translate/blob/v0.3.5/index.mjs)（`name`、`inject: ['sessions']`、`Config`、`apply`）。
- 服务实现： [`lib/service.mjs`](https://github.com/PerryLink/dsh-translate/blob/v0.3.5/lib/service.mjs)（`TranslateService`、路由解析、按会话取模型）。
- 引擎与协议：`lib/engine.mjs`、`lib/wire.mjs`（同上 tag 下的 `lib/`）。
- 浏览器半边： [`client/index.js`](https://github.com/PerryLink/dsh-translate/blob/v0.3.5/client/index.js)（DOM 遍历、占位符、`remote.translatePage`）。
- 组合声明里的 client 注入： [`cordis.patch.yml`](https://github.com/PerryLink/dsh-translate/blob/v0.3.5/cordis.patch.yml)。
- 测试：`test/*.test.mjs`（`client-apply.test.mjs`、`engine.test.mjs` 等）；上游 README 的「翻什么，不翻什么」「成本与隐私」「已知限制」可对照。

### 改造练习

增加一个「只翻译选中部分」的入口（或给 `maxSegments` 加一个 UI 可调的设置），要求遵守现有占位符遮罩规则，且不新增任何会话落盘/日志输出。改完跑 `test/` 全量。

### 检查边界

- 它证明「client bundle + Remote 服务」这条契约能跑；不证明翻译质量，也不保证任意页面/任意 DOM 都安全（样例有自己的跳过规则）。
- 样例的隐私承诺只覆盖样例自身的代码路径；不构成安全审计。
- 不是 dsh-ee 扩展，不经过企业 npm 源审核或 dsh-ee 的扩展协议检查。

### 来源许可

- 仓库：[PerryLink/dsh-translate @ v0.3.5](https://github.com/PerryLink/dsh-translate/tree/v0.3.5)；许可：[Apache-2.0](https://github.com/PerryLink/dsh-translate/blob/v0.3.5/LICENSE)；版权人：PerryLink contributors。

---

## 3. dsh-output-styles（输出风格与渲染注册表）

### 定位

命令 + 渲染样例：注册 `/style` 斜杠命令、把当前风格作为 system prompt 段落注入、按会话用 storage domain 持久化，并往 `output.render.*` 注册渲染器。适合学习「命令注册、system prompt 注入、渲染注册表、按会话状态」的组合。

### 前置

- Node `^22.19.0 || >=24.9.0`；
- DSH `>=0.2.1-0 <0.3.0 || >=0.2.2-0 <0.3.0`；
- 需要 web profile 才能看到渲染效果。

### 安装

```bash
dsh plugin --profile web add dsh-output-styles@0.8.3
```

### 走读入口

- 插件入口与注入： [`src/index.ts`](https://github.com/PerryLink/dsh-output-styles/blob/v0.8.3/src/index.ts)（`inject: ['systemPrompt', 'storageDomain']`）。
- 运行期： [`src/runtime.ts`](https://github.com/PerryLink/dsh-output-styles/blob/v0.8.3/src/runtime.ts)（注册 `/style`、system prompt 段落、存储域持久化）。
- 命令解析与状态： [`src/style-command.ts`](https://github.com/PerryLink/dsh-output-styles/blob/v0.8.3/src/style-command.ts)（`STYLE_COMMAND = 'style'`）。
- 风格库与渲染：`src/style-library.ts`、`src/renderers.ts`（同上 tag 下的 `src/`）。
- 浏览器半边：`src/client/index.ts`；组合声明：`cordis.patch.yml`；构建产物 `lib/` 可对照 TS 看编译结果。
- 测试：`tests/*.spec.ts`；上游 README 的 What you get / How it works 可对照。

### 改造练习

新增一个自定义风格文件（放在样例说明的位置），让 `/style <名字>` 能切换；再给该风格加一个只影响展示的 renderer 注册。要求不改变模型请求内容（除 system prompt 段落外），且切换后新会话/旧会话的行为符合样例的会话持久化规则。

### 检查边界

- 它证明命令、system prompt 注入、会话存储、renderer 注册这条链路；渲染只影响显示，不是安全边界。
- 样例不覆盖多用户/企业集中配置；也不构成对模型行为的保证。
- 不是 dsh-ee 扩展，不经过企业源的预检/审核。

### 来源许可

- 仓库：[PerryLink/dsh-output-styles @ v0.8.3](https://github.com/PerryLink/dsh-output-styles/tree/v0.8.3)；许可：[Apache-2.0](https://github.com/PerryLink/dsh-output-styles/blob/v0.8.3/LICENSE)；版权人：PerryLink contributors。

---

## 4. dsh-library（本地知识库：服务与存储）

### 定位

服务 + 存储样例：`ctx.service` + `storageDomain` 注册 `dsh_library` 服务和工具族（`library_add`、`library_remove`、`library_list`、`library_search`、`library_cite_check`、`library_diagnose`），做本地切片、哈希嵌入、BM25/重排、引用校验与清除验证。默认零模型、零网络。

### 前置

- Node `^22.19.0 || >=24.9.0`；
- DSH `>=0.2.1-0 <0.3.0 || >=0.2.2-0 <0.3.0`；
- 需要 storage-domain（样例通过 `inject: ['storageDomain', 'tools', 'commands']` 声明）；
- 默认零模型零网络；可选嵌入器配置见上游文档，不要在样例里接真实密钥。

### 安装

```bash
dsh plugin --profile web add dsh-library@0.3.0
```

### 走读入口

- 插件入口与服务注册： [`src/index.ts`](https://github.com/PerryLink/dsh-library/blob/v0.3.0/src/index.ts)（工具族与 `/library` 命令）。
- 检索质量实现： [`src/quality/`](https://github.com/PerryLink/dsh-library/tree/v0.3.0/src/quality)（`bm25.ts`、`citation.ts`、`relevance.ts`、`purge.ts` 等）。
- 嵌入 seam： [`src/embedding.ts`](https://github.com/PerryLink/dsh-library/blob/v0.3.0/src/embedding.ts)；切片/扩展：`src/text.ts`、`src/expand.ts`。
- 组合声明：`cordis.patch.yml`；测试：`tests/*.spec.ts`。
- 第三方算法归属：上游 `THIRD_PARTY_NOTICES.md`（同 tag 根目录），引用再分发时一并保留。

### 改造练习

无模型路径下给 `library_search` 增加一个可解释的排序因素（例如引用密度），先在 `tests/` 写用例固定期望，再改实现。要求：零网络、零模型仍可跑；`library_cite_check` 对无出处内容仍报「证据不足」。

### 检查边界

- 它证明服务 + 存储 + 检索 + 清除验证这条链路；不证明检索质量在你的语料上够用（需要用你自己的评测集验证）。
- 数据只在本机 profile 的存储里；不是企业知识库，也不接 dsh-ee 的知识库权限/审计。
- 样例的第三方算法归属声明是上游义务；复制/改写代码时保留。

### 来源许可

- 仓库：[PerryLink/dsh-library @ v0.3.0](https://github.com/PerryLink/dsh-library/tree/v0.3.0)；许可：[Apache-2.0](https://github.com/PerryLink/dsh-library/blob/v0.3.0/LICENSE)；归属：PerryLink contributors；算法移植自多个 Apache-2.0 项目，完整名单见上游 `THIRD_PARTY_NOTICES.md`。

---

## 5. dsh-industry-research（领域技能包：技能与多工具）

### 定位

技能 + 工具样例：包内随发两个 `SKILL.md`（行业研究方法、公司研究方法），注册四个工具 `industry_map`、`industry_track`、`company_scan`、`industry_report`，带合成 fixtures 与 SHA-256 台账（可追溯合成数据）。适合学习「随包分发技能、领域包结构、多工具协作」。

### 前置

- Node `^22.19.0 || >=24.9.0`；
- DSH `>=0.2.1-0 <0.3.0 || >=0.2.2-0 <0.3.0`；
- `ctx.web` 可选，没有时走内置回退（不一定需要外网）；
- 数据是**合成**的，不要当真实市场结论。

### 安装

```bash
dsh plugin --profile web add dsh-industry-research@0.3.25
```

### 走读入口

- 插件入口与四个工具注册： [`src/index.ts`](https://github.com/PerryLink/dsh-industry-research/blob/v0.3.25/src/index.ts)。
- 研究链与后台任务：`src/chain.ts`、`src/jobs.ts`；工具实现：`src/tools/`（`company.ts`、`map.ts`、`report.ts`、`track.ts`）。
- 技能正文： [`skills/industry-research-method/SKILL.md`](https://github.com/PerryLink/dsh-industry-research/blob/v0.3.25/skills/industry-research-method/SKILL.md)、[`skills/company-research-method/SKILL.md`](https://github.com/PerryLink/dsh-industry-research/blob/v0.3.25/skills/company-research-method/SKILL.md)。
- 合成数据与台账：`fixtures/`、`coverage/`（同 tag 根目录）；组合声明：`cordis.patch.yml`；测试：`test/`（`chain.spec.ts`、`company.spec.ts`、`composition.spec.ts` 等）。
- 上游 README 的 Tools & surfaces / Examples 可对照工具面。

### 改造练习

新增一个 `industry_compare` 工具（比较两家/两个行业），复用现有 `chain` 的数据结构与 fixtures 台账规则；补测试。要求不新增网络依赖，输出仍标注数据来源与合成属性。

### 检查边界

- 它证明「随包技能 + 多工具 + 合成数据追溯」；不证明研究结论真实、完整或可投资（fixtures 是合成的）。
- 技能是给模型看的提示词，不构成安全控制；不要把公司敏感数据塞进合成样例路径。
- 不是 dsh-ee 扩展，不经过企业 npm 源审核；企业数据接入用 dsh-ee 的知识库/企业连接。

### 来源许可

- 仓库：[PerryLink/dsh-industry-research @ v0.3.25](https://github.com/PerryLink/dsh-industry-research/tree/v0.3.25)；许可：[Apache-2.0](https://github.com/PerryLink/dsh-industry-research/blob/v0.3.25/LICENSE)；版权人：PerryLink contributors；第三方归属见上游 `THIRD_PARTY_NOTICES.md`。

---

## 6. dsh-test-drive（安装验证：装、启、断言、卸）

### 定位

验证样例：在临时 `DSH_HOME` 里安装一个包、启动、断言能力，再卸载并产出验证记录；用 `ctx.jobs` 跑后台任务。两个工具：`test_drive`、`drive_report`。适合学习「安装冒烟 + 结果报告」的写法，也适合当成 CI 里的 DSH 插件验证动作。

### 前置

- Node `^22.19.0 || >=24.9.0`；
- DSH `>=0.2.1-0 <0.3.0 || >=0.2.2-0 <0.3.0`；
- 需要 `dsh` CLI 与 pnpm，安装被测包需要网络；
- **DSH Desktop 不能当测试宿主**（上游 README 专门有一节）；在 CLI host 上跑。

### 安装

```bash
dsh plugin --profile web add dsh-test-drive@0.3.25
```

### 走读入口

- 插件入口与工具注册： [`src/index.ts`](https://github.com/PerryLink/dsh-test-drive/blob/v0.3.25/src/index.ts)；工具：`src/tools.ts`（`test_drive`、`drive_report`）。
- 驱动与编排：`src/drive.ts`、`src/driver.ts`；隔离环境：`src/workspace.ts`（临时 `DSH_HOME`）。
- 断言与报告：`src/capability.ts`、`src/report.ts`、`src/result.ts`。
- GitHub Action 定义： [`action.yml`](https://github.com/PerryLink/dsh-test-drive/blob/v0.3.25/action.yml)；测试：`test/`（同上 tag）。
- 上游 README 的 Quick start / Examples / Configuration 可对照。

### 改造练习

给验证结果增加一项断言（例如「安装后 `dsh plugin list` 能列出被测包」），并在驱动器里用临时 `DSH_HOME` 执行；要求失败时输出可定位的日志，且不污染真实 `$DSH_HOME`/`~/.npmrc`。

### 检查边界

- 它证明「安装/启动/断言/卸载」这条验证流程；不证明被测插件安全或质量合格，也不覆盖 Desktop。
- 它会真的安装包、跑命令；在本机跑时注意隔离环境，不要用生产 profile 或真实凭据。
- 不是 dsh-ee 的发布预检；dsh-ee 的预检/审核不跑这个样例。

### 来源许可

- 仓库：[PerryLink/dsh-test-drive @ v0.3.25](https://github.com/PerryLink/dsh-test-drive/tree/v0.3.25)；许可：[Apache-2.0](https://github.com/PerryLink/dsh-test-drive/blob/v0.3.25/LICENSE)；版权人：PerryLink contributors；第三方归属见上游 `THIRD_PARTY_NOTICES.md`。

---

## 7. dsh-permission-rules（工具策略：规则与网络）

### 定位

策略中间件样例：在 `tools/pre-execute` 瀑布里做 `allow` / `deny` / `ask` 规则、网络策略（allow-host）与规则测试向量，带热重载的设置页。适合学习「策略 seam、规则文件格式、网络策略、配置页」。

### 前置

- Node `^22.19.0 || >=24.9.0`；
- DSH 0.2.1 线（engines 兼容更早的 0.1.x/0.2.x，样例以 0.2.1 线验证）；
- 需要 web 设置页；WSL 或规则文件在 `/mnt/<盘>` 下时，样例会改用轮询（原生文件事件不可靠）；
- 规则文件默认路径与格式见上游 README 的 Rule syntax / Network policy。

### 安装

```bash
dsh plugin --profile web add dsh-permission-rules@0.7.21
```

### 走读入口

- 插件入口与注入： [`src/index.ts`](https://github.com/PerryLink/dsh-permission-rules/blob/v0.7.21/src/index.ts)。
- 规则匹配： [`src/rules.ts`](https://github.com/PerryLink/dsh-permission-rules/blob/v0.7.21/src/rules.ts)；内置高风险规则：`rules/builtin-high-risk.yaml`。
- 网络策略：`src/network.ts`、`src/allow-host.ts`、`src/proxy.ts`；执行接缝：`src/runtime.ts`。
- 设置页与热重载：`src/client/`（`index.ts`、`section.ts`、`locales.ts`、`remote.ts`）。
- 测试：`test/`（含 `builtin-rules.spec.ts`、`allow-host.spec.ts`、`dispatch.spec.ts` 等规则测试向量）；上游 README 的 Rule syntax / Network policy / Examples 可对照。

### 改造练习

新增一条 `deny` 规则及对应测试向量（例如禁止某类命令），验证热重载后新规则生效、坏规则不崩溃；再验证网络策略的放行/拒绝与日志输出。要求不绕过审批或扩大默认放行范围。

### 检查边界

- 它是**策略中间件**，不是沙箱：只约束经过 DSH 工具/命令 seam 的调用；直接执行的代码、其它插件、宿主进程不受它保护。
- 样例的规则/测试只覆盖样例自己的语义；不构成安全认证，也不能替代 dsh-ee 的权限、审计与审核。
- 不是 dsh-ee 扩展，不经过企业源审核。

### 来源许可

- 仓库：[PerryLink/dsh-permission-rules @ v0.7.21](https://github.com/PerryLink/dsh-permission-rules/tree/v0.7.21)；许可：[Apache-2.0](https://github.com/PerryLink/dsh-permission-rules/blob/v0.7.21/LICENSE)；版权人：PerryLink contributors。

---

## 8. dsh-memento（记忆服务：审批与审计）

### 定位

高级样例：定义 `ctx.memory` 服务（`add`/`replace`/`remove`/`query`/`seed` + budgets），服务提供者用 `node:sqlite`（WAL、0600）落本地存储，写方法内部强制走审批门（`waterfall` 审批接缝），模型无论经哪个工具/插件间接调用都无法绕过；另外带一个 MCP server 入口与冻结快照的 system prompt 注入。

### 前置

- Node `^22.19.0 || >=24.0.0`（需要 `node:sqlite`）；
- DSH 0.2.x（engines 兼容 0.1.2 起的多条线，样例以 0.2.x 验证）；
- `$DSH_HOME` 可写（数据库在 `$DSH_HOME/dsh-memento/memory.db`）；
- 只读场景会以 `readOnly: true` 打开数据库；先跑一次样例自己的测试确认环境。

### 安装

```bash
dsh plugin --profile web add dsh-memento@0.5.30
```

### 走读入口

- 插件入口 / 三个角色 seam： [`index.mjs`](https://github.com/PerryLink/dsh-memento/blob/v0.5.30/index.mjs)（Service Definition `ctx.memory`、Provider、Consumer）。
- 审批门： [`lib/gate.mjs`](https://github.com/PerryLink/dsh-memento/blob/v0.5.30/lib/gate.mjs)；SQLite 存储：`lib/store.mjs`。
- 协议与导出： [`lib/protocol.mjs`](https://github.com/PerryLink/dsh-memento/blob/v0.5.30/lib/protocol.mjs)；MCP 入口：`bin/mcp-server.mjs`、`lib/mcp.mjs`。
- 架构与协议文档： [`ARCHITECTURE.md`](https://github.com/PerryLink/dsh-memento/blob/v0.5.30/ARCHITECTURE.md)、[`docs/protocol-v1.md`](https://github.com/PerryLink/dsh-memento/blob/v0.5.30/docs/protocol-v1.md)、`docs/adapters-guide.md`。
- 测试：`test/`（含 `protocol-conformance/`）；上游 README 的 MCP server / Tools & surfaces 可对照。

### 改造练习

写一个最小 adapter（照 `docs/adapters-guide.md` 的约定）把记忆导出到另一种存储，再补一条协议一致性测试。要求：写路径仍然经过审批门，导出/导入仍受预算与上限约束；不新增绕过 `ctx.memory` 直接写数据库的入口。

### 检查边界

- 它证明「服务 seam + 不可绕过审批 + SQLite provider + MCP 入口」的写法；审批门的强度依赖 DSH 的审批接缝，不是 dsh-ee 的企业审批。
- 数据只是本机一份 SQLite；不是企业级持久化、备份、多用户隔离方案。
- 样例协议是它自己的 v1，不是 dsh-ee 扩展协议；不构成安全审计。

### 来源许可

- 仓库：[PerryLink/dsh-memento @ v0.5.30](https://github.com/PerryLink/dsh-memento/tree/v0.5.30)；许可：[Apache-2.0](https://github.com/PerryLink/dsh-memento/blob/v0.5.30/LICENSE)；版权人：PerryLink contributors 及个人贡献者（完整名单见上游 `package.json` 的 `contributors`）；第三方归属见上游 `THIRD_PARTY_NOTICES.md`。

---

## 附录：来源与许可总览

| 样例 | 固定 tag | 许可 | 归属 | 备注 |
|---|---|---|---|---|
| dsh-claim-check | `v0.1.2` | Apache-2.0 | PerryLink contributors | 依赖 `@perrylink/dsh-research-core@0.1.1` |
| dsh-translate | `v0.3.5` | Apache-2.0 | PerryLink contributors | 手写 client bundle |
| dsh-output-styles | `v0.8.3` | Apache-2.0 | PerryLink contributors | web profile |
| dsh-library | `v0.3.0` | Apache-2.0 | PerryLink contributors；算法移植自多个 Apache-2.0 项目 | 见上游 `THIRD_PARTY_NOTICES.md` |
| dsh-industry-research | `v0.3.25` | Apache-2.0 | PerryLink contributors | 合成数据 |
| dsh-test-drive | `v0.3.25` | Apache-2.0 | PerryLink contributors | Desktop 不支持 |
| dsh-permission-rules | `v0.7.21` | Apache-2.0 | PerryLink contributors | 策略不是沙箱 |
| dsh-memento | `v0.5.30` | Apache-2.0 | PerryLink contributors 及个人贡献者 | 审批 + SQLite + MCP |

只链固定版本，不复制上游 README/截图/推广材料。若要把样例代码或片段再分发，按 Apache-2.0 保留上游 `LICENSE`、`NOTICE`/`THIRD_PARTY_NOTICES.md` 与版权声明。
