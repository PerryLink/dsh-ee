// 通用组件：数据表分页 / 排序 / 搜索、行操作菜单、确认对话框、可搜索选择、日期选择、提示
import { ADMIN_PW, BASE, api, assert, assertEq, login, newPage, shot, step, tag } from "../lib.mjs";

export default async function (t) {
  const page = await newPage(t);
  await login(page, "admin", ADMIN_PW);
  const prefix = `ui${tag}.`;
  for (let i = 1; i <= 25; i++) {
    const r = await api(page, "POST", "/api/admin/users", {
      username: `${prefix}${String(i).padStart(2, "0")}`, password: "ui-password-123",
      display_name: `测试${tag}-${i}`, department: i % 2 ? "研发效能部" : "财务部",
    });
    assertEq(r.status, 201, "创建测试用户");
  }
  step("创建 25 个测试用户");

  await page.goto(`${BASE}/admin/users`);
  await page.getByText(/第 1–20 条，共 \d+ 条/).waitFor();
  await page.getByRole("button", { name: "下一页" }).click();
  await page.getByText(/第 21–\d+ 条/).waitFor();
  step("分页：翻到第 2 页");
  const header = page.getByRole("columnheader", { name: "用户" });
  await header.getByRole("button").click();
  assertEq(await header.getAttribute("aria-sort"), "ascending", "点一次排序");
  await header.getByRole("button").click();
  assertEq(await header.getAttribute("aria-sort"), "descending", "点两次排序");
  step("排序：升序 → 降序");
  const search = page.getByPlaceholder("搜索用户名、显示名、部门或角色");
  await search.fill(`${prefix}07`);
  await page.getByText("共 1 条").waitFor();
  step("搜索命中 1 条");
  await shot(page, "ui-users");

  const row = page.locator("tbody tr").first();
  await row.getByRole("button", { name: "更多操作" }).click();
  await page.getByRole("menuitem", { name: "禁用" }).click();
  const dialog = page.getByRole("alertdialog");
  await dialog.waitFor();
  await dialog.getByRole("button", { name: "取消" }).click();
  await dialog.waitFor({ state: "hidden" });
  await row.getByText("正常").waitFor();
  step("确认对话框：取消后状态不变");
  await row.getByRole("button", { name: "更多操作" }).click();
  await page.getByRole("menuitem", { name: "禁用" }).click();
  await dialog.getByRole("button", { name: "禁用" }).click();
  await row.getByText("已禁用").waitFor();
  await page.locator("[data-sonner-toast]").first().waitFor();
  assertEq(await page.evaluate(() => getComputedStyle(document.body).pointerEvents), "auto", "对话框关闭后 body pointer-events");
  step("确认禁用，出现提示消息，页面仍可点击");
  await row.getByRole("button", { name: "更多操作" }).click();
  await page.getByRole("menuitem", { name: "启用" }).click();
  await row.getByText("正常").waitFor();
  await row.getByRole("button", { name: "更多操作" }).click();
  await page.getByRole("menuitem", { name: "重置密码" }).click();
  await page.getByLabel("新密码").waitFor();
  step("重新启用；重置密码在行下方展开");

  await page.goto(`${BASE}/admin/quotas`);
  await page.getByPlaceholder("搜索员工、显示名或部门").fill(`${prefix}07`);
  await page.locator("tbody tr").first().getByRole("button", { name: "调整额度" }).click();
  await page.locator("form button", { hasText: "选择日期" }).click();
  const grid = page.getByRole("grid");
  await grid.waitFor();
  assert((await grid.locator("button[disabled]").count()) > 0, "今天之前的日期应不可选");
  await grid.locator("button:not([disabled])").last().click();
  step("日期选择禁用过去的日期");

  await page.goto(`${BASE}/admin/tokens`);
  await page.getByRole("button", { name: "下发令牌" }).first().click();
  await page.locator("form").getByRole("combobox").first().click();
  await page.getByPlaceholder("搜索…").fill(`测试${tag}-12`);
  await page.locator("[cmdk-item]").first().click();
  assert((await page.locator("form").getByRole("combobox").first().innerText()).includes(`${prefix}12`), "可搜索选择没选中");
  await page.getByLabel("名称", { exact: true }).fill("ui 测试令牌");
  step("可搜索选择员工");
  await shot(page, "ui-issue-key");
  await page.locator("form").getByRole("button", { name: "下发令牌" }).click();
  await page.locator("pre").filter({ hasText: /^\s*ee_at_\S+\s*$/ }).waitFor();
  await page.getByPlaceholder("部门名称").fill("财务部");
  await page.getByRole("button", { name: "吊销整个部门的令牌" }).click();
  await page.getByRole("alertdialog").getByRole("button", { name: "吊销" }).click();
  await page.getByRole("alertdialog").waitFor({ state: "hidden" });
  await page.getByLabel("状态", { exact: true }).selectOption("revoked");
  await page.locator("tbody tr").filter({ hasText: "ui 测试令牌" }).first().getByText("已吊销").waitFor();
  step("下发统一令牌并按部门批量吊销");

  await page.getByRole("button", { name: "退出登录" }).hover();
  await page.getByRole("tooltip").waitFor();
  step("悬停提示");
}
