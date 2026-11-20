import assert from "node:assert/strict";
import { test } from "node:test";
import { McpBridge, canonArgs, renderResult } from "../src/bridge.js";
import { registerApproval, describeArgs } from "../src/approval.js";
import { normalizeConfig } from "../src/config.js";

const hubMeta = { enable: { e: "write", zh: "启用能力", en: "Enable a capability" }, revoke: { e: "danger", zh: "吊销令牌", en: "Revoke a token" } };

function setup(serverTools: any[], onCall?: (params: any) => any) {
  const registered = new Map<string, any>();
  const hooks: Record<string, (...args: any[]) => any> = {};
  const ctx = {
    tools: { register(tool: any) { registered.set(tool.name, tool); return () => registered.delete(tool.name); } },
    on(name: string, fn: any) { hooks[name] = fn; },
  };
  const http = {
    async fetch(_path: string, init: RequestInit) {
      if (!init.body) return new Response(null);
      const body = JSON.parse(String(init.body));
      if (body.method === "initialize") return new Response(JSON.stringify({ result: {} }), { headers: { "mcp-session-id": "s1" } });
      if (body.method === "tools/list") return new Response(JSON.stringify({ result: { tools: serverTools } }));
      if (body.method === "tools/call") return new Response(JSON.stringify({ result: onCall ? onCall(body.params) : { content: [{ type: "text", text: "ok" }] } }));
      throw new Error("unexpected");
    },
  };
  const bridge = new McpBridge(ctx as any, normalizeConfig({ base: "http://localhost", serverName: "ee" }), http as any);
  registerApproval(ctx as any, bridge);
  return { registered, hooks, bridge };
}

const exec = (name: string, args: any) => ({ name, arguments: args, callId: "c1" });
const pass = async () => ({ kind: "allow" });

test("读操作直接放行，写操作要用户批准", async () => {
  const { hooks, bridge } = setup([{ name: "ee_hub", inputSchema: { type: "object" }, _meta: { dshee: { actions: hubMeta } } }]);
  await bridge.start();
  assert.deepEqual(await hooks["tools/pre-execute"](exec("mcp__ee__ee_hub", { action: "search" }), pass), { kind: "allow" });
  const ask = await hooks["tools/pre-execute"](exec("mcp__ee__ee_hub", { action: "enable", id: 12 }), pass);
  assert.equal(ask.kind, "ask");
  assert.match(ask.displayReason.zh, /启用能力.*id=12/);
  assert.match(ask.displayReason.en, /Enable a capability \(id=12\)/);
  bridge.stop();
});

test("没有标注的工具（企业连接、别的插件）不受影响", async () => {
  const { hooks, bridge } = setup([{ name: "crm__lookup", inputSchema: { type: "object" } }]);
  await bridge.start();
  assert.deepEqual(await hooks["tools/pre-execute"](exec("mcp__ee__crm__lookup", { q: "x" }), pass), { kind: "allow" });
  assert.deepEqual(await hooks["tools/pre-execute"](exec("some_other_tool", { action: "enable" }), pass), { kind: "allow" });
  bridge.stop();
});

test("Feishu owner reply bypasses approval but other actions and similarly named tools do not", async () => {
  const meta = { reply: { e: "write", zh: "回复飞书", en: "Reply to Feishu" }, remove: { e: "write", zh: "删除", en: "Remove" } };
  const { hooks, bridge } = setup([
    { name: "ee_feishu", _meta: { dshee: { actions: meta } } },
    { name: "crm__ee_feishu", _meta: { dshee: { actions: meta } } },
  ]);
  await bridge.start();
  try {
    assert.deepEqual(await hooks["tools/pre-execute"](exec("mcp__ee__ee_feishu", { action: "reply", id: 12, text: "answer" }), pass), { kind: "allow" });
    assert.equal((await hooks["tools/pre-execute"](exec("mcp__ee__ee_feishu", { action: "remove" }), pass)).kind, "ask");
    assert.equal((await hooks["tools/pre-execute"](exec("mcp__ee__crm__ee_feishu", { action: "reply" }), pass)).kind, "ask");
  } finally { bridge.stop(); }
});

test("危险操作：第一步拿预览不打扰用户，第二步带上预览请求批准", async () => {
  const preview = "将吊销令牌「笔记本」（ee_at_abcd…）。";
  const { hooks, registered, bridge } = setup(
    [{ name: "ee_tokens", inputSchema: { type: "object" }, _meta: { dshee: { actions: hubMeta } } }],
    (params) => (params.arguments.confirm
      ? { content: [{ type: "text", text: "已完成" }] }
      : { content: [{ type: "text", text: JSON.stringify({ confirm_required: true, confirm: "n1", preview }) }] }),
  );
  await bridge.start();
  const name = "mcp__ee__ee_tokens";
  const first = { action: "revoke", id: "at:3" };
  assert.deepEqual(await hooks["tools/pre-execute"](exec(name, first), pass), { kind: "allow" });
  await registered.get(name).execute(first);
  const second = { id: "at:3", action: "revoke", confirm: "n1" };
  const ask = await hooks["tools/pre-execute"](exec(name, second), pass);
  assert.equal(ask.kind, "ask");
  assert.ok(ask.displayReason.zh.includes(preview), "审批框应带上服务端给的预览");
  // 预览对不上（参数变了）就不带预览，但仍要批准
  const other = await hooks["tools/pre-execute"](exec(name, { action: "revoke", id: "at:4", confirm: "n1" }), pass);
  assert.equal(other.kind, "ask");
  assert.ok(!other.displayReason.zh.includes(preview));
  bridge.stop();
});

test("本机 action 由插件自己执行，不发给服务端", async () => {
  let serverCalls = 0;
  const { registered, bridge } = setup([{ name: "ee_plugins", inputSchema: { type: "object" } }], () => { serverCalls++; return { content: [] }; });
  bridge.registerLocal("ee_plugins.upload", async (args) => ({ content: [{ type: "text", text: `local ${args.path}` }] }));
  await bridge.start();
  const tool = registered.get("mcp__ee__ee_plugins");
  const result = await tool.execute({ action: "upload", path: "/tmp/a.tgz" });
  assert.equal(result.content[0].text, "local /tmp/a.tgz");
  await tool.execute({ action: "catalog" });
  assert.equal(serverCalls, 1);
  bridge.stop();
});

test("工具结果渲染成文字，错误带前缀", () => {
  assert.equal(renderResult({ content: [{ type: "text", text: "你好" }], isError: false }), "你好");
  assert.equal(renderResult({ content: [{ type: "text", text: "没有权限" }], isError: true }), "错误：没有权限");
  assert.equal(renderResult({ a: 1 }), '{"a":1}');
});

test("参数规范化与摘要", () => {
  assert.equal(canonArgs({ b: 1, a: { d: 1, c: 2 }, confirm: "x" }), canonArgs({ a: { c: 2, d: 1 }, b: 1 }));
  assert.equal(describeArgs({ action: "create", kind: "prompt", slug: "weekly", spec: { title: "周报" } }), "（slug=weekly，kind=prompt，title=周报）");
  assert.equal(describeArgs({ action: "create", slug: "weekly" }, false), " (slug=weekly)");
  assert.equal(describeArgs({ action: "search" }), "");
});
