# 钉钉登录与机器人对话

员工可以在钉钉里私聊企业内部应用机器人。**消息经 dsh-ee 服务端转给员工电脑上已连接的 DSH**，不是钉钉直连电脑，也不是 dsh-ee 云端代跑模型。DSH 用当前员工的模型和工具权限处理消息，答案发回同一个机器人私聊。

## 管理员：一次配置

1. 在[钉钉开放平台](https://open-dev.dingtalk.com/)新建**企业内部应用**，取得 Client ID（AppKey）、Client Secret（AppSecret）和企业 CorpId。
2. 在 dsh-ee 「系统设置 → 登录方式 → 企业统一登录」选择**钉钉**，填以上三项并开启统一登录。复制页面显示的 OAuth 回调地址（`https://<dsh-ee>/api/auth/oidc/callback`），在钉钉「钉钉登录与分享」登记。使用员工浏览器能访问的 dsh-ee 地址。
3. 给应用申请 **通讯录个人信息读权限（Contact.User.Read）** 和 **成员信息读权限**，设置应用可见范围，发布应用。无需获取手机号。dsh-ee 的「测试连接」只验证应用凭据；再用一个实际员工账号试登录可验证回调和权限。
4. 给同一个应用添加**机器人**，接收消息选 **Stream 模式**，申请**企业内机器人发送消息权限**，发布。dsh-ee 需要向钉钉发出 HTTPS 和 WebSocket 连接，**无需公网机器人回调**。OAuth 浏览器回调仍然需要。
5. 在 dsh-ee 打开「**钉钉机器人对话**」并保存即可。默认关闭；关闭后机器人停止收发，钉钉登录仍可使用。

钉钉和飞书都是当前企业统一登录配置的预设，不能同时启用。切换预设不会把原有账号或其 DSH 令牌转换成钉钉身份。当地管理员账号可继续用原密码；钉钉不提供已验证邮箱声明，不能与「要求邮箱已验证」同时启用。

## 员工：登录一次，连接 DSH

1. 在 dsh-ee 登录页选「用钉钉登录」，授权时选管理员配置的企业。首次成功登录会绑定钉钉员工身份。默认允许首登建号；关掉自动建号时，由管理员预建同名企业统一登录账号。绑定不通过昵称、邮箱或手机号匹配已有本地、LDAP 或飞书账号。
2. 按 dsh-ee 的「连接 DSH」指引，用**同一个钉钉登录的账号**连接电脑上的 DSH，并使用含钉钉对话的新版接入插件。「个人账号」中可查看钉钉绑定和 DSH 状态。
3. 在钉钉私聊机器人并发文字。群聊不会转给 DSH。不在线时，队列保留消息一小时；一小时内打开 DSH 可继续处理。未绑定或消息不是文字时，机器人会提示。DSH 模型发送答复失败或没有调用回复工具时，插件尝试发送状态提示。

## 数据与验证

- 授权码经钉钉服务器换取用户凭据；只有该企业的内部员工可以绑定。服务端将登录身份解析成 `dingtalk:<CorpId>:<userId>`，用它与 Stream 私聊中的企业员工 ID 匹配，不信任显示名。
- 只有该员工的 Agent 令牌能消费及回复自己的消息。消息按应用和消息 ID 去重，未回复的投递十分钟后可重试，队列一小时过期并定时清理。审计记录事件但不写聊天内容。
- 回复使用钉钉「批量发送人与机器人会话中机器人消息」API，发送到同一机器人私聊，不伪装为用户，也不把会话 Webhook URL 交给插件。

```sh
cd server
DSH_EE_TEST_DSN='root:@tcp(127.0.0.1:4000)/' go test -v -count=1 ./internal/api -run '^TestDingTalkLoginAndBotChat$'
```

此测试使用真实 dsh-ee、TiDB 和 MCP，模拟钉钉 OAuth/Stream/OpenAPI 及已连接 DSH。输出包含从钉钉私聊到 DSH，再返回钉钉的往返实录。

钉钉官方文档：[网页登录](https://open.dingtalk.com/document/orgapp/tutorial-obtaining-user-personal-information)、[根据 unionId 获取 userId](https://open.dingtalk.com/document/orgapp/query-a-user-by-the-union-id)、[Stream 协议](https://open.dingtalk.com/document/direction/stream-mode-protocol-access-description)、[机器人接收消息](https://open.dingtalk.com/document/orgapp/robot-receive-message)、[机器人单聊发送](https://open.dingtalk.com/document/orgapp/chatbots-send-one-on-one-chat-messages-in-batches)。
