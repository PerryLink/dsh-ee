import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { merge, run, servers } from "../setup.mjs";

test("生成的配置：令牌只引用环境变量，不带 DSH 的客户端标识", () => {
  const s = servers({ server: "https://ee.acme.com/", name: "dsh-ee", local: ["@acme/oa-local"] });
  assert.deepEqual(s["dsh-ee"], { type: "http", url: "https://ee.acme.com/mcp", headers: { Authorization: "Bearer ${DSH_EE_TOKEN}" } });
  assert.ok(!JSON.stringify(s).includes("X-DshEE-Client"), "不能冒充 DSH，否则写操作绕过服务端确认");
  assert.deepEqual(s["acme-oa-local"].env, { DSH_EE_URL: "https://ee.acme.com", DSH_EE_TOKEN: "${DSH_EE_TOKEN}", DSH_EE_HARNESS: "claude-code" });
  assert.throws(() => servers({ server: "ftp://x", name: "a", local: [] }), /server/);
  assert.throws(() => servers({ server: "https://x", name: "a", local: ["bad name; rm -rf"] }), /npm/);
});

test("合并进已有的 .mcp.json，别的服务器不动；技能复制到 .claude/skills", () => {
  assert.deepEqual(merge({ mcpServers: { other: { command: "x" } }, extra: 1 }, { a: { url: "u" } }), { mcpServers: { other: { command: "x" }, a: { url: "u" } }, extra: 1 });
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "cc-"));
  const skills = fs.mkdtempSync(path.join(os.tmpdir(), "skills-"));
  fs.mkdirSync(path.join(skills, "dsh-ee-x"));
  fs.writeFileSync(path.join(skills, "dsh-ee-x", "SKILL.md"), "---\nname: dsh-ee-x\n---\n");
  fs.writeFileSync(path.join(dir, ".mcp.json"), JSON.stringify({ mcpServers: { keep: { command: "k" } } }));
  const logs = [];
  assert.equal(run(["--server", "http://127.0.0.1:8090", "--dir", dir, "--skills", skills], (s) => logs.push(s)), 0);
  const cfg = JSON.parse(fs.readFileSync(path.join(dir, ".mcp.json"), "utf8"));
  assert.deepEqual(Object.keys(cfg.mcpServers).sort(), ["dsh-ee", "keep"]);
  assert.ok(fs.existsSync(path.join(dir, ".claude", "skills", "dsh-ee-x", "SKILL.md")));
});
