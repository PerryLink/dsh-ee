import assert from "node:assert/strict";
import { test } from "node:test";
import { registerCommands } from "../src/commands.js";
import { normalizeConfig } from "../src/config.js";

test("并发 /ee-login 只申请一个设备码", async () => {
  const original = globalThis.fetch;
  let codes = 0;
  let tokens = 0;
  globalThis.fetch = (async (input: any) => {
    const url = String(input);
    if (url.endsWith("/api/device/code")) {
      codes++;
      await new Promise((resolve) => setTimeout(resolve, 40));
      return new Response(JSON.stringify({
        device_code: "device-1",
        user_code: "ABCD-EFGH",
        verification_uri: "http://ee/device",
        verification_uri_complete: "http://ee/device?code=ABCD-EFGH",
        interval: 1,
      }), { headers: { "content-type": "application/json" } });
    }
    if (url.endsWith("/api/device/token")) {
      tokens++;
      return new Response(JSON.stringify({ access_token: "ee_at_test", scopes: [], user: { username: "u", department: "d" }, expires_at: "" }), { headers: { "content-type": "application/json" } });
    }
    throw new Error("unexpected " + url);
  }) as typeof fetch;
  try {
    const ctx = { get: () => undefined } as any;
    const tm = { set: async () => undefined, syncNpmrc: async () => undefined } as any;
    const handle = registerCommands(ctx, normalizeConfig({ base: "http://ee" }), tm);
    const [first, second] = await Promise.all([handle.login("ABCD-EFGH"), handle.login("ABCD-EFGH")]);
    assert.equal(first.kind, "success");
    assert.equal(second.kind, "success");
    assert.match(second.text, /正在发起/);
    assert.equal(codes, 1, "并发登录只申请一个设备码");
    assert.equal(tokens, 1);
  } finally {
    globalThis.fetch = original;
  }
});


test("/ee-status 5xx 不误报登录失效", async () => {
  const original = globalThis.fetch;
  globalThis.fetch = (async () => new Response("{}", { status: 500 })) as typeof fetch;
  try {
    const handlers: Record<string, any> = {};
    const ctx = {
      effect: () => undefined,
      get: (name: string) => name === "commands" ? { register: (def: any) => { handlers[def.name] = def; return () => undefined; } } : undefined,
    } as any;
    const tm = { resolve: async () => ({ value: "T", source: "store" }), npmrcMatches: async () => true } as any;
    registerCommands(ctx, normalizeConfig({ base: "http://ee" }), tm);
    const result = await handlers["ee-status"].handler({});
    assert.equal(result.kind, "error");
    assert.match(result.text, /HTTP 500/);
    assert.doesNotMatch(result.text, /登录已失效/);
  } finally {
    globalThis.fetch = original;
  }
});
