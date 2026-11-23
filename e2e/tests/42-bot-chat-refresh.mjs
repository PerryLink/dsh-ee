// 机器人对话：可用机器人列表的刷新（回归）。
// 列表是侧栏入口与页面共用的模块级状态，以前探测一次就一直用到整页刷新：管理员中途开通 / 收回机器人，
// 员工不重新加载页面就看不到。现在：
// - 每次打开 /bot-chat 重新拉一次（只拉一次，不随渲染反复请求），界面不退回「加载中」；
// - 页面上有看得见的「刷新机器人列表」，没有机器人时也在；
// - 刷新失败保留上一次的列表并提示，不把正在用的页面换成报错页。
// E2E 环境没有配置云端运行时，/api/runtime/chat/* 用路由拦截代替；页面逻辑与登录、权限都是真的。
import { ADMIN_PW, BASE, api, assert, assertEq, login, newPage, shot, step, tag } from "../lib.mjs";

export default async function (t) {
  const admin = await newPage(t);
  const page = await newPage(t);
  const username = `botchat-emp-${tag}`;
  const password = "botchat-employee-password-1";

  const botA = { id: 1, name: `客服助手-${tag}` };
  const botB = { id: 2, name: `报销助手-${tag}` };
  let bots = [botA];
  let fail = false;
  let hits = 0;
  await page.route("**/api/runtime/chat/bots", (route) => {
    hits++;
    if (fail) return route.abort();
    return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(bots) });
  });
  await page.route("**/api/runtime/chat/conversations", (route) => route.fulfill({ status: 200, contentType: "application/json", body: "[]" }));
  // 页面一旦退回整页「加载中」就记下来：原地刷新不应该出现
  await page.addInitScript(() => {
    window.__botChatLoadingSeen = 0;
    new MutationObserver(() => {
      if ([...document.querySelectorAll("[role=status]")].some((el) => el.textContent?.trim() === "正在加载…")) window.__botChatLoadingSeen++;
    }).observe(document, { childList: true, subtree: true });
  });
  const settle = () => page.waitForTimeout(800);
  const radio = (bot) => page.getByRole("radio", { name: bot.name, exact: true });
  const refreshBtn = () => page.getByRole("button", { name: "刷新机器人列表", exact: true });
  const entry = page.locator("[data-bot-chat-entry]");

  await login(admin, "admin", ADMIN_PW);
  assertEq((await api(admin, "POST", "/api/admin/users", { username, password })).status, 201, "创建员工");
  await login(page, username, password);
  await entry.waitFor();
  await settle();

  // ---- 打开页面：重新拉一次，且只拉一次 ----
  let base = hits;
  bots = [botA, botB];
  await page.evaluate(() => (window.__botChatLoadingSeen = 0));
  await entry.click();
  await page.waitForURL((u) => u.pathname === "/bot-chat");
  await radio(botB).waitFor();
  await settle();
  assertEq(hits - base, 1, "打开对话页应重新拉取一次机器人列表（不多不少）");
  assertEq(await page.evaluate(() => window.__botChatLoadingSeen), 0, "已有列表时打开页面不应退回整页加载中");
  assertEq(await refreshBtn().first().isVisible(), true, "有机器人时应看得到刷新按钮");
  step("打开对话页重新拉取一次，侧栏探测后新开通的机器人直接出现，没有加载闪烁");

  // ---- 离开再回来：又拉一次；机器人被全部收回时进入「暂无机器人」，刷新按钮仍在 ----
  await page.goBack();
  await page.waitForURL((u) => u.pathname !== "/bot-chat");
  base = hits;
  bots = [];
  await entry.click();
  await page.getByRole("heading", { name: "暂无可用的机器人", exact: true }).waitFor();
  await settle();
  assertEq(hits - base, 1, "再次打开对话页应再拉取一次");
  assertEq(await refreshBtn().count(), 2, "没有机器人时会话栏和欢迎区都应有刷新按钮");
  assertEq(await refreshBtn().last().isVisible(), true, "没有机器人时刷新按钮应可见");
  await shot(page, "bot-chat-refresh-empty");
  step("没有机器人时页面照常可用，并提供「刷新机器人列表」");

  // ---- 手动刷新：不重新加载页面就拿到新开通的机器人 ----
  base = hits;
  bots = [botA];
  await refreshBtn().last().click();
  await radio(botA).waitFor();
  await settle();
  assertEq(hits - base, 1, "点一次刷新只请求一次");
  assertEq(await page.evaluate(() => window.__botChatLoadingSeen), 0, "手动刷新不应退回整页加载中");
  step("手动刷新后新开通的机器人出现");

  // ---- 刷新失败：保留原列表并提示，可以再试 ----
  fail = true;
  await refreshBtn().first().click();
  const alert = page.getByRole("alert").filter({ hasText: "机器人列表刷新失败" });
  await alert.first().waitFor();
  assertEq(await radio(botA).isVisible(), true, "刷新失败后应保留上一次的机器人列表");
  assertEq(await entry.isVisible(), true, "刷新失败后侧栏入口应保留");
  fail = false;
  bots = [botA, botB];
  await refreshBtn().first().click();
  await radio(botB).waitFor();
  assertEq(await alert.count(), 0, "刷新成功后失败提示应消失");
  assert(await radio(botA).isVisible(), "刷新成功后原有机器人仍在");
  step("刷新失败保留原列表并提示，重试成功后提示消失");

  // ---- 英文界面：刷新按钮有本地化文案 ----
  const en = await newPage(t, { lang: "en" });
  await en.route("**/api/runtime/chat/bots", (route) => route.fulfill({ status: 200, contentType: "application/json", body: "[]" }));
  await en.route("**/api/runtime/chat/conversations", (route) => route.fulfill({ status: 200, contentType: "application/json", body: "[]" }));
  await login(en, username, password);
  await en.goto(`${BASE}/bot-chat`);
  await en.getByRole("button", { name: "Refresh bots", exact: true }).first().waitFor();
  step("英文界面显示 Refresh bots");
}
