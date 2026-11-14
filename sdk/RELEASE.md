# SDK 发布流程

本文件说明 `sdk/typescript`（npm 包 `@dsh-ee/harness-client`）和 `sdk/python`
（PyPI 包 `dsh-ee-harness`）的版本、兼容矩阵、changelog、tag、发布权限与回滚。

发布动作由 [`.github/workflows/release-sdk.yml`](../.github/workflows/release-sdk.yml) 执行：
**普通 push / PR 不会触发它，也不会自动发布**；只有 maintainer 手动 `workflow_dispatch`
或推送 `sdk-ts-v*` / `sdk-py-v*` tag 才会进入发布流程，且真正 publish 还需要凭据和
environment 审批（见 §8）。

> 当前状态：两个包都是 `0.1.0`，**尚未发布到 npm / PyPI**。本仓库只提供发布工作流和
> 发布前检查；首次发布需要 maintainer 完成 §8 的一次性配置。

## 1. 版本号

- 两个包独立按语义化版本发布：`0.x` 期间不保证 API 完全稳定，破坏性变更会在
  CHANGELOG 中标注（与各包 CHANGELOG 头部的约定一致）。
- 版本的**单一事实源**是 `sdk/typescript/package.json` 的 `version` 和
  `sdk/python/pyproject.toml` 的 `project.version`；tag、代码里的版本常量、
  CHANGELOG、COMPATIBILITY 必须和它一致，由 `scripts/sdk/verify-package.mjs` 校验。
- tag 与版本一一对应，**tag 不可移动、版本号不可复用**：
  - TypeScript：`sdk-ts-vX.Y.Z`（例如 `sdk-ts-v0.1.0`）
  - Python：`sdk-py-vX.Y.Z`（例如 `sdk-py-v0.1.0`）
- 预发布版本分别遵循各自生态：
  - npm 用 semver 后缀（例如 `0.2.0-rc.1`），工作流会把它发到 `next` dist-tag，
    不会顶掉 `latest`；
  - PyPI 用 PEP 440（例如 `0.2.0rc1`），PyPI 会把它标记为 pre-release，正常
    `pip install` 默认不会选中。
- 两个包版本不必同步；一个包发版不改另一个包的版本。

## 2. 发布前的版本 bump 清单

一次发布是一个普通 PR（合入 main 后再打 tag），PR 里要同时改：

### TypeScript（`sdk/typescript`）

- [ ] `package.json` → `version`
- [ ] `src/index.ts` → `export const SDK_VERSION = '<version>'`
- [ ] `src/mcp.ts` → `DEFAULT_CLIENT_INFO.version`
- [ ] `test/client.test.mjs` → 断言 `SDK_VERSION 是 <version>` 与 `assert.equal(SDK_VERSION, '<version>')`
- [ ] `README.md` → 状态行（是否已发布、版本号、安装示例里的 tarball 文件名）
- [ ] `CHANGELOG.md` → `## [Unreleased]` 内容改名成 `## [<version>] - <YYYY-MM-DD>`，保留新的空 `## [Unreleased]`
- [ ] `COMPATIBILITY.md` → `SDK 版本` 行；有服务端契约变化时同步第 2/3/4 节

### Python（`sdk/python`）

- [ ] `pyproject.toml` → `project.version`
- [ ] `dsh_ee_harness/__init__.py` → `__version__`
- [ ] `dsh_ee_harness/client.py` → `DEFAULT_MCP_CLIENT_INFO["version"]`
- [ ] `README.md` → 状态行（是否已发布 PyPI、版本号）
- [ ] `CHANGELOG.md` → 同 TypeScript 的 `Unreleased` → 版本小节规则
- [ ] `COMPATIBILITY.md` → `SDK 版本` 行；服务端契约变化时同步

### 仓库内其它引用

- [ ] `sdk/README.md` 顶部状态表（发布状态、版本号）
- [ ] 搜索遗漏：`grep -rn '<旧版本>' sdk --exclude-dir=node_modules --exclude-dir=dist`

这些检查脚本也在 `ci.yml` 的 `sdk` job 里运行（只读校验，不发布）；改完版本后本地再跑一遍：

```sh
node scripts/sdk/verify-package.mjs --package typescript --tag sdk-ts-vX.Y.Z
node scripts/sdk/verify-package.mjs --package python --tag sdk-py-vX.Y.Z
```

检查项包括：包元数据（name/license/engines/exports/publishConfig）、代码内版本常量、
测试断言、CHANGELOG 版本小节、COMPATIBILITY 版本行、tag 前缀与版本一致性。

## 3. 兼容矩阵

各包有独立的兼容性说明，发布时必须同步更新：

- TypeScript：[`typescript/COMPATIBILITY.md`](typescript/COMPATIBILITY.md)
- Python：[`python/COMPATIBILITY.md`](python/COMPATIBILITY.md)
- 服务端接入契约：[`../docs/l1-client.md`](../docs/l1-client.md)、
  [`../docs/harness-integration.md`](../docs/harness-integration.md)

当前矩阵（发布 PR 需更新本表）：

| SDK | 包名 | 版本 | dsh-ee API 主版本 | 依赖的服务端能力 | 运行环境 | 状态 |
|---|---|---|---|---|---|---|
| TypeScript | `@dsh-ee/harness-client` | 0.1.0 | `api_version = 1` | PR #123、#128、#129、#132 | Node >= 18 / 浏览器（需 CORS） | 未发布 npm |
| Python | `dsh-ee-harness` | 0.1.0 | `api_version = 1` | PR #123、#128、#129、#132 | Python >= 3.9（仅标准库） | 未发布 PyPI |

矩阵口径：

- SDK 对 dsh-ee 的 API **主版本**做显式校验；服务端升主版本时 SDK 以新的
  minor/major 版本发布，不静默猜测。
- 服务端新增能力优先做成可选/可探测；旧部署上缺失的能力按服务端实际响应退化，
  不因为响应头缺失就让基础端点失败。
- 写操作、工具调用不自动重试；`Retry-After` 由调用方按幂等性决定是否使用。

## 4. Changelog 规则

- `CHANGELOG.md` 的 `## [Unreleased]` 累积合入 main 但未发布的变化；
  发布 PR 把它改成 `## [<version>] - <YYYY-MM-DD>`，并保留新的空 `## [Unreleased]`。
- 已发布小节只允许补充描述性/链接修正，不改变语义；版本号不可回填。
- 破坏性变更单独标记（`### Breaking changes`），0.x 也照写。
- 服务端契约相关变化要引用对应 PR 号（例如 `PR #128`），方便和 COMPATIBILITY 对齐。

## 5. 本地发布前检查（dry run，不产生副作用）

```sh
# 1) 发布脚本与工作流自检（零依赖，不需要 npm install）
node --test scripts/sdk/*.test.mjs
node scripts/sdk/verify-release-workflow.mjs

# 2) TypeScript：安装 / 类型检查 / 测试 / 打包 / 校验 tarball
cd sdk/typescript
npm ci --no-audit --no-fund
npm run typecheck
npm test                                   # 构建 + node:test，全部 mock fetch
mkdir -p ../../dist/sdk-ts
npm pack --pack-destination ../../dist/sdk-ts   # 只生成 tarball，不发布
node ../../scripts/sdk/verify-package.mjs --package typescript --dist ../../dist/sdk-ts

# 3) Python：测试 / 构建 sdist+wheel / 校验产物
cd ../python
python3 -m unittest discover -s tests -v
python3 -m build --outdir ../../dist/sdk-python   # 需要 `pip install build`
node ../../scripts/sdk/verify-package.mjs --package python --dist ../../dist/sdk-python
```

`verify-package.mjs` 会检查：

- npm：`name/version/license/engines/exports/publishConfig/files/prepack`；tarball 里
  必须包含 `dist/index.js`、`dist/index.d.ts`、README、LICENSE、CHANGELOG、
  COMPATIBILITY，不得包含 `src/`、`test/`、`node_modules/`、未编译的 `.ts`；
- PyPI：`project.name/version/requires-python/license/readme/dependencies/urls`；
  wheel 的 `METADATA`（Name/Version/Requires-Python/License/Summary）、文件清单
  （必须含 `dsh_ee_harness/__init__.py`、`client.py`，不得含 `tests/`、`.pyc`）；
  sdist 顶层目录名、`PKG-INFO` 与文件清单；
- 两个包共同的：代码内版本常量、测试断言、CHANGELOG/COMPATIBILITY 里的版本、
  `--tag` 与包版本的一致性。

手动 dry run 工作流（GitHub UI）：

1. 打开 Actions → `release-sdk` → **Run workflow**；
2. 保持 `dry_run: true`（默认），必要时选 `package`；
3. 跑完后检查 preflight 的发布计划摘要、两个 build job 的 artifact 与
   `verify-package` 输出；**dry-run 不会执行 publish**。

## 6. 发布工作流

文件：`.github/workflows/release-sdk.yml`。

| 触发 | 行为 |
|---|---|
| `workflow_dispatch`（默认 `dry_run: true`） | 构建 / 测试 / 打包 / 校验 + 上传 artifact；不发布 |
| `workflow_dispatch` + `dry_run: false` | 通过 preflight 后进入 publish（受 environment 审批与凭据约束） |
| push tag `sdk-ts-v*` | 非 dry-run；构建 TypeScript 包并尝试发布到 npm |
| push tag `sdk-py-v*` | 非 dry-run；构建 Python 包并尝试发布到 PyPI |
| 普通 push / PR / `ci.yml` | **不会触发本工作流，也不会发布** |

job 结构：

| job | 做什么 | 产物 |
|---|---|---|
| `preflight` | 解析工作流 YAML、校验脚本和包元数据、计算发布计划、校验 tag 与版本 | job 输出（typescript/python/publish/npm_token/tag/version/npm_tag）+ step summary |
| `typescript-sdk` | `npm ci` / `typecheck` / `test` / `npm pack` / `verify-package --dist` | `sdk-ts-package`（tarball，保留 14 天） |
| `python-sdk` | `unittest` / `compileall` / `python -m build` / `verify-package --dist` | `sdk-py-package`（wheel + sdist，保留 14 天） |
| `publish-npm` | 下载 tarball、再次校验、`npm publish --provenance` | npm 上的新版本 |
| `publish-pypi` | 下载 sdist/wheel、再次校验、`pypa/gh-action-pypi-publish` | PyPI 上的新版本 |

publish 的两道门：

1. **非 dry-run**：只有 tag push 或显式 `dry_run: false` 手动触发才会 `publish=true`；
2. **凭据/信任发布已配置**：
   - npm：需要 secrets `NPM_TOKEN`（repository/组织级 secret）。未配置时 preflight
     输出 `npm_token=false`，`publish-npm` 直接跳过并在 step summary 里告警——
     **不会**在仓库里硬编码或伪造凭据；
   - PyPI：使用 trusted publisher（OIDC，`id-token: write`），不需要 PyPI token secret；
     前提是 maintainer 已在 PyPI 侧配置好 trusted publisher（见 §8）。未配置时上传会在
     上传前失败，不会产生半发布状态。

此外：

- publish job 都绑定 `environment`（`npm` / `pypi`），可以把 required reviewers 配成
  最后一道人工审批门；
- 默认权限只有 `contents: read`，`id-token: write` 只加在 publish job；
- npm 发布带 `--provenance`（npm 官方 provenance 证明包由本仓库该 workflow 构建）；
- 同一个 ref 的发布有 concurrency 串行控制，避免同一 tag 重复执行。

## 7. 发布步骤（tag 发布）

```sh
# 0) 前置：发布 PR 已合入 main，CI 绿，版本 bump 完成
git switch main
git pull --ff-only

# 1) 打 tag（tag 版本必须等于包版本，否则 preflight 失败且不会发布）
git tag -a sdk-ts-vX.Y.Z -m "@dsh-ee/harness-client X.Y.Z"
git tag -a sdk-py-vX.Y.Z -m "dsh-ee-harness X.Y.Z"   # 两个包独立，按需打
git push origin sdk-ts-vX.Y.Z                        # 或用 Personal Access Token/SSH 推送

# 2) 观察 Actions → release-sdk：
#    preflight → typescript-sdk/python-sdk → publish-npm/publish-pypi（environment 审批）
# 3) 发布后核对 registry：
npm view @dsh-ee/harness-client@X.Y.Z version dist.integrity repository.url
#    PyPI 页面/JSON API 核对 dsh-ee-harness X.Y.Z 与 sdist/wheel 文件
# 4) 记录：workflow run URL、step summary 里的 sha256、发布后的版本号
```

注意：

- **tag 用 `GITHUB_TOKEN` 推送不会触发 workflow**（GitHub 的限制），tag push 必须由
  维护者的个人凭据完成；
- tag push 也会触发现有 `ci.yml`（其 `on: push` 覆盖所有分支/tag），这是正常的回归
  验证，不代表 release 工作流被普通 CI 触发；
- tag 与版本不一致时 preflight 直接失败：不要移动/重打已经存在的 tag，改版本后按
  新版本重来；已发布的版本不能复用。

## 8. 发布权限与一次性配置（maintainer 清单）

### npm（首次发布前）

- [ ] 有 npm 组织/作用域 `@dsh-ee` 的管理权限（`npm org ls dsh-ee`；没有就
      `npm org create dsh-ee` 并把 maintainer 加为 `developer`/`admin`），确认 scoped
      包名 `@dsh-ee/harness-client` 可发布；
- [ ] 创建 **Granular Access Token**：只授予 `@dsh-ee/harness-client` 的 read/write、
      勾选 2FA bypass（automation）并设最短有效期；不要用 legacy token；
- [ ] 仓库 Settings → Secrets and variables → Actions：新增 `NPM_TOKEN`
      （必须是 **repository/组织级 secret**；preflight 没有绑定 environment，
      environment 级 secret 在那里读不到，会导致发布被跳过）；
- [ ] Settings → Environments：新建 `npm`，配置 required reviewers（至少 1 名 maintainer）；
- [ ] 首次发布后检查 npm 页面：scope、license、repository、provenance 标识正常。

### PyPI（首次发布前）

- [ ] PyPI 账号开启 2FA（PyPI 对上传强制）；
- [ ] 在 PyPI 配置 trusted publisher（新项目用 **pending publisher**）：
      Owner = 仓库 owner、Repository = `dsh-ee`、Workflow name = `release-sdk.yml`、
      Environment name = `pypi`（必须与工作流里的 `environment: pypi` 逐字一致）；
- [ ] 仓库 Settings → Environments：新建 `pypi`，配置 required reviewers；
- [ ] 不需要、也不要创建带上传权限的长期 PyPI token secret（工作流用 OIDC）；
- [ ] 首次发布后补全 PyPI 项目 metadata/维护者。

### 仓库（一次性）

- [ ] 保护 `main`（PR + CI 必需）；限制谁能创建/推送 `sdk-*` tag；
- [ ] 确认 `release-sdk.yml` 已在默认分支：手动 `workflow_dispatch` 只能选默认分支上的
      workflow，tag push 则使用 tag 指向的提交里的 workflow 文件（所以先把发布 PR 合进 main）；
- [ ] 只有 maintainer 能审批 `npm` / `pypi` environment（即发布权限集中在这里）；
- [ ] 如需公告渠道/发布窗口，按团队约定执行（本仓库不强制）。

### 权限口径

- 普通贡献者：只能提 PR，不能推 `sdk-*` tag、不能审批 environment；
- maintainer：可合入发布 PR、打 tag、审批 environment；
- 发布用的 token/trusted publisher 只存在于 GitHub secrets / PyPI 侧，仓库文件中
  不出现任何凭据（`verify-release-workflow.mjs` 会检查这一点）。

## 9. 回滚 / 撤回

发布事故的顺序：**先止损（deprecate/yank）→ 再发 patch → 最后排查根因**，并保留
workflow run、artifact sha256 作为证据。

### 尚未真正 publish

- dry run / build job 失败：没有副作用，修好后重跑；
- publish job 在 environment 审批阶段取消：同样没有副作用。

### npm 已发布

- 优先 **deprecate**（保留版本，提示升级）：

  ```sh
  npm deprecate @dsh-ee/harness-client@X.Y.Z "发布有误，请升级到 X.Y.(Z+1)"
  ```

- `latest` 指错版本时回退 dist-tag（不删除已发布版本）：

  ```sh
  npm dist-tag add @dsh-ee/harness-client@X.Y.(Z-1) latest
  ```

- 72 小时内且满足 npm 撤回政策时可以 `npm unpublish @dsh-ee/harness-client@X.Y.Z`，
  但会让依赖该版本的 lockfile 失效；**默认不用**，用 deprecate + patch 替代；
- 修复后按新版本重新发版，不能把同一个版本号重新 publish（registry 会拒绝）。

### PyPI 已发布

- PyPI 不支持删除/覆盖同版本文件；在项目 release 页面 **Yank** 该版本，
  `pip` 默认会跳过被 yank 的版本（显式精确 pin 时才会装）；必要时 yank 整个 release；
- Yank 后按新版本发 patch；不要尝试复用版本号。

### 代码回滚

- 对发布 PR 做 revert，并更新 CHANGELOG/COMPATIBILITY，然后按**新版本**发布；
- 已存在的 tag 不移动、不删除后重打（否则制品来源不可追溯）；
- 若回滚涉及服务端契约（API 主版本变化），按 COMPATIBILITY 的兼容策略处理：
  SDK 以新版本显式声明支持的主版本，不静默猜测。

## 10. 故障排查

| 现象 | 可能原因 | 处理 |
|---|---|---|
| 手动触发没发布 | `dry_run` 默认 `true` | 确认 summary 里 `publish=否`；要真实发布需显式 `dry_run: false` 并满足凭据条件 |
| `publish-npm` 被跳过 | 未配置 `NPM_TOKEN` | 配 secret 后重跑；不要在代码里硬编码 token |
| npm 401/403/OTP | granular token 权限不足或没开 2FA bypass | 重新生成 token，限定到 `@dsh-ee/harness-client` |
| npm `E403` version already published | 版本号已存在 | bump 版本，不能复用 |
| npm 预发布报 “must specify a tag” | 预发布版本不能用默认 `latest` | 工作流已按版本自动选 `latest`/`next`；手动 publish 时要显式 `--tag` |
| PyPI 上传失败（trusted publisher） | owner/repo/workflow/environment 与 PyPI 配置不一致 | 逐字核对 §8 的四项；特别是 `environment: pypi` |
| provenance 失败 | 仓库非 public / 缺 `id-token: write` / workflow 不在默认分支 | 先确认这三项；不要为了绕过而删 `--provenance`，需要改动就走 PR 评审 |
| `verify-package` 失败 | 版本常量/测试断言/CHANGELOG/COMPATIBILITY/tag 不一致 | 按 `✗` 提示修；tag 不匹配时先修版本再重新打 tag |
| tag push 没触发 workflow | tag 名不匹配 `sdk-ts-v*`/`sdk-py-v*`；或 tag 用 `GITHUB_TOKEN` 推送；或 workflow 不在默认分支 | 用个人凭据重推正确 tag（先删除错误 tag 再重建） |
| 同一 tag 重复执行 | 重跑 workflow 或并发的旧 run | 先确认没有 in-flight run；重跑 publish 会因版本已存在失败（这是保护） |

## 11. 非目标（边界）

- 工作流不创建 GitHub Release、不自动改 README/CHANGELOG（发布 PR 由人维护）；
- 不自动化 npm org / PyPI 账号、profile、2FA 配置；
- 不发布服务端二进制/镜像（那走 `make release` / `deploy/` 的独立流程）；
- 不做跨语言版本强同步：TypeScript 与 Python 可以按需独立发布；
- 不使用长期 PyPI token，不在仓库文件、日志或 step summary 中写任何凭据。
