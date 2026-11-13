// 隔离模式的服务端管理：每个测试文件启动一个全新的 dsh-ee + 独立数据库 + 独立数据目录。
//
// CI 用 E2E_MANAGED=1 打开；本地不设时仍可用外部已启动的服务端（兼容旧文档）。
import { spawn, spawnSync } from "node:child_process";
import net from "node:net";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));

export function isManaged() {
  return process.env.E2E_MANAGED === "1";
}

function baseDsn() {
  const raw = process.env.E2E_DSN;
  if (!raw) throw new Error("隔离模式需要 E2E_DSN（例如 root:@tcp(127.0.0.1:4000)/）");
  const noQuery = raw.split("?")[0];
  const slash = noQuery.lastIndexOf("/");
  if (slash < 0) throw new Error(`E2E_DSN 缺少数据库分隔符：${raw}`);
  return noQuery.slice(0, slash + 1);
}

function baseUrl() {
  return (process.env.E2E_BASE || "http://127.0.0.1:8080").replace(/\/+$/, "");
}

function portOf(base) {
  const u = new URL(base);
  return Number(u.port || (u.protocol === "https:" ? 443 : 80));
}

function binary() {
  const bin = process.env.E2E_BIN || path.resolve(here, "../dist/dsh-ee");
  if (!fs.existsSync(bin)) throw new Error(`找不到 E2E_BIN：${bin}；先构建 dist/dsh-ee`);
  return bin;
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/** 隔离模式必须自己占端口；如果已有服务在监听，不能把健康检查误认成新进程。 */
async function assertPortFree(port) {
  await new Promise((resolve, reject) => {
    const s = net.createServer();
    s.once("error", (e) => reject(new Error(`端口 ${port} 已被占用：${e.message}`)));
    s.listen(port, "127.0.0.1", () => s.close(resolve));
  });
}

function waitExit(child, ms) {
  return new Promise((resolve) => {
    if (child.exitCode !== null) return resolve(true);
    const timer = setTimeout(() => resolve(false), ms);
    child.once("exit", () => {
      clearTimeout(timer);
      resolve(true);
    });
  });
}

function dropDatabases(dsnBase, db) {
  if (process.env.E2E_KEEP_DB === "1") return;
  const m = /^(?:(?<user>[^:@/]*)(?::(?<pass>[^@/]*))?@)?tcp\((?<addr>[^)]+)\)/.exec(dsnBase);
  if (!m) return;
  const [host, port] = m.groups.addr.split(":");
  const dataDb = `${db}_data`;
  // datahub 会自动建 <主库>_data 和 <dataDb>_reader 只读账号；只删主库会留下这两样。
  const reader = `${dataDb}_reader`.slice(-32);
  const sql = `DROP DATABASE IF EXISTS \`${db}\`; DROP DATABASE IF EXISTS \`${dataDb}\`; DROP USER IF EXISTS '${reader}'@'%'`;
  const args = ["-h", host, "-P", port || "3306", "-u", m.groups.user || "root"];
  if (m.groups.pass) args.push(`-p${m.groups.pass}`);
  args.push("-e", sql);
  const r = spawnSync(process.env.E2E_MYSQL || "mysql", args, { stdio: "ignore", timeout: 15000 });
  if (r.error && r.error.code !== "ENOENT") {
    console.error(`  清理数据库 ${db}/${dataDb} 失败：${r.error.message}`);
  } else if (typeof r.status === "number" && r.status !== 0) {
    console.error(`  清理数据库 ${db}/${dataDb} 失败：mysql 退出码 ${r.status}（库名已保留）`);
  }
}

/** 找一个空闲端口（给需要额外隔离实例的测试用，例如低限流实例）。 */
export async function findFreePort() {
  return await new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const port = server.address().port;
      server.close(() => resolve(port));
    });
  });
}

/**
 * 启动一个隔离的服务端。调用方必须在测试结束后 await stop()。
 * 默认端口沿用 E2E_BASE（默认 8080）；每个测试串行启动，所以不会冲突。
 *
 * 可选参数（35-harness-sdk 的 429 用例需要低限流实例）：
 *   port  指定端口（默认取 E2E_BASE 的端口；传 findFreePort() 的结果可起第二个实例）
 *   env   额外环境变量，例如 { DSH_EE_RATE_LIMIT: "3" }
 */
export async function startManagedServer({ port: portOption = 0, env: extraEnv = {} } = {}) {
  const dsnBase = baseDsn();
  const bin = binary();
  const db = `dsh_ee_e2e_${process.pid.toString(36)}_${crypto.randomBytes(3).toString("hex")}`;
  const dsn = dsnBase + db;
  const base = baseUrl();
  const basePort = portOf(base);
  const port = portOption || basePort;
  const url = port === basePort ? base : `http://127.0.0.1:${port}`;
  await assertPortFree(port);
  const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), "dsh-ee-e2e-"));
  const cleanup = () => {
    dropDatabases(dsnBase, db);
    fs.rmSync(dataDir, { recursive: true, force: true });
  };
  const env = {
    ...process.env,
    ...extraEnv,
    DSH_EE_ADDR: `127.0.0.1:${port}`,
    DSH_EE_DSN: dsn,
    DSH_EE_SECRET_KEY: crypto.randomBytes(32).toString("base64"),
    DSH_EE_BOOTSTRAP_ADMIN_PASSWORD: process.env.ADMIN_PW || "",
    DSH_EE_DATA_DIR: dataDir,
  };
  let output = "";
  const child = spawn(bin, [], { env, stdio: ["ignore", "pipe", "pipe"], windowsHide: true });
  child.on("error", (e) => { output += `\nspawn: ${e.message}`; });
  child.stdout.on("data", (b) => { output += b; });
  child.stderr.on("data", (b) => { output += b; });

  const deadline = Date.now() + Number(process.env.E2E_START_TIMEOUT_MS || 30_000);
  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      cleanup();
      throw new Error(`服务端启动即退出（code=${child.exitCode}）：\n${output.slice(-4000)}`);
    }
    try {
      const r = await fetch(`${url}/healthz`);
      if (r.ok) {
        return {
          dsn: dsnBase,
          db,
          dataDir,
          url,
          logs: () => output.slice(-8000),
          stop: async () => {
            if (child.exitCode === null) {
              child.kill("SIGTERM");
              if (!(await waitExit(child, 5000))) {
                child.kill("SIGKILL");
                await waitExit(child, 2000);
              }
            }
            cleanup();
          },
        };
      }
    } catch {
      // 还没起来
    }
    await sleep(150);
  }
  child.kill("SIGKILL");
  await waitExit(child, 2000);
  cleanup();
  throw new Error(`服务端 30s 内没有就绪：${url}\n${output.slice(-4000)}`);
}
