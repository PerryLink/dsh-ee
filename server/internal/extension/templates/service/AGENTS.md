# 这是一个 dsh-ee 服务扩展（Go）

它是一个 HTTP 服务，由企业自己部署，dsh-ee 按协议调它。用的是 Go SDK `github.com/hikariming/dsh-ee/extensions/sdk-go`。

## 照这个流程做，不要跳步

1. `go get github.com/hikariming/dsh-ee/extensions/sdk-go@main && go mod tidy`。
2. 在 `main.go` 里注册工具（`ext.Tool`），需要的话再注册 `ext.Validate`、`ext.Options`、`ext.Knowledge`、`ext.OAuth`、`ext.Endpoint`。
   清单是 SDK 按你注册的东西自动生成的，不用手写。每种能力的写法看 dsh-ee 仓库的 `extensions/examples/demo-extension`。
3. 把需求写成场景测试，放在 `tests/*.yaml`（谁、调什么、应该得到什么）。先写测试，再写代码。
4. 跑 `dsh-ee extension test --run "go run ." . --json`。它会把扩展跑起来，按协议逐项测，连同你的场景测试。
   每一条失败都有 `code`、`message`、`fix`，照着改，再跑。**全部通过才算做完，不能自己宣布做完。**
5. 要手工试：一个终端 `dsh-ee extension dev`，另一个终端照它打印的命令启动扩展，再用 `dsh-ee extension call` 以某位员工的身份调。
6. `dsh-ee extension report http://127.0.0.1:9000 > DELIVERY.md` 生成交付说明，把测试输出和「请补充」的地方补上。
7. 把代码仓库（或镜像）和交付说明交给管理员。**部署和安装由人来做。**

## 规矩

- 只读的工具才标 `ReadOnly: true`；没标的一律当写操作。不要为了省掉确认把写操作标成只读。
- 调用人是 `c.User`（来自验过签的令牌）。不要让模型通过参数指定「替谁操作」。
- 出错用 `sdk.Errorf(sdk.CodeXxx, …)`：员工没授权用 `CodeAuthorize`，管理员没配好用 `CodeSetup`，源系统拒绝用 `CodeForbidden`，
  源系统挂了用 `CodeUnavailable`，参数不对用 `CodeInvalid`，东西不存在用 `CodeNotFound`。dsh-ee 靠错误码决定怎么告诉员工。
- 不要把 `c.Credential`、`c.Shared`、`c.Config` 里的密钥打进日志，也不要放进返回结果。`extension test` 会查。
- 不要在代码里写死凭据和内网地址：地址和密钥做成清单的 `Config`，由管理员安装时填。
- 知识源的 `Changes`：cursor 表示「这之前的都给过了」，要单调往前；没有新变化时返回空列表和同一个 cursor。不要把页码当 cursor。
- 不要为了让测试通过去改测试。需要真实系统的地址、测试账号、生产凭据时停下来问人。

协议见 dsh-ee 仓库的 docs/extension-protocol.md，设计见 docs/plugins.md，完整流程见技能 `dsh-ee-extension-dev`。
