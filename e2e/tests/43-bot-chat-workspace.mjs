// 机器人对话：以机器人为中心的三栏工作区。
// - 左栏只有机器人（没有会话列表、没有「新建会话」）；每个机器人一条连续的对话，切走再切回是同一条；
// - 预览、未读点来自真实的回复状态；已读位置记在本机（账号 + 机器人），重新打开页面仍然有效；
// - 更早的消息通过 ?before= 往前翻；日期分隔按语言显示；
// - 发送后切到别的机器人，原来那条仍在等回复，回复到了左栏出现未读点；
// - 右栏资料：动态（定时任务只读、云端电脑状态）与文件库（真实产物链接），可关闭、可从名字胶囊重新打开；
// - 文件库里的文件可以在网页里预览（Markdown 排版 / 原文）；
// - 全屏：盖住系统侧栏和顶栏，刷新后保持，可以退出，窄屏也能用；
// - ?bot= 与旧的 ?conversation= 链接都能回到对应的机器人；窄屏上机器人列表和资料是可关闭的浮层。
// E2E 环境没有配置云端运行时，/api/runtime/chat/* 用有状态的路由拦截代替；页面逻辑与登录、权限都是真的。
import { ADMIN_PW, BASE, api, assert, assertEq, login, newPage, shot, step, tag } from "../lib.mjs";

const json = (route, body, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
const ago = (ms) => new Date(Date.now() - ms).toISOString();
const DAY = 86_400_000;

export default async function (t) {
  const admin = await newPage(t);
  const page = await newPage(t);
  const username = `botchat-ws-${tag}`;
  const password = "botchat-workspace-password-1";

  // ---- 假后端的状态 ----
  const botA = { id: 11, name: `客服助手-${tag}` };
  const botB = { id: 12, name: `报销助手-${tag}` };
  const botC = { id: 13, name: `周报助手-${tag}` };
  const delegationFailures = [
    ["the target bot was busy with another message", "目标机器人正在处理另一条消息。", "The target bot was busy with another message."],
    ["the employee already has 5 cloud tasks queued or running", "你排队或运行中的云任务已达到 5 个的上限。", "You have reached the limit of 5 queued or running cloud tasks."],
    ["the delegating turn is being retried", "发起委派的对话轮次正在重试。", "The delegating turn is being retried."],
    ["unrecognized backend reason", "委派未能完成。", "The delegation could not be completed."],
  ];
  let showDelegationFailures = false;
  const turn = (id, content, summary, created_at, status = "succeeded") => ({ id, content, status, summary, created_at });
  const threads = {
    501: {
      bot: botA,
      messages: [
        turn(1, `最早的问题-${tag}`, `最早的回答-${tag}`, ago(3 * DAY)),
        turn(2, `昨天的问题-${tag}`, `昨天的回答-${tag}`, ago(DAY)),
        turn(3, `今天的问题-${tag}`, `今天的回答-${tag}`, ago(0)),
      ],
    },
    502: { bot: botB, messages: [turn(20, `报销怎么走-${tag}`, `请在系统里提交报销单-${tag}`, ago(0))] },
  };
  const threadOf = (bot) => Object.keys(threads).find((id) => threads[id].bot === bot);
  const PAGE_SIZE = 2;
  const hits = { create: 0, send: 0, before: [], sendBodies: [] };
  let nextThread = 503;
  let nextTask = 100;

  const listBots = () =>
    [botA, botB, botC].map((bot) => {
      const id = threadOf(bot);
      const last = id ? threads[id].messages.at(-1) : undefined;
      return {
        ...bot,
        ...(id ? { thread_id: Number(id) } : {}),
        ...(last ? { last_task_id: last.id, last_status: last.status, last_message: last.status === "succeeded" ? last.summary : last.content } : {}),
      };
    });
  const conversationOf = (id) => ({ id: Number(id), bot_id: threads[id].bot.id, bot_name: threads[id].bot.name, title: "", updated_at: ago(0) });
  const profiles = {
    [botA.id]: {
      schedule: "0 9 * * *",
      schedule_enabled: true,
      running: false,
      artifacts: [
        { id: 8, task_id: 3, name: `明日待办-${tag}.md`, source: "chat", size: 40, created_at: ago(0) },
        { id: 7, task_id: 3, name: `report-${tag}.csv`, source: "task", size: 2048, created_at: ago(0) },
      ],
    },
    [botB.id]: { schedule: "", schedule_enabled: false, running: true, artifacts: [] },
    [botC.id]: { schedule: "30 1 * * *", schedule_enabled: false, running: false, artifacts: [] },
  };

  const mock = async (p) => {
    await p.route("**/api/runtime/chat/bots", (route) => json(route, listBots()));
    await p.route("**/api/runtime/chat/bots/*/profile", (route) => {
      const id = new URL(route.request().url()).pathname.split("/").at(-2);
      return profiles[id] ? json(route, profiles[id]) : json(route, { error: "not found" }, 404);
    });
    await p.route(/\/api\/runtime\/chat\/conversations(\/[^?]*)?(\?.*)?$/, (route) => {
      const req = route.request();
      const url = new URL(req.url());
      const parts = url.pathname.split("/").slice(5); // ["", "api", "runtime", "chat", "conversations", id?, "messages"?]
      const [id, sub] = parts;
      if (!id) {
        if (req.method() !== "POST") return json(route, []);
        // 幂等：同一个机器人总是同一条对话
        hits.create++;
        const bot = [botA, botB, botC].find((b) => b.id === req.postDataJSON().bot_id);
        if (!bot) return json(route, { code: "runtime_chat.bot_unavailable" }, 404);
        let tid = threadOf(bot);
        if (!tid) {
          tid = String(nextThread++);
          threads[tid] = { bot, messages: [] };
        }
        return json(route, conversationOf(tid), 201);
      }
      const thread = threads[id];
      if (!thread) return json(route, { code: "runtime_chat.not_found" }, 404);
      if (sub === "messages") {
        hits.send++;
        const body = req.postDataJSON();
        hits.sendBodies.push(body);
        if (thread.messages.some((m) => m.status === "queued" || m.status === "running")) return json(route, { code: "runtime_chat.busy" }, 409);
        const created = turn(nextTask++, body.content, "", ago(0), "queued");
        thread.messages.push(created);
        return json(route, created, 201);
      }
      const before = url.searchParams.get("before");
      if (before) hits.before.push(before);
      const older = before ? thread.messages.filter((m) => m.id < Number(before)) : thread.messages;
      const pageOf = older.slice(-PAGE_SIZE);
      const more = older.length > pageOf.length;
      const delegations = showDelegationFailures && thread.bot === botA && !before
        ? delegationFailures.map(([error], i) => ({
            id: i + 1,
            parent_task_id: pageOf.at(-1).id,
            parent_bot_id: botA.id,
            parent_bot_name: botA.name,
            target_bot_id: botB.id,
            target_bot_name: botB.name,
            kind: "task",
            status: i === 2 ? "canceled" : "failed",
            content: "Check the report",
            depth: 1,
            error,
          }))
        : [];
      return json(route, { conversation: conversationOf(id), messages: pageOf, delegations, ...(more ? { next_before: pageOf[0].id } : {}) });
    });
    await p.route("**/api/runtime/tasks/*/artifacts/*", (route) =>
      route.request().url().endsWith("/artifacts/8")
        ? route.fulfill({ status: 200, contentType: "application/octet-stream", body: `# 明日待办\n\n- 九点站会-${tag}\n` })
        : route.fulfill({ status: 200, contentType: "text/csv", body: "a,b\n1,2\n" }),
    );
  };
  await mock(page);

  const radio = (p, bot) => p.getByRole("radio", { name: bot.name, exact: true });
  const log = (p) => p.getByRole("log", { name: "对话记录", exact: true });
  const pane = (p) => p.getByRole("complementary", { name: "机器人资料", exact: true });
  const computer = (p) => pane(p).locator("[data-profile-computer]");
  const pill = (p, bot) => p.getByRole("button", { name: `${bot.name} 的资料`, exact: true });
  const composer = (p, bot) => p.getByPlaceholder(`给 ${bot.name} 发消息`, { exact: true });
  const checked = async (p, bot) => (await radio(p, bot).getAttribute("aria-checked")) === "true";
  const unread = (p, bot) => radio(p, bot).getAttribute("data-unread");

  await login(admin, "admin", ADMIN_PW);
  assertEq((await api(admin, "POST", "/api/admin/users", { username, password })).status, 201, "创建员工");
  await login(page, username, password);

  // ---- 打开页面：默认选中第一个机器人，直接是它那条对话；没有会话列表和新建会话 ----
  await page.goto(`${BASE}/bot-chat`);
  await log(page).getByText(`今天的回答-${tag}`).waitFor();
  assert(await checked(page, botA), "默认应选中第一个机器人");
  assertEq(await page.getByRole("radio").count(), 3, "左栏应列出全部机器人");
  assertEq(await page.getByRole("button", { name: /新建会话|开始新会话/ }).count(), 0, "不应再有新建会话的入口");
  assertEq(await page.locator("[data-bot-current]").innerText().then((s) => s.includes(botA.name)), true, "左栏顶部应突出显示选中的机器人");
  assertEq(await composer(page, botA).isVisible(), true, "输入框应提示「给 <机器人> 发消息」");
  assertEq(await log(page).getByText(`昨天的回答-${tag}`).isVisible(), true, "应显示最近一页消息");
  assertEq(await log(page).getByText(`最早的回答-${tag}`).count(), 0, "更早的消息不应一次读完");
  const days = await page.locator("[data-day-separator]").allInnerTexts();
  assertEq(days.join("|"), "昨天|今天", "日期分隔应按语言显示昨天 / 今天");
  assertEq(hits.create, 0, "列表已带 thread_id 时不应再请求创建");
  step("默认进入第一个机器人的连续对话，带本地化日期分隔，没有会话列表");

  // ---- 往前翻：通过 before 读更早的一页 ----
  await page.getByRole("button", { name: "加载更早的消息", exact: true }).click();
  await log(page).getByText(`最早的回答-${tag}`).waitFor();
  assertEq(hits.before.join(","), "2", "更早的消息应带 before=<上一页给的 next_before> 读取一次");
  assertEq(await page.getByRole("button", { name: "加载更早的消息", exact: true }).count(), 0, "到头后不再提供加载更早的消息");
  assertEq((await page.locator("[data-day-separator]").count()) === 3, true, "补上更早的一天后应多一条日期分隔");
  step("更早的消息通过 before 读取并接在前面");

  // ---- 未读点与预览：来自真实回复；看过之后清掉，并记在本机 ----
  assertEq(await unread(page, botB), "true", "别的机器人有没看过的回复时应有未读点");
  assertEq(await unread(page, botC), "false", "没有回复的机器人不应有未读点");
  assert((await radio(page, botB).innerText()).includes(`请在系统里提交报销单-${tag}`), "左栏应预览最近一条回复");
  assert((await radio(page, botC).innerText()).includes("还没有消息"), "没聊过的机器人应如实显示还没有消息");
  await radio(page, botB).click();
  await log(page).getByText(`请在系统里提交报销单-${tag}`).waitFor();
  await page.waitForURL((u) => u.searchParams.get("bot") === String(botB.id));
  assert(await checked(page, botB), "点击后应选中该机器人");
  assertEq(await log(page).getByText(`今天的回答-${tag}`).count(), 0, "切换后不应残留上一个机器人的消息");
  await radio(page, botA).click();
  await log(page).getByText(`今天的回答-${tag}`).waitFor();
  assertEq(await log(page).getByText(`最早的回答-${tag}`).isVisible(), true, "切回来应是同一条对话，翻出来的更早消息还在");
  assertEq(await unread(page, botB), "false", "看过之后未读点应消失");
  step("机器人之间切换保持各自的同一条对话，未读点随阅读清除");

  // ---- 没聊过的机器人：用幂等的创建接口取得唯一的对话，来回切换不会多建 ----
  await radio(page, botC).click();
  await page.getByText(`向 ${botC.name} 发送第一条消息吧。`, { exact: true }).waitFor();
  await radio(page, botA).click();
  await radio(page, botC).click();
  await page.getByText(`向 ${botC.name} 发送第一条消息吧。`, { exact: true }).waitFor();
  assertEq(hits.create, 1, "没有 thread_id 的机器人只应请求一次创建");
  step("没有对话的机器人只取得一次对话，不产生隐藏的额外会话");

  // ---- 发送 → 切走 → 模拟回复：原来那条继续等回复，回复到了出现未读点 ----
  await radio(page, botA).click();
  await composer(page, botA).fill(`帮我生成报表-${tag}`);
  await composer(page, botA).press("Enter");
  await log(page).getByText(`帮我生成报表-${tag}`).waitFor();
  await log(page).getByRole("status").filter({ hasText: /已排队|正在处理/ }).waitFor();
  assertEq(hits.send, 1, "回车应只发送一次");
  assert(typeof hits.sendBodies[0].idempotency_key === "string" && hits.sendBodies[0].idempotency_key.length > 0, "发送应带幂等键");
  assertEq(await page.getByRole("button", { name: "发送", exact: true }).isDisabled(), true, "回复完成前不能发送下一条");
  // 资料栏一直开着、也没有换机器人：云端电脑被这次回复唤醒后，状态应自己跟上（只看资料接口）
  await computer(page).filter({ hasText: "休眠中" }).waitFor();
  profiles[botA.id].running = true;
  await computer(page).filter({ hasText: "运行中" }).waitFor({ timeout: 15_000 });
  await radio(page, botB).click();
  await log(page).getByText(`请在系统里提交报销单-${tag}`).waitFor();
  assert((await radio(page, botA).innerText()).includes("正在回复…"), "等待回复时左栏应显示正在回复");
  assertEq(await unread(page, botA), "false", "回复还没完成时不应有未读点");
  // 模拟云任务完成
  const pending = threads[501].messages.at(-1);
  pending.status = "succeeded";
  pending.summary = `**报表**已生成-${tag}`;
  await page.waitForFunction((id) => document.querySelector(`[data-bot="${id}"]`)?.getAttribute("data-unread") === "true", String(botA.id), { timeout: 15_000 });
  assert((await radio(page, botA).innerText()).includes(`报表已生成-${tag}`), "回复到达后左栏预览应更新为回复内容");
  await radio(page, botA).click();
  await log(page).locator("strong", { hasText: "报表" }).waitFor();
  assertEq(await unread(page, botA), "false", "打开后未读点应消失");
  assertEq(await page.getByRole("button", { name: "发送", exact: true }).count(), 1, "回复完成后输入区仍在");
  // 回复已经结束、云端电脑稍后才休眠：资料栏开着时同样应自己跟上
  await computer(page).filter({ hasText: "运行中" }).waitFor();
  profiles[botA.id].running = false;
  await computer(page).filter({ hasText: "休眠中" }).waitFor({ timeout: 15_000 });
  await shot(page, "bot-chat-workspace");
  step("发送后切到别的机器人，回复到达时出现未读点，打开后显示 Markdown 回复");

  // ---- 右栏资料：动态（只读的定时任务 + 云端电脑状态）与文件库 ----
  await pane(page).waitFor();
  assertEq(await pill(page, botA).getAttribute("aria-expanded"), "true", "宽屏默认展开资料栏");
  assertEq(await pane(page).getByRole("tab", { name: "动态", exact: true }).getAttribute("aria-selected"), "true", "默认是动态页签");
  await pane(page).getByText("每日定时任务", { exact: true }).waitFor();
  assert((await pane(page).locator("[data-profile-schedule]").innerText()).includes("0 9 * * *"), "应显示真实的定时规则");
  assert((await pane(page).locator("[data-profile-schedule]").innerText()).includes("已开启"), "应显示定时任务的开关状态");
  assert((await pane(page).locator("[data-profile-schedule]").innerText()).includes("由管理员在后台设置和开关"), "应说明为什么这里不能开关");
  assert((await pane(page).locator("[data-profile-computer]").innerText()).includes("休眠中"), "应显示云端电脑状态");
  assertEq(await pane(page).getByRole("switch").count(), 0, "员工侧不提供定时任务开关");
  await pane(page).getByRole("tab", { name: "文件库", exact: true }).click();
  const file = pane(page).getByRole("link", { name: `下载 report-${tag}.csv`, exact: true });
  await file.waitFor();
  assertEq(await file.getAttribute("href"), "/api/runtime/tasks/3/artifacts/7", "文件库链接应走已有的产物下载地址");
  assertEq(await api(page, "GET", await file.getAttribute("href")).then((r) => r.status), 200, "产物地址应可访问");
  assert((await pane(page).locator("[data-profile-library]").innerText()).includes("定时任务"), "文件库应标出定时任务的产物");
  // 预览：Markdown 默认排版，可切到原文；下载仍走产物地址
  await pane(page).getByRole("button", { name: `预览 明日待办-${tag}.md`, exact: true }).click();
  const viewer = page.locator("[data-file-preview]");
  await viewer.getByText(`九点站会-${tag}`).waitFor();
  assertEq(await viewer.getByRole("heading", { name: "明日待办", exact: true }).count(), 1, "Markdown 应排版显示");
  await page.waitForTimeout(300);
  await shot(page, "bot-chat-file-preview");
  await viewer.getByRole("button", { name: "原文", exact: true }).click();
  await viewer.getByText("# 明日待办", { exact: false }).waitFor();
  assertEq(await viewer.getByRole("link", { name: "下载", exact: true }).getAttribute("href"), "/api/runtime/tasks/3/artifacts/8", "预览里的下载走产物地址");
  await page.keyboard.press("Escape");
  await viewer.waitFor({ state: "detached" });
  await pane(page).getByRole("button", { name: "关闭机器人资料", exact: true }).click();
  assertEq(await pane(page).count(), 0, "关闭后资料栏应收起");
  assertEq(await pill(page, botA).getAttribute("aria-expanded"), "false", "名字胶囊应反映资料栏已收起");
  await pill(page, botA).click();
  await pane(page).waitFor();
  await radio(page, botB).click();
  await pane(page).getByRole("tab", { name: "动态", exact: true }).click();
  await pane(page).getByText("没有设置定时任务", { exact: true }).waitFor();
  assert((await pane(page).locator("[data-profile-computer]").innerText()).includes("运行中"), "另一个机器人应显示自己的状态");
  await pane(page).getByRole("tab", { name: "文件库", exact: true }).click();
  await pane(page).getByText("这个机器人还没有生成过文件。", { exact: true }).waitFor();
  step("资料栏显示真实的定时任务、电脑状态与文件库，可关闭并从名字胶囊重新打开");

  // ---- 重新打开：?bot= 回到对应机器人；已读位置在本机保留；旧的 ?conversation= 链接换算成机器人 ----
  await page.goto(`${BASE}/bot-chat?bot=${botB.id}`);
  await log(page).getByText(`请在系统里提交报销单-${tag}`).waitFor();
  assert(await checked(page, botB), "?bot= 应选中对应的机器人");
  assertEq(await unread(page, botA), "false", "重新打开后，看过的回复不应再标未读");
  await page.goto(`${BASE}/bot-chat?conversation=501`);
  await log(page).getByText(`报表已生成-${tag}`).waitFor();
  await page.waitForURL((u) => u.searchParams.get("bot") === String(botA.id) && !u.searchParams.has("conversation"));
  assert(await checked(page, botA), "旧的 ?conversation= 链接应回到对应的机器人");
  assertEq(hits.create, 1, "重新打开不应再创建对话");
  step("?bot= 与旧的 ?conversation= 链接都回到对应机器人的同一条对话");

  // ---- 全屏：盖住系统侧栏和顶栏，只留机器人列表、对话和资料；刷新后保持；可以退出 ----
  const workspace = (p) => p.locator("[data-bot-workspace]");
  const covered = (p) => p.evaluate(() => !!document.elementFromPoint(8, 8)?.closest("[data-bot-workspace]"));
  assertEq(await covered(page), false, "默认不全屏，系统侧栏可见");
  await page.getByRole("button", { name: "全屏", exact: true }).click();
  assertEq(await workspace(page).getAttribute("data-fullscreen"), "true", "进入全屏");
  const box = await workspace(page).boundingBox();
  const vp = page.viewportSize();
  assert(box && box.x === 0 && box.y === 0 && Math.round(box.width) === vp.width && Math.round(box.height) === vp.height, `全屏应铺满窗口：${JSON.stringify(box)} vs ${JSON.stringify(vp)}`);
  assertEq(await covered(page), true, "全屏时系统侧栏被盖住");
  assertEq(await composer(page, botA).isVisible(), true, "全屏时输入框还在");
  assertEq(await radio(page, botB).isVisible(), true, "全屏时机器人列表还在");
  await page.reload();
  await log(page).getByText(`报表已生成-${tag}`).waitFor();
  assertEq(await workspace(page).getAttribute("data-fullscreen"), "true", "刷新后保持全屏");
  await page.waitForTimeout(400); // 等入场动画播完再截图
  await shot(page, "bot-chat-fullscreen");
  await page.getByRole("button", { name: "退出全屏", exact: true }).click();
  assertEq(await workspace(page).getAttribute("data-fullscreen"), "false", "退出全屏");
  assertEq(await covered(page), false, "退出后系统侧栏回来");
  step("全屏盖住系统侧栏和顶栏，刷新后保持，可以退出");

  // ---- 窄屏：机器人列表和资料是可关闭的浮层 ----
  const phone = await newPage(t, { viewport: { width: 390, height: 800 } });
  await mock(phone);
  await login(phone, username, password);
  await phone.goto(`${BASE}/bot-chat`);
  await log(phone).getByText(`报表已生成-${tag}`).waitFor();
  assertEq(await radio(phone, botB).isVisible(), false, "窄屏默认不展开机器人列表");
  assertEq(await pane(phone).count(), 0, "窄屏默认不展开资料");
  await phone.getByRole("button", { name: "打开机器人列表", exact: true }).click();
  await radio(phone, botB).waitFor();
  await radio(phone, botB).click();
  await log(phone).getByText(`请在系统里提交报销单-${tag}`).waitFor();
  assertEq(await radio(phone, botB).isVisible(), false, "选中机器人后列表应收起");
  await pill(phone, botB).click();
  await pane(phone).getByRole("tab", { name: "文件库", exact: true }).waitFor();
  await phone.keyboard.press("Escape");
  assertEq(await pane(phone).count(), 0, "窄屏上 Esc 应关闭资料浮层");
  await pill(phone, botB).click();
  await pane(phone).getByRole("button", { name: "关闭机器人资料", exact: true }).click();
  assertEq(await pane(phone).count(), 0, "窄屏上资料可以关闭");
  assertEq(await composer(phone, botB).isVisible(), true, "关闭后回到对话");
  await shot(phone, "bot-chat-workspace-mobile");
  await phone.getByRole("button", { name: "全屏", exact: true }).click();
  assertEq(await workspace(phone).getAttribute("data-fullscreen"), "true", "窄屏也能全屏");
  assertEq(await composer(phone, botB).isVisible(), true, "窄屏全屏时输入框还在");
  await phone.getByRole("button", { name: "打开机器人列表", exact: true }).click();
  await radio(phone, botA).waitFor();
  await phone.keyboard.press("Escape");
  assertEq(await workspace(phone).getAttribute("data-fullscreen"), "true", "Esc 先关浮层，不退出全屏");
  await phone.getByRole("button", { name: "退出全屏", exact: true }).click();
  assertEq(await workspace(phone).getAttribute("data-fullscreen"), "false", "窄屏可以退出全屏");
  step("窄屏上可以选择机器人、打开并关闭资料");

  showDelegationFailures = true;
  await page.reload();
  for (const [i, [, zh]] of delegationFailures.entries()) {
    await page.locator(`[data-delegation="${i + 1}"]`).getByText(`没有完成：${zh}`, { exact: true }).waitFor();
  }

  // ---- 英文界面：页签与输入提示有本地化文案 ----
  const en = await newPage(t, { lang: "en" });
  await mock(en);
  await login(en, username, password);
  await en.goto(`${BASE}/bot-chat`);
  await en.getByPlaceholder(`Message ${botA.name}`, { exact: true }).waitFor();
  const enPane = en.getByRole("complementary", { name: "Bot profile", exact: true });
  await enPane.getByRole("tab", { name: "Activity", exact: true }).waitFor();
  await enPane.getByRole("tab", { name: "Library", exact: true }).click();
  await enPane.getByRole("link", { name: `Download report-${tag}.csv`, exact: true }).waitFor();
  step("英文界面显示 Activity / Library 与 Message <bot>");
  for (const [i, [, , english]] of delegationFailures.entries()) {
    await en.locator(`[data-delegation="${i + 1}"]`).getByText(`Not done: ${english}`, { exact: true }).waitFor();
  }
  step("委派失败和取消原因以当前语言显示，未知原因不直接展示");
}
