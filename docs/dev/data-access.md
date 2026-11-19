---
slug: data-access
category: access
title: "企业数据对接：给员工分权限查询"
summary: "数据集可用范围、按员工过滤行、企业连接员工身份与应用数据表的选型和配置。"
tags: [数据中台, 权限, 企业连接]
updated_at: 2026-11-19
lang: zh-CN
---

## 先分清两件事

「能用这个数据源」和「能看里面哪些数据」不是同一层权限。
给销售部开放客户数据集，不等于每个销售只能看到自己负责的客户。
前者用**可用范围**，后者要在数据集 SQL 或源系统里落实，不能只靠提示词让 AI 自觉过滤。

## 怎么选

| 数据在哪里 / 要做什么 | 接法 | 谁能用 | 行权限在哪里做 |
| --- | --- | --- | --- |
| CSV / Excel 客户清单、人员主数据，只读查询 | 数据中台：上传表 → 数据集 | 数据集「可用范围」 | SQL 用服务端自动填的 `:me_*` 参数 |
| CRM / ERP 等现成系统，有实时接口 | 企业连接（MCP / OpenAPI） | 连接「可用范围」 | 开启「员工身份」，源系统按员工鉴权 |
| 应用自己产生的报名、登记、台账 | 应用自带数据表 | 先有应用使用权限 | 表的 `visibility`：`own` / `shared` / `author` |
| 知识库文档按等级、项目等控制可读性 | 知识库权限 + 字段与访问规则 | 知识库权限 | 文档访问规则；不是数据集行权限 |

已有主数据不要再复制到每个应用的小表里；工作台直接引用数据中台或企业系统。

## 一、数据中台：范围管入口，SQL 管行

### 数据怎么进来

管理员在「企业资产 → 数据中台」上传 CSV / Excel 生成表，再把查询保存为有标识的数据集。
默认存在主库同服务器的 `<主库名>_data` 库；部署可以用 `DSH_EE_DATA_DSN` 指定其他库。
数据集是一条 `SELECT`（可以用 `WITH`），查询由只有该库 `SELECT` 权限的数据库账号执行。
若自动创建只读账号失败，需 DBA 配好 `DSH_EE_DATA_READ_DSN`，不能用高权限账号代替。

这里的只读账号防止查询写库，**不替你隔离员工能看哪些行**。
上传的数据是导入的副本，不会自动跟原 Excel 或 CRM 同步；更新时还要上传替换或追加。

实现见 `server/internal/config/config.go`、`server/internal/datahub/datahub.go`、
`server/internal/datahub/sqltext.go` 和 `server/internal/api/datahub.go`。
界面术语见 `web/src/i18n/zh/hub.ts` 的 `dataHub`。

### 第一层：可用范围

每个数据集的「可用范围」可以选全员、部门、角色或指定员工：

- 全员（`all`）开启时，不再看其余条件。
- 否则部门、角色、员工**命中任意一项就允许**，不是同时满足。
- 部门按员工的部门字符串精确匹配，不是组织树继承；选「销售部」不自动包含「销售一部」。
- 未开全员且三个列表都空时，普通员工无人命中。

同一规则用于工作台数据源 `{"dataset": "my-customers"}` 和 DSH 工具 `data__dataset`。
不在范围内时，查询返回「没有数据集 X，或你没有权限用它」，不会因为知道标识就能查。

**有 `connections.manage` 的人可以使用所有数据集，不受可用范围限制。**
不要给普通查询用户授予这个管理权限，也不要用管理员账号验证范围是否正确。
这个例外只绕过数据集范围，SQL 里的员工行过滤仍按当前调用人执行。

实现：`server/internal/store/assets.go` 的 `Scope.Allows`、
`server/internal/api/hub.go` 的 `viewer.inScope` / `manages`、
`server/internal/api/datahub.go` 的 `seesDataSet` / `findDataSet`。
工作台和 MCP 分别从 `server/internal/api/app_web.go`、`datahub.go` 调同一套查询逻辑。

### 第二层：按当前员工过滤行

数据集 SQL 可以用以下三个自动参数，服务端在每次执行时填入：

| 参数 | 值 | 常见用途 |
| --- | --- | --- |
| `:me_username` | 当前员工用户名 | 表里有负责人账号时，优先用它关联 |
| `:me_department` | 当前员工部门 | 看本部门客户、同部门同事 |
| `:me_name` | 当前员工显示名；未设置时退回用户名 | 只有负责人姓名的旧表 |

这三个参数由 `runDataSet` 从当前登录员工取值，调用者即使在 `params` 里传同名值也不能覆盖。
目前**没有**自动角色参数或自定义员工字段参数；不要自行假设 `:me_roles` 等会生效。

普通参数如 `:owner`、`:department`、`:since` 来自调用者 / AI，或数据集默认值。
它们适合日期、状态等查询条件，**不是权限边界**。
例如 `WHERE 负责人 = :owner` 不能保证只能看自己，给它默认填自己的名字也不行。

`me_name` 用的是显示名：重名可能互相看到客户，改名可能查不到旧数据。
表中能保存负责人账号时，优先改用 `me_username`，并维护员工账号和源数据的对应关系。
部门字段也要统一口径；用等号过滤时，SQL 的大小写等比较细节还受数据库列排序规则影响。
员工部门为空时，不要让空部门客户意外成为可见数据，可在 SQL 中明确排除空值。

每次查询最多返回 **1000 行**；超过时 `truncated=true`，`total` 是已返回行数，不是全表总数。
统计总量请在带相同权限条件的 SQL 中做 `COUNT` / `SUM`，不要让 AI 把截断的明细当成全量。
限制见 `server/internal/datahub/datahub.go` 的 `MaxRows` / `Query` / `scan`。

## 二、常见配置：只看自己 / 本部门的客户

以下表名、列名是示例，请换成实际上传的数据。

1. **整理员工与数据的关联。** 员工账号、部门要填对；客户表建议有 `负责人账号`、`部门` 列。
   如目前只有 `负责人` 姓名列，先核对重名和改名问题，再决定是否暂用显示名。
2. **上传主数据。** 管理员把客户 CSV / Excel 上传为 `customers`，核对列名和导入结果。
3. **新建「我负责的客户」数据集。** 标识设为 `my-customers`，只返回业务需要的列：

```sql
SELECT 客户 AS name, 年合同额 AS arr
FROM customers
WHERE 负责人账号 = :me_username
ORDER BY arr DESC
```

只有姓名列时，条件换为 `WHERE 负责人 = :me_name`。
如果业务要求看本部门客户，另建 `dept-customers`，不要让调用者自行传部门：

```sql
SELECT 客户 AS name, 年合同额 AS arr
FROM customers
WHERE :me_department <> '' AND 部门 = :me_department
ORDER BY arr DESC
```

4. **设置可用范围。** 「我的客户」开放给需要查询的销售；「本部门客户」如只供经理使用，
   就单独限制到经理角色或指定员工。不要把「部门」和「经理角色」同时勾上当成 AND 条件。
   也不要同时给销售开放一个不带过滤条件的「全部客户」数据集，否则仍能从那里查全量。
5. **试运行并保存。** 管理员「试运行」使用的是管理员自己的三个自动参数，
   不会模拟某个销售；管理员查不到客户不一定是 SQL 错。
6. **在工作台 / DSH 使用。** 工作台组件的数据源写 `{"dataset": "my-customers"}`；
   DSH 中让 AI 用 `data__dataset` 查询 `my-customers`。权限由服务端执行，不靠 AI 选择姓名。
7. **用真实普通员工验收。** 准备两名不同负责人、不同部门的测试员工和已知归属的客户，
   分别在工作台与 DSH 检查：
   - 正常：各自只返回授权行，本部门数据集只返回自己部门。
   - 空结果：没有负责客户的人返回空列表，不退回全量；无部门的人不匹配空部门客户。
   - 越权：传别人的 `me_username` / `me_department` 不改变身份；范围外账号收到无权限提示。
   - 旁路：没有其他宽范围、无行过滤的数据集或共享高权限连接能查到同一批敏感数据。

仓库种子示例见 [示例公司](https://github.com/hikariming/dsh-ee/blob/main/docs/demo-company.md)：
`my-customers` 用 `负责人 = :me_name`，`dept-colleagues` 用 `部门 = :me_department`。
它们是演示口径，不代表生产环境应该用姓名作唯一标识。

## 三、企业连接：让源系统知道是谁

MCP / OpenAPI 连接本身也有「可用范围」，控制哪些员工可以使用这个连接。
但如果只配管理员的**共享凭据**，源系统看到的是同一个服务账号，不知道实际员工是谁。
在同一工具、参数和共享凭据下，范围内员工拿到的是同一套源系统权限下的数据，
不会自动套用数据集的 `me_*` 规则。

要沿用 CRM 等系统中每个员工自己的权限，给连接启用「员工身份」：

| 模式 | 配置 / 员工动作 | 源系统要做什么 |
| --- | --- | --- |
| `ee_jwt` | 管理员配置受众、请求头及需要携带的声明 | 验证 dsh-ee 签发的短期 JWT，识别员工并检查权限；不支持时加适配层 |
| `user_secret` | 员工在「我的连接」填自己的 API token | 按这个员工 token 的权限返回数据 |
| `oauth` | 扩展配置 OAuth 客户端，员工在「我的连接」授权 | 按员工授权令牌及授权范围返回数据 |

`oauth` 当前只用于带 OAuth 客户端配置的扩展连接，不是任意手工连接填个模式就能用。
`ee_jwt` 可按连接选择携带部门、角色等声明，但**带上声明不等于自动授权**，源系统必须实施检查。
共享凭据和员工身份可以并存，请使用不同请求头；不要在源系统失败时回退为管理员全量查询。

写操作还要员工确认：DSH 有审批卡；服务端默认 `non_dsh` 对非 DSH 调用要求网页确认，
可以设为 `always` 一律服务端确认，或 `off` 关闭这层确认。确认不替代源系统的写权限校验。
参见 `server/internal/api/write_confirm.go`；`docs/plugins.md` §7.5 的历史方案请结合实现进度读。

实现与接入细节：`server/internal/api/user_identity.go`、`server/internal/api/ext_oauth.go`、
[员工身份 JWT](https://github.com/hikariming/dsh-ee/blob/main/docs/ee-jwt.md)、
[扩展凭据 §7](https://github.com/hikariming/dsh-ee/blob/main/docs/plugins.md#7-用谁的身份凭据核心)。

## 四、应用自带数据表：不是数据中台数据集

应用内的报名、登记等小表用 `{"table": "signups"}` 引用，权限由表的 `visibility` 决定：

| 值 | 普通应用使用者 | 作者和应用管理员 |
| --- | --- | --- |
| `own`（各自填写，默认） | 只能看、改自己的行，可以新增 | 看、改全部 |
| `shared`（共同维护） | 能看、改全部，可以新增 | 看、改全部 |
| `author`（作者发布） | 只读全部 | 可新增、修改全部 |

先要能使用应用，才能访问它的数据表。这里的管理员指有应用管理能力的人，
代码用 `plugins.manage` 判断；不要把 `own` 当成连作者和管理员也隔离的私密存储。
实现见 `server/internal/api/app_data.go` 的 `appTargetFor`、`canRead` / `canWrite`。
界面文案见 `web/src/i18n/zh/hub.ts` 的 `appData`。

## 五、字段与访问规则能不能直接管数据集？

**目前不能。** 扩展字段对象是 `document`、`kbase`、`user`，文档访问规则按用户与文档字段计算；
不是任意 SQL 表的列权限 / 行权限引擎，也没有给数据集绑定这些规则的入口。
见 `server/internal/api/fields.go` 的 `fieldObject` 与文档规则求值逻辑。

因此不要在「字段与访问规则」里配完部门规则，就假设所有数据集也受保护。
数据集继续用可用范围 + `me_*` SQL；企业连接交给源系统；应用小表用 `visibility`。
字段规则本身的说明见 [权限与访问规则速查](./access-rules.md) 和
[访问控制设计](https://github.com/hikariming/dsh-ee/blob/main/docs/access-control.md)。
