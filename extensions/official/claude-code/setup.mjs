#!/usr/bin/env node
/**
 * Claude Code 适配器（docs/plugins.md §6.4「Agent 适配器」、§10 的 official/claude-code）。
 *
 * 在一个项目目录里：
 *   - 把 dsh-ee 的 /mcp 写进 .mcp.json（Streamable HTTP，令牌从环境变量 DSH_EE_TOKEN 取，不写进文件）；
 *   - 端侧扩展（--local @acme/oa-local）作为本机 MCP 服务写进去，带上 DSH_EE_URL / DSH_EE_TOKEN / DSH_EE_HARNESS；
 *   - 把 dsh-ee 的技能（extensions/skills/*）复制到 .claude/skills/。
 *
 * 不写 X-DshEE-Client 请求头：Claude Code 可以被设成自动同意，写操作要走 dsh-ee 的服务端确认（§7.5）。
 *
 *   node setup.mjs --server https://ee.acme.com [--dir 项目目录] [--name dsh-ee] [--local @acme/oa-local ...] [--skills 技能目录] [--dry-run]
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));

export function parseArgs(argv) {
  const o = { dir: process.cwd(), name: "dsh-ee", local: [], skills: path.resolve(here, "../../skills"), dryRun: false };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    const next = () => {
      if (i + 1 >= argv.length) throw new Error(`${a} 后面要跟一个值`);
      return argv[++i];
    };
    if (a === "--server") o.server = next();
    else if (a === "--dir") o.dir = path.resolve(next());
    else if (a === "--name") o.name = next();
    else if (a === "--local") o.local.push(next());
    else if (a === "--skills") o.skills = path.resolve(next());
    else if (a === "--dry-run") o.dryRun = true;
    else if (a === "-h" || a === "--help") o.help = true;
    else throw new Error(`不认识的参数 ${a}`);
  }
  return o;
}

const PKG = /^(@[a-z0-9][a-z0-9._-]*\/)?[a-z0-9][a-z0-9._-]*$/;

/** 生成要合并进 .mcp.json 的服务器配置。 */
export function servers(o) {
  let base;
  try {
    const u = new URL(o.server);
    if (u.protocol !== "https:" && u.protocol !== "http:") throw new Error();
    base = u.origin + u.pathname.replace(/\/+$/, "");
  } catch {
    throw new Error(`--server ${JSON.stringify(o.server ?? "")} 不对，要像 https://ee.acme.com 这样`);
  }
  if (!/^[A-Za-z0-9_-]{1,40}$/.test(o.name)) throw new Error("--name 只能是字母、数字、_ 和 -");
  const out = {
    [o.name]: { type: "http", url: `${base}/mcp`, headers: { Authorization: "Bearer ${DSH_EE_TOKEN}" } },
  };
  for (const pkg of o.local) {
    if (!PKG.test(pkg)) throw new Error(`--local ${JSON.stringify(pkg)} 不是合法的 npm 包名`);
    const key = pkg.replace(/^@/, "").replace(/[^A-Za-z0-9_-]/g, "-");
    out[key] = { command: "npx", args: ["-y", pkg], env: { DSH_EE_URL: base, DSH_EE_TOKEN: "${DSH_EE_TOKEN}", DSH_EE_HARNESS: "claude-code" } };
  }
  return out;
}

/** 合并进已有的 .mcp.json：同名的换掉，别的不动。 */
export function merge(existing, add) {
  const cur = existing && typeof existing === "object" ? existing : {};
  return { ...cur, mcpServers: { ...(cur.mcpServers ?? {}), ...add } };
}

function copyDir(src, dst, dryRun, log) {
  for (const ent of fs.readdirSync(src, { withFileTypes: true })) {
    const s = path.join(src, ent.name);
    const d = path.join(dst, ent.name);
    if (ent.isDirectory()) copyDir(s, d, dryRun, log);
    else {
      log(`写 ${d}`);
      if (!dryRun) {
        fs.mkdirSync(path.dirname(d), { recursive: true });
        fs.copyFileSync(s, d);
      }
    }
  }
}

export function run(argv, log = (s) => console.log(s)) {
  const o = parseArgs(argv);
  if (o.help || !o.server) {
    log("用法：node setup.mjs --server https://ee.acme.com [--dir 项目目录] [--local @acme/oa-local ...] [--dry-run]");
    return o.help ? 0 : 2;
  }
  const file = path.join(o.dir, ".mcp.json");
  let existing = {};
  if (fs.existsSync(file)) {
    try {
      existing = JSON.parse(fs.readFileSync(file, "utf8"));
    } catch {
      throw new Error(`${file} 不是合法的 JSON，先手工修好再运行`);
    }
  }
  const next = merge(existing, servers(o));
  log(`写 ${file}`);
  if (!o.dryRun) fs.writeFileSync(file, JSON.stringify(next, null, 2) + "\n");
  if (fs.existsSync(o.skills)) {
    for (const ent of fs.readdirSync(o.skills, { withFileTypes: true })) {
      if (ent.isDirectory() && fs.existsSync(path.join(o.skills, ent.name, "SKILL.md"))) {
        copyDir(path.join(o.skills, ent.name), path.join(o.dir, ".claude", "skills", ent.name), o.dryRun, log);
      }
    }
  }
  log("完成。启动 Claude Code 前先 export DSH_EE_TOKEN=<你在 dsh-ee「我的令牌」里建的令牌>。");
  return 0;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  try {
    process.exitCode = run(process.argv.slice(2));
  } catch (e) {
    console.error(e instanceof Error ? e.message : String(e));
    process.exitCode = 1;
  }
}
