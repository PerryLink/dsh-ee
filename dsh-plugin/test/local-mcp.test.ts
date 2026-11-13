import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import {
  createLocalMcp,
  deriveExtensionId,
  localMcpEntry,
  localMcpSnippet,
  localServerName,
  parseExtensionId,
  parseLocalCommand,
  parsePackageSpec,
  readLocalExtensions,
  registerLocalMcpCommand,
} from "../src/local-mcp.js";
import { normalizeConfig } from "../src/config.js";

const config = normalizeConfig({ base: "https://ee.example.com/", tokenRef: "DSHEE_TOKEN" });

/** 假的 dsh-mcp-manager：拒绝重名，记下注册 / 注销。 */
function fakeManager(configured: string[] = []) {
  const registered = new Map<string, any>();
  const calls: Array<[string, any]> = [];
  const disposed: string[] = [];
  const taken = () => new Set([...configured, ...registered.keys()]);
  const manager = {
    list: () => [...taken()].map((name) => ({ name, configured: configured.includes(name), status: { state: "connected" } })),
    register: (name: string, entry: any) => {
      if (taken().has(name)) throw new Error(`mcp-manager: server "${name}" is already configured or registered`);
      registered.set(name, entry);
      calls.push([name, entry]);
      return () => {
        if (registered.delete(name)) disposed.push(name);
      };
    },
  };
  return { manager, registered, calls, disposed };
}

function ctxWith(services: Record<string, any>) {
  return {
    get: (name: string) => services[name],
    effect: (fn: () => any) => fn(),
  } as any;
}

function tempStore(): string {
  return path.join(fs.mkdtempSync(path.join(os.tmpdir(), "dsh-ee-local-mcp-")), "local-mcp.json");
}

const baseOptions = () => ({ store: tempStore(), npmrc: "/home/alice/.dsh/profiles/web/.npmrc", platform: "linux" as const });

test("包名解析：作用域包、版本、非 npm 地址都处理", () => {
  assert.deepEqual(parsePackageSpec("@acme/legacy-oa-local"), { package: "@acme/legacy-oa-local", version: "" });
  assert.deepEqual(parsePackageSpec("@acme/legacy-oa-local@0.1.0"), { package: "@acme/legacy-oa-local", version: "0.1.0" });
  assert.deepEqual(parsePackageSpec(" legacy-oa-local "), { package: "legacy-oa-local", version: "" });
  assert.deepEqual(parsePackageSpec("legacy-oa-local@1.2.3-rc.1"), { package: "legacy-oa-local", version: "1.2.3-rc.1" });
  for (const bad of ["", "   ", "file:../sdk", "https://x/y.tgz", "git+https://x/y.git", "@acme/", "@/x", "a@", "@", "x@1 2", "Abc/Def"]) {
    const parsed = parsePackageSpec(bad);
    assert.ok("error" in parsed, `应拒绝 ${JSON.stringify(bad)}`);
  }
});

test("扩展 id 与服务名：只用 [A-Za-z0-9_-]，服务名不超过 32", () => {
  assert.equal(parseExtensionId("acme/legacy-oa"), "acme/legacy-oa");
  assert.equal(parseExtensionId("../x"), undefined);
  assert.equal(parseExtensionId("Acme/x"), undefined);
  assert.equal(localServerName("acme/legacy-oa", "@acme/legacy-oa-local"), "acme-legacy-oa");
  assert.equal(localServerName(undefined, "@acme/legacy-oa-local"), "acme-legacy-oa-local");
  assert.equal(localServerName(undefined, "legacy-oa-local"), "legacy-oa-local");
  const long = localServerName("acme/" + "x".repeat(80), "x");
  assert.ok(long.length <= 32 && /^[A-Za-z0-9_-]{1,32}$/.test(long));
  assert.equal(localServerName("!!", "!!"), "ee-local");
  assert.equal(deriveExtensionId("@acme/legacy-oa-local"), "acme/legacy-oa-local");
  assert.equal(deriveExtensionId("legacy-oa-local"), "local/legacy-oa-local");
});

test("注册项：npx 起包，env 走凭据模板与企业 npm 源，不落令牌明文", () => {
  const entry = localMcpEntry(config, { package: "@acme/legacy-oa-local", version: "0.1.0" }, { npmrc: "/p/.npmrc", platform: "linux" });
  assert.equal(entry.transport, "stdio");
  assert.equal(entry.command, "npx");
  assert.deepEqual(entry.args, ["-y", "@acme/legacy-oa-local@0.1.0"]);
  assert.deepEqual(entry.env, {
    DSH_EE_URL: "https://ee.example.com",
    DSH_EE_TOKEN: "${credential:DSHEE_TOKEN}",
    DSH_EE_HARNESS: "dsh",
    NPM_CONFIG_USERCONFIG: "/p/.npmrc",
  });
  assert.equal(entry.startupTimeoutMs, 60_000);
  assert.ok(!JSON.stringify(entry).includes("ee_at_"), "注册项里不能有令牌明文");
  assert.equal(localMcpEntry(config, { package: "@acme/legacy-oa-local", version: "" }, { npmrc: "", platform: "win32" }).command, "npx.cmd");
  assert.deepEqual(localMcpEntry(config, { package: "@acme/legacy-oa-local", version: "" }, { npmrc: "", platform: "win32" }).args, ["-y", "@acme/legacy-oa-local"]);
});

test("配置片段：可复制的 cordis.patch.yml 一行，带凭据模板和 expandTemplates", () => {
  const snippet = localMcpSnippet(config, { id: "acme/legacy-oa", package: "@acme/legacy-oa-local", version: "0.1.0", serverName: "acme-legacy-oa" }, "/p/.npmrc");
  assert.match(snippet, /name: '@deepseek-ai\/dsh-mcp-client'/);
  assert.match(snippet, /serverName: 'acme-legacy-oa'/);
  assert.match(snippet, /transport: stdio/);
  assert.match(snippet, /command: 'npx'/);
  assert.match(snippet, /args: \['-y', '@acme\/legacy-oa-local@0\.1\.0'\]/);
  assert.match(snippet, /DSH_EE_TOKEN: '\$\{credential:DSHEE_TOKEN\}'/);
  assert.match(snippet, /NPM_CONFIG_USERCONFIG: '\/p\/\.npmrc'/);
  assert.match(snippet, /expandTemplates: true/);
  assert.doesNotMatch(snippet, /ee_at_/);
});

test("命令解析：list/add/remove/snippet、--id、--yes 与错误参数", () => {
  assert.deepEqual(parseLocalCommand(undefined), { action: "list", target: "", yes: false });
  assert.deepEqual(parseLocalCommand(""), { action: "list", target: "", yes: false });
  assert.deepEqual(parseLocalCommand("list"), { action: "list", target: "", yes: false });
  assert.deepEqual(parseLocalCommand("add @acme/legacy-oa-local@0.1.0 --id acme/legacy-oa --yes"), {
    action: "add", target: "@acme/legacy-oa-local@0.1.0", id: "acme/legacy-oa", yes: true,
  });
  assert.deepEqual(parseLocalCommand("remove acme/legacy-oa"), { action: "remove", target: "acme/legacy-oa", yes: false });
  assert.deepEqual(parseLocalCommand("snippet @acme/x -y"), { action: "snippet", target: "@acme/x", yes: true });
  assert.match(parseLocalCommand("install x").error!, /子命令/);
  assert.match(parseLocalCommand("add").error!, /要跟包名/);
  assert.match(parseLocalCommand("add x --id").error!, /--id/);
  assert.match(parseLocalCommand("add x --id ../x").error!, /扩展 id/);
  assert.match(parseLocalCommand("add x --unknown").error!, /不认识的参数/);
  assert.match(parseLocalCommand("add a b").error!, /多余参数/);
});

test("添加：先给计划不落地；--yes 才注册并写记录；重复添加不重装", async () => {
  const fake = fakeManager();
  const ctx = ctxWith({ mcpManager: fake.manager });
  const options = baseOptions();
  const local = createLocalMcp(ctx, config, options);

  const preview = await local.add(parseLocalCommand("add @acme/legacy-oa-local@0.1.0 --id acme/legacy-oa"));
  assert.equal(preview.kind, "success");
  assert.match(preview.text, /确认后执行/);
  assert.match(preview.text, /mcp__acme-legacy-oa__/);
  assert.equal(fake.registered.size, 0, "没确认前不注册");
  assert.deepEqual(readLocalExtensions(options.store), []);

  const done = await local.add(parseLocalCommand("add @acme/legacy-oa-local@0.1.0 --id acme/legacy-oa --yes"));
  assert.equal(done.kind, "success");
  assert.match(done.text, /已注册/);
  assert.equal(fake.registered.size, 1);
  const [[name, entry]] = fake.calls;
  assert.equal(name, "acme-legacy-oa");
  assert.deepEqual(entry.args, ["-y", "@acme/legacy-oa-local@0.1.0"]);
  const stored = readLocalExtensions(options.store);
  assert.equal(stored.length, 1);
  assert.equal(stored[0].id, "acme/legacy-oa");
  assert.equal(stored[0].serverName, "acme-legacy-oa");

  const again = await local.add(parseLocalCommand("add @acme/legacy-oa-local@0.1.0 --id acme/legacy-oa --yes"));
  assert.match(again.text, /已经添加过/);
  assert.equal(fake.calls.length, 1);
});

test("不覆盖：已有同名 MCP 服务时报错，不注册也不写记录", async () => {
  const fake = fakeManager(["acme-legacy-oa"]);
  const local = createLocalMcp(ctxWith({ mcpManager: fake.manager }), config, baseOptions());
  const result = await local.add(parseLocalCommand("add @acme/legacy-oa-local@0.1.0 --id acme/legacy-oa --yes"));
  assert.equal(result.kind, "error");
  assert.match(result.text, /不覆盖/);
  assert.equal(fake.calls.length, 0);
});

test("换版本：同一扩展重新注册新的包版本，记录只留一条", async () => {
  const fake = fakeManager();
  const options = baseOptions();
  const local = createLocalMcp(ctxWith({ mcpManager: fake.manager }), config, options);
  await local.add(parseLocalCommand("add @acme/legacy-oa-local@0.1.0 --id acme/legacy-oa --yes"));
  const updated = await local.add(parseLocalCommand("add @acme/legacy-oa-local@0.2.0 --id acme/legacy-oa --yes"));
  assert.equal(updated.kind, "success");
  assert.equal(fake.calls.length, 2);
  assert.deepEqual(fake.calls[1][1].args, ["-y", "@acme/legacy-oa-local@0.2.0"]);
  assert.deepEqual(fake.disposed, ["acme-legacy-oa"]);
  const stored = readLocalExtensions(options.store);
  assert.equal(stored.length, 1);
  assert.equal(stored[0].version, "0.2.0");
});

test("没有 mcpManager：add 报阻塞并给配置片段；snippet 命令始终可用", async () => {
  const local = createLocalMcp(ctxWith({}), config, baseOptions());
  assert.equal(local.available, false);
  const result = await local.add(parseLocalCommand("add @acme/legacy-oa-local@0.1.0 --id acme/legacy-oa --yes"));
  assert.equal(result.kind, "error");
  assert.match(result.text, /没有 MCP 管理接口/);
  assert.match(result.text, /@deepseek-ai\/dsh-mcp-client/);
  assert.match(result.text, /cordis\.patch\.yml/);
  assert.deepEqual(readLocalExtensions(baseOptions().store), []);

  const snippet = local.snippet(parseLocalCommand("snippet @acme/legacy-oa-local@0.1.0 --id acme/legacy-oa"));
  assert.equal(snippet.kind, "success");
  assert.match(snippet.text, /serverName: 'acme-legacy-oa'/);
});

test("启动时按记录恢复注册；同名服务已存在时跳过、不覆盖；stop 注销全部", async () => {
  const fake = fakeManager();
  const options = baseOptions();
  const first = createLocalMcp(ctxWith({ mcpManager: fake.manager }), config, options);
  await first.add(parseLocalCommand("add @acme/legacy-oa-local@0.1.0 --id acme/legacy-oa --yes"));
  first.stop();
  assert.deepEqual(fake.disposed, ["acme-legacy-oa"]);

  // 员工自己又在别处配了同名服务：启动时不能覆盖
  const blocked = fakeManager(["acme-legacy-oa", "acme-other"]);
  const second = createLocalMcp(ctxWith({ mcpManager: blocked.manager }), config, options);
  const started = await second.start();
  assert.deepEqual(started.registered, []);
  assert.equal(started.skipped.length, 1);
  assert.match(started.skipped[0].reason, /不覆盖/);
  second.stop();

  // 没有冲突时按记录自动恢复（DSH 重启后员工不用再加一次）
  const clean = fakeManager();
  const third = createLocalMcp(ctxWith({ mcpManager: clean.manager }), config, options);
  const restored = await third.start();
  assert.deepEqual(restored.registered, ["acme/legacy-oa"]);
  assert.equal(clean.registered.get("acme-legacy-oa")?.args[1], "@acme/legacy-oa-local@0.1.0");
  third.stop();
  assert.deepEqual(clean.disposed, ["acme-legacy-oa"]);
});

test("没有 mcpManager 时启动不抛错；移除按 id / 包名 / 服务名都能找到", async () => {
  const options = baseOptions();
  const noManager = createLocalMcp(ctxWith({}), config, options);
  const started = await noManager.start();
  assert.deepEqual(started, { registered: [], skipped: [] });

  const fake = fakeManager();
  const local = createLocalMcp(ctxWith({ mcpManager: fake.manager }), config, options);
  await local.add(parseLocalCommand("add @acme/legacy-oa-local@0.1.0 --id acme/legacy-oa --yes"));
  const removed = await local.remove(parseLocalCommand("remove @acme/legacy-oa-local"));
  assert.equal(removed.kind, "success");
  assert.deepEqual(fake.disposed, ["acme-legacy-oa"]);
  assert.deepEqual(readLocalExtensions(options.store), []);
  assert.match((await local.remove(parseLocalCommand("remove @acme/legacy-oa-local"))).text, /没有找到/);
});

test("list 显示记录与状态；/ee-local 命令接到同一个句柄", async () => {
  const fake = fakeManager();
  const registered: any[] = [];
  const ctx = ctxWith({
    mcpManager: fake.manager,
    commands: { register: (definition: any) => { registered.push(definition); return () => undefined; } },
  });
  const local = createLocalMcp(ctx, config, baseOptions());
  registerLocalMcpCommand(ctx, local);
  assert.equal(registered.length, 1);
  assert.equal(registered[0].name, "ee-local");

  const empty = await registered[0].handler({ rawInput: "" });
  assert.match(empty.text, /还没有添加/);

  const added = await registered[0].handler({ rawInput: "add @acme/legacy-oa-local@0.1.0 --id acme/legacy-oa --yes" });
  assert.equal(added.kind, "success");
  const listed = await registered[0].handler({ rawInput: "list" });
  assert.match(listed.text, /acme\/legacy-oa/);
  assert.match(listed.text, /serverName=acme-legacy-oa/);
  assert.match(listed.text, /connected/);
  assert.equal((await registered[0].handler({ rawInput: "help" })).kind, "success");
  assert.equal((await registered[0].handler({ rawInput: "bad" })).kind, "error");
});

test("本机记录文件坏了不影响启动，只按空记录处理", () => {
  const file = tempStore();
  fs.writeFileSync(file, "{ not json");
  assert.deepEqual(readLocalExtensions(file), []);
  fs.writeFileSync(file, JSON.stringify({ version: 1, extensions: [{ id: "../x" }, { id: "acme/x", package: "@acme/x-local", version: "0.1.0", serverName: "acme-x", addedAt: "2026-01-01T00:00:00.000Z" }] }));
  assert.deepEqual(readLocalExtensions(file).map((item) => item.id), ["acme/x"]);
});

test("员工改了 dsh-ee 地址：refresh 按新地址重新注册，令牌仍走凭据模板", async () => {
  const fake = fakeManager();
  const options = baseOptions();
  const local = createLocalMcp(ctxWith({ mcpManager: fake.manager }), config, options);
  await local.add(parseLocalCommand("add @acme/legacy-oa-local@0.1.0 --id acme/legacy-oa --yes"));
  assert.equal(fake.calls[0][1].env.DSH_EE_URL, "https://ee.example.com");

  config.base = "https://ee2.example.com";
  try {
    const refreshed = await local.refresh();
    assert.deepEqual(refreshed.registered, ["acme/legacy-oa"]);
    const last = fake.calls[fake.calls.length - 1];
    assert.equal(last[1].env.DSH_EE_URL, "https://ee2.example.com");
    assert.equal(last[1].env.DSH_EE_TOKEN, "${credential:DSHEE_TOKEN}");
  } finally {
    config.base = "https://ee.example.com";
  }
});
