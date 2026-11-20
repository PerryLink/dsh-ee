// 新建应用 / 新建连接的入口一致性与手动编辑器懒挂载：
// - /assets/new?kind=app 与菜单「新建应用」(/build/new) 是同一套「让 AI 帮我做 / 手动配置」双页签，默认 agent；
// - 手动表单首次进「手动配置」才挂载：agent 页签下不替用户打编辑器要用的接口（/api/hub、/api/kb 各一次，来自 agent 引导）；
// - ?mode=manual 直接进表单，切页签不丢已经填的内容，?form=expert 的标题是「新建专家」；
// - 连接页 agent 页签下手动编辑器不挂载，完全不出现 /api/hub、/api/kb（ConnAgentGuide 自己不请求这两个）。
import { ADMIN_PW, BASE, assertEq, login, newPage, shot, step } from "../lib.mjs";

export default async function (t) {
  const page = await newPage(t);
  await login(page, "admin", ADMIN_PW);
  const hits = [];
  page.on("request", (r) => {
    const p = new URL(r.url()).pathname;
    if (p === "/api/hub" || p === "/api/kb") hits.push(p);
  });
  const count = (p) => hits.filter((x) => x === p).length;
  const settle = () => page.waitForTimeout(1200);

  // ---- 旧深链 ?kind=app 与菜单「新建应用」同一体验：默认 agent，手动表单不挂载 ----
  for (const path of ["/assets/new?kind=app", "/build/new"]) {
    hits.length = 0;
    await page.goto(`${BASE}${path}`);
    await page.locator("[data-app-agent-guide]").waitFor();
    await settle();
    assertEq(await page.getByLabel("标识").count(), 0, `${path}：agent 页签下不应挂载手动编辑器`);
    assertEq(count("/api/hub"), 1, `${path}：只应由 agent 引导请求一次 /api/hub（实际 ${hits.join(",")}）`);
    assertEq(count("/api/kb"), 1, `${path}：只应由 agent 引导请求一次 /api/kb（实际 ${hits.join(",")}）`);
  }
  step("?kind=app 与 /build/new 都默认 agent 页签，手动编辑器未挂载（不再重复请求 /api/hub、/api/kb）");

  // ---- ?mode=manual 直接进表单；?form=expert 标题跟着变 ----
  await page.goto(`${BASE}/assets/new?kind=app&mode=manual`);
  await page.getByLabel("标识").waitFor();
  await page.locator("[data-app-agent-guide]").waitFor({ state: "hidden" });
  assertEq(await page.locator("[data-app-agent-guide]").isVisible(), false, "手动页签下 agent 引导应隐藏");
  await page.goto(`${BASE}/assets/new?kind=app&form=expert`);
  await page.getByText("新建专家", { exact: true }).waitFor();
  await page.locator("[data-app-agent-guide]").waitFor();
  step("?mode=manual 直接进手动表单；?form=expert 的标题是「新建专家」");
  await shot(page, "new-app-entry");

  // ---- 切页签：首次进手动才挂载，之后不卸载、内容不丢 ----
  await page.goto(`${BASE}/assets/new?kind=app`);
  await page.locator("[data-app-agent-guide]").waitFor();
  await page.getByRole("button", { name: "手动配置", exact: true }).click();
  await page.getByLabel("标识").fill("keep-me");
  await page.getByLabel("标题").fill("内容保留");
  await page.getByRole("button", { name: "让 AI 帮我做（推荐）", exact: true }).click();
  await page.getByLabel("标识").waitFor({ state: "hidden" });
  assertEq(await page.getByLabel("标识").count(), 1, "切回 agent 后手动编辑器应保持挂载（隐藏）");
  assertEq(await page.getByLabel("标识").isVisible(), false, "切回 agent 后手动编辑器应隐藏");
  await page.getByRole("button", { name: "手动配置", exact: true }).click();
  await page.getByLabel("标识").waitFor();
  assertEq(await page.getByLabel("标识").inputValue(), "keep-me", "切页签后应保留已填的标识");
  assertEq(await page.getByLabel("标题").inputValue(), "内容保留", "切页签后应保留已填的标题");
  step("切页签不丢已填内容");

  // ---- 连接页：agent 页签下手动编辑器不挂载，完全不出现 /api/hub、/api/kb ----
  hits.length = 0;
  await page.goto(`${BASE}/assets/new?kind=connection`);
  await page.locator("[data-conn-agent-guide]").waitFor();
  await settle();
  assertEq(await page.getByLabel("标识").count(), 0, "连接 agent 页签下不应挂载手动编辑器");
  assertEq(hits.length, 0, `连接 agent 页签不应请求 /api/hub、/api/kb（实际 ${hits.join(",")}）`);
  await page.goto(`${BASE}/assets/new?kind=connection&mode=manual`);
  await page.getByLabel("标识").waitFor();
  await page.locator("[data-conn-agent-guide]").waitFor({ state: "hidden" });
  assertEq(await page.locator("[data-conn-agent-guide]").isVisible(), false, "连接手动页签下 agent 引导应隐藏");
  step("连接 agent 页签不再为隐藏的手动表单请求 /api/hub、/api/kb");
}
