import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { PassThrough } from "node:stream";
import { test } from "node:test";
import { CookieStore, EEClient, ExtError, cookieHeader, defineExtension, webSignIn } from "../src/index.js";

/** 假的 dsh-ee：记下 confirm / audit 请求，confirm 第一次要求确认、第二次放行。 */
function fakeEE() {
  const calls: { path: string; body: any; auth: string | null }[] = [];
  let approved = false;
  const fetchFn = (async (url: string, init: RequestInit) => {
    const p = new URL(url).pathname;
    const body = JSON.parse(String(init.body));
    calls.push({ path: p, body, auth: new Headers(init.headers).get("authorization") });
    if (p === "/api/local/confirm") {
      const res = approved ? { approved: true } : { approved: false, id: 1, url: "http://ee/confirm/1", message: "请打开 http://ee/confirm/1 确认" };
      return new Response(JSON.stringify(res), { status: 200 });
    }
    return new Response(null, { status: 204 });
  }) as typeof fetch;
  return { calls, approve: () => (approved = true), ee: new EEClient({ url: "http://ee/", token: "T", harness: "claude-code", fetch: fetchFn }) };
}

function demo(ee: EEClient, submitted: unknown[]) {
  return defineExtension({
    id: "acme/legacy-oa",
    version: "0.1.0",
    name: { zh: "老 OA", en: "Legacy OA" },
    description: "演示",
    package: "@acme/legacy-oa-local",
    ee,
    tools: [
      { name: "list_forms", description: "列出待办", readOnly: true, params: [{ name: "status", type: "string", enum: ["pending", "done"] }], handler: () => [{ id: "F-1" }] },
      {
        name: "submit_leave",
        description: "提交请假单",
        params: [{ name: "days", type: "number", required: true }],
        handler: (args) => {
          submitted.push(args);
          return "已提交";
        },
      },
      {
        name: "needs_login",
        description: "要登录",
        readOnly: true,
        handler: () => {
          throw new ExtError("authorize", "请先登录老 OA");
        },
      },
      { name: "boom", description: "内部出错", readOnly: true, handler: () => { throw new Error("secret stack"); } },
    ],
  });
}

test("清单和工具定义：只读按标记，参数都是模型填", () => {
  const f = fakeEE();
  const ext = demo(f.ee, []);
  const m = ext.manifest() as any;
  assert.equal(m.kind, "local");
  assert.equal(m.package, "@acme/legacy-oa-local");
  assert.deepEqual(m.permissions, { approval: true, audit: true });
  assert.equal(m.tools[0].readOnly, true);
  assert.equal(m.tools[1].readOnly, undefined, "没标只读的不写 readOnly");
  assert.equal(m.tools[1].params[0].fill, "model");
  const tools = ext.listTools();
  assert.deepEqual(tools[1].inputSchema, { type: "object", properties: { days: { type: "number" } }, required: ["days"] });
  assert.equal(tools[1].annotations.readOnlyHint, false);
  assert.throws(() => defineExtension({ id: "Bad Id", version: "1", name: "x", package: "x", tools: [] }), /id/);
});

test("写操作先经 dsh-ee 确认，同意后才执行；调用摘要回报审计", async () => {
  const f = fakeEE();
  const submitted: unknown[] = [];
  const ext = demo(f.ee, submitted);

  const first = await ext.callTool("submit_leave", { days: 2 });
  assert.equal(first.isError, true);
  assert.match(first.content[0].text!, /confirm\/1/);
  assert.equal(submitted.length, 0, "没确认前不能执行");
  const confirm = f.calls.find((c) => c.path === "/api/local/confirm")!;
  assert.deepEqual(confirm.body, { extension: "acme/legacy-oa", version: "0.1.0", tool: "submit_leave", args: { days: 2 }, harness: "claude-code" });
  assert.equal(confirm.auth, "Bearer T");

  f.approve();
  const second = await ext.callTool("submit_leave", { days: 2 });
  assert.equal(second.isError, undefined);
  assert.equal(second.content[0].text, "已提交");
  assert.deepEqual(submitted, [{ days: 2 }]);

  // 只读的不问 dsh-ee
  const before = f.calls.filter((c) => c.path === "/api/local/confirm").length;
  const read = await ext.callTool("list_forms", {});
  assert.match(read.content[0].text!, /F-1/);
  assert.equal(f.calls.filter((c) => c.path === "/api/local/confirm").length, before);

  await new Promise((r) => setImmediate(r));
  const audits = f.calls.filter((c) => c.path === "/api/local/audit").map((c) => c.body);
  assert.equal(audits.length, 3);
  assert.deepEqual(
    audits.map((a) => [a.tool, a.ok, a.code]),
    [["submit_leave", false, "confirm"], ["submit_leave", true, ""], ["list_forms", true, ""]],
  );
  assert.ok(audits.every((a) => !("result" in a) && !("args" in a)), "回报里不带参数和返回内容");
});

test("没配 dsh-ee 时写操作不执行；错误码原样带出，内部异常不外泄", async () => {
  const submitted: unknown[] = [];
  const ext = demo(new EEClient({ url: "", token: "" }), submitted);
  const w = await ext.callTool("submit_leave", { days: 1 });
  assert.equal(w.isError, true);
  assert.deepEqual(w._meta, { "dsh-ee/error": { code: "setup" } });
  assert.equal(submitted.length, 0);

  const a = await ext.callTool("needs_login");
  assert.deepEqual(a._meta, { "dsh-ee/error": { code: "authorize" } });
  assert.equal(a.content[0].text, "请先登录老 OA");

  const origErr = console.error;
  console.error = () => undefined;
  try {
    const b = await ext.callTool("boom");
    assert.equal(b.content[0].text, "扩展内部出错了");
    assert.doesNotMatch(JSON.stringify(b), /secret stack/);
  } finally {
    console.error = origErr;
  }
});

test("stdio 上的 MCP：initialize、tools/list、tools/call，通知不回", async () => {
  const f = fakeEE();
  const ext = demo(f.ee, []);
  const input = new PassThrough();
  const output = new PassThrough();
  const lines: any[] = [];
  output.on("data", (d) => String(d).split("\n").filter(Boolean).forEach((l) => lines.push(JSON.parse(l))));
  ext.serveStdio(input, output);
  input.write(JSON.stringify({ jsonrpc: "2.0", id: 1, method: "initialize", params: { protocolVersion: "2025-06-18" } }) + "\n");
  input.write(JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" }) + "\n");
  input.write(JSON.stringify({ jsonrpc: "2.0", id: 2, method: "tools/list" }) + "\n");
  input.write(JSON.stringify({ jsonrpc: "2.0", id: 3, method: "tools/call", params: { name: "list_forms", arguments: {} } }) + "\n");
  input.write("not json\n");
  input.write(JSON.stringify({ jsonrpc: "2.0", id: 4, method: "resources/list" }) + "\n");
  await new Promise((r) => setTimeout(r, 50));
  const byId = Object.fromEntries(lines.filter((l) => l.id !== null).map((l) => [l.id, l]));
  assert.equal(byId[1].result.protocolVersion, "2025-06-18");
  assert.equal(byId[1].result.serverInfo.name, "acme/legacy-oa");
  assert.equal(byId[2].result.tools.length, 4);
  assert.match(byId[3].result.content[0].text, /F-1/);
  assert.equal(byId[4].error.code, -32601);
  assert.ok(lines.some((l) => l.id === null && l.error.code === -32700));
  assert.equal(lines.length, 5, "通知没有回复");
});

test("Cookie 请求头按域名、路径、过期和 secure 挑选", () => {
  const now = 1_000_000;
  const cookies = [
    { name: "sid", value: "a", domain: ".oa.acme.local", path: "/", expires: -1 },
    { name: "old", value: "b", domain: "oa.acme.local", path: "/", expires: now - 1 },
    { name: "app", value: "c", domain: "oa.acme.local", path: "/app", expires: now + 10 },
    { name: "sec", value: "d", domain: "oa.acme.local", path: "/", expires: -1, secure: true },
    { name: "other", value: "e", domain: "evil.local", path: "/", expires: -1 },
  ];
  assert.equal(cookieHeader(cookies, "http://oa.acme.local/app/list", now), "sid=a; app=c");
  assert.equal(cookieHeader(cookies, "https://x.oa.acme.local/application", now), "sid=a; sec=d");
});

test("webSignIn 等到要的 cookie 出现后存下，关窗口", async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "dsh-ee-local-"));
  const store = new CookieStore("acme/legacy-oa", dir);
  let polls = 0;
  let closed = false;
  const launcher = {
    launch: async () => ({
      newContext: async () => ({
        newPage: async () => ({ goto: async () => undefined, url: () => "http://oa/home", isClosed: () => false }),
        cookies: async () => (++polls < 3 ? [] : [{ name: "SESSION", value: "v", domain: "oa", path: "/", expires: -1, secure: false, httpOnly: true }]),
      }),
      close: async () => {
        closed = true;
      },
    }),
  };
  const got = await webSignIn({ url: "http://oa/login", cookiesOf: ["SESSION"], store, launcher, timeoutMs: 5_000 });
  assert.equal(got[0].value, "v");
  assert.ok(closed);
  assert.ok(store.has(["SESSION"]));
  assert.equal((fs.statSync(store.file).mode & 0o777).toString(8), "600");
  await assert.rejects(
    webSignIn({ url: "http://oa/login", cookiesOf: ["NEVER"], store, launcher, timeoutMs: 600 }),
    (e: any) => e instanceof ExtError && e.code === "authorize",
  );
});
