// Harness SDK 契约测试（mock/live 分层）。
//
// mock 层：不依赖 dsh-ee 服务端，用本地 node:http 假服务端跑一遍 SDK 的完整 HTTP/MCP 链路
//   （发现 ETag、能力、模型、chat 流式/非流式、MCP 分页、写确认 _meta、错误/限流头）。
// live 层：连真实 dsh-ee（隔离模式由 run.mjs 启动），用统一令牌 + 真实模型上游走
//   SDK 的发现/能力/模型/chat/MCP/设备码；429 用例单独起一个 DSH_EE_RATE_LIMIT=3 的隔离实例。
//
// 契约依赖：发现/能力（#123）、MCP 分页与写确认 _meta（#128）、限流头（#129）。
// 这些 PR 未合并时 live 层会探测并打印“跳过”，不会误报；合并后自动开始断言。
//
// 环境变量：
//   DSH_EE_SDK_E2E_MOCK_ONLY=1  只跑 mock 层（本地快速验证用）
//
// 运行：cd e2e && ADMIN_PW=… node run.mjs 35-harness-sdk
// 或：  cd e2e && DSH_EE_SDK_E2E_MOCK_ONLY=1 node -e "import('./tests/35-harness-sdk.mjs').then(m=>m.default({errors:[],pages:[]}))"
import fs from "node:fs";
import http from "node:http";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath, pathToFileURL } from "node:url";
import { ADMIN_PW, BASE, assert, assertEq, step, tag } from "../lib.mjs";
import { findFreePort, isManaged, startManagedServer } from "../server.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const sdkDir = path.resolve(here, "../../sdk/typescript");
const sdkDist = path.join(sdkDir, "dist/index.js");

/** 构建并加载 SDK（CI 的 e2e job 没有单独构建 SDK，这里按需构建）。 */
export async function loadSdk() {
  if (!fs.existsSync(sdkDist)) {
    step("构建 sdk/typescript（npm ci + npm run build）");
    const npm = process.platform === "win32" ? "npm.cmd" : "npm";
    for (const args of [
      ["ci", "--no-audit", "--no-fund"],
      ["run", "build"],
    ]) {
      const result = spawnSync(npm, args, { cwd: sdkDir, encoding: "utf8" });
      if (result.status !== 0) {
        throw new Error(`sdk/typescript 构建失败（npm ${args.join(" ")}）：\n${result.stderr || result.stdout}`);
      }
    }
  }
  return import(pathToFileURL(sdkDist).href);
}

// ---- 小型 HTTP 工具（e2e 里不引入额外依赖） ----

function listen(server) {
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => resolve(server.address().port));
  });
}

function close(server) {
  return new Promise((resolve) => server.close(resolve));
}

function readBody(req) {
  return new Promise((resolve) => {
    let text = "";
    req.on("data", (chunk) => (text += chunk));
    req.on("end", () => resolve(text));
  });
}

function json(res, payload, status = 200, headers = {}) {
  res.writeHead(status, { "content-type": "application/json; charset=utf-8", ...headers });
  res.end(JSON.stringify(payload));
}

function cookieFrom(response) {
  const values = response.headers.getSetCookie?.() ?? [];
  const single = response.headers.get("set-cookie");
  return (values.length ? values : [single]).filter(Boolean).map((value) => value.split(";")[0]).join("; ");
}

async function postJson(url, payload, headers = {}) {
  return fetch(url, {
    method: "POST",
    headers: { "content-type": "application/json", ...headers },
    body: JSON.stringify(payload),
  });
}

// ---- mock 层 ----

/**
 * 假 dsh-ee：只实现 SDK 用到的路由，用来在没有任何服务端 PR 依赖的情况下验证 wire 协议。
 * 请求记录在 requests[] 里，断言 SDK 发出的头/体。
 */
async function mockLayer(sdk) {
  const requests = [];
  const state = { models429: false };
  const server = http.createServer(async (req, res) => {
    const bodyText = await readBody(req);
    const body = bodyText ? JSON.parse(bodyText) : undefined;
    const record = { method: req.method, url: req.url, headers: req.headers, body };
    requests.push(record);
    const url = new URL(req.url, "http://mock");

    if (url.pathname === "/.well-known/dsh-ee") {
      if (req.headers["if-none-match"] === '"mock-doc"') {
        res.writeHead(304, { etag: '"mock-doc"' });
        res.end();
        return;
      }
      return json(
        res,
        {
          name: "dsh-ee",
          version: "9.9.9-mock",
          api_version: "1",
          device_authorization_endpoint: `${mockBase}/api/device/code`,
          token_endpoint: `${mockBase}/api/device/token`,
          scopes_supported: ["models", "connections", "agent"],
          models: { protocol: "openai-chat-completions", streaming: true },
          mcp: { transport: "streamable-http", protocol_versions: ["2025-06-18"] },
          apps: { web: true, data_tools: true },
          limits: { max_request_bytes: 1, max_response_bytes: 1 },
          deprecation: { sunset_header: "Sunset" },
        },
        200,
        { etag: '"mock-doc"' },
      );
    }

    if (url.pathname === "/v1/capabilities") {
      return json(res, {
        api_version: "1",
        authenticated: true,
        server: { name: "dsh-ee", version: "9.9.9-mock" },
        user: { username: "mock-user" },
        scopes: ["models", "connections", "agent"],
        models: ["mock-model"],
        tools: { count: 2 },
        apps: [],
        revision: "mock-rev",
      });
    }

    if (url.pathname === "/v1/models") {
      if (state.models429) {
        return json(
          res,
          { error: { message: "请求过于频繁，请稍后重试", type: "rate_limit_error", code: "rate_limit_exceeded" } },
          429,
          {
            "retry-after": "7",
            "x-ratelimit-limit": "60",
            "x-ratelimit-remaining": "0",
            "x-ratelimit-reset": "1700000060",
          },
        );
      }
      return json(
        res,
        { object: "list", data: [{ id: "mock-model", object: "model", owned_by: "dsh-ee" }] },
        200,
        { "x-ratelimit-limit": "60", "x-ratelimit-remaining": "59", "x-ratelimit-reset": "1700000000" },
      );
    }

    if (url.pathname === "/v1/chat/completions") {
      if (body?.stream === true) {
        res.writeHead(200, { "content-type": "text/event-stream" });
        const base = {
          id: "mock-stream",
          object: "chat.completion.chunk",
          created: 1700000000,
          model: body.model,
        };
        res.write(`data: ${JSON.stringify({ ...base, choices: [{ index: 0, delta: { role: "assistant" }, finish_reason: null }] })}\n\n`);
        res.write(`data: ${JSON.stringify({ ...base, choices: [{ index: 0, delta: { content: "pong" }, finish_reason: null }] })}\n\n`);
        res.write(`data: ${JSON.stringify({ ...base, choices: [], usage: { prompt_tokens: 2, completion_tokens: 1, total_tokens: 3 } })}\n\n`);
        res.write("data: [DONE]\n\n");
        res.end();
        return;
      }
      return json(res, {
        id: "mock-chat",
        object: "chat.completion",
        created: 1700000000,
        model: body?.model,
        choices: [{ index: 0, message: { role: "assistant", content: "pong" }, finish_reason: "stop" }],
        usage: { prompt_tokens: 2, completion_tokens: 1, total_tokens: 3 },
      });
    }

    if (url.pathname === "/mcp") {
      if (req.method === "DELETE") {
        res.writeHead(200);
        res.end();
        return;
      }
      const request = body;
      if (request.method === "initialize") {
        return json(
          res,
          {
            jsonrpc: "2.0",
            id: request.id,
            result: {
              protocolVersion: "2025-06-18",
              capabilities: { tools: { listChanged: true } },
              serverInfo: { name: "dsh-ee-mock", version: "9.9.9" },
              instructions: "mock instructions",
            },
          },
          200,
          { "mcp-session-id": "mock-session-1" },
        );
      }
      if (request.method === "notifications/initialized") {
        res.writeHead(202);
        res.end();
        return;
      }
      if (request.method === "tools/list") {
        const params = request.params ?? {};
        const full = [{ name: "mock__a" }, { name: "mock__b" }];
        if (params.limit === 0) {
          return json(res, { jsonrpc: "2.0", id: request.id, result: { tools: full } });
        }
        if (params.cursor === "mock-next") {
          return json(res, { jsonrpc: "2.0", id: request.id, result: { tools: [full[1]] } });
        }
        return json(res, {
          jsonrpc: "2.0",
          id: request.id,
          result: { tools: [full[0]], nextCursor: "mock-next" },
        });
      }
      if (request.method === "tools/call") {
        return json(res, {
          jsonrpc: "2.0",
          id: request.id,
          result: {
            content: [{ type: "text", text: "这个操作会修改数据，要先由你本人在网页上确认" }],
            isError: true,
            _meta: {
              "dsh-ee/confirmation": {
                id: 99,
                url: `${mockBase}/confirm/99`,
                expires_at: "2026-01-01T00:00:00Z",
              },
            },
          },
        });
      }
      return json(res, { jsonrpc: "2.0", id: request.id, error: { code: -32601, message: "不支持的方法" } });
    }

    res.writeHead(404);
    res.end("not found");
  });
  const port = await listen(server);
  const mockBase = `http://127.0.0.1:${port}`;
  try {
    const client = new sdk.DshHarnessClient({ baseUrl: mockBase, token: "ee_at_mock" });

    // 1) 发现 + ETag 304 + 版本头
    const discovery = await client.discovery();
    assertEq(discovery.api_version, "1", "mock discovery api_version");
    assertEq(discovery.name, "dsh-ee", "mock discovery name");
    assertEq(requests[0].headers["dsh-ee-api-version"], "1", "发现请求带 API 版本头");
    assertEq(requests[0].headers.authorization, undefined, "发现请求不带令牌");
    const cached = await client.discovery();
    assertEq(cached.version, discovery.version, "304 后返回缓存");
    assertEq(requests[1].headers["if-none-match"], '"mock-doc"', "第二次发现带 If-None-Match");
    step("mock：发现文档 + ETag 304 + Dsh-EE-API-Version");

    // 2) 能力
    const caps = await client.capabilities();
    assertEq(caps.authenticated, true, "mock capabilities authenticated");
    assertEq(caps.user.username, "mock-user", "mock capabilities user");
    step("mock：/v1/capabilities");

    // 3) 模型 + 限流头
    const models = await client.listModels();
    assertEq(models[0].id, "mock-model", "mock model id");
    assertEq(client.lastRateLimit?.limit, 60, "mock 限流头被解析");
    assertEq(requests.at(-1).headers.authorization, "Bearer ee_at_mock", "模型请求带 Bearer");
    step("mock：/v1/models + X-RateLimit-*");

    // 4) 非流式 chat
    const completion = await client.createChatCompletion({
      model: "mock-model",
      messages: [{ role: "user", content: "ping" }],
    });
    assertEq(completion.choices[0].message.content, "pong", "mock chat 非流式");
    assertEq(requests.at(-1).body.stream, false, "非流式请求带 stream:false");
    step("mock：chat 非流式");

    // 5) 流式 chat
    const chunks = [];
    for await (const chunk of client.streamChatCompletion({
      model: "mock-model",
      messages: [{ role: "user", content: "ping" }],
    })) {
      chunks.push(chunk);
    }
    assertEq(chunks.length, 3, "mock 流式分块数");
    assertEq(chunks[1].choices[0].delta.content, "pong", "mock 流式内容");
    assertEq(chunks[2].usage.total_tokens, 3, "mock 流式 usage");
    step("mock：chat 流式（SSE + [DONE]）");

    // 6) MCP：initialize / 分页 / tools/call + _meta
    const session = await client.connectMcp();
    assertEq(session.sessionId, "mock-session-1", "mock MCP session");
    const tools = await client.listAllTools();
    assertEq(tools.map((tool) => tool.name).join(","), "mock__a,mock__b", "mock MCP 分页拼接");
    const mcpRequests = requests.filter((record) => record.url === "/mcp" && record.method === "POST");
    const initRequest = mcpRequests.find((record) => record.body?.method === "initialize");
    for (const record of mcpRequests.filter((item) => item.body?.method !== "initialize")) {
      assertEq(record.headers["mcp-session-id"], "mock-session-1", "MCP 后续请求带 Mcp-Session-Id");
      assertEq(record.headers["mcp-protocol-version"], "2025-06-18", "MCP 后续请求带协议版本");
    }
    assertEq(initRequest.headers["authorization"], "Bearer ee_at_mock", "MCP initialize 带 Bearer");
    assert(initRequest.headers.accept.includes("text/event-stream"), "MCP Accept 同时要 JSON/SSE");

    const result = await client.callTool("mock__write", { q: "x" });
    assertEq(result.isError, true, "mock 写确认 isError");
    const confirmation = sdk.extractConfirmation(result);
    assertEq(confirmation.id, 99, "mock 写确认 _meta id");
    assert(confirmation.url.endsWith("/confirm/99"), "mock 写确认 _meta url");
    assert(sdk.isPendingConfirmation(result), "mock isPendingConfirmation");
    await client.close();
    const deleteRecord = requests.find((record) => record.method === "DELETE" && record.url === "/mcp");
    assert(deleteRecord, "关闭会话发 DELETE /mcp");
    step("mock：MCP initialize / 分页 / tools-call + _meta / DELETE");

    // 7) 错误 / 429
    state.models429 = true;
    await client.listModels().then(
      () => {
        throw new Error("mock 429 没有抛错");
      },
      (error) => {
        assert(error instanceof sdk.DshApiError, "mock 429 应是 DshApiError");
        assertEq(error.status, 429, "mock 429 status");
        assertEq(error.isRateLimited, true, "mock 429 isRateLimited");
        assertEq(error.retryAfter, 7, "mock 429 Retry-After");
        assertEq(error.rateLimit?.remaining, 0, "mock 429 X-RateLimit-Remaining");
        assertEq(error.apiCode, "rate_limit_exceeded", "mock 429 code");
      },
    );
    step("mock：429 错误与限流头");
  } finally {
    await close(server);
  }
}

// ---- live 层 ----

async function liveLayer(sdk) {
  step(`live：服务端 ${BASE}`);
  const login = await postJson(`${BASE}/api/auth/login`, { username: "admin", password: ADMIN_PW });
  assertEq(login.status, 200, "管理员登录");
  const me = await login.json();
  const cookie = cookieFrom(login);
  assert(cookie, "登录响应应带会话 cookie");

  const model = `sdk-e2e-${tag}`;
  const upstream = http.createServer(async (req, res) => {
    const bodyText = await readBody(req);
    if (req.headers.authorization !== "Bearer sk-sdk-upstream") {
      res.writeHead(401);
      res.end("{}");
      return;
    }
    const parsed = bodyText ? JSON.parse(bodyText) : {};
    if (req.url === "/v1/chat/completions") {
      if (parsed.stream === true) {
        res.writeHead(200, { "content-type": "text/event-stream" });
        const base = { id: "sdk-live-stream", object: "chat.completion.chunk", created: 1, model: parsed.model };
        res.write(`data: ${JSON.stringify({ ...base, choices: [{ index: 0, delta: { role: "assistant" }, finish_reason: null }] })}\n\n`);
        res.write(`data: ${JSON.stringify({ ...base, choices: [{ index: 0, delta: { content: "pong" }, finish_reason: null }] })}\n\n`);
        res.write(`data: ${JSON.stringify({ ...base, choices: [], usage: { prompt_tokens: 2, completion_tokens: 1, total_tokens: 3 } })}\n\n`);
        res.write("data: [DONE]\n\n");
        res.end();
        return;
      }
      return json(res, {
        id: "sdk-live-chat",
        object: "chat.completion",
        created: 1,
        model: parsed.model,
        choices: [{ index: 0, message: { role: "assistant", content: "pong" }, finish_reason: "stop" }],
        usage: { prompt_tokens: 2, completion_tokens: 1, total_tokens: 3 },
      });
    }
    res.writeHead(404);
    res.end("{}");
  });
  const upstreamPort = await listen(upstream);

  let providerId;
  let tokenInfo;
  let client;
  try {
    // 1) 真实模型上游 + 统一令牌
    const providerResponse = await postJson(
      `${BASE}/api/admin/providers`,
      {
        name: `SDK 契约测试 ${tag}`,
        vendor: "custom",
        base_url: `http://127.0.0.1:${upstreamPort}/v1`,
        api_key: "sk-sdk-upstream",
        models: [model],
      },
      { cookie },
    );
    assertEq(providerResponse.status, 201, "创建模型上游");
    providerId = (await providerResponse.json()).id;

    const tokenResponse = await postJson(
      `${BASE}/api/admin/tokens`,
      {
        user_id: me.id,
        name: `sdk-e2e-${tag}`,
        scopes: ["models", "agent", "connections"],
        expires_in_days: 1,
      },
      { cookie },
    );
    assertEq(tokenResponse.status, 201, "签发统一令牌");
    const issued = await tokenResponse.json();
    tokenInfo = issued.info;
    const token = issued.token;
    assert(String(token).startsWith("ee_at_"), "统一令牌前缀");

    const observed = [];
    client = new sdk.DshHarnessClient({
      baseUrl: BASE,
      token,
      onResponse: (info) => observed.push(info),
    });
    const contract = { discovery: false, capabilities: false, pagination: false, rateLimit: false };

    // 2) 发现 / 能力（#123 合并后才存在）
    // 旧部署上该路径会落到三种兜底：404、没有嵌入前端时的 503、以及嵌入了前端时的
    // SPA index.html（HTTP 200 + text/html）。先探测内容类型，避免把 HTML 当契约 JSON。
    const discoveryProbe = await fetch(`${BASE}/.well-known/dsh-ee`, { headers: { accept: "application/json" } });
    const probeContentType = discoveryProbe.headers.get("content-type") ?? "";
    const discoveryAvailable = discoveryProbe.status === 200 && probeContentType.includes("application/json");
    if (!discoveryAvailable) {
      step(
        `live：跳过 discovery/capabilities（服务端未合并 #123：HTTP ${discoveryProbe.status}，content-type ${probeContentType || "—"}）`,
      );
    } else {
      try {
        const discovery = await client.discovery();
        contract.discovery = true;
        assertEq(discovery.api_version, "1", "发现文档 api_version");
        assertEq(client.lastApiVersion, "1", "Dsh-EE-API-Version 响应头");
        assert(discovery.device_authorization_endpoint.endsWith("/api/device/code"), "发现文档设备码端点");
        assert(discovery.mcp.protocol_versions.includes("2025-06-18"), "发现文档 MCP 协议版本");
        const caps = await client.capabilities();
        contract.capabilities = true;
        assertEq(caps.authenticated, true, "能力摘要 authenticated");
        assertEq(caps.user.username, "admin", "能力摘要 user");
        assert(caps.models.includes(model), "能力摘要模型列表");
        step("live：discovery + capabilities（#123）");
      } catch (error) {
        if (![404, 503].includes(error.status)) throw error;
        step(`live：跳过 discovery/capabilities（服务端未合并 #123：HTTP ${error.status}）`);
      }
    }

    // 3) 模型目录与 OpenAI 兼容 chat
    const models = await client.listModels();
    assert(models.some((item) => item.id === model), "SDK 读到刚建的模型：" + JSON.stringify(models));
    if (client.lastRateLimit) {
      contract.rateLimit = true;
      assert(client.lastRateLimit.limit > 0, "X-RateLimit-Limit 应大于 0");
    }
    const completion = await client.createChatCompletion({
      model,
      messages: [{ role: "user", content: "ping" }],
    });
    assertEq(completion.choices?.[0]?.message?.content, "pong", "SDK 非流式 chat");
    step("live：/v1/models + /v1/chat/completions（非流式）");

    // 4) 流式 chat
    const chunks = [];
    for await (const chunk of client.streamChatCompletion({
      model,
      messages: [{ role: "user", content: "ping" }],
    })) {
      chunks.push(chunk);
    }
    assert(chunks.length > 0, "SDK 流式 chat 应收到分块");
    assert(JSON.stringify(chunks).includes("pong"), "SDK 流式 chat 内容");
    step("live：/v1/chat/completions（SSE 流式）");

    // 5) MCP：initialize / tools/list（#128 分页，未合并时回退全量）/ tools/call
    const session = await client.connectMcp();
    assert(session.serverInfo?.name, "MCP initialize serverInfo");
    assert(session.sessionId, "MCP initialize 会话 id");
    const firstPage = await client.listTools({ limit: 1 });
    if (firstPage.nextCursor !== undefined) {
      contract.pagination = true;
      const names = firstPage.tools.map((tool) => tool.name);
      let cursor = firstPage.nextCursor;
      let pages = 1;
      while (cursor !== undefined && pages < 50) {
        const page = await client.listTools({ limit: 1, cursor });
        names.push(...page.tools.map((tool) => tool.name));
        cursor = page.nextCursor;
        pages += 1;
      }
      assertEq(new Set(names).size, names.length, "MCP 分页不应重复工具");
      assert(names.length > 1, "MCP 分页应取到多个工具");
      step(`live：MCP tools/list 分页（#128，${names.length} 个工具/${pages} 页）`);
    } else {
      step("live：跳过 MCP 分页断言（服务端未合并 #128：无 nextCursor）");
    }
    const tools = await client.listAllTools();
    assert(tools.some((tool) => tool.name === "ee_me"), "MCP 工具应包含 ee_me：" + JSON.stringify(tools.map((tool) => tool.name)));
    const meResult = await client.callTool("ee_me");
    assert(Array.isArray(meResult.content) && meResult.content.length > 0, "tools/call 返回 content");
    step("live：MCP initialize + tools/list + tools/call(ee_me)");

    // 6) 设备码：申请 → 管理员网页会话确认 → SDK 轮询拿到令牌
    const deviceClient = new sdk.DshHarnessClient({ baseUrl: BASE });
    const code = await deviceClient.requestDeviceCode({
      clientName: `sdk-e2e-${tag}`,
      scopes: ["models", "agent", "connections"],
    });
    const approve = await postJson(`${BASE}/api/device/${encodeURIComponent(code.user_code)}`, { approve: true }, { cookie });
    assertEq(approve.status, 200, "确认设备码");
    const deviceToken = await deviceClient.waitForDeviceToken(code, { intervalMs: 100, timeoutMs: 20_000 });
    assertEq(deviceToken.user.username, "admin", "设备码令牌 user");
    assert(String(deviceToken.access_token).startsWith("ee_at_"), "设备码令牌前缀");
    deviceClient.setToken(deviceToken.access_token);
    const deviceModels = await deviceClient.listModels();
    assert(deviceModels.some((item) => item.id === model), "设备码令牌可读模型");
    await deviceClient.close();
    step("live：设备码申请 → 确认 → SDK 拿到令牌并可用");

    // 7) 429：隔离模式单独起一个低限流实例（需要 #129 的限流头）
    if (!isManaged()) {
      step("live：跳过 429 用例（非 managed 模式，无法起独立低限流实例）");
    } else if (!contract.rateLimit) {
      step("live：跳过 429 用例（服务端未合并 #129：无 X-RateLimit-* 头）");
    } else {
      const port = await findFreePort();
      const limitedServer = await startManagedServer({ port, env: { DSH_EE_RATE_LIMIT: "3" } });
      try {
        const limited = new sdk.DshHarnessClient({ baseUrl: limitedServer.url, token: "ee_at_bogus" });
        let rateLimited;
        for (let attempt = 0; attempt < 6 && !rateLimited; attempt += 1) {
          try {
            await limited.listModels();
          } catch (error) {
            if (error.status === 429) rateLimited = error;
            else if (error.status !== 401) throw error;
          }
        }
        assert(rateLimited, "低限流实例应返回 429");
        assertEq(rateLimited.isRateLimited, true, "429 isRateLimited");
        assertEq(rateLimited.rateLimit?.limit, 3, "429 X-RateLimit-Limit=3");
        assert(typeof rateLimited.retryAfter === "number", "429 应带 Retry-After");
        step(`live：独立低限流实例 429（limit=3, retry-after=${rateLimited.retryAfter}s）`);
      } finally {
        await limitedServer.stop();
      }
    }

    assert(observed.some((info) => info.path === "/v1/models"), "onResponse 应观测到模型请求");
    await client.close();
  } finally {
    await close(upstream);
    if (client) await client.close().catch(() => {});
    // 清理测试数据（隔离模式库会被删除，非隔离模式也不留脏数据）。
    // revoke 的 ids 与 client_name 是两种互斥过滤，分两次调用。
    if (tokenInfo?.id) {
      await fetch(`${BASE}/api/admin/tokens/revoke`, {
        method: "POST",
        headers: { cookie, "content-type": "application/json" },
        body: JSON.stringify({ ids: [tokenInfo.id] }),
      }).catch(() => {});
    }
    await fetch(`${BASE}/api/admin/tokens/revoke`, {
      method: "POST",
      headers: { cookie, "content-type": "application/json" },
      body: JSON.stringify({ client_name: `sdk-e2e-${tag}` }),
    }).catch(() => {});
    if (providerId !== undefined) {
      await fetch(`${BASE}/api/admin/providers/${providerId}`, { method: "DELETE", headers: { cookie } }).catch(() => {});
    }
  }
}

export default async function (t) {
  void t;
  const sdk = await loadSdk();
  await mockLayer(sdk);
  if (process.env.DSH_EE_SDK_E2E_MOCK_ONLY === "1") {
    step("跳过 live 层（DSH_EE_SDK_E2E_MOCK_ONLY=1）");
    return;
  }
  await liveLayer(sdk);
}
