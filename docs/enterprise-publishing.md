# 企业发布、审核与权限（管理员视角）

> 面向企业管理员和审核员：DSH 插件或企业能力从谁发布、卡在哪一步、谁能批、员工最终怎么拿到；扩展安装为什么还要管理员确认。规则以代码为准（`server/internal/registry`、`server/internal/api`）；作者视角见 [plugin-authors.md](./plugin-authors.md)，数据字段权限见 [access-control.md](./access-control.md)。
>
> 本文写当前版本（`dsh-ee@20aafbb`）已经实现的行为；签名、企业目录、自动升级/回滚等还没实现，见 §8。

## 1. 两种来源

| 来源 | 怎么进来 | 谁发布 | 审核 |
|---|---|---|---|
| 上游镜像 | 管理员在「上游预载」里同步 npm 上的插件（连同依赖闭包） | 上游作者 | 手动同步默认进待审核；开启「新版本同步 = auto」时同步即上架（`Upstream.AutoSync`，默认 `review`） |
| 企业托管 | 作者 `npm publish` 到 dsh-ee 的企业 npm 源，或网页「分享插件」上传 `.tgz` | 企业内作者 | 默认「白名单」模式：发布后待审核，管理员通过才上架 |

企业作用域默认 `@company`，可以加多个内部作用域（`Registry.ExtraScopes`）；企业作用域里的包不允许从上游预载（避免覆盖内部包）。

## 2. 发布前的自动检查

发布前作者可以先预检：`POST /api/me/packages/check`（网页「分享插件 → 网页上传」里点「预检」）。预检只读、不写库、不占版本号，返回：

- **自动检查**：`bundle`（`dsh.bundle` / patch 文件）、`install-scripts`、`secrets`（疑似密钥）、`license`、`engines.dsh`、`dependencies`、`size` 等，分 `fail` / `warn` / `info`；
- **发布冲突**：包名、版本号、企业作用域、同名上游包、归属、版本占用；
- **结论**：`can_submit`（没有硬冲突，发布接口会接受）、`will_publish`（开放收录模式下检查无 fail 项，发布即上架）、`requires_review`（提交后进审核）。

发布（`npm publish` 或网页上传）时服务端会再跑一遍同样的检查并存成审核页上的扫描结果；`secrets` fail、bundle 缺失这类硬问题不要绕过，审核也过不了。

## 3. 审核与状态

插件版本有四种状态（`server/internal/store/registry.go`）：`pending`（待审核）、`published`（已上架）、`rejected`（被驳回）、`unpublished`（已下架）。

| 动作 | 允许的状态 → 目标 | 要求 |
|---|---|---|
| `approve` 通过 | `pending` → `published` | 要指定版本 |
| `reject` 驳回 | `pending` → `rejected` | 要指定版本，且**必须填原因** |
| `unpublish` 下架 | `published` / `pending` → `unpublished` | **必须填原因** |
| `restore` 恢复 | `unpublished` / `rejected` → `published` | 整包恢复只恢复「上过架」的版本；被驳回、没审过的版本要逐个恢复 |

- 审核入口：`GET/POST /api/admin/review`（需要 `plugins.manage`）；待审核队列里能看到发布者、扫描结果、版本说明和上一版本，可下载 tarball 检查内容（`GET /api/admin/tarball`）。
- 版本号发布后不能复用，包括被驳回、被下架的版本；修完请升版本号。
- `review.approve`、`review.reject`、`package.status` 都写审计日志。
- 同一次审核还会往飞书推结果卡片（配置了飞书时）。

## 4. 上架后的可用范围与下发

在「能力管理」里给每个插件设置策略（`PUT /api/admin/plugin-policies/{name}`）：

| 项 | 取值 | 作用 |
|---|---|---|
| 可用范围 | 部门 / 角色 / 人（`Scope`），默认全公司 | 控制这条插件对谁「可见、可自选安装」 |
| 下发方式 | `optional`（自选，默认）/ `auto`（自动安装） | `auto` 且在范围内 → 员工 DSH 通过 `/api/dsh/catalog` 的必装插件自动安装；也被「启用的微应用」依赖的必装插件带进来 |

`/index/dsh-index.json` 只列已上架插件的元数据（公开、不需要令牌）；员工最终能不能装上，还受企业 npm 源读权限（`registry.use`）和 DSH 自己的安装策略影响。插件内容本身不改，改的是「给谁、怎么装」。

## 5. 权限点与角色

发布 / 审核 / 权限这条链上的权限点（完整目录在 `server/internal/rbac/rbac.go` 的 `Catalog`）：

| 权限点 | 初始归属 | 能做什么 |
|---|---|---|
| `registry.publish` | 员工角色 `member` | 发布插件、技能、提示词与微应用（发布后进审核），申请引入社区插件 |
| `plugins.manage` | 超级管理员（内置 `admin` 角色） | 审核、上下架、可用范围与下发方式 |
| `registry.manage` | 超级管理员 | npm 源路由、上游、访问令牌 |
| `upstream.manage` | 超级管理员 | 上游预载的在线同步与离线导入 |
| `connections.manage` | 超级管理员 | 企业连接与扩展的安装 / 升级 / 卸载、凭据 |
| `distribution.manage` | 超级管理员 | DSH 本体的分发 |
| `fields.manage` | 超级管理员 | 扩展字段、访问规则、用于访问控制的字段值 |
| `audit.view` | 超级管理员 | 审计日志（发布、审核、权限、设置等操作） |
| `keys.manage` / `quotas.manage` | 超级管理员 | 令牌与额度 |

超级管理员的权限可以拆给自建的管理员角色，实现分权（见下面「管理约束」）。

- 角色在「角色与权限」里配；权限点可以做细粒度分工（例如只给「审核」不给「连接管理」）。
- **管理约束**（[access-control.md](./access-control.md) §3）：可以关掉超级管理员、设置互斥角色、禁止给自己授权、让「授管理角色 / 改权限配置」走双人批准。开了 `config` 双人批准后，改角色、字段、规则和这些约束要提交配置变更请求，由另一位管理员批准；通过配置令牌（`dsh-ee config apply`）提交的变更不能自己批准。
- 令牌 scope 只是上限，和管理权限求交；`admin` scope 只对开启管理模式（`admin_until > now`）的令牌生效。

## 6. 扩展的安装、审核与权限

扩展走的是「企业连接」这条线，权限点和插件分开：

1. **谁能装**：`connections.manage`（管理员）。员工能拿到「开发扩展」的说明（`extensions.develop`），但不能安装。
2. **装之前看计划**：`POST /api/admin/extensions/check` 只读检查并生成安装计划，列出：身份与版本、请求会发往哪些地址、凭据模式与「会交给扩展什么」、反向权限（`kb` / `approval` / `audit` / `notify` / `endpoint`）、工具与写操作、附带应用、`minVersion`。
3. **哪些变化必须再确认**（`confirm:true` 才安装）：服务地址变化、新增反向权限、同版本号但清单/接口文档内容变了、凭据模式变化、新增允许请求的地址。
4. **新工具默认不启用**：新增加的写操作工具、只读→写的变化，会先停用或要求管理员确认；应用里非只读工具默认关闭。
5. **装上之后**：同步工具清单、审计 `extension.install`（带 `id@version#摘要前 12 位`）、每小时健康检查；扩展在跑的服务上换了清单只记下来，等管理员重新安装确认，不会自动生效。
6. **扩展带的字段**：清单 `fields` 声明的字段在安装时创建，以扩展 id 作前缀（如 `acme/oa.project`），和手工字段、别的扩展的字段不会冲突。目前没有单独的「卸载扩展」入口；删除连接不会级联删字段和值，字段会留下来由管理员决定删不删（这样访问规则不会因为卸载突然失效）。
7. **端侧扩展是弱保证**：跑在员工电脑上，本机进程可以「不问不报」；要求强审计的企业应只用声明式 / 服务扩展。

## 7. 审计与留痕

管理动作都写审计日志（「审计日志」页，`audit.view` 可看）：审核通过/驳回、上下架、扩展安装/升级、字段与规则变更、字段值修改、令牌与权限、设置变更。调用侧另记连接调用记录（扩展版本、工具、凭据模式、耗时、成败；不记返回内容）。审核、安装的审计不能替代对包内容的人工检查——自动检查只覆盖静态项和已知模式。

## 8. 当前未实现 / 边界

- **没有签名与目录审核**：`extension sign` / `extension verify` 不存在，安装不校验签名；不要对外声称「已签名 / 已认证」。
- **没有企业扩展目录、自动升级、一键回滚**：声明式扩展升级只能再装一次；旧版本文件要自己留。
- **不做评分 / 认证 / 排名**：dsh-ee 的「发布预检（静态）」不等于 ENTRY / FLOW / BOOST，也不等于安全审计；详见 [plugin-quality.md](./plugin-quality.md)。
- **开放收录模式仍需人工兜底**：`open` 模式下无 fail 项即上架，但 `secrets`、bundle 缺失这类 fail 项仍会进待审核。
- **上游镜像不改变包内容**：只做依赖闭包镜像，不审代码；上游包的安全由作者和上游生态负责。
