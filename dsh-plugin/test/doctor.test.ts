import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { compareVersions, runLocalDoctor } from "../src/doctor.js";
import { npmrcState } from "../src/token.js";
import { normalizeConfig } from "../src/config.js";

const config = normalizeConfig({ base: "http://ee.test", serverName: "ee", version: "1.0.0" });

function withNpmrc(content: string | null, fn: () => Promise<void> | void) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "ee-doctor-"));
  const file = path.join(dir, ".npmrc");
  if (content !== null) fs.writeFileSync(file, content);
  const old = process.env.NPM_CONFIG_USERCONFIG;
  process.env.NPM_CONFIG_USERCONFIG = file;
  return Promise.resolve(fn()).finally(() => { if (old === undefined) delete process.env.NPM_CONFIG_USERCONFIG; else process.env.NPM_CONFIG_USERCONFIG = old; });
}

test("版本比较", () => {
  assert.ok(compareVersions("1.2.0", "1.1.9") > 0);
  assert.ok(compareVersions("1.0.0", "1.0.0-rc.1") > 0);
  assert.equal(compareVersions("2.0.0", "2.0.0"), 0);
  assert.ok(compareVersions("1.0.0", "1.0.1") < 0);
});

const noProfile = { get: () => undefined } as any;

test("npmrc 状态：缺失、不一致、一致", async () => {
  const line = (t: string) => `//ee.test/npm/:_authToken=${t}\n`;
  await withNpmrc(null, () => assert.equal(npmrcState(noProfile, config, "T"), "missing"));
  await withNpmrc("registry=https://x\n", () => assert.equal(npmrcState(noProfile, config, "T"), "missing"));
  await withNpmrc(line("OLD"), () => assert.equal(npmrcState(noProfile, config, "T"), "mismatch"));
  await withNpmrc(line("T"), () => assert.equal(npmrcState(noProfile, config, "T"), "ok"));
});

function fixture(opts: { token?: string; whoami?: number; models?: string[]; server?: any; installed?: string[] }) {
  const ctx = {
    get: (name: string) => name === "pluginManager" ? { listBundles: async () => (opts.installed ?? []).map((n) => ({ name: n, installed: true })) } : undefined,
  };
  const tokens = { resolve: async () => opts.token === undefined ? undefined : { value: opts.token, source: "credentials" } };
  const http = {
    async fetch(p: string) {
      if (p === "/api/dsh/whoami") {
        return opts.whoami === 200 || opts.whoami === undefined
          ? new Response(JSON.stringify({ username: "alice", department: "研发" }))
          : new Response(JSON.stringify({ code: "token_revoked" }), { status: opts.whoami });
      }
      if (p === "/v1/models") return new Response(JSON.stringify({ data: (opts.models ?? ["chat"]).map((id) => ({ id })) }));
      throw new Error("unexpected " + p);
    },
  };
  const bridge = {
    status: { lastSyncAt: 1, tools: 3, error: "" },
    callTool: async () => opts.server ? { content: [{ type: "text", text: JSON.stringify(opts.server) }] } : { isError: true, content: [{ type: "text", text: "off" }] },
  };
  return { ctx, tokens, http, bridge };
}

const byId = (checks: any[]) => Object.fromEntries(checks.map((c) => [c.id, c]));

test("没有凭据：只报登录，不继续往下查", async () => {
  const f = fixture({});
  const { checks } = await runLocalDoctor(f.ctx as any, config, f.tokens as any, f.http as any, f.bridge as any);
  assert.equal(checks.length, 1);
  assert.equal(checks[0].status, "fail");
  assert.match(String(checks[0].fix), /\/ee-login/);
});

test("令牌被吊销：给出 /ee-login；npmrc 被删：给出修复建议", async () => {
  await withNpmrc(null, async () => {
    const f = fixture({ token: "T", whoami: 401 });
    const { checks, summary } = await runLocalDoctor(f.ctx as any, config, f.tokens as any, f.http as any, f.bridge as any);
    const c = byId(checks);
    assert.equal(c.token.status, "fail");
    assert.match(c.token.fix, /\/ee-login/);
    assert.equal(c.npmrc.status, "warn");
    assert.match(c.npmrc.title, /缺少企业 npm 源配置/);
    assert.ok(c.npmrc.fix);
    assert.match(summary, /1 项失败/);
    assert.ok(!JSON.stringify(checks).includes('"T"'), "不能把令牌写进结果");
  });
});

test("健康环境加服务端结果：版本过旧与缺必装插件各报一项", async () => {
  await withNpmrc("//ee.test/npm/:_authToken=T\n", async () => {
    const server = {
      checks: [{ id: "quota", status: "ok", title: "额度充足" }],
      default_model: "chat", connect_version: "1.1.0", required_plugins: [{ name: "@acme/a" }, { name: "@acme/b" }],
    };
    const f = fixture({ token: "T", server, installed: ["@acme/a"] });
    const { checks, summary } = await runLocalDoctor(f.ctx as any, config, f.tokens as any, f.http as any, f.bridge as any);
    const c = byId(checks);
    assert.equal(c.token.status, "ok");
    assert.equal(c.npmrc.status, "ok");
    assert.equal(c.models.status, "ok");
    assert.equal(c["server.quota"].status, "ok");
    assert.equal(c.plugin_version.status, "warn");
    assert.match(c.required_plugins.title, /@acme\/b/);
    assert.equal(c.bridge.status, "ok");
    assert.match(summary, /2 项需要注意/);
  });
});

test("服务端 Agent 检查不可用时不算失败", async () => {
  await withNpmrc("//ee.test/npm/:_authToken=T\n", async () => {
    const f = fixture({ token: "T" });
    const { checks } = await runLocalDoctor(f.ctx as any, config, f.tokens as any, f.http as any, f.bridge as any);
    assert.ok(!checks.some((c) => c.status === "fail"));
  });
});

test("绑定文件损坏时 doctor 报 warn", async () => {
  const f = fixture({});
  const bindings = { warning: () => "微应用会话绑定文件已损坏" };
  const { checks } = await runLocalDoctor(f.ctx as any, config, f.tokens as any, f.http as any, f.bridge as any, bindings as any);
  assert.equal(byId(checks).app_bindings.status, "warn");
});


test("whoami 5xx 不报登录失效", async () => {
  await withNpmrc("//ee.test/npm/:_authToken=T\n", async () => {
    const f = fixture({ token: "T", whoami: 500 });
    const { checks } = await runLocalDoctor(f.ctx as any, config, f.tokens as any, f.http as any, f.bridge as any);
    const c = byId(checks).token;
    assert.equal(c.status, "fail");
    assert.match(c.title, /HTTP 500/);
    assert.doesNotMatch(c.title, /登录已失效/);
  });
});
