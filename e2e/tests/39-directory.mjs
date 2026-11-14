// 企业插件目录管理页（docs/plugin-signing.md §8、#146）：
// 空态 → 导入签名目录（校验计划 / issues）→ 登记 → 列表显示条目 → 安装（只出计划 → 确认）→
// 插件进入待审核（不绕过审核）；错误目录被拦下；员工没有这一页和接口权限。
// 目录签名的私钥只在测试进程里生成，用 dsh-ee directory sign 签名；页面和接口都不碰私钥。
import { execFile } from "node:child_process";
import crypto from "node:crypto";
import fs from "node:fs";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import zlib from "node:zlib";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { ADMIN_PW, BASE, api, assert, assertEq, login, nav, newPage, shot, step, tag } from "../lib.mjs";

const run = promisify(execFile);
const here = path.dirname(fileURLToPath(import.meta.url));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/** 最小的 tar 写入器：npm pack 格式（package/ 目录），和 07-registry 用的一样。 */
function tgz(files) {
  const blocks = [];
  for (const [name, body] of Object.entries(files)) {
    const data = Buffer.from(body);
    const h = Buffer.alloc(512);
    h.write(`package/${name}`, 0);
    h.write("0000644\0", 100);
    h.write("0000000\0", 108);
    h.write("0000000\0", 116);
    h.write(data.length.toString(8).padStart(11, "0") + "\0", 124);
    h.write("00000000000\0", 136);
    h.write("        ", 148);
    h.write("0", 156);
    h.write("ustar\0" + "00", 257);
    let sum = 0;
    for (const b of h) sum += b;
    h.write(sum.toString(8).padStart(6, "0") + "\0 ", 148);
    blocks.push(h, data, Buffer.alloc((512 - (data.length % 512)) % 512));
  }
  blocks.push(Buffer.alloc(1024));
  return zlib.gzipSync(Buffer.concat(blocks));
}

/** 假的上游 registry：一个插件包，只提供固定版本。 */
function fakeUpstream(name, version) {
  const pkg = { name, version, license: "MIT", dsh: { bundle: { patch: "cordis.patch.yml" } } };
  const body = tgz({ "package.json": JSON.stringify(pkg), "cordis.patch.yml": "- {}\n" });
  const integrity = "sha512-" + crypto.createHash("sha512").update(body).digest("base64");
  const shasum = crypto.createHash("sha1").update(body).digest("hex");
  const hits = { packument: 0, tarball: 0 };
  const server = http.createServer((req, res) => {
    const url = decodeURIComponent(req.url);
    if (url === `/${name}`) {
      hits.packument++;
      res.setHeader("Content-Type", "application/json");
      return res.end(
        JSON.stringify({
          name,
          "dist-tags": { latest: version },
          time: { [version]: "2026-01-01T00:00:00Z" },
          versions: { [version]: { ...pkg, dist: { integrity, shasum, tarball: `http://127.0.0.1:${server.address().port}/${name}/-/${name}-${version}.tgz` } } },
        }),
      );
    }
    if (url === `/${name}/-/${name}-${version}.tgz`) {
      hits.tarball++;
      return res.end(body);
    }
    res.statusCode = 404;
    res.end("{}");
  });
  return { server, integrity, hits };
}

/** 生成目录签名用的 Ed25519 私钥 / 公钥 PEM（私钥只在测试临时目录里）。 */
function ed25519Keys() {
  const { publicKey, privateKey } = crypto.generateKeyPairSync("ed25519");
  return {
    priv: privateKey.export({ type: "pkcs8", format: "pem" }).toString(),
    pub: publicKey.export({ type: "spki", format: "pem" }).toString(),
  };
}

/** 用 dsh-ee directory sign 给目录签名（canonical JSON 由 Go 自己算，测试不重复实现）。 */
async function signDirectory(bin, docFile, privFile, outFile) {
  try {
    const r = await run(bin, ["directory", "sign", docFile, "--key", privFile, "-o", outFile, "--json"], { timeout: 20000 });
    return r.stdout;
  } catch (e) {
    throw new Error(`directory sign 失败：${(e.stdout ?? "") + (e.stderr ?? "")}`);
  }
}

export default async function (t) {
  const name = `dsh-plugin-dir-e2e-${tag}`;
  const version = "2.0.0";
  const up = fakeUpstream(name, version);
  await new Promise((r) => up.server.listen(0, "127.0.0.1", r));
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "dsh-ee-dir-"));
  const bin = process.env.E2E_BIN || path.resolve(here, "../../dist/dsh-ee");
  const page = await newPage(t);
  await login(page, "admin", ADMIN_PW);
  const original = (await api(page, "GET", "/api/settings")).data;

  try {
    // 上游指向假 registry，清单里用的固定版本 + 摘要都来自它。
    const settings = structuredClone(original);
    settings.upstream.mode = "online";
    settings.upstream.registry = "custom";
    settings.upstream.customRegistry = `http://127.0.0.1:${up.server.address().port}`;
    settings.registry.proxyMode = "cache";
    assertEq((await api(page, "PUT", "/api/admin/settings", settings)).status, 200, "设置 npm 上游");

    // 目录签名公钥（私钥只写临时文件，交给 dsh-ee directory sign）
    const keys = ed25519Keys();
    const privFile = path.join(tmp, "dir-key.pem");
    const pubFile = path.join(tmp, "dir-pub.pem");
    fs.writeFileSync(privFile, keys.priv);
    fs.writeFileSync(pubFile, keys.pub);
    assertEq(
      (await api(page, "POST", "/api/admin/signing-keys", { public_key: keys.pub, purpose: "directory", note: `e2e 目录签名 ${tag}` })).status,
      201,
      "加目录签名公钥",
    );

    // 未签名的目录文档 → CLI 签名
    const docFile = path.join(tmp, "dsh-ee-directory.json");
    const signedFile = path.join(tmp, "dsh-ee-directory.signed.json");
    fs.writeFileSync(
      docFile,
      JSON.stringify({
        format: 2,
        directory_id: `e2e-dir-${tag}`,
        title: { zh: "E2E 企业目录", en: "E2E enterprise directory" },
        generated_at: new Date().toISOString(),
        issuer: { name: "E2E 平台组" },
        entries: [
          {
            kind: "dsh-plugin",
            name,
            version,
            channel: "stable",
            digest: up.integrity,
            source: { type: "npm" },
            publisher: { username: "admin", department: "平台组" },
            notes: "e2e 目录安装",
          },
        ],
      }),
    );
    await signDirectory(bin, docFile, privFile, signedFile);
    const signedText = fs.readFileSync(signedFile, "utf8");
    assert(JSON.parse(signedText).signature?.key_id, "签名后的目录里应有 key_id");
    step(`生成并签名目录 e2e-dir-${tag}`);

    // 从管理后台菜单进入「企业插件目录」
    await page.goto(`${BASE}/admin`);
    await page.locator("aside").getByRole("link", { name: "企业插件目录" }).first().waitFor();
    await nav(page, { name: "企业插件目录" });
    await page.waitForURL((u) => u.pathname === "/admin/directory");
    await page.getByRole("heading", { name: "企业插件目录" }).waitFor();
    await page.getByText("还没有登记任何目录条目").waitFor();
    assertEq((await api(page, "GET", "/api/admin/directory")).data.count, 0, "初始没有登记条目");
    step("从菜单进入企业插件目录，空态");

    // 非法 JSON：校验报错，不会产生计划
    await page.getByTestId("directory-import-document").fill("{");
    await page.getByTestId("directory-check").click();
    await page.getByTestId("directory-import").getByRole("alert").waitFor();
    step("非法 JSON 被拦下并显示错误");

    // 未签名目录：校验不通过，登记按钮不可用
    const unsigned = JSON.stringify({ format: 2, directory_id: "e2e-unsigned", issuer: { name: "x" }, entries: [] });
    await page.getByTestId("directory-import-document").fill(unsigned);
    await page.getByTestId("directory-check").click();
    await page.getByTestId("directory-plan").waitFor();
    await page.getByText("没有通过").first().waitFor();
    assert(await page.getByTestId("directory-apply").isDisabled(), "校验不通过时登记按钮应禁用");
    step("未签名 / 非法目录不能登记");

    // 签名目录：校验通过 → 登记
    await page.getByTestId("directory-import-document").fill(signedText);
    await page.getByTestId("directory-check").click();
    await page.getByTestId("directory-plan").waitFor();
    await page.getByText("目录签名有效").waitFor();
    assertEq((await page.getByTestId("directory-plan-id").innerText()).trim(), `e2e-dir-${tag}`, "计划里的目录标识");
    await page.getByTestId("directory-apply").click();
    await page.getByText(/已登记：新增 1/).waitFor();
    assertEq((await api(page, "GET", "/api/admin/directory")).data.count, 1, "登记后应有一条条目");
    step("校验通过并登记目录，列表出现条目");

    const row = page.getByTestId("directory-entries").locator("tbody tr").first();
    await row.getByText("未安装").waitFor();
    await row.getByText(name).waitFor();
    await row.getByText("插件").first().waitFor();
    step("列表显示条目：类型 / 名称 / 来源 / 未安装");

    // 安装：先只出计划（不启动同步），确认后才执行
    const jobsBefore = (await api(page, "GET", "/api/admin/upstream/jobs")).data.jobs.length;
    assertEq(jobsBefore, 0, "安装前不应该有同步任务");
    await row.getByRole("button", { name: "安装" }).click();
    const dialog = page.getByTestId("directory-action-dialog");
    await dialog.waitFor();
    await dialog.getByTestId("directory-action-plan").waitFor();
    await dialog.getByText("同步后待审核").waitFor();
    assertEq((await api(page, "GET", "/api/admin/upstream/jobs")).data.jobs.length, jobsBefore, "只出计划不应启动同步任务");
    await dialog.getByTestId("directory-action-confirm").click();
    await dialog.getByTestId("directory-action-result").waitFor();
    await dialog.getByText("同步任务").waitFor();
    step("安装对话框：只出计划 → 确认 → 已开始同步");

    // 关掉对话框，列表刷新后条目变成「待审核」（同步 → pending，不绕过审核）
    await dialog.getByTestId("directory-action-close").click();
    let pending = null;
    for (let i = 0; i < 80; i++) {
      const list = (await api(page, "GET", "/api/admin/directory")).data;
      pending = list.entries.find((e) => e.name === name && e.version === version);
      if (pending?.installed_status === "pending") break;
      await sleep(250);
    }
    assertEq(pending?.installed_status, "pending", "同步后的插件版本应进入待审核");
    assertEq(pending?.installed, false, "待审核版本不应算已安装");
    await page.getByTestId("directory-page").getByRole("button", { name: "刷新" }).click();
    await row.getByText("待审核").waitFor();
    step("同步完成：列表显示待审核（审核通过前不算已安装）");
    await shot(page, "directory");

    // 员工：菜单里没有这一页，直接访问是「没有权限」，接口 403
    const uname = `dir.${tag}`;
    assertEq(
      (await api(page, "POST", "/api/admin/users", { username: uname, display_name: "E2E 目录只读", password: "qa-password-12" })).status,
      201,
      "创建员工账号",
    );
    const member = await newPage(t);
    await login(member, uname, "qa-password-12");
    assertEq((await api(member, "GET", "/api/admin/directory")).status, 403, "员工读目录应 403");
    await member.goto(`${BASE}/admin/directory`);
    await member.getByText("页面不存在，或你没有访问权限").waitFor();
    step("员工没有企业插件目录页和接口权限");
  } finally {
    await new Promise((r) => up.server.close(r));
    fs.rmSync(tmp, { recursive: true, force: true });
  }
}
