# 这是一个 dsh-ee 声明式扩展

目录里只有清单 `dsh-ee.extension.json` 和接口文档 `openapi.yaml`，没有代码。dsh-ee 读它们、自己发请求。

## 照这个流程做，不要跳步

1. 把 `openapi.yaml` 换成对方系统真实的接口文档（只留需要的接口；每个接口要有 `operationId`，它就是工具名）。
2. 改清单：`servers`（请求只会发往这里列出的地址）、`credential`（用谁的身份）、`tools`（对接口的补充）。
   字段名不要凭记忆写，用 `dsh-ee schema get manifest` 查。
3. 跑 `dsh-ee extension check . --json`。每一条结果都有 `where`（位置）和 `fix`（改法），照着改，再跑，直到 `ok` 为 `true`。
4. `dsh-ee extension pack .` 打成 zip；`dsh-ee extension report . > DELIVERY.md` 生成交付说明，把「请补充」的地方补上。
5. 把 zip 和交付说明交给管理员。**安装由管理员在网页上做，不要自己装。**

## 规矩

- 只读的工具才标 `"readOnly": true`；没标的一律当写操作，调用前要员工确认。不要为了省掉确认把写操作标成只读。
- 对方系统按人鉴权时用 `ee_jwt`（对方能验 JWT）或 `user_secret`（员工各有 token），不要用 `shared`。
- 该由搭应用的人或管理员定的参数（项目编号、组织 id 之类）用 `"fill": "app"` / `"admin"`，不要交给模型填。
- 返回很大的接口用 `keep` 只保留需要的字段。
- 清单里不要写任何凭据。需要真实地址、测试账号时停下来问人。

完整说明见技能 `dsh-ee-extension-dev`，设计见 dsh-ee 仓库的 docs/plugins.md。
