---
name: dsh-ee-access-config
description: 配 dsh-ee 的权限：管理角色、扩展字段（文档等级、项目等）和按字段的访问规则，以及「关掉超级管理员、互斥角色、双人批准」这类管理约束。当用户要「加一个字段，只有某些人能看」「按等级控制谁能看文档」「把管理员分权」「为什么某人看不到某篇文档」时使用。
---

# 配 dsh-ee 的权限

dsh-ee 的权限配置（角色、管理约束、字段、规则）可以导出成一个 YAML 文件，改完先看影响、跑访问测试，再提交成变更请求，**由管理员在网页上批准后才生效**。你只负责改文件、算影响、提交；批准永远是人。

**做完的标准：访问测试全部通过、`plan` 的影响范围和需求对得上、变更请求已经提交。** 不要自己宣布生效，也不要想办法绕过批准。

## 0. 准备

- 要一个带 `config` 范围的令牌：员工在 dsh-ee 网页「我的令牌」里建（账号要有「管理字段和访问规则」或「管理角色」权限）。没有就停下来让人建，不要用别的令牌。
- `export DSH_EE_URL=https://<dsh-ee 地址> DSH_EE_TOKEN=<令牌>`，或每条命令带 `--server`、`--token`。`dsh-ee` 不在 PATH 里时写完整路径。

## 1. 先读现状

```
dsh-ee config export > access.yaml
```

看清楚已有的角色、字段、规则和 `policy`。**文件里没写的角色保持不变**（角色牵着人）；**没写的字段和规则会被删掉**（字段和规则就是这份文件说了算）。

## 2. 先把需求写成访问测试

改配置之前，把「谁应该能看到什么、谁不应该看到什么」写成测试，放在 `tests.yaml`：

```yaml
- as: zhangsan            # 用户名
  can_read: [差旅报销制度]   # 文档标题：应该能看
  cannot_read: [薪酬方案]     # 应该看不到
- as: lisi
  can: [fields.manage]    # 权限点
  cannot: [roles.manage]
- as: wangwu
  search: 报销制度          # 检索结果里
  must_contain: [差旅报销制度]
  must_not_contain: [薪酬方案]
```

用户名和文档标题要用真实存在的（写错了测试会直接报出来）；不知道就问用户，不要编。

## 3. 改配置

- **字段**：`object` 是 `document` / `kbase` / `user`；`type` 是 `text` / `number` / `date` / `select` / `tags` / `level`。`level` 的 `options` 从低到高写。用于访问控制的字段写 `access: true`。字段的类型和键定了就不能改，要换类型就新建一个字段。
- **规则**：只会在知识库原有权限之上**再收紧**，不会放宽。`op` 三选一：
  - `level_gte`：用户的等级 ≥ 文档的等级（`user_field`、`doc_field` 都是 `level` 字段，选项要一致）；
  - `intersects`：文档的值和用户的值至少有一个相同（比如项目）；
  - `contains`：用户的值在文档的值里（比如 `user_field: "@department"` 和文档的「可见部门」）。
  `user_field` 可以用内置属性 `@department`、`@username`、`@roles`。`missing` 默认 `deny`（文档没填就当最低、看不到），只有需求明确说「没填就不限制」才写 `open`。`kbases` 留空是全部知识库。
- **管理约束**（`policy`）：`super_disabled`（关掉超级管理员，关之前每个管理权限点都要有人持有）、`no_self_grant`、`exclusive`（互斥角色组）、`dual_approval`（`roles.assign`、`config`）。这些动静大，需求没明确要就不要动。
- **角色**：`permissions` 是权限点编码。不能授出你自己没有的权限点。

## 4. 看影响、跑测试

```
dsh-ee config plan access.yaml
dsh-ee config test access.yaml --tests tests.yaml
```

- `plan` 给出会变什么，以及**谁会少看到 / 多看到哪些文档、谁的权限点会变**。拿它和需求逐条核对：影响了需求以外的人或文档，就回去改配置。`plan` 报了问题（`✗`）先改到没有。
- `test` 一条不过，就用 `dsh-ee config explain --user <用户> --doc <文档 id> --config access.yaml` 看是哪一步判的，改配置，不要改测试来迁就。
- 要批量核对用 `dsh-ee config simulate --users a,b --kb <知识库标识> --config access.yaml`。

`explain` 和 `simulate` 只能问你自己看得到的文档，只返回「能不能看」和标题。

## 5. 提交

```
dsh-ee config apply access.yaml --note "一句话说明这次为什么改"
```

它生成一条变更请求，**不会直接生效**。把下面这些交给管理员，请他到「字段与访问规则 → 变更请求」里批准：

- 需求是什么，改了什么（`plan` 的「会变的」）；
- 影响谁（`plan` 的影响范围），其中最近看过、以后看不到的文档要特别说明；
- 访问测试和结果；
- 你拿不准、自己定的地方。

用 `dsh-ee config status` 看批准了没有。生效后要撤回，管理员在同一个页面上点「回滚到生效前」。

## 6. 不能做的事

- 不要批准自己提交的变更，也不要让人把令牌给你去网页上点批准。
- 不要为了让测试通过去改测试、删测试，或者把 `missing` 改成 `open`。
- 不要删掉文件里你不理解的字段和规则：没写的会被删掉，删之前先问。
- 不要动 `policy` 里需求没提的开关。把自己锁在外面时，只能由人在服务器上跑 `dsh-ee admin recover`。
