import assert from "node:assert/strict";
import fs from "node:fs";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";

// 假的老 OA：要带 JSESSIONID=ok 才给数据，否则 302 到登录页
function fakeOA() {
  const posted: unknown[] = [];
  const srv = http.createServer((req, res) => {
    if (!(req.headers.cookie ?? "").includes("JSESSIONID=ok")) {
      res.writeHead(302, { location: "/login" }).end();
      return;
    }
    if (req.method === "GET" && req.url?.startsWith("/api/forms")) {
      res.writeHead(200, { "content-type": "application/json" }).end(JSON.stringify([{ id: "F-1", title: "年假", status: "pending", internal: "不该给模型" }]));
      return;
    }
    let body = "";
    req.on("data", (c) => (body += c));
    req.on("end", () => {
      posted.push(JSON.parse(body));
      res.writeHead(200, { "content-type": "application/json" }).end(JSON.stringify({ id: "F-2" }));
    });
  });
  return { srv, posted };
}

// 假的 dsh-ee：一律放行，记下回报
function fakeEE() {
  const seen: string[] = [];
  const srv = http.createServer((req, res) => {
    seen.push(req.url ?? "");
    if (req.url === "/api/local/confirm") res.writeHead(200, { "content-type": "application/json" }).end(JSON.stringify({ approved: true }));
    else res.writeHead(204).end();
  });
  return { srv, seen };
}

const listen = (s: http.Server) => new Promise<number>((r) => s.listen(0, "127.0.0.1", () => r((s.address() as any).port)));

test("带着员工的 cookie 取数、提交；写操作先经 dsh-ee", async () => {
  const oa = fakeOA();
  const ee = fakeEE();
  const oaPort = await listen(oa.srv);
  const eePort = await listen(ee.srv);
  const home = fs.mkdtempSync(path.join(os.tmpdir(), "legacy-oa-"));
  process.env.DSH_EE_LOCAL_HOME = home;
  process.env.LEGACY_OA_URL = `http://127.0.0.1:${oaPort}`;
  process.env.DSH_EE_URL = `http://127.0.0.1:${eePort}`;
  process.env.DSH_EE_TOKEN = "T";
  // 员工之前已经登录过：cookie 在本机
  fs.mkdirSync(path.join(home, "acme_legacy-oa"), { recursive: true });
  fs.writeFileSync(path.join(home, "acme_legacy-oa", "cookies.json"), JSON.stringify([{ name: "JSESSIONID", value: "ok", domain: "127.0.0.1", path: "/", expires: -1 }]));
  try {
    const { default: ext } = await import("../src/extension.js");
    const list = await ext.callTool("list_forms", { status: "pending" });
    assert.equal(list.isError, undefined, JSON.stringify(list));
    assert.match(list.content[0].text!, /F-1/);
    assert.doesNotMatch(list.content[0].text!, /不该给模型/);

    const bad = await ext.callTool("submit_leave", { days: 99 });
    assert.deepEqual(bad._meta, { "dsh-ee/error": { code: "invalid" } });

    const ok = await ext.callTool("submit_leave", { days: 2, reason: "年假" });
    assert.equal(ok.content[0].text, "已提交请假单 F-2");
    assert.deepEqual(oa.posted, [{ type: "leave", days: 2, reason: "年假" }]);
    await new Promise((r) => setTimeout(r, 50));
    assert.ok(ee.seen.filter((u) => u === "/api/local/confirm").length >= 1, "写操作要先问 dsh-ee");
    assert.equal(ee.seen.filter((u) => u === "/api/local/audit").length, 3, "三次调用回报三次");
  } finally {
    oa.srv.close();
    ee.srv.close();
  }
});
