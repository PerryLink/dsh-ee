# dsh-ee 命令行

> 状态：按 `origin/main`（20aafbb）与 CLI PR（#122，分支代码，基线尚未合入）核对。**#122 的两个命令**（`package check`、`doctor`）标注「PR #122，合并后生效」；能力以对应 PR 合入为准（这里是 #122），其余命令在基线二进制里可用。
>
> 命令都编译在同一个 `dsh-ee` 二进制里，没有单独的 CLI 包。容器里用 `docker compose exec dsh-ee dsh-ee <子命令>`；systemd/二进制部署直接 `dsh-ee <子命令>`。

## 0. 总览

```
dsh-ee                                   启动服务（不带子命令时）
dsh-ee help | -h | --help                打印全部用法
dsh-ee version | --version               打印构建版本

dsh-ee extension init|check|test|dev|call|pack|report    写扩展（离线为主，见 §2）
dsh-ee schema list|get                                    查机器可读规格（离线）
dsh-ee config export|plan|test|apply|changes|explain|simulate
                                                          权限配置即代码（连服务端）
dsh-ee admin recover                                      应急恢复账号（在服务器上连库）
dsh-ee package check <文件.tgz>   [PR #122，合并后生效]   发布前预检（连服务端）
dsh-ee doctor                     [PR #122，合并后生效]   发布链路只读诊断（连服务端）
```

说明：

- 子命令都区分大小写；`dsh-ee help` 能看到与二进制同步的用法。
- 标「PR #122，合并后生效」的命令来自独立 CLI PR，尚未合入基线；能力以 #122 合入为准。
- **不认识的顶层子命令会被忽略并启动服务**（`runCLI` 只在匹配到已知子命令时接管），脚本里建议先 `dsh-ee version` 验证。
- 有子命令在基线二进制里不存在（`package`/`doctor`）；调用会落到「启动服务」。以 `dsh-ee help` 输出为准。

## 1. 命令矩阵

### 1.1 服务与规格

| 命令 | 作用 | 是否需要服务/数据库 | 主要参数 | 输出 |
|---|---|---|---|---|
| `dsh-ee` | 启动 HTTP 服务 | 需要数据库 | 全部来自环境变量（见 [operations.md](./operations.md) §3） | 日志到 stderr |
| `dsh-ee version` / `--version` | 打印构建版本（`-ldflags` 写入，未注入时为 `dev`） | 否 | — | 一行版本 |
| `dsh-ee schema list [--json]` | 列出内置规格名与说明 | 否（编译进二进制） | `--json` | 人读表格 / JSON 数组 |
| `dsh-ee schema get <名字>` | 打印一份规格原文 | 否 | 名字：`manifest`、`errors` | JSON |

`errors` 规格包含 `extension check` 会报的全部检查项（code、severity、含义、能不能忽略）；`manifest` 是扩展清单的 JSON Schema（仓库里 `extensions/schemas/` 是同一份，有测试保证一致）。

### 1.2 扩展命令

| 命令 | 作用 | 参数 | 退出码 |
|---|---|---|---|
| `extension init <目录> --id 作者/名字 [--kind declarative\|service]` | 在空目录生成扩展骨架（带 `AGENTS.md`） | `--id` 必填；`--kind` 默认 `declarative` | 0 成功；1 目录非空/写失败；2 用法或模板错误 |
| `extension check <目录> [--json]` | 静态检查：清单、接口文档、只读/写标记、凭据写法 | 只接受**目录**；`--json` | 0 通过；1 有错误；2 用法错误/不是目录 |
| `extension test <目录\|地址> [--run "启动命令"] [--listen 地址] [--credential 值] [--config JSON] [--write] [--json]` | 起一个模拟 dsh-ee，按协议逐项测；目录里有 `tests/*.yaml` 时一起跑场景测试 | 目录：先跑 check，服务扩展需要 `--run` 启动；地址：测已在跑的服务扩展。`--write` 会真的调用写工具（改数据） | 0 通过；1 测试失败/扩展起不来；2 用法错误 |
| `extension dev [--listen 地址]` | 起一个模拟的 dsh-ee（只有 JWKS 和预置员工），Ctrl+C 结束 | 默认监听 `127.0.0.1:7788` | 0 正常结束；1 启动失败；2 用法错误 |
| `extension call <地址> <工具> [--as 员工] [--args JSON] [--credential 值] [--config JSON] [--listen 地址]` | 以某位员工身份调一次扩展的工具 | 扩展启动时的 `DSH_EE_URL` 要指向模拟环境 | 0 HTTP 200；1 调用失败/非 200；2 用法错误 |
| `extension pack <目录> [-o 文件] [--json]` | 把**声明式**扩展打成 zip，算清单摘要和包 sha256 | 只对声明式扩展；先跑 check，不通过不打包 | 0 成功；1 check 没通过/读写出错；2 用法错误或不是声明式 |
| `extension report <目录\|地址>` | 生成给管理员看的交付说明（Markdown） | 目录或清单地址 | 0 成功且检查无错误；1 有错误/取不到清单；2 用法错误 |

补充：

- `check` 只收目录（URL 由 `test`、`report` 接受）；文档或旧版 `plugins.md` 里「`check <目录|地址>`」的写法与代码不符，以本节为准。
- `extension test`/`dev`/`call` 不连真实 dsh-ee：模拟环境是 `server/internal/extension/exttest`，只提供 JWKS 和几个预置员工（`zhangsan`、`lisi`、`wangwu`、`admin`）。
- `check` 的 `--json` 输出字段：`ok`、`identity`、`kind`、`digest`、`servers`、`credential`、`tools`、`errors`、`warnings`、`results[]`（每条含 `code`、`severity`、`where`、`message`、`fix`）。
- `test` 的 `--json` 输出字段：`ok`、`identity`、`url`、`passed`、`failed`、`skipped`、`cases[]`（每条含 `group`、`name`、`status`、`code`、`message`、`fix`）。

### 1.3 权限配置命令

`config` 把权限配置当文件改（设计见 [agent-dev.md](./agent-dev.md) §6.2）。命令只是管理接口的客户端；**批准永远在网页上由人完成**。

| 命令 | 作用 | 是否改状态 |
|---|---|---|
| `config export [--json]` | 导出当前角色、管理约束、字段、规则（默认 YAML；`--json` 拉 JSON） | 只读 |
| `config plan <文件>` | 看这份配置生效后会变什么、影响谁；不生效 | 只读（服务端试算） |
| `config test [<配置文件>] --tests <测试文件>` | 跑访问测试；不给配置文件就用现在的配置 | 只读 |
| `config apply <文件> [--note 说明]` | 提交成变更请求，等管理员在网页批准 | 写（只建请求） |
| `config changes` / `config status` | 最近的变更请求与状态 | 只读 |
| `config explain --user 用户 --doc 文档id [--config 文件]` | 某人能不能看某篇文档、经过哪些判断 | 只读 |
| `config simulate --users a,b (--kb 知识库 \| --docs 1,2) [--config 文件]` | 一张「谁能看什么」的表 | 只读 |

- 连接参数：`--server`/`DSH_EE_URL`、`--token`/`DSH_EE_TOKEN`；令牌要有 `config` scope，账号要有 `fields.manage` 或 `roles.manage`（接口按这两个权限点任一校验）。
- 所有 `config` 子命令都支持 `--json`。
- 退出码：0 成功；1 服务端返回错误、`plan` 有问题或 `test` 有用例不通过；2 用法/配置错误（含缺地址、缺令牌）。
- `config apply` 提交后打印变更请求 ID 与网页地址（`<服务地址>/admin/fields?tab=changes`）。

### 1.4 发布链路（PR #122，合并后生效）

> 以下两个命令来自**尚未合入基线**的独立 CLI PR #122；合入前二进制里没有。命令行为以 PR #122 的分支代码为准；能力以 #122 合入为准，合入后本节的「PR #122」标记可以删掉。

| 命令 | 作用 | 主要参数 | 退出码 |
|---|---|---|---|
| `package check <文件.tgz> [--server 地址] [--token 令牌] [--json]` | 把 `npm pack` 生成的 tgz 交给服务端做发布前预检（自动检查 + 发布冲突），不发布、不占版本号 | 连接参数见 §2；`--json` 原样输出服务端结果 | 0 通过；1 包有问题/有阻塞项；2 用法或本地配置错误；3 调用服务失败（网络、401/403、5xx） |
| `doctor [--server 地址] [--token 令牌] [--json]` | 只读诊断发布链路：地址/令牌来源、`/healthz` 连通与版本、令牌有效性、发布预检权限（空包探 400/403/401）、`.npmrc` 企业源与令牌一致性、企业源 `/-/ping`、本机 Node 与 dsh 版本 | 同上 | 0 没有失败项（可有提醒）；1 有失败项；2 用法错误 |

`doctor` 的 JSON 输出字段：`summary`、`ok`、`checks[]`（`id`、`status`＝`ok`/`warn`/`fail`、`title`、`detail`、`fix`）。检查项 id 依次是 `endpoint`、`server`、`token`、`publish-scope`、`npm-source`、`npmrc`、`node`、`dsh`。

`package check` 的人类可读输出包括：包名@版本与类型（普通包/DSH 插件）、自动检查通过/提醒/失败数、非 pass 项说明、发布冲突、结论（不能提交 / 进人工审核 / 可直接上架 / 可以提交）。`--json` 输出服务端字段：`name`、`version`、`is_plugin`、`can_submit`、`will_publish`、`requires_review`、`scan[]`、`conflicts[]`。

### 1.5 应急恢复

| 命令 | 作用 | 说明 |
|---|---|---|
| `admin recover` | 在**服务器上**生成一个 1 小时有效的恢复账号（有全部管理权限点，不依赖超级管理员） | 读与服务和相同的环境变量（`DSH_EE_DSN` 等），会先跑迁移；生成新用户 `recover-<8位>`，审计记 `admin.recover`（`via=cli`），到期自动停用。在能连数据库的机器上执行，用完把管理角色重新分好即弃用 |

退出码：0 成功；1 读配置/连库/迁移/生成失败；2 用法错误。

## 2. 配置来源

### 2.1 `package check` / `doctor`（PR #122，合并后生效）

按顺序找服务地址和令牌；先找到的生效，后面的不再看：

| 值 | 顺序 | 说明 |
|---|---|---|
| 服务地址 | `--server` → `DSH_EE_URL` → `.npmrc` | `.npmrc` 里取「以 `/npm` 结尾的 registry」并去掉 `/npm`（`setup.sh` 写入的形态是 `<地址>/npm/`）；多个命中时按 registry key 排序取第一个 |
| 令牌 | `--token` → `DSH_EE_TOKEN` → `$DSH_HOME/dsh-ee/token` → `.npmrc` | 令牌文件是 `setup.sh` / 接入插件在凭据库不可用时留下的；`.npmrc` 取键名含 `/npm` 的 `:_authToken`（只读，不打印令牌） |

- `.npmrc` 路径：`NPM_CONFIG_USERCONFIG` 指定的文件，否则 `~/.npmrc`。
- `$DSH_HOME` 默认 `~/.dsh`。
- 地址会去掉尾部 `/`；必须是 `http(s)://host`。`package check` 需要令牌范围 `registry:publish` 或 `agent`；`doctor` 会报告令牌是否够、快到期、`.npmrc` 与当前令牌是否一致。
- 请求头：`Authorization: Bearer <令牌>`、`X-DshEE-Client: dsh-ee-cli/<version>`。

### 2.2 `config`

只认 `--server` / `DSH_EE_URL` 和 `--token` / `DSH_EE_TOKEN`（**不读** `.npmrc`、不读 `$DSH_HOME/dsh-ee/token`）。地址缺省或不是 http(s) 时报用法错误。

### 2.3 扩展与规格命令

- `extension init/check/pack` 与 `schema`：完全离线，不读服务器配置；`extension report <目录>` 也离线，`report <地址>` 会拉清单。
- `extension test/dev/call`：在 `--listen` 地址起本地模拟环境（`dev`/`call` 默认 `127.0.0.1:7788`；`test <目录>` 自己拉起扩展时用随机空闲端口，`test <地址>` 用 `127.0.0.1:7788`）；目录方式还会给扩展进程注入 `DSH_EE_URL`、`EXTENSION_ADDR`（以及 `EXTENSION_URL`），`--run` 的命令要在该目录下能启动并监听 `EXTENSION_ADDR`。
- 真实服务地址只在 `extension test <http(s)地址>` / `extension report <地址>` / `extension call <地址>` 时出现，且扩展自身启动时的 `DSH_EE_URL` 要指向模拟环境。

### 2.4 `admin recover`

读服务端同款环境变量（`DSH_EE_DSN`、`DSH_EE_SECRET_KEY`、`DSH_EE_DB_WAIT` 等），不读 `.npmrc`。见 [operations.md](./operations.md) §10。

## 3. 退出码约定

| 码 | 含义 | 出现在 |
|---|---|---|
| 0 | 成功 | 全部命令；`check` 无错误、`test` 全过、`plan/test` 通过、`package check` 通过、`doctor` 无 fail |
| 1 | 检查发现问题，或服务端/运行期错误 | `extension check/test/report/pack`、`config plan/test/apply/…`、`admin recover`、`package check`（包问题）、`doctor`（有 fail） |
| 2 | 用法或本地配置错误 | 全部命令（参数、文件不存在、缺地址/令牌、目录不是目录…） |
| 3 | 调用服务失败 | 仅 `package check`：网络错误、401/403、服务端 5xx |

脚本里建议：

```bash
dsh-ee extension check ./my-ext --json || exit $?       # 0/1/2
dsh-ee package check ./pkg.tgz --json || case $? in     # 0/1/2/3（PR #122）
  1) echo "包或服务端拒绝";; 2) echo "用法/配置";; 3) echo "服务不可用";; esac
```

## 4. 常见用法

```bash
# 写一个声明式扩展
dsh-ee extension init ./hr-ext --id acme/hr --kind declarative
dsh-ee extension check ./hr-ext
dsh-ee extension report ./hr-ext > handoff.md
dsh-ee extension pack ./hr-ext -o hr-ext.zip

# 服务扩展：本地模拟环境里跑一遍
dsh-ee extension test ./my-ext --run "go run ." --json
dsh-ee extension dev &                    # 另一个终端
dsh-ee extension call http://127.0.0.1:9000/create_ticket --as zhangsan --args '{"title":"测试"}'

# 查规格
dsh-ee schema list
dsh-ee schema get errors | head

# 权限配置即代码
dsh-ee config export > access.yaml
dsh-ee config plan access.yaml
dsh-ee config apply access.yaml --note "新增项目字段"

# 发布链路（PR #122，合并后生效）
npm pack
dsh-ee doctor
dsh-ee package check ./my-plugin-1.2.3.tgz --json

# 应急
dsh-ee admin recover
```

延伸：[operations.md](./operations.md)（部署/升级/备份）、[troubleshooting.md](./troubleshooting.md)（错误与修复）、[plugin-authors.md](./plugin-authors.md)（插件发布）、[plugins.md](./plugins.md)（扩展设计）、[extension-protocol.md](./extension-protocol.md)（扩展协议）。
