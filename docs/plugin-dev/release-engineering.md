# 发布工程：多包维护、发布流水线与回滚

> **来源与许可**
> - 主来源：[PerryLink/dsh-primer](https://github.com/PerryLink/dsh-primer) 的 [guide/release-engineering.zh-CN.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/guide/release-engineering.zh-CN.md)（同 pin 下另有英文版 [guide/release-engineering.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/guide/release-engineering.md)）
> - 版本：`dsh-primer@0.4.3`；公开 pin：tag `v0.4.3` = commit `8750d31718c16601514e59fe1831de73cfed3d20`（2026-11-03）；npm 发布记录的 `gitHead` = `fa2b74234530193c6cdeb82265167328ce6beb90`（用于核对 npm 发布物；该 commit 与 tag 树仅发布 workflow 不同）
> - 许可：Apache-2.0（上游 `LICENSE`；上游 `NOTICE.md` 声明其自有文本以 Apache-2.0 发布）
> - 版权：© 2026 dsh-primer contributors。上游 `LICENSE` 为 Apache-2.0 模板、未填写版权主体；PerryLink 仅对其拥有版权的材料声明版权。
> - 补充来源：[guide/version-matrix.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/guide/version-matrix.md)（peer 区间与版本线）；`PerryLink/dsh-plugin-guide@0.5.5`（tag `v0.5.5` = commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`）的 [docs/release-checklist.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/release-checklist.md)（发布前清单）（Apache-2.0）。dsh-ee 侧流程来自本仓库基线 `20aafbb` 的 `docs/plugin-authors.md`、`server/internal/api/package_check.go`、`server/internal/registry/tarball.go`（自己的文档与代码事实）。
> - 修改说明：中文节选并改写；删除上游的规模数字、上游特定镜像渠道与营销段、本机路径和带日期的生态统计；保留四份契约、版本线、发布流水线、README 一致性、迁移波、registry 行为与 tag 门禁；新增「回滚与事故处理」与 dsh-ee 企业源发布/审核/版本不可复用说明；与 [release-checklist.md](./release-checklist.md) 互链。
> - 同步日期：2026-11-12
>
> 本页不是 DSH 官方文档，也不是发布认证。命令与平台行为以目标版本的官方文档、registry 实际行为为准；`static ≠ runtime`、`not-run ≠ pass`。

## 0. 你实际在维护的是四份契约

插件不是「一个 TypeScript 包」，而是四份契约；发布出问题，一定是其中一份先漂移了：

| 契约 | 位置 | 断裂方式 |
|---|---|---|
| Bundle 清单 | `package.json` → `dsh.bundle.patch` | patch 文件改名，或该键被删掉 |
| 安装面 | 公共 npm + dsh-ee 企业源（作用域/包名） | 发布落到错误的 tag/scope，或根本没发出去 |
| 兼容窗口 | `engines.dsh` + `@deepseek-ai/*` 的 `peerDependencies` | 宿主换了 alpha/rc 线，范围不再接受它 |
| 可发现性 | README、npm `keywords`、dsh-ee 审核与可见范围 | 仓库改名/描述漂移，或上架后没配可见范围 |

下面全部围绕「如何低成本地让这四份契约在多个包、多个版本线上保持一致」。单包发布前的逐项自查见 [release-checklist.md](./release-checklist.md)。

## 1. 布局与命名

从 1 个插件维护到几十个，只差两个习惯：

1. **一个插件一个仓库，各自独立发布**。monorepo 会让一个失败的门禁卡住所有插件的发布。
2. **可预测的命名**：包名 `dsh-<能力>`，仓库同名；scoped 名（`@company/dsh-x`）也可以，但仓库名/企业作用域要保持稳定——发现、审核和审计都依赖它。

每个仓库保留一份 `AGENTS.md` 写清该仓自己的规则；跨仓约定集中到一份共享文档（本页）。组合变大后，仓库内文件是该仓的权威。

## 2. 版本线矩阵

开发者预览期，宿主会同时存在多条线。只钉一条线的组合，在用户从另一条线安装的那一刻就坏。同时接受全部窗口的写法（每个预发布 tuple 一条子句；每个 `-0` 下界覆盖该 tuple 的全部预发布，**绝不写成裸 `>=0.1.7` 形态**）：

```jsonc
{
  "peerDependencies": {
    "@deepseek-ai/cordis": "^4.0.2",
    "@deepseek-ai/dsh-tools": ">=0.1.2-rc.1 <0.2.0 || >=0.1.5-alpha.1 <0.2.0 || >=0.1.6-0 <0.2.0 || >=0.1.7-0 <0.2.0 || >=0.2.0-0 <0.3.0 || >=0.2.1-0 <0.3.0"
  },
  "devDependencies": {
    "@deepseek-ai/dsh-tools": "0.2.1-alpha.2"
  }
}
```

> ⚠️ **不要写开区间 `>=0.1.2-rc.1 <0.3.0`**：它横跨整代破坏性变更，门禁会放行 `0.2.x` 宿主，而插件还在调 `0.1.x` 的 API——**安装期放行、运行期炸**。上界必须按世代切（`<0.2.0` / `<0.3.0`）。完整口径见 [version-matrix.md](./version-matrix.md) §5。

- **`devDependencies` 钉最新已发布的线**：类型检查要赶在用户之前报错。
- **`dependencies` 钉 stable/rc 线，`peerDependencies` 接受多个窗口**。不要为了让自己的 CI 变绿而收窄 peer 范围——那等于把另一条线上的用户静默卸载。
- **绝不用手改几十个仓库的钉号**。脚本化的一波（patch bump、更新钉号、过门禁、发布）是唯一可持续的路径；把一波当原子批次执行，并为每个仓留回滚注记（上一个 tag + 要还原的确切钉号）。
- **`engines.dsh` 与 peer 范围要保持一致**；自 DSH 0.2.1-alpha.4 起 `engines.dsh` 存在时宿主以它为准，否则回退 peer（见 [version-matrix.md](./version-matrix.md) §5）。

## 3. 发布流水线

每个仓库的发布 = 推一个 tag，触发 workflow：

```yaml
on:
  push:
    tags: ['v*']
jobs:
  publish:
    steps:
      - run: pnpm install --frozen-lockfile
      - run: pnpm run build
      - run: npm publish --provenance --access public
        env:
          NODE_AUTH_TOKEN: ${{ secrets.NPM_TOKEN }}
```

五条用时间换来的经验：

1. **`--frozen-lockfile` 是一道门禁，而且它是对的**：发布提交漏了 lockfile 就会死在这里；绝不要在锁文件不干净时发布。
2. **用 `--provenance` 发布**：一个参数换来签名的供应链凭证，是给审查者最便宜的信任信号。
3. **secret 按仓存**：每个会发布的仓都要有自己的 `NPM_TOKEN`；令牌轮换时要全量重播种，否则故障会晚很多、一个一个仓冒出来。
4. **已推送的 tag 不能重发**：产物错了就修提交、强制移动 tag、重跑，并接受第一次尝试已成为公开历史。
5. **要知道 registry 的复制延迟**：发布后立刻跑的校验任务可能仍解析到上一个版本；等 `latest` 稳定后重跑，而不是去「修」一个本来没坏的包。

**dsh-ee 企业源发布**多两步：先调预检（`POST /api/me/packages/check`，不写库、不占版本号），再 `npm publish --registry https://<dsh-ee>/npm/` 或网页上传；默认白名单模式进审核，通过后才进 packument/索引。**企业侧版本号不可复用**（含驳回、下架的版本），与公共 npm 的版本不可变一起，构成「只能用新版本修复」的纪律。见 [release-checklist.md](./release-checklist.md) §7 与 [`docs/plugin-authors.md`](../plugin-authors.md)。

## 4. 一个版本，三个面

发布一次，要让下面三个面都到位：

| 面 | 机制 | 要盯的失败模式 |
|---|---|---|
| npm（公共或企业源） | tag 触发 workflow / `npm publish` | dist-tag 落错线；企业 scope/审核状态不对 |
| GitHub Release / CHANGELOG | 同一个 tag 触发的 workflow（幂等） | release notes 与 CHANGELOG 漂移；上了 npm 却没有 Release 页 |
| dsh-ee 企业源 | 预检 → 发布 → 审核 → 可见范围 | 审核状态、可见范围/下发方式没配；索引/缓存延迟 |

GitHub Release 由**发布 npm 的同一个 tag workflow** 创建，**不要手工建**——手工建正是 notes 与 `CHANGELOG.md` 漂移的来源，而且这个缺口在 registry 侧看不见。Release 创建要幂等（已存在就跳过），这样重跑、补 tag 都不会让 job 失败：

```sh
if gh release view "$TAG" >/dev/null 2>&1; then
  echo "release $TAG already exists; skipping"
  exit 0
fi
node scripts/changelog-section.mjs "$VERSION" > release-notes.md || true
if [ -s release-notes.md ]; then
  gh release create "$TAG" --title "$TAG" --notes-file release-notes.md
else
  gh release create "$TAG" --generate-notes
fi
```

细节：Release job 需要 `permissions: contents: write`（publish job 的 `contents: read` 不够，job 级权限会覆盖 workflow 级）；它应是 `needs:` 发布 job 的**独立 job**，这样 CHANGELOG 缺小节不会把已成功的 npm 发布变成红灯。「缺小节 → 生成式 notes」好过「没有 Release 页」。

## 5. README 一致性属于构建的一部分

如果组合提供多语言 README，就把它们当构建产物：

- 语言集合固定且被检查：只给一种语言加章节而其他语言不加，必须让 CI 失败。
- tag 之前先跑编码门禁：UTF-8 无 BOM、无乱码、无替换字符。**已发布的 README 修不回来**——npm 元数据不可变，registry 甚至可能不展示修正后的文件。
- 写锚点/词边界正则时注意：CJK 字符后的词边界行为与英文不同。

## 6. 宿主破坏兼容时的迁移波

宿主明确写了会破坏兼容，所以要按「波」计划，而不是指望稳定：

1. **探测**：在用户撞上之前，先对最新已发布的线跑类型检查矩阵。某个事件类型被移除或签名变了，会在某个仓先炸成编译错误——这个错误就告诉你全部仓的影响面。
2. **分诊**：把「钉号更新」「机械改写」「语义重设计」分开。只有前两类属于一波；第三类单独发布、单独写 changelog。
3. **原子执行**：一批 patch bump、一次门禁、一波发布，并为每个仓留回滚注记。
4. **记录事故**：例如发布提交漏了锁文件导致 CI 红（补提交 + 强制移动 tag）；用另一包管理器生成的锁文件含 workspace link 条目、干净环境无法复现（删锁文件、隔离重装、重新生成）。

**经验值**：一波超过约四个仓，CI 队列就会饱和、各 run 结算乱序；按此分批。

## 7. 打 tag 前 / 后的门禁清单

打 tag 前：

- [ ] `pnpm install --frozen-lockfile` 干净。
- [ ] 对**最新已发布的线**做类型检查，而不是你开发时用的那条。
- [ ] 单元测试全绿。
- [ ] 包不变量：`files` 白名单确实包含构建产物，且产物存在。
- [ ] 多语 README 一致 + 编码审计（无 BOM、无乱码、无替换字符）。
- [ ] `CHANGELOG.md` 有即将打的版本小节；Release 由同一个 tag workflow 幂等创建。
- [ ] 版本号与意图一致（patch = 修复波，minor = 功能，major = 破坏性）。
- [ ] 远端还没有这个 tag。
- [ ] 若发企业源：预检通过（或已解释 warn）、版本号未被用过、`engines.dsh` 已声明。

推 tag 后：

- [ ] npm `dist-tags.latest` 等于新版本（注意复制延迟）。
- [ ] GitHub Release 已建且带 notes。
- [ ] 企业源：审核状态/可见范围/下发方式符合预期。
- [ ] 所有钉住该包的消费仓已同一批升级。
- [ ] 记录回滚注记（上一个 tag + 要还原的钉号）。

## 8. 看起来像你的 bug、其实不是的 registry 行为

- **`versions[].readme` 可能为空**：即使 tarball 里的 README 与仓库逐字节一致，包页也可能展示旧 README；这是 registry 侧呈现行为，不要靠「重发一次」去修。
- **`description` 在包元数据里约 255 字符处被截断**：校验发布时按截断形式比对，而不是全文。
- **scoped 包**在某些下载量端点上会返回占位值；用 range 端点，别用 point 端点。
- **发布成功不等于可见**：公共 registry 有复制/缓存延迟；企业源还有审核与索引刷新。发布后等状态稳定再验证，不要反复重发（版本不可复用）。
- **企业侧审核状态是独立状态机**：`pending`/上架/驳回与公共 npm 的 `latest` 不是同一件事，别把「npm publish 成功」当成「企业已可见」。

## 9. 回滚与事故处理

npm 与 dsh-ee 的共同前提：**已发布版本不可变**。所以「回滚」通常不是删版本，而是：

1. **先止损**：把有问题的版本 `npm deprecate`（公共侧）；企业侧由管理员下架/调整可见范围，停止继续分发。
2. **恢复可用**：让用户装回上一个可用版本（临时回滚），或立刻发 patch 新版本。
3. **修复发布**：升版本号重发（企业侧版本号不可复用）；更新 `engines.dsh`/peer 范围时同样升版本。
4. **留证据**：保留失败版本号、tarball 哈希、日志、发布/审核时间线；事故报告只写事实与处置，不写未公开的利用细节。
5. **补门禁**：把导致事故的检查加进 CI（lockfile、编码、生命周期脚本、预检结果、`--dump-config`）。

## 10. 几个会伪造结论的本地工具

下面每一条都返回一个自信的错误答案，而不是报错；写下来好让下一个人认得出这个形状：

- **`npm view <pkg>@<ver> A B --json` 会凭空造出「缺失」**：一次问两个字段时，它可能把实际存在的字段报成空值。每次只查一个字段；当真要下结论时，解开已发布的 tarball 读 `package.json`——那才是地面真相。
- **遵守 `.gitignore` 的内容检索会返回假阴性**：当工作区根目录忽略一切（`*` 加一条例外）时，在它上面检索会一无所获，而「一无所获」会被读成「不存在」的证据。断言某个模式不存在前，先用一个不查忽略规则的扫描确认。
- **Windows PowerShell 5.1 的 `Set-Content -Encoding utf8` 会写 BOM**：以 `EF BB BF` 开头的 JSON 请求体会解析失败，报错却指向载荷内容。机器要读的文件用显式编码器（`UTF8Encoding($false)` 或等价的 Node 写法）。

## 11. 与 release-checklist 的分工

| 文档 | 回答什么 |
|---|---|
| [release-checklist.md](./release-checklist.md) | **一个包**发布前逐项自查：manifest、包内容、静态 ENTRY、安装冒烟、企业侧提交 |
| 本页 | **多个包/持续发布**：四份契约、版本线、流水线、README parity、迁移波、registry 行为、回滚与 tag 门禁 |

单包发布先走 checklist；维护多个包或做批次发布时，用本页的波次与门禁；企业侧流程仍以 [`docs/plugin-authors.md`](../plugin-authors.md) 与 dsh-ee 页面为准。

## 12. 边界

- 本页是社区经验整理，不是 DSH 官方发布规范，也不是 dsh-ee 的发布政策；与官方文档/registry 实际行为冲突时，以官方与实跑为准。
- 不包含上游的家族规模、渠道营销、内部镜像与带日期的统计；这些不适用于企业文档。
- 所有命令与包管理器行为以目标版本为准；涉及企业 npm 的，以 dsh-ee 部署配置为准。

---

> 改编自 [PerryLink/dsh-primer](https://github.com/PerryLink/dsh-primer) v0.4.3（tag `v0.4.3` = commit `8750d31718c16601514e59fe1831de73cfed3d20`）`guide/release-engineering.zh-CN.md`（原文：[release-engineering.zh-CN.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/guide/release-engineering.zh-CN.md)；Apache-2.0，版权归上游 contributors），有删改；补充来源 `PerryLink/dsh-plugin-guide` v0.5.5（tag `v0.5.5` = commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`）`docs/release-checklist.md`（Apache-2.0）；dsh-ee 侧流程取自本仓库基线 `20aafbb`；不代表上游背书。同步日期 2026-11-12。
