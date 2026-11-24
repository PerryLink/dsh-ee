import { execFile } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { ADMIN_PW, BASE, SHOTS, api, assert, assertEq, login, newPage, step, tag } from "../lib.mjs";

const exec = promisify(execFile);
const fixture = fileURLToPath(new URL("../fixtures/conan-source", import.meta.url));
const conan = process.env.CONAN_BIN;
const liveProxy = process.env.E2E_PACKAGE_PROXY === "1";
const record = process.env.E2E_RECORD === "1";

export default async function (t) {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "dsh-ee-package-sources-"));
  try {
    const authPage = await newPage(t);
    await login(authPage, "admin", ADMIN_PW);
    const minted = await api(authPage, "POST", "/api/me/tokens", { name: "package-source-e2e", scopes: ["registry:read", "registry:publish"] });
    assertEq(minted.status, 201, "签发制品源令牌");
    const token = minted.data.token;
    const state = await authPage.context().storageState();
    const page = record
      ? await newPage(t, { viewport: { width: 1440, height: 960 }, recordVideo: { dir: SHOTS, size: { width: 1440, height: 960 } } })
      : authPage;
    if (record) await page.context().addCookies(state.cookies);
    await page.goto(`${BASE}/admin/registry`);
    await page.getByRole("heading", { name: "Conan 与 Raw 源" }).waitFor();
    const create = async (protocol, name) => {
      await page.getByRole("button", { name: "新增源" }).click();
      await page.getByLabel(/^类型/).selectOption(protocol);
      await page.getByLabel("源名称", { exact: true }).fill(name);
      await page.getByRole("button", { name: "创建源" }).click();
      await page.locator("pre").filter({ hasText: `${BASE}/${protocol}/${name}` }).first().waitFor();
    };
    const capture = async (name) => {
      if (record) {
        await page.waitForTimeout(900);
        await page.screenshot({ path: path.join(SHOTS, `${name}.png`) });
      }
    };
    const refresh = () => page.getByRole("button", { name: "刷新列表" }).click();
    const conanName = `cpp-${tag}`;
    await create("conan", conanName);
    await page.locator("pre").filter({ hasText: "conan remote add" }).waitFor();
    await capture("package-sources-conan-instructions");
    const runConan = async (home, ...args) => {
      const { stdout, stderr } = await exec(conan, args, {
        cwd: tmp, env: { ...process.env, CONAN_HOME: path.join(tmp, home), CONAN_PASSWORD: token },
        timeout: 60_000,
      });
      return stdout + stderr;
    };
    if (conan) {
      for (const home of ["producer", "consumer"]) {
        await runConan(home, "profile", "detect");
        await runConan(home, "remote", "add", "dsh", `${BASE}/conan/${conanName}`);
        await runConan(home, "remote", "login", "dsh", "dsh-ee");
      }
      const recipe = path.join(tmp, "recipe");
      fs.cpSync(fixture, recipe, { recursive: true });
      await runConan("producer", "create", recipe);
      await runConan("producer", "upload", "dsh-ee-demo/1.0", "-r", "dsh", "--confirm");
      const installed = await runConan("consumer", "install", "--requires=dsh-ee-demo/1.0", "-r", "dsh");
      assert(/dsh-ee-demo\/1\.0.*Downloaded/.test(installed), "独立 Conan 缓存应从本地源下载二进制包");
      const listed = await runConan("consumer", "list", "dsh-ee-demo/1.0:*", "-r", "dsh");
      assert(listed.includes("dsh-ee-demo/1.0") && listed.includes("packages"), "Conan 源应列出二进制包");
      step("Conan 2：分别使用生产者与消费者缓存完成 create → upload → install");
      await refresh();
      await page.getByRole("cell", { name: /conan_package\.tgz$/ }).waitFor();
      await page.getByRole("region", { name: "制品列表" }).scrollIntoViewIfNeeded();
      await capture("package-sources-conan-artifacts");
    }
    const rawName = `raw-${tag}`;
    await create("raw", rawName);
    await page.locator("pre").filter({ hasText: "curl -f" }).waitFor();
    const rawFile = "release-notes.txt";
    const payload = "release 1.0\n";
    fs.writeFileSync(path.join(tmp, rawFile), payload);
    const curl = async (...args) => {
      try {
        return (await exec("curl", ["-fsS", "--max-time", "20", "-H", `Authorization: Bearer ${token}`, ...args], { cwd: tmp })).stdout;
      } catch (error) {
        throw new Error(`curl failed: ${error.stderr ?? error.code}`);
      }
    };
    const rawURL = `${BASE}/raw/${rawName}/releases/${rawFile}`;
    await curl("-T", rawFile, rawURL);
    assertEq(await curl(rawURL), payload, "Raw GET");
    assertEq(await curl("-r", "0-6", rawURL), "release", "Raw Range");
    step("Raw：curl PUT、GET 和 Range 返回预期内容");
    await refresh();
    await page.getByRole("cell", { name: "releases/release-notes.txt" }).waitFor();
    await capture("package-sources-raw-artifact");
    if (liveProxy) {
      const createProxy = async (data) => assertEq((await api(authPage, "POST", "/api/admin/registry/sources", { ...data, mode: "proxy" })).status, 201, "创建只读代理");
      await createProxy({ protocol: "raw", name: `git-${tag}`, upstream_url: "https://raw.githubusercontent.com/git/git/v2.45.0/" });
      assert((await curl(`${BASE}/raw/git-${tag}/README.md`)).includes("Git - fast, scalable"), "Raw proxy 应返回 Git README");
      step("Raw：curl 通过只读代理读取固定 Git README");
      if (conan) {
        await createProxy({ protocol: "conan", name: `center-${tag}` });
        await runConan("consumer", "remote", "add", "dsh-center", `${BASE}/conan/center-${tag}`);
        await runConan("consumer", "remote", "login", "dsh-center", "dsh-ee");
        const downloaded = await runConan("consumer", "download", "zlib/1.3.1", "-r", "dsh-center", "--only-recipe");
        assert(downloaded.includes("zlib/1.3.1"), "ConanCenter 代理应找到 zlib");
        step("ConanCenter：通过只读代理下载 zlib/1.3.1 配方");
      }
    }
    if (record) {
      const video = page.video();
      await page.context().close();
      await video.saveAs(path.join(SHOTS, "package-sources-demo.webm"));
    }
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }
}
