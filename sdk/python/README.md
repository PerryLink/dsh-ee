# dsh-ee Harness Python 客户端（最小实现）

dsh-ee 的第三方 Harness 最小 Python 客户端，**仅标准库**（`urllib`），无需安装依赖。
与 [`../typescript`](../typescript) 是同一套 dsh-ee L1 契约的两种语言实现；TypeScript 版
覆盖流式 chat、SSE 响应、ETag 细节更完整，Python 版用于脚本/自动化快速接入。

> 状态：`0.1.0`，未发布 PyPI。这是通用 HTTP/MCP 客户端，不实现 Anthropic Messages /
> OpenAI Responses 协议，也不声称兼容 Claude Code / Codex 的专有协议。

## 覆盖范围

| 能力 | 方法 |
|---|---|
| 服务发现（ETag 缓存） | `client.discovery(refresh=False)` |
| 能力摘要 | `client.capabilities()` |
| 模型目录 | `client.list_models()` |
| 非流式 chat completion | `client.chat_completion(model, messages, **params)` |
| 设备码登录 | `request_device_code()` / `poll_device_token()` / `wait_for_device_token()` / `login_with_device_code()` |
| MCP | `connect_mcp()` / `list_tools()` / `list_all_tools()` / `call_tool()` / `close()` |
| 写确认 | `extract_confirmation(result)` / `is_pending_confirmation(result)` |
| 错误与限流头 | `DshClientError`、`DshJsonRpcError`、`DeviceFlowError`、`parse_rate_limit_headers()`、`parse_retry_after()` |

依赖的服务端契约与 TypeScript 版相同：发现/能力/API 版本头（#123）、MCP 分页与写确认
`_meta`（#128）、限流与 `X-Request-Id`（#129）、令牌 `client_kind`（#132）。

## 安装

```sh
# 从本仓库直接使用（推荐先设置 PYTHONPATH 或 pip install -e）
cd sdk/python
python3 -m pip install -e .

# 或只跑测试（不需要安装）
cd sdk/python
python3 -m unittest discover -s tests -v
```

要求 Python >= 3.9。

## 快速开始

```python
from dsh_ee_harness import DshClient

client = DshClient("https://dsh-ee.example.com", token="ee_at_...")

print(client.discovery()["version"])
print([model["id"] for model in client.list_models()])

completion = client.chat_completion(
    "company-chat",
    [{"role": "user", "content": "ping"}],
)
print(completion["choices"][0]["message"]["content"])
```

## 设备码登录

```python
from dsh_ee_harness import DeviceFlowError, DshClient

client = DshClient("https://dsh-ee.example.com")
token = client.login_with_device_code(
    client_name="my-harness",
    on_code=lambda code: print("请打开", code["verification_uri_complete"]),
    on_pending=lambda code, attempt: print("等待确认:", code),
)
# 登录成功后 client 已带令牌
print(token["user"]["username"], token["expires_at"])
```

`wait_for_device_token()` 会处理 `authorization_pending`、`slow_down`（自动 +5 秒）与
`rate_limited`；`access_denied` / `expired_token` 立即抛 `DeviceFlowError`。

## MCP 工具

```python
from dsh_ee_harness import extract_confirmation

session = client.connect_mcp()
print(session.server_info, session.session_id)

tools = session.list_all_tools()          # 自动跟随 nextCursor 取全量
page = session.list_tools(limit=50)       # 单页；limit=0 取全量
everything = session.list_tools(limit=0)

result = session.call_tool("crm__lookup", {"q": "张三"})
if result.get("isError"):
    confirmation = extract_confirmation(result)
    if confirmation:
        print("请在网页确认:", confirmation["url"], confirmation["expires_at"])
        # 用户确认后，用完全相同的参数再调一次才会真正执行
    else:
        print("工具返回错误:", result["content"])
session.close()
```

## 错误与限流

```python
from dsh_ee_harness import DshClientError, DshJsonRpcError

try:
    client.list_models()
except (DshClientError, DshJsonRpcError) as error:
    print(error.status, error.request_id, error)
    if error.is_rate_limited:
        wait = error.retry_after or 60
        print(f"{wait}s 后重试")
```

`DshClientError` 带 `status` / `request_id` / `api_version` / `rate_limit` / `retry_after` /
`code` / `error_type` / `body`；JSON-RPC 错误另有 `rpc_code` / `rpc_data`。
限流头也可单独解析：`parse_rate_limit_headers(headers)`、`parse_retry_after(headers)`。

## 自定义传输（代理 / 测试）

`transport` 是签名为 `(method, url, headers, body, timeout) -> (status, headers, body)`
的可调用对象；默认 `urllib_transport`。测试里可以注入假的 transport，不访问网络：

```python
def fake_transport(method, url, headers, body, timeout):
    return 200, {"content-type": "application/json"}, b'{"object":"list","data":[]}'

client = DshClient("https://ee.example.com", transport=fake_transport)
assert client.list_models() == []
```

## 测试

```sh
cd sdk/python
python3 -m unittest discover -s tests -v
```

测试全部使用假的 transport，不访问网络。

## 已知边界

- 不支持流式 chat（`urllib` 一次性读 body）；需要流式请用 TypeScript 版。
- 不自动重试；不实现 MCP 的 GET 事件流、resources/prompts、sampling/roots、刷新令牌。
- 不发送 `X-DshEE-Client: dsh-connect/…`（保持第三方 Harness 的默认分页语义）。
- 未连接真实 dsh-ee 部署做端到端验证；契约按服务端 PR #123/#128/#129/#132 核对。

## 示例与兼容性

- 示例（MCP-only Claude Code / Codex、通用 Harness、浏览器 CORS）：
  [`../examples/`](../examples/README.md)
- 与 dsh-ee API 版本、MCP、工具面的兼容矩阵：[`COMPATIBILITY.md`](./COMPATIBILITY.md)
- 各版本变更记录：[`CHANGELOG.md`](./CHANGELOG.md)
- 包元数据：`pyproject.toml` 的 `[project.urls]` 指向
  <https://github.com/hikariming/dsh-ee>；本包**尚未发布**到 PyPI。

## License

Apache-2.0（见仓库根 `LICENSE`）。
