"""Python 客户端单元测试：全部使用假的 transport，不访问网络。"""

import json
import unittest

from dsh_ee_harness import (
    DeviceFlowError,
    DshClient,
    DshClientError,
    DshJsonRpcError,
    McpSession,
    extract_confirmation,
    is_pending_confirmation,
    parse_rate_limit_headers,
    parse_retry_after,
)

BASE = "https://ee.example.com"

DISCOVERY = {
    "name": "dsh-ee",
    "version": "1.2.3",
    "api_version": "1",
    "device_authorization_endpoint": f"{BASE}/api/device/code",
    "token_endpoint": f"{BASE}/api/device/token",
    "scopes_supported": ["models", "agent"],
    "models": {"protocol": "openai-chat-completions", "streaming": True},
    "mcp": {"transport": "streamable-http", "protocol_versions": ["2025-06-18"]},
    "apps": {"web": True, "data_tools": True},
    "limits": {"max_request_bytes": 1, "max_response_bytes": 1},
    "deprecation": {"sunset_header": "Sunset"},
}

CAPABILITIES = {
    "api_version": "1",
    "authenticated": True,
    "server": {"name": "dsh-ee", "version": "1.2.3"},
    "user": {"username": "alice", "department": "研发"},
    "scopes": ["agent", "models"],
    "models": ["company-chat"],
    "tools": {"count": 3},
    "apps": [],
    "revision": "rev-1",
}

INITIALIZE_RESULT = {
    "protocolVersion": "2025-06-18",
    "capabilities": {"tools": {"listChanged": True}},
    "serverInfo": {"name": "dsh-ee", "version": "1.2.3"},
    "instructions": "这些工具连着企业内部系统",
}


def json_response(payload, status=200, headers=None):
    return status, {"content-type": "application/json; charset=utf-8", **(headers or {})}, payload


class FakeTransport:
    """可注入的 transport：handler(call, index) -> (status, headers, payload)。"""

    def __init__(self, handler):
        self.handler = handler
        self.calls = []

    def __call__(self, method, url, headers, body, timeout):
        call = {
            "method": method,
            "url": url,
            "headers": {str(key).lower(): str(value) for key, value in headers.items()},
            "json": json.loads(body.decode("utf-8")) if body else None,
            "timeout": timeout,
        }
        self.calls.append(call)
        status, response_headers, payload = self.handler(call, len(self.calls) - 1)
        if isinstance(payload, bytes):
            raw = payload
        elif payload is None:
            raw = b""
        else:
            raw = json.dumps(payload).encode("utf-8")
        return status, response_headers, raw


def mcp_handler(overrides=None):
    """处理 MCP 握手的 transport handler；overrides 按 method 覆盖。"""
    overrides = overrides or {}

    def handler(call, index):
        if call["method"] == "DELETE":
            return 200, {}, None
        request = call["json"]
        method = request["method"]
        if method in overrides:
            return overrides[method](request, call)
        if method == "initialize":
            return json_response(
                {"jsonrpc": "2.0", "id": request["id"], "result": INITIALIZE_RESULT},
                headers={"mcp-session-id": "sess-1"},
            )
        if method == "notifications/initialized":
            return 202, {}, None
        raise AssertionError(f"未预期的 MCP 方法：{method}")

    return handler


class TestDiscoveryAndCapabilities(unittest.TestCase):
    def test_discovery_etag_cache_and_headers(self):
        transport = FakeTransport(
            lambda call, index: (
                (304, {"etag": '"doc-v1"'}, None)
                if call["headers"].get("if-none-match") == '"doc-v1"'
                else json_response(
                    DISCOVERY,
                    headers={
                        "etag": '"doc-v1"',
                        "x-request-id": "rid-1",
                        "dsh-ee-api-version": "1",
                    },
                )
            )
        )
        client = DshClient(f"{BASE}/", transport=transport)
        doc = client.discovery()
        self.assertEqual(doc["name"], "dsh-ee")
        self.assertEqual(transport.calls[0]["url"], f"{BASE}/.well-known/dsh-ee")
        self.assertEqual(transport.calls[0]["headers"]["dsh-ee-api-version"], "1")
        self.assertNotIn("authorization", transport.calls[0]["headers"])
        self.assertEqual(client.last_request_id, "rid-1")
        self.assertEqual(client.last_api_version, "1")

        self.assertEqual(client.discovery(), doc)
        self.assertEqual(transport.calls[1]["headers"]["if-none-match"], '"doc-v1"')
        self.assertEqual(client.discovery(refresh=True), doc)
        self.assertNotIn("if-none-match", transport.calls[2]["headers"])

    def test_capabilities_anonymous_and_authenticated(self):
        transport = FakeTransport(
            lambda call, index: json_response(
                {**CAPABILITIES, "authenticated": "authorization" in call["headers"]}
            )
        )
        anonymous = DshClient(BASE, transport=transport)
        self.assertFalse(anonymous.capabilities()["authenticated"])
        self.assertNotIn("authorization", transport.calls[0]["headers"])

        authed = DshClient(BASE, token="ee_at_test", transport=transport)
        self.assertTrue(authed.capabilities()["authenticated"])
        self.assertEqual(transport.calls[1]["headers"]["authorization"], "Bearer ee_at_test")

    def test_neutral_error_shape(self):
        transport = FakeTransport(
            lambda call, index: json_response(
                {
                    "error": {
                        "type": "authentication_error",
                        "code": "token_invalid",
                        "message": "令牌无效、已吊销或已过期",
                        "request_id": "rid-err",
                    }
                },
                status=401,
            )
        )
        client = DshClient(BASE, token="ee_at_bad", transport=transport)
        with self.assertRaises(DshClientError) as ctx:
            client.capabilities()
        error = ctx.exception
        self.assertEqual(error.status, 401)
        self.assertEqual(error.error_type, "authentication_error")
        self.assertEqual(error.code, "token_invalid")
        self.assertEqual(error.request_id, "rid-err")


class TestModelsAndChat(unittest.TestCase):
    def test_models_and_rate_limit_headers(self):
        transport = FakeTransport(
            lambda call, index: json_response(
                {"object": "list", "data": [{"id": "company-chat", "object": "model", "owned_by": "dsh-ee"}]},
                headers={
                    "X-RateLimit-Limit": "60",
                    "X-RateLimit-Remaining": "59",
                    "X-RateLimit-Reset": "1700000000",
                },
            )
        )
        client = DshClient(BASE, token="ee_at_test", transport=transport)
        models = client.list_models()
        self.assertEqual(models[0]["id"], "company-chat")
        self.assertEqual(transport.calls[0]["headers"]["authorization"], "Bearer ee_at_test")
        self.assertEqual(client.last_rate_limit.limit, 60)
        self.assertEqual(client.last_rate_limit.remaining, 59)

    def test_chat_completion_body(self):
        completion = {
            "id": "chatcmpl-1",
            "object": "chat.completion",
            "created": 1,
            "model": "company-chat",
            "choices": [{"index": 0, "message": {"role": "assistant", "content": "pong"}}],
        }
        transport = FakeTransport(lambda call, index: json_response(completion))
        client = DshClient(BASE, token="ee_at_test", transport=transport)
        result = client.chat_completion("company-chat", [{"role": "user", "content": "ping"}], temperature=0.2)
        self.assertEqual(result["choices"][0]["message"]["content"], "pong")
        self.assertEqual(transport.calls[0]["url"], f"{BASE}/v1/chat/completions")
        self.assertEqual(
            transport.calls[0]["json"],
            {
                "model": "company-chat",
                "messages": [{"role": "user", "content": "ping"}],
                "stream": False,
                "temperature": 0.2,
            },
        )

    def test_openai_error_shape(self):
        transport = FakeTransport(
            lambda call, index: json_response(
                {
                    "error": {
                        "message": "你的账号没有调用大模型的权限，请联系管理员",
                        "type": "permission_error",
                        "code": "permission_denied",
                    }
                },
                status=403,
            )
        )
        client = DshClient(BASE, token="ee_at_test", transport=transport)
        with self.assertRaises(DshClientError) as ctx:
            client.list_models()
        error = ctx.exception
        self.assertEqual(error.status, 403)
        self.assertEqual(error.error_type, "permission_error")
        self.assertEqual(error.code, "permission_denied")

    def test_rate_limit_error(self):
        transport = FakeTransport(
            lambda call, index: json_response(
                {"error": {"message": "请求过于频繁", "type": "rate_limit_error", "code": "rate_limit_exceeded"}},
                status=429,
                headers={
                    "Retry-After": "30",
                    "X-RateLimit-Limit": "60",
                    "X-RateLimit-Remaining": "0",
                    "X-RateLimit-Reset": "1700000060",
                },
            )
        )
        client = DshClient(BASE, token="ee_at_test", transport=transport)
        with self.assertRaises(DshClientError) as ctx:
            client.list_models()
        error = ctx.exception
        self.assertTrue(error.is_rate_limited)
        self.assertEqual(error.retry_after, 30)
        self.assertEqual(error.rate_limit.remaining, 0)

    def test_header_parsers(self):
        info = parse_rate_limit_headers(
            {"X-RateLimit-Limit": "60", "X-RateLimit-Remaining": "42", "X-RateLimit-Reset": "1700000000"}
        )
        self.assertEqual((info.limit, info.remaining, info.reset), (60, 42, 1700000000))
        self.assertIsNone(parse_rate_limit_headers({"X-RateLimit-Limit": "60"}))
        self.assertEqual(parse_retry_after({"Retry-After": "30"}), 30)
        self.assertIsNone(parse_retry_after({}))


class TestDeviceFlow(unittest.TestCase):
    DEVICE_CODE = {
        "device_code": "dc-1",
        "user_code": "ABCD-EFGH",
        "verification_uri": f"{BASE}/device",
        "verification_uri_complete": f"{BASE}/device?code=ABCD-EFGH",
        "interval": 2,
        "expires_in": 60,
    }
    TOKEN = {
        "access_token": "ee_at_new",
        "scopes": ["models"],
        "user": {"username": "alice"},
        "expires_at": "2026-01-01T00:00:00Z",
    }

    def test_request_device_code(self):
        transport = FakeTransport(lambda call, index: json_response(self.DEVICE_CODE))
        client = DshClient(BASE, token="ee_at_should_not_send", transport=transport)
        code = client.request_device_code(client_name="my-harness", scopes=["models", "agent"])
        self.assertEqual(code["device_code"], "dc-1")
        self.assertNotIn("authorization", transport.calls[0]["headers"])
        self.assertEqual(
            transport.calls[0]["json"],
            {"client_name": "my-harness", "scopes": ["models", "agent"]},
        )

    def test_poll_error_codes(self):
        scenarios = [
            ("authorization_pending", 400, True),
            ("slow_down", 400, True),
            ("access_denied", 400, False),
            ("expired_token", 400, False),
            ("rate_limited", 429, True),
        ]
        for flow_code, status, retryable in scenarios:
            transport = FakeTransport(lambda call, index, code=flow_code, st=status: json_response({"error": code}, status=st))
            client = DshClient(BASE, transport=transport)
            with self.assertRaises(DeviceFlowError) as ctx:
                client.poll_device_token("dc-1")
            self.assertEqual(ctx.exception.flow_code, flow_code)
            self.assertEqual(ctx.exception.retryable, retryable)

    def test_wait_for_device_token_slow_down(self):
        responses = [
            (400, {"error": "authorization_pending"}),
            (400, {"error": "slow_down"}),
            (200, self.TOKEN),
        ]

        def handler(call, index):
            status, payload = responses[index]
            return json_response(payload, status=status)

        transport = FakeTransport(handler)
        client = DshClient(BASE, transport=transport)
        sleeps = []
        pending = []
        token = client.wait_for_device_token(
            self.DEVICE_CODE,
            on_pending=lambda code, attempt: pending.append(code),
            sleep=lambda seconds: sleeps.append(seconds),
        )
        self.assertEqual(token["access_token"], "ee_at_new")
        self.assertEqual(sleeps, [2, 7])
        self.assertEqual(pending, ["authorization_pending", "slow_down"])

    def test_wait_timeout(self):
        transport = FakeTransport(
            lambda call, index: json_response({"error": "authorization_pending"}, status=400)
        )
        client = DshClient(BASE, transport=transport)
        with self.assertRaises(DeviceFlowError) as ctx:
            client.wait_for_device_token(self.DEVICE_CODE, timeout=0)
        self.assertEqual(ctx.exception.flow_code, "expired_token")

    def test_login_sets_token(self):
        def handler(call, index):
            if call["url"].endswith("/api/device/code"):
                return json_response(self.DEVICE_CODE)
            if call["url"].endswith("/api/device/token"):
                return json_response(self.TOKEN)
            return json_response({"object": "list", "data": [{"id": "company-chat"}]})

        transport = FakeTransport(handler)
        client = DshClient(BASE, transport=transport)
        shown = []
        token = client.login_with_device_code(
            client_name="my-harness",
            on_code=lambda code: shown.append(code["device_code"]),
            sleep=lambda seconds: None,
        )
        self.assertEqual(shown, ["dc-1"])
        self.assertEqual(token["access_token"], "ee_at_new")
        self.assertTrue(client.has_token)
        client.list_models()
        self.assertEqual(transport.calls[-1]["headers"]["authorization"], "Bearer ee_at_new")


class TestMcp(unittest.TestCase):
    def test_initialize_session_headers_and_reuse(self):
        transport = FakeTransport(mcp_handler())
        client = DshClient(BASE, token="ee_at_test", transport=transport)
        session = client.connect_mcp()
        self.assertEqual(session.session_id, "sess-1")
        self.assertTrue(session.is_initialized)
        self.assertEqual(session.server_info["name"], "dsh-ee")
        self.assertEqual(len(transport.calls), 2)

        init_call = transport.calls[0]
        self.assertEqual(init_call["url"], f"{BASE}/mcp")
        self.assertEqual(init_call["method"], "POST")
        self.assertEqual(init_call["headers"]["authorization"], "Bearer ee_at_test")
        self.assertIn("text/event-stream", init_call["headers"]["accept"])
        self.assertNotIn("mcp-session-id", init_call["headers"])
        self.assertEqual(init_call["json"]["method"], "initialize")
        self.assertEqual(init_call["json"]["params"]["protocolVersion"], "2025-06-18")

        notification = transport.calls[1]
        self.assertEqual(notification["json"]["method"], "notifications/initialized")
        self.assertEqual(notification["headers"]["mcp-session-id"], "sess-1")
        self.assertEqual(notification["headers"]["mcp-protocol-version"], "2025-06-18")

        self.assertIs(client.connect_mcp(), session)
        self.assertEqual(len(transport.calls), 2)

    def test_list_tools_pagination(self):
        tools = [{"name": "a"}, {"name": "b"}, {"name": "c"}]

        def on_tools_list(request, call):
            params = request.get("params") or {}
            if params.get("cursor") == "cur-1":
                return json_response({"jsonrpc": "2.0", "id": request["id"], "result": {"tools": [tools[2]]}})
            return json_response(
                {
                    "jsonrpc": "2.0",
                    "id": request["id"],
                    "result": {"tools": [tools[0], tools[1]], "nextCursor": "cur-1"},
                }
            )

        transport = FakeTransport(mcp_handler({"tools/list": on_tools_list}))
        client = DshClient(BASE, transport=transport)
        page = client.list_tools()
        self.assertEqual([tool["name"] for tool in page["tools"]], ["a", "b"])
        self.assertEqual(page["nextCursor"], "cur-1")
        self.assertEqual(client.list_tools(cursor="cur-1")["nextCursor"], None)
        all_tools = client.list_all_tools(page_size=2)
        self.assertEqual([tool["name"] for tool in all_tools], ["a", "b", "c"])

    def test_call_tool_confirmation(self):
        def on_tools_call(request, call):
            return json_response(
                {
                    "jsonrpc": "2.0",
                    "id": request["id"],
                    "result": {
                        "content": [{"type": "text", "text": "请先在网页确认"}],
                        "isError": True,
                        "_meta": {
                            "dsh-ee/confirmation": {
                                "id": 42,
                                "url": f"{BASE}/confirm/42",
                                "expires_at": "2026-01-01T00:00:00Z",
                            }
                        },
                    },
                }
            )

        transport = FakeTransport(mcp_handler({"tools/call": on_tools_call}))
        client = DshClient(BASE, transport=transport)
        result = client.call_tool("crm__lookup", {"q": "张三"})
        self.assertTrue(result["isError"])
        confirmation = extract_confirmation(result)
        self.assertEqual(confirmation["id"], 42)
        self.assertEqual(confirmation["url"], f"{BASE}/confirm/42")
        self.assertIsNotNone(confirmation["expires_at_dt"])
        self.assertTrue(is_pending_confirmation(result))
        tools_call = [call for call in transport.calls if call["json"] and call["json"].get("method") == "tools/call"][0]
        self.assertEqual(tools_call["json"]["params"], {"name": "crm__lookup", "arguments": {"q": "张三"}})
        self.assertIsNone(extract_confirmation({"content": []}))

    def test_jsonrpc_error(self):
        def on_tools_call(request, call):
            return json_response(
                {
                    "jsonrpc": "2.0",
                    "id": request["id"],
                    "error": {"code": -32602, "message": "没有这个工具"},
                }
            )

        transport = FakeTransport(mcp_handler({"tools/call": on_tools_call}))
        client = DshClient(BASE, transport=transport)
        with self.assertRaises(DshJsonRpcError) as ctx:
            client.call_tool("nope")
        self.assertEqual(ctx.exception.rpc_code, -32602)
        self.assertEqual(ctx.exception.status, 200)

    def test_rate_limited_mcp(self):
        def on_tools_list(request, call):
            return json_response(
                {"jsonrpc": "2.0", "id": None, "error": {"code": -32000, "message": "请求过于频繁"}},
                status=429,
                headers={
                    "Retry-After": "5",
                    "X-RateLimit-Limit": "60",
                    "X-RateLimit-Remaining": "0",
                    "X-RateLimit-Reset": "1700000060",
                },
            )

        transport = FakeTransport(mcp_handler({"tools/list": on_tools_list}))
        client = DshClient(BASE, transport=transport)
        with self.assertRaises(DshJsonRpcError) as ctx:
            client.list_tools()
        error = ctx.exception
        self.assertEqual(error.rpc_code, -32000)
        self.assertEqual(error.status, 429)
        self.assertTrue(error.is_rate_limited)
        self.assertEqual(error.retry_after, 5)

    def test_close_sends_delete(self):
        transport = FakeTransport(mcp_handler())
        client = DshClient(BASE, transport=transport)
        session = client.connect_mcp()
        client.close()
        self.assertTrue(session.is_closed)
        delete_call = [call for call in transport.calls if call["method"] == "DELETE"][0]
        self.assertEqual(delete_call["url"], f"{BASE}/mcp")
        self.assertEqual(delete_call["headers"]["mcp-session-id"], "sess-1")
        self.assertIsNone(delete_call["json"])
        self.assertIsInstance(session, McpSession)


if __name__ == "__main__":
    unittest.main()
