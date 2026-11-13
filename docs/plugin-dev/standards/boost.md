# BOOST 加速实践：有没有用好 Cordis 原生能力

> 上游是 **DSH-BOOST 加速实践**（v2 的第三套，上游版本 `v2.0.0-rc.1`，日期 2026-10-31）。
> 本页是作者摘要：BOOST 是**实践库**，不是插件等级，也不是“第三个徽章”。

```text
Source: https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/33-DSH-BOOST加速实践.md@19c410bd261d942c360386334d4841543a646579
License: CC BY 4.0 (https://creativecommons.org/licenses/by/4.0/)
© 2026 PerryLink
Changes:
- 精简为作者摘要：保留定位（实践库/非等级）、八条 LEV、证据卡字段、判定状态与边界
- 删除上游内部实测表（LEV-3/6/7 的具体数字、results/ 路径、真实 provider 复测细节）；这些是自评证据，会随锚点过期，不能当生产基线
- 用“证据卡字段说明表”替换上游 YAML 示例，避免示例数值被误读为实测结果
- 删除内部研究文件引用（91/95/34/35/36）与“下一步”计划；保留可执行的做法与证据要求
- 新增「dsh-ee 审核怎么用 BOOST」与「不要写成营销结论」两节（dsh-ee 侧新增文字，非上游）
Date: 2026-11-12
Modified version: dsh-ee docs
非官方·非认证·非安全审计 / Not official, not a certification, not a security audit.
```

## 一、它回答什么

- `DSH-ENTRY` 判定“能不能被识别、安装、启动”；
- `DSH-FLOW` 判定“用户旅程能不能复现”；
- `DSH-BOOST` 登记“有没有用 Cordis 原生能力把插件做得更可靠、更好维护”，性能收益另行测量。

**BOOST 与 FLOW 不可比**：FLOW 验旅程、BOOST 验实践，两者量纲不同。FLOW 通过不能证明 BOOST 收益，BOOST 基准也不能替代 FLOW 验收；不存在“BOOST 比 FLOW 高级”或“BOOST 省钱 X%”的可比结论。

**不是第三个徽章**：一条 LEV 只说明“这个做法在给定锚点下有证据”，不代表插件整体“高级”。

## 二、八条 LEV

| ID | 要求 | 证据看什么 |
|---|---|---|
| LEV-1 可逆 | 定时器/连接/watcher/子进程进 `ctx.effect()` + label；无模块级持有 | restart N 次 `getEffects()` 回基线；reload 100 次泄漏 0 |
| LEV-2 等待 | 必需 `inject`；等待 `ctx.inject()`；可选才 `ctx.get()`；禁轮询 | 缺服务 0 TypeError / PENDING 可枚举；恢复后 1 epoch ACTIVE |
| LEV-3 热配置 | 可调字段 `volatile()` + `.get()`；HMR 只当代码热更 | 配置变更 restart 0；级联重启 0；effect 增量 0 |
| LEV-4 事件 | 事件名/`@mode` 一致；waterfall 观察者 `next()`；等待用 serial/bail/parallel，纯观察用 emit；通知型容错 | mode 不一致 0；监听器故障隔离 1/1；短路分支测试 |
| LEV-5 seam/隔离 | 只经 seam；不 import 实现；多实例才 isolate/group；per-agent `agent.ctx` | consumer 改动 0；两 realm 2/2；`leakedServices 0` |
| LEV-6 前缀/装配 | 工具/提示声明逐字节稳定；易变信息走 `systemPrompt.context`；复用 assembly | prefix 命中/token；两次 boot schema diff 0 |
| LEV-7 并行/长活 | 独立调用并行有界；长活走 jobs；模型面有序；取消贯穿 | `T≈max(T_i)`；kill 后 100% 停止；step 阻塞 0 |
| LEV-8 组合/证据卡 | bundle patch/preset 表达差异；每条实践附证据卡 | 新增层 1；单行启停不影响其他；`leaked 0`；证据卡可复跑 |

> LEV-6 的边界：前缀/装配证据只说明“稳定前缀可复用”或“缓存可命中”，不能换算成费用节省，也不构成计价依据。任何“省 X%”的数字都必须来自可复跑基准，且写明环境/输入/负向控制。

## 三、证据卡：每条实践一张

上游要求每条实践附一张证据卡，字段与填写要求如下（**本表是字段说明，不含任何实测数值**）：

| 字段 | 填什么 |
|---|---|
| `subject` | 被检对象：包名@版本 |
| `host` | 宿主锚点：dsh / cordis / node / 平台版本 |
| `method` | 方法：单机单次还是多次；对照臂是什么；是不是生产基准 |
| `scope` | 覆盖哪条 LEV 的哪部分 |
| `command` | 可复跑命令（绝对可粘贴） |
| `result` | 结果；只填实际量到的值，不填“应该” |
| `negativeControl` | 负向对照（没有对照的收益不采信） |
| `generatedAt` / `expiresAt` | 生成/过期日期（过期证据按 `stale` 处理） |
| `recordHash` | 证据记录哈希 |
| `claimLevel` | 自评/工具验证；不得混用 |
| `issuer` / `selfAssessed` | 谁出的、是否自声明 |

**不能量化**：开发人时、维护成本、“少踩坑”、LOC 节省（合成示例不算证据）→ 只作 WARN/主观登记，不冒充 PASS。

## 四、判定状态

| 状态 | 含义 |
|---|---|
| `boost-evidenced` | 至少 1 条 LEV 有可复跑证据卡，锚点完整 |
| `boost-warn` | 有做法但指标不可测 / 样本不足 |
| `boost-fail` | 声称的收益与证据矛盾 |
| `boost-not-run` | 未测；不是 PASS |

不设“通过线 / 等级 / 市场门禁”。

## 五、dsh-ee 审核怎么用 BOOST

- BOOST 不是准入门槛；dsh-ee 不做运行时基准，也不替作者测性能。
- 作者若自述 BOOST 收益，审核只当**证据阅读**：看证据卡字段是否完整、是否有负向对照、是否过期；不把它转成上架排序或 badge。
- 无法复跑的自述（“优化了 30%”没有命令/环境/对照）不采信，也不写进任何对外结论。

## 六、不要写成营销结论

- 不写“性能提升 X 倍 / 省 X% / 业界最佳”之类无法复跑的结论；
- 不把 BOOST 实践登记写成“高级插件 / 推荐 / 等级”；
- 不把 FLOW 与 BOOST 拼成一个分数；不与其他插件排名；
- 引用上游数据时必须带锚点（版本/commit/日期），过期即作废。

## 七、边界

- 本页对应上游 `v2.0.0-rc.1` 快照；上游证据与基准会演进，命令固定 `dsh-standards@1.6.9`。
- BOOST 的“有证据”只代表该做法在给定锚点下可复跑，不代表插件整体质量、性能或安全性。
- 本页不复制上游 `results/` 或实测数字；需要原始数据请直接看上游快照。

## 上游原文与相关页

- 上游原文：[33-DSH-BOOST加速实践.md@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/33-DSH-BOOST加速实践.md)
- 上游证据规则：[docs/status-taxonomy.md@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/docs/status-taxonomy.md)
- 本目录：[ENTRY](./entry.md)、[FLOW](./flow.md)、[checker 命令与报告字段](./checker-cli.md)、[证据边界与常见误读](./evidence-and-misreadings.md)

**非官方 · 非认证 · 非安全审计。**
