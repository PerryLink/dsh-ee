// 本地飞书 + 模拟 DSH 宿主/模型；真实 OIDC、队列、插件中继和 MCP 工具调用。
import http from "node:http";
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
import { generateKeyPairSync, randomUUID, sign as cryptoSign, createHash } from "node:crypto";
import { setTimeout as sleep } from "node:timers/promises";
import { ADMIN_PW, BASE, SHOTS, api, assert, assertEq, login, logout, newPage, shot, step, tag } from "../lib.mjs";

const verificationToken = "local-feishu-chat-verification";
const appSecret = "local-feishu-chat-secret";
const openId = "ou-chat-employee";
const firstQuestion = "帮我总结一下今天的待办";
const secondQuestion = "等我打开电脑后，帮我整理一下明天的计划";
const b64json = (v) => Buffer.from(JSON.stringify(v)).toString("base64url");

function chatPage() {
  return `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>本地飞书聊天预览</title>
<style>
*{box-sizing:border-box}body{margin:0;background:#f2f5fa;color:#182230;font:16px "Noto Sans SC",sans-serif}
main{max-width:1000px;margin:28px auto;border:1px solid #dbe3ee;border-radius:18px;background:white;overflow:hidden;box-shadow:0 10px 45px #1428480c}
header{padding:22px 30px;border-bottom:1px solid #e7ecf3}h1{font-size:25px;margin:10px 0}.badge{color:#925500;background:#fff3d6;padding:4px 10px;border-radius:6px;font-size:13px}
header p,footer{color:#66758b;font-size:13px;line-height:1.8;margin:8px 0}#status{color:#2463ca;font-size:14px}
#thread{padding:8px 30px;min-height:260px}.row{display:flex;margin:10px 0}.row.employee{justify-content:flex-end}.message{max-width:83%}small{display:block;color:#66758b;font-size:12px;margin-bottom:4px}
.bubble{background:#f0f3f8;border-radius:12px;padding:10px 14px;white-space:pre-wrap;line-height:1.65;font-size:14px}.employee .bubble{background:#e7efff;color:#184ca5}.notice .bubble{background:#fff5dc;color:#805a08}
form{display:flex;gap:12px;padding:16px 30px;border-top:1px solid #e7ecf3}input{flex:1;padding:13px;border:1px solid #cbd5e1;border-radius:8px;font:inherit}button{background:#2563eb;color:white;border:0;border-radius:8px;padding:12px 26px;font:inherit}footer{padding:0 30px 16px}#error{color:#b42318;padding:0 30px}
</style><main><header><span class="badge">本地验证 · 非真实飞书租户</span><h1>本地飞书聊天预览</h1>
<div id="status">正在连接…</div><p>飞书私聊 → dsh-ee 服务端队列 → 自己的 DSH → ee_feishu → 回复原消息</p></header>
<section id="thread" aria-label="私聊记录"></section><form><input aria-label="发送给机器人的消息" placeholder="给自己的 DSH 发一条消息" required><button>发送</button></form><p id="error" role="alert"></p>
<footer>真实服务端 / 真实 dsh-connect 中继 / 真实 MCP tools/call · DSH 宿主与模型为确定性模拟，不代表真实模型能力。</footer></main>
<script>
let previous='';
async function refresh(){
  const r=await fetch('/thread'); if(!r.ok)throw new Error('读取聊天失败'); const data=await r.json();
  document.querySelector('#status').textContent=data.status;
  const value=JSON.stringify(data.entries);if(value===previous)return;previous=value;
  const thread=document.querySelector('#thread');thread.replaceChildren();
  for(const item of data.entries){
    const row=document.createElement('article');row.className='row '+item.kind;row.dataset.messageId=item.messageId;
    const message=document.createElement('div');message.className='message';
    const name=document.createElement('small');name.textContent=item.author;
    const bubble=document.createElement('div');bubble.className='bubble';bubble.textContent=item.text;
    message.append(name,bubble);row.append(message);thread.append(row);
  }
}
document.querySelector('form').addEventListener('submit',async e=>{
  e.preventDefault();const input=document.querySelector('input');const button=document.querySelector('button');button.disabled=true;
  try{const r=await fetch('/send',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({text:input.value})});if(!r.ok)throw new Error(await r.text());input.value='';await refresh();}
  catch(e){document.querySelector('#error').textContent=e.message;}finally{button.disabled=false;}
});
const update=()=>refresh().catch(e=>{document.querySelector('#error').textContent=e.message;});update();setInterval(update,300);
</script></html>`;
}

async function fakeFeishu() {
  const { privateKey, publicKey } = generateKeyPairSync("rsa", { modulusLength: 2048 });
  const jwk = publicKey.export({ format: "jwk" });
  const grants = new Map();
  const entries = [], replies = [], events = [];
  const profile = { open_id: openId, name: "飞书员工", email: "chat.employee@example.com", department_ids: ["dep-rd"] };
  const state = { entries, replies, events, status: "DSH 尚未连接" };
  const server = http.createServer(async (req, res) => {
    const url = new URL(req.url, "http://localhost");
    const json = (status, value) => { res.writeHead(status, { "Content-Type": "application/json" }); res.end(JSON.stringify(value)); };
    const body = async () => { let raw = ""; for await (const chunk of req) raw += chunk; return raw; };
    try {
      if (url.pathname === "/.well-known/openid-configuration") return json(200, { issuer: server.url, authorization_endpoint: server.url + "/authorize", token_endpoint: server.url + "/token", jwks_uri: server.url + "/jwks", userinfo_endpoint: server.url + "/userinfo" });
      if (url.pathname === "/jwks") return json(200, { keys: [{ ...jwk, kid: "chat-key", use: "sig", alg: "RS256" }] });
      if (url.pathname === "/authorize") {
        const code = randomUUID();
        grants.set(code, { challenge: url.searchParams.get("code_challenge"), nonce: url.searchParams.get("nonce") });
        const callback = new URL(url.searchParams.get("redirect_uri"));
        callback.searchParams.set("code", code); callback.searchParams.set("state", url.searchParams.get("state"));
        res.writeHead(302, { Location: callback.href }); return res.end();
      }
      if (url.pathname === "/token" && req.method === "POST") {
        const form = new URLSearchParams(await body());
        const grant = grants.get(form.get("code"));
        const hash = createHash("sha256").update(form.get("code_verifier") || "").digest("base64url");
        if (!grant || form.get("client_id") !== "dsh-ee" || form.get("client_secret") !== appSecret || hash !== grant.challenge) return json(400, { error: "invalid_grant" });
        grants.delete(form.get("code"));
        const payload = { iss: server.url, aud: "dsh-ee", sub: openId, preferred_username: profile.email, name: profile.name, email: profile.email, nonce: grant.nonce, iat: Math.floor(Date.now() / 1000), exp: Math.floor(Date.now() / 1000) + 300 };
        const data = b64json({ alg: "RS256", kid: "chat-key", typ: "JWT" }) + "." + b64json(payload);
        return json(200, { id_token: data + "." + cryptoSign("RSA-SHA256", data, privateKey).toString("base64url"), access_token: "chat-access-token", token_type: "Bearer" });
      }
      if (url.pathname === "/userinfo") return json(200, { sub: openId, department: "研发部" });
      if (url.pathname === "/open-apis/auth/v3/tenant_access_token/internal" && req.method === "POST") {
        const input = JSON.parse(await body());
        if (input.app_id !== "dsh-ee" || input.app_secret !== appSecret) return json(403, { code: 1 });
        return json(200, { code: 0, tenant_access_token: "tenant-chat", expire: 7200 });
      }
      if (url.pathname.startsWith("/open-apis/")) {
        if (req.headers.authorization !== "Bearer tenant-chat") return json(401, { error: "unauthorized" });
        if (url.pathname.includes("/departments/parentchildren")) return json(200, { code: 0, data: { items: [{ open_department_id: "dep-rd", name: "研发部" }], has_more: false } });
        if (url.pathname.includes("/users/find_by_department")) return json(200, { code: 0, data: { items: [profile], has_more: false } });
        const match = url.pathname.match(/^\/open-apis\/im\/v1\/messages\/([^/]+)\/reply$/);
        if (match && req.method === "POST") {
          const messageId = decodeURIComponent(match[1]);
          assert(events.some((event) => event.event.message.message_id === messageId), "回复必须对应真实入站消息");
          const payload = JSON.parse(await body());
          assertEq(payload.msg_type, "post", "回复保留 Markdown 格式");
          const content = JSON.parse(payload.content);
          const text = content.zh_cn.content.flat().map((item) => item.text).join("\n");
          replies.push({ messageId, text, payload });
          entries.push({ messageId, text, kind: text.includes("不在线") ? "notice" : "bot", author: text.includes("不在线") ? "dsh-ee · 离线提示" : "DSH · 本地模拟模型" });
          return json(200, { code: 0, data: { message_id: `om-reply-${replies.length}` } });
        }
      }
      if (url.pathname === "/chat") { res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" }); return res.end(chatPage()); }
      if (url.pathname === "/thread") return json(200, { entries, status: state.status });
      if (url.pathname === "/send" && req.method === "POST") {
        const { text } = JSON.parse(await body());
        if (typeof text !== "string" || !text.trim()) return json(400, { error: "请输入文字消息" });
        const messageId = `om-chat-${randomUUID()}`;
        const event = { schema: "2.0", header: { event_id: randomUUID(), token: verificationToken, event_type: "im.message.receive_v1", app_id: "dsh-ee", create_time: String(Date.now()) }, event: { sender: { sender_id: { open_id: openId }, sender_type: "user" }, message: { message_id: messageId, chat_id: "oc-local-p2p", chat_type: "p2p", message_type: "text", content: JSON.stringify({ text }), create_time: String(Date.now()) } } };
        events.push(event);
        entries.push({ messageId, text, kind: "employee", author: "飞书员工 · 我" });
        const response = await fetch(`${BASE}/api/auth/feishu/event`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(event) });
        assertEq(response.status, 200, "发送经过真实事件回调");
        return json(200, { messageId });
      }
      return json(404, { error: "not found" });
    } catch (error) { return json(500, { error: error.message }); }
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  server.url = `http://127.0.0.1:${server.address().port}`;
  return { server, state };
}

async function until(check, label, timeout = 15_000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) { if (await check()) return; await sleep(250); }
  throw new Error(`等待超时：${label}`);
}

export default async function (t) {
  const { server: idp, state } = await fakeFeishu();
  const requirePlugin = createRequire(new URL("../../dsh-plugin/package.json", import.meta.url));
  const { register } = await import(pathToFileURL(requirePlugin.resolve("tsx/esm/api")).href);
  const loader = register({ namespace: `feishu-e2e-${tag}` });
  let relay;
  let bridge;
  const record = process.env.E2E_RECORD === "1";
  const started = Date.now();
  const seconds = () => (Date.now() - started) / 1000;
  const pause = async () => { if (record) await sleep(1400); };
  const stateDir = fs.mkdtempSync(path.join(SHOTS, "feishu-relay-"));
  const timings = {};
  try {
    const { FeishuChatRelay } = await loader.import("../../dsh-plugin/src/feishu-chat.ts", import.meta.url);
    const { HttpClient } = await loader.import("../../dsh-plugin/src/http.ts", import.meta.url);
    const { normalizeConfig } = await loader.import("../../dsh-plugin/src/config.ts", import.meta.url);
    const { setLang } = await loader.import("../../dsh-plugin/src/i18n/index.ts", import.meta.url);
    const { McpBridge } = await loader.import("../../dsh-plugin/src/bridge.ts", import.meta.url);
    const { registerApproval } = await loader.import("../../dsh-plugin/src/approval.ts", import.meta.url);
    setLang("zh-CN");
    const page = await newPage(t, { viewport: { width: 1280, height: 960 }, ...(record ? { recordVideo: { dir: process.env.E2E_RECORD_DIR || path.join(SHOTS, "feishu-bot-video"), size: { width: 1280, height: 960 } } } : {}) });
    const video = page.video();
    await login(page, "admin", ADMIN_PW);
    await page.goto(`${BASE}/admin/auth`);
    await page.getByRole("button", { name: "飞书", exact: true }).click();
    const oidc = page.locator("#auth-oidc");
    await oidc.getByRole("switch").first().click();
    await oidc.getByLabel("Issuer").fill(idp.url);
    await oidc.getByLabel("Client ID").fill("dsh-ee");
    await oidc.getByLabel("Client Secret").fill(appSecret);
    await oidc.getByLabel("通讯录 API 地址").fill(idp.url);
    const botRow = oidc.getByText("飞书机器人对话", { exact: true }).locator("..").locator("..");
    await botRow.getByRole("switch").click();
    await oidc.getByLabel(/^Verification Token/).fill(verificationToken);
    await oidc.getByRole("button", { name: "保存统一登录配置" }).click();
    await page.getByText("已保存统一登录配置").waitFor();
    const cfg = await api(page, "GET", "/api/admin/auth/oidc");
    assertEq(cfg.data.feishuBot, true, "机器人开关已保存");
    assert(!cfg.data.feishuNotifyActions && !cfg.data.feishuNotify, "机器人不依赖通知或卡片审批开关");
    await oidc.getByText(`${BASE}/api/auth/feishu/event`, { exact: true }).waitFor();
    for (const permission of ["im.message.receive_v1", "im:message.p2p_msg:readonly", "im:message:send_as_bot"]) assert((await oidc.innerText()).includes(permission), `显示配置要求 ${permission}`);
    await botRow.evaluate((el) => el.scrollIntoView({ block: "center" }));
    await page.screenshot({ path: path.join(SHOTS, "feishu-bot-admin-switch.png") }); await pause();
    step("管理员在网页开启唯一机器人开关，保存 Verification Token，显示事件 URL 与权限");

    const eventRequest = (payload) => fetch(`${BASE}/api/auth/feishu/event`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(payload) });
    const challenge = await eventRequest({ type: "url_verification", token: verificationToken, challenge: "local-chat-challenge" });
    assertEq(challenge.status, 200, "事件地址验证");
    assertEq((await challenge.json()).challenge, "local-chat-challenge", "challenge 原样返回");
    assertEq((await eventRequest({ type: "url_verification", token: "wrong", challenge: "bad" })).status, 403, "错误 token 被拒绝");

    await logout(page); await page.goto(`${BASE}/login`);
    await page.getByRole("link", { name: "用飞书登录" }).click();
    await page.waitForURL((url) => url.pathname !== "/login" && !url.pathname.startsWith("/api/auth/oidc"));
    const me = await api(page, "GET", "/api/auth/me");
    assertEq(me.data.external_dn, `feishu:${openId}`, "真实 OIDC 绑定员工 open_id");
    await api(page, "PUT", "/api/me/welcome", { dismissed: true });
    const issued = await api(page, "POST", "/api/me/tokens", { name: `feishu-chat-${tag}`, client_kind: "dsh", client_name: "DSH local E2E", scopes: ["agent", "hub", "connections"] });
    assertEq(issued.status, 201, "正常员工路由签发 DSH 令牌");
    assertEq(issued.data.info.client_kind, "dsh", "使用 DSH 令牌语义，不关闭服务端写操作确认");
    const config = normalizeConfig({ base: BASE, serverName: "company" });
    const tokens = { config, resolve: async () => ({ value: issued.data.token, source: "e2e" }), invalidate: async () => { throw new Error("员工令牌不应失效"); } };
    const client = new HttpClient(tokens);

    const rows = {}, listeners = new Set(), hooks = new Map(), turns = [], registered = new Map();
    const emit = (id, running) => { rows[id] = { id, running, completed: !running }; for (const listener of listeners) listener(); };
    const ctx = {
      on: (name, hook) => { const list = hooks.get(name) || []; list.push(hook); hooks.set(name, list); }, get: () => undefined,
      tools: { register: (tool) => { registered.set(tool.name, tool); return () => registered.delete(tool.name); } },
      workspaces: { initializeDefault: async () => ({ workspaceId: "local-workspace" }) },
      sessions: {
        list: { getSnapshot: () => ({ current: "", phase: "ready", byId: rows }), subscribe: (fn) => { listeners.add(fn); return () => listeners.delete(fn); } },
        create: async () => { const id = `local-dsh-${Object.keys(rows).length + 1}`; rows[id] = { id, running: false }; return id; },
        using: async (id, _options, fn) => fn({ ready: Promise.resolve({ session: { id } }) }),
      },
      conversation: { sendSession: async (session, prompt) => {
        emit(session.id, true);
        try {
          const match = prompt.match(/action=reply，id=(\d+)/);
          assert(match, "真实插件提示词包含回复工具与消息 id");
          const question = prompt.split("飞书消息：\n")[1];
          assert([firstQuestion, secondQuestion].includes(question), "模型收到原始飞书文字");
          const text = question === firstQuestion ? "今天的待办建议：\n1. 先确认最重要的交付事项\n2. 跟进协作进度，处理阻塞\n3. 下班前回顾完成情况\n（本地模拟回答，未读取真实任务数据）" : "明天的计划建议：\n1. 上午处理高优先级事项\n2. 下午安排协作与复盘\n离线期间的消息已由服务端队列交给你的 DSH。\n（本地模拟回答）";
          const args = { action: "reply", id: Number(match[1]), text };
          const exec = { name: "mcp__company__ee_feishu", agent: { session }, arguments: args };
          const chain = hooks.get("tools/pre-execute") || [];
          const invoke = (i) => i < chain.length ? chain[i](exec, () => invoke(i + 1)) : registered.get(exec.name).execute(args, exec);
          const result = await invoke(0);
          assert(result.kind !== "ask", "Feishu 回复不应要求员工到电脑批准");
          assert(!result.isError, JSON.stringify(result));
          turns.push({ sessionId: session.id, id: args.id, question });
        } catch (error) { t.errors.push(`模拟模型：${error.message}`); throw error; }
        finally { emit(session.id, false); }
      } },
    };
    bridge = new McpBridge(ctx, config, client);
    registerApproval(ctx, bridge);
    await bridge.sync();
    assert(registered.has("mcp__company__ee_feishu"), "真实 MCP 与桥接器注册 ee_feishu");
    step("员工通过假飞书 OIDC 登录，用正常路由创建 DSH 令牌；真实 McpBridge 注册 ee_feishu");
    relay = new FeishuChatRelay(ctx, config, tokens, client, { file: path.join(stateDir, "feishu-chat.json") });
    const status = async () => { const r = await client.fetch("/api/me/feishu-chat"); assertEq(r.status, 200, "员工状态 API"); return r.json(); };
    relay.start();
    await until(async () => (await status()).online, "DSH 长轮询在线");
    state.status = "DSH 已连接 · 消息由自己的电脑处理";
    await page.goto(`${idp.url}/chat`);
    await page.getByRole("textbox").fill(firstQuestion); await pause();
    await page.getByRole("button", { name: "发送", exact: true }).click();
    await page.getByText("今天的待办建议：", { exact: false }).waitFor();
    await until(() => turns.length === 1, "MCP 工具完成");
    assertEq(state.replies.length, 1, "在线时只有模型回复，没有离线或 fallback 提示");
    assertEq((await eventRequest(state.events[0])).status, 200, "飞书重试仍成功");
    await shot(page, "feishu-bot-online-chat"); await pause();
    step("网页发送私聊 → 真实事件队列 → FeishuChatRelay → 模拟模型 → /mcp tools/call ee_feishu → 原消息回复；重试去重");

    await relay.stop();
    state.status = "DSH 已停止 · 等待服务端在线状态自然过期（60 秒）";
    timings.idleStart = seconds();
    await until(async () => !(await status()).online, "服务端 60 秒在线状态自然过期", 75_000);
    timings.idleEnd = seconds();
    state.status = "DSH 离线 · 消息将暂存在 dsh-ee 服务端";
    await page.getByRole("textbox").fill(secondQuestion); await pause();
    await page.getByRole("button", { name: "发送", exact: true }).click();
    await page.getByText("你的 DSH 当前不在线。", { exact: false }).waitFor();
    assertEq(turns.length, 1, "停止中继时不执行模型");
    assertEq(state.replies.length, 2, "离线消息只有即时离线提示");
    await shot(page, "feishu-bot-offline-notice"); await pause();
    step("停止真实中继，等待 60 秒 presence 自然过期；新私聊收到服务端离线提示");

    relay.start(); state.status = "DSH 已重新连接 · 正在处理服务端排队消息";
    await page.getByText("明天的计划建议：", { exact: false }).waitFor();
    await until(() => turns.length === 2, "排队消息 MCP 回复完成");
    state.status = "DSH 已连接 · 离线期间的排队消息已回复";
    await page.getByText(state.status, { exact: true }).waitFor();
    assertEq(turns[0].sessionId, turns[1].sessionId, "重启复用本机专用会话");
    assertEq(Object.keys(rows).length, 1, "只创建一个 DSH 专用会话");
    assertEq(state.replies.length, 3, "两次工具回复加一次离线提示，无额外 fallback 或重复回复");
    assertEq(state.replies[1].messageId, state.replies[2].messageId, "离线提示与最终回复关联同一原消息");
    assertEq(state.replies[0].messageId, state.events[0].event.message.message_id, "在线回复关联原消息");
    assertEq(await page.locator("#error").innerText(), "", "预览页面无错误");
    await shot(page, "feishu-bot-online-chat"); await pause();
    step("重启同一真实中继，复用专用会话领取排队消息；第二次 MCP 回复出现在同一聊天记录");
    await relay.stop();
    if (video) {
      await page.context().close();
      const output = { video: await video.path(), ...timings };
      fs.writeFileSync(path.join(SHOTS, "feishu-bot-recording.json"), JSON.stringify(output, null, 2));
      step(`录屏原片：${output.video}`);
      step(`GIF 可跳过的自然离线等待：${timings.idleStart.toFixed(2)}–${timings.idleEnd.toFixed(2)} 秒`);
    }
  } finally {
    await relay?.stop();
    bridge?.stop();
    await loader.unregister();
    fs.rmSync(stateDir, { recursive: true, force: true });
    await new Promise((resolve) => idp.close(resolve));
  }
}
