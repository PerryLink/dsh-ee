# 发布流程（Releasing）

> 面向发布负责人：dsh-ee 如何从 `main` 上的一个 commit 走到一个 GitHub Release，发布前检查什么、
> 产物有哪些、怎么回滚。版本号与兼容窗口的规则见 [VERSIONING.md](./VERSIONING.md)。
>
> **自动化边界**：push `v*` tag 触发 [release.yml](../.github/workflows/release.yml)，完成「校验 tag →
> 构建 Linux 静态二进制 → 校验和 / release notes / docs 版本快照 → GitHub Release」。它**不**推送任何
> 包 registry、容器 registry，也**不**部署任何环境；镜像、离线包和部署仍按 §6 手工执行。

## 1. 发布前（本地只读校验）

发布前在本地跑一遍脚本测试与只读检查（不需要数据库、不会创建 tag、不改仓库）：

```bash
# 发布/快照脚本的自测（CI 的 release validate job 也跑这三组）
node --test scripts/release/verify-tag.test.mjs \
            scripts/release/release-workflow.test.mjs \
            scripts/docs/version-snapshot.test.mjs

# 已提交的 docs 版本快照索引/哈希自洽
node scripts/docs/version-snapshot.mjs --check

# 文档相对链接、锚点；站内文档生成物与 docs/ 一致
node scripts/check-docs.mjs
node scripts/docs/build.mjs --check
```

另外确认：

- `main` 上最近一次 CI（`.github/workflows/ci.yml`）全绿；
- 版本号按 [VERSIONING.md](./VERSIONING.md) §1 选好（例如 `v0.2.0`、`v0.3.0-rc.1`）；
- 上一个版本的 Release、二进制/镜像/离线包与升级前数据库备份已归档（回滚要用）。

## 2. 用临时 tag 试跑整条发布链路（不产生真实 tag）

不创建真实 tag 也能验证「tag 校验 + docs 快照 + release notes 底稿」的逻辑：在**临时 clone** 里打一个
本地 tag（不 push），把 `--root` 指向它。

```bash
tmp="$(mktemp -d)"
git clone --no-hardlinks . "$tmp/dsh-ee"
cd "$tmp/dsh-ee"

# 只在临时 clone 里打 tag，不推送、不影响真实仓库
git tag -a v0.0.0-dryrun -m "dry run"

node scripts/release/verify-tag.mjs --tag v0.0.0-dryrun --ref main
node scripts/docs/version-snapshot.mjs --tag v0.0.0-dryrun --dry-run

rm -rf "$tmp/dsh-ee"
```

`--dry-run` 只打印将写入的文件；真实发布时由 workflow 把快照生成到 `dist/docs-versions` 作为 Release asset。
想连「真实写入 + 只读校验」一起试，可以把 `--dry-run` 换成 `--out "$tmp/docs-versions"`，然后
`node scripts/docs/version-snapshot.mjs --check --out "$tmp/docs-versions" --check-git`。

## 3. 打 tag 并触发 workflow（维护者操作）

```bash
git checkout main
git pull --ff-only
git tag -a v0.2.0 -m "dsh-ee v0.2.0"     # 注解 tag；tag 必须指向 main 上已合并的 commit
git push origin v0.2.0                    # 只推这一个 tag，触发 release workflow
```

规则与常见错误：

- tag 名必须匹配 `vMAJOR.MINOR.PATCH[-预发布]`；不支持 `1.2.3`、`v1.2` 这类名字。
- tag 的 commit 必须在 `origin/main` 上（`verify-tag --ref origin/main` 会检查）。先把发布内容合并进主干，再打 tag。
- **不要移动/覆盖已推送的 tag**。发错了就发下一个 PATCH 版本，不要 `git push -f` 去改 tag。
- 本流程不创建实际 tag 以外的任何记录；tag 推上去以后 workflow 自动跑，无需手动点按钮。

## 4. workflow 做了什么（逐步）

| 步骤 | 做什么 | 失败意味着 |
| --- | --- | --- |
| 检出 | `actions/checkout`，`fetch-depth: 0`：tag 校验、release notes 需要完整历史与 tag | clone 失败 / 权限问题 |
| 校验 tag | `node scripts/release/verify-tag.mjs --tag <tag> --sha <GITHUB_SHA> --ref origin/main`：tag 名 SemVer、tag commit 与检出/`GITHUB_SHA` 一致、tag 在 `main` 上；只读，不创建 tag | 见 `docs:` 报错，按 §9 处理；不会带着错 tag 继续构建 |
| 构建 | `make release VERSION=<tag>`：前端嵌入 + linux/amd64 与 linux/arm64 静态二进制，版本由 tag 注入 | 前端/插件构建或 Go 交叉编译失败，先本地复现 |
| 二进制版本 | `./dist/dsh-ee-linux-amd64 version` 必须等于 tag | `VERSION` 没传对 / ldflags 丢失 |
| docs 快照 | `node scripts/docs/version-snapshot.mjs --tag <tag> --out dist/docs-versions`，再 `--check --check-git` 只读校验 | tag 上的 docs 有发布集外的相对链接、或 tag 没 fetch 到 |
| 校验和 | 版本化复制二进制为 `dsh-ee-<tag>-linux-<arch>`，打包 `docs-versions-<tag>.tar.gz`，对两者生成 `SHA256SUMS` | 文件缺失/打包失败 |
| release notes | 从上个 `v*` tag 到本 tag 的非 merge 提交生成底稿，附版本策略与回滚指引链接 | 上一步的产物缺失 |
| artifact | 上传 `dist/` 为 workflow artifact（保留 14 天），便于排查 | 一般不影响 Release 创建 |
| GitHub Release | `gh release create --verify-tag`：附两个二进制、`SHA256SUMS`、`release-notes.md`、docs 快照包 | 同名 Release 已存在 / 权限不足 |

workflow 只用仓库内置的 `GITHUB_TOKEN`（`contents: write`），不需要 secrets；`validate` job 只在 PR 上跑，
只读地重复 §1 的脚本测试、快照 `--check` 与文档链接检查。

## 5. Release notes 要求

workflow 自动生成的 notes 只是**提交列表底稿**（`## 变更（vX.Y.Z..vA.B.C）`）。发布负责人在 GitHub Release
页面编辑一次 body，补齐：

1. **行为变化**：升级后用户/管理员会看到什么不同；预发布/正式通道；
2. **升级步骤**：是否需要额外操作；数据库/数据目录/`.env` 注意事项；
3. **回滚说明**：回滚到哪个版本、需要恢复备份吗、有哪些新版数据旧版读不懂；
4. **已知限制 / 弃用**：按 [VERSIONING.md](./VERSIONING.md) §4 标注弃用项与替代方案；
5. 文件校验和（`SHA256SUMS`）与 docs 快照包的下载入口。

编辑命令（不新建 Release、不改 tag）：

```bash
gh release edit v0.2.0 --notes-file dist/release-notes.md   # 或直接在网页编辑
```

## 6. 手工补充分发物（不在 release workflow 里）

workflow 刻意不发布 registry 和部署环境，以下由发布负责人/运维按需执行：

```bash
# 容器镜像（推不推内部 registry 由运维流程决定，workflow 不做）
make image VERSION=v0.2.0
docker push <内部 registry>/dsh-ee:v0.2.0

# 离线安装包（镜像 + 数据库镜像 + compose + 安装脚本 + 静态二进制）
make bundle VERSION=v0.2.0 ARCH=amd64
make bundle VERSION=v0.2.0 ARCH=arm64
# 产物在 deploy/out/，内含 SHA256SUMS；目标机器按 deploy/README.md 升级
```

- 离线包/镜像与 GitHub Release 上的二进制必须同 tag、同 commit；升级时**保留 `.env`**（主密钥/DSN 不变）。
- 分发物归档一份（上一版尤其重要），否则回滚无物可回。
- docs 快照长期归档（可选）：发布后在 `main` 上运行
  `node scripts/docs/version-snapshot.mjs --tag v0.2.0`，提交 `docs/versions/` 后跑
  `node scripts/docs/version-snapshot.mjs --check` 与 `node scripts/check-docs.mjs`，再走普通 PR。
  快照目录是生成物：不手改，历史版本只追加。

## 7. 发布 checklist

- [ ] `main` 上 CI 全绿；§1 的本地只读校验全过；版本号已按 VERSIONING 确认
- [ ] 上一个版本部署物、Release asset、升级前数据库/数据目录备份已归档
- [ ] 注解 tag 指向 `main` 上已合并 commit，已 push，release workflow 触发且全部成功
- [ ] GitHub Release assets 齐全：`dsh-ee-<tag>-linux-amd64`、`-arm64`、`SHA256SUMS`、`release-notes.md`、
      `docs-versions-<tag>.tar.gz`
- [ ] 下载二进制与 docs 快照包复核：`sha256sum -c SHA256SUMS`，`./dsh-ee-<tag>-linux-amd64 version` 输出等于 tag
- [ ] release notes 已补齐行为变化/升级步骤/回滚说明/已知限制；弃用项按 VERSIONING §4 标注
- [ ] 需要时补发镜像/离线包，并确认与 tag 同 commit；分发物已归档
- [ ] 部署验证至少覆盖：`/healthz`、`/readyz`、登录、`/v1/models` 或模型网关、npm 源（见
      [operations.md](./operations.md) §8）
- [ ] 触发一次 docs 快照归档 PR（可选）；`--check` 与 `check-docs` 通过

## 8. 回滚

发布链路本身不回滚 tag；分三层处理：

1. **产物有问题（构建/资产/notes）**：不移动 tag，修正后发下一个 PATCH 版本；需要时删除/替换 Release 上的错误
   asset 并在 notes 说明。已推送 tag 不重打。
2. **代码有问题**：在 `main` 上 revert 对应合并 commit（或提交修复），走正常 PR 合入，再按 §3 发新版本。
3. **部署回滚**：重新部署上一版二进制/镜像，保留原 `.env`；按 [operations.md](./operations.md) §5.5 确认
   「迁移不保证只增、旧版可能读不懂新数据、没有自动降级迁移」，跨大版本用升级前备份恢复。
   `ee_at_` 统一令牌在旧版可能不可用，需要改回旧令牌或前滚。

docs 版本快照是归档物：历史目录不动；要重算索引只能重新生成并让 `--check` 通过。

## 9. 常见失败与处理

| 现象 / 报错 | 原因 | 处理 |
| --- | --- | --- |
| `release: 本地没有 tag` | checkout 没拿到 tag（fetch-depth 不全）或 tag 还没 push | 确认 workflow 用 `fetch-depth: 0`；先 push tag |
| `release: 当前 checkout 的 HEAD … 不是 tag` | 触发方式不是 tag push（例如手动在分支上跑） | 用 push tag 触发；本地校验先 `git checkout <tag>` |
| `release: … 不在 origin/main 上` | tag 打在未合并的 commit/分支上 | 先合并到 `main`，再在 `main` 的 commit 打 tag |
| `release: tag「…」不是 vMAJOR.MINOR.PATCH` | tag 名不合规 | 删除本地错误 tag，换合规版本号重打（已推送的错 tag 不要覆盖，发新版本） |
| `make release` 失败 | 前端/插件构建或 Go 交叉编译失败 | 本地跑 `make release VERSION=<tag>` 复现并修 |
| `dsh-ee-linux-amd64 version` 与 tag 不一致 | `VERSION` 没传或 ldflags 丢失 | 检查 `make release VERSION=<tag>`；不要改二进制版本手工绕过 |
| `docs: … 相对链接「…」指向发布白名单之外` | tag 上的文档用了复制到快照后会断链的相对链接 | 改成仓库绝对链接，或把目标加进 `scripts/docs/manifest.json`；重新走发布（tag 不移动则发新版本） |
| `gh release create` 报已存在 | 同版本 Release 已经创建过 | `gh release view <tag>` 查看；修正后发新版本，不要复用版本号 |
| docs 快照 `--check` 失败 | 快照被手改 / 索引与目录不一致 | 用 `version-snapshot.mjs --tag <tag>` 重新生成（必要时 `--force`），再提交 |
| artifact 上传失败 | 权限/大小/action 版本问题 | 看 job 日志；`dist/` 被 `.gitignore` 忽略不影响上传 |

## 10. 相关文档

- 版本号、兼容/迁移窗口、快照口径：[VERSIONING.md](./VERSIONING.md)
- 升级、回滚、备份：[operations.md](./operations.md) §5、§6
- 部署包、镜像、`.env`、安装脚本：[deploy/README.md](../deploy/README.md)、[server/README.md](../server/README.md)
- 开发环境、测试矩阵、CI 全貌：[contributing.md](./contributing.md)
- 脚本与 workflow 本身：[verify-tag.mjs](../scripts/release/verify-tag.mjs)、
  [version-snapshot.mjs](../scripts/docs/version-snapshot.mjs)、[release.yml](../.github/workflows/release.yml)
