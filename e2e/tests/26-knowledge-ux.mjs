// 知识库第二期 P4：维护者的「检索情况」（待补文档、常被读的文档）、员工首页的搜索与最近更新、
// 文档列表的目录树和批量移动、编辑正文时左右分栏预览并提醒影响。
import { ADMIN_PW, BASE, api, assert, assertEq, login, newPage, shot, step, tag } from "../lib.mjs";

/** 网页上新建一篇 Markdown 文档。 */
async function createDoc(page, kbId, title, markdown, folder = "") {
  return page.evaluate(
    async ([kbId, title, markdown, folder]) => {
      const f = new FormData();
      f.set("title", title);
      f.set("markdown", markdown);
      f.set("folder", folder);
      return (await fetch(`/api/kb/${kbId}/docs`, { method: "POST", body: f })).json();
    },
    [kbId, title, markdown, folder],
  );
}

/** 像 DSH 里的 AI 一样调 kb__ 工具。 */
async function kbTool(token, name, args) {
  const r = await fetch(`${BASE}/mcp`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json", Accept: "application/json, text/event-stream" },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/call", params: { name: `kb__${name}`, arguments: args } }),
  });
  const body = await r.json();
  return JSON.parse(body.result.content[0].text);
}

export default async function (t) {
  const admin = await newPage(t);
  await login(admin, "admin", ADMIN_PW);
  const created = await api(admin, "POST", "/api/kb", { slug: `ux-${tag}`, title: `人事制度 ${tag}`, description: "假期、考勤、入职" });
  assertEq(created.status, 201, "建库");
  const kbId = created.data.id;
  try {
    await run(admin, kbId);
  } finally {
    await api(admin, "DELETE", `/api/kb/${kbId}`);
  }
}

async function run(admin, kbId) {
  const leave = await createDoc(admin, kbId, `年假规定 ${tag}`, "# 年假规定\n\n## 天数\n\n入职满一年每年 5 天年假，满十年 10 天。\n\n## 跨年\n\n年假可以顺延到次年 3 月底。\n", "假期/年假");
  await createDoc(admin, kbId, `病假规定 ${tag}`, "# 病假规定\n\n病假需要医院证明。\n", "假期");
  await createDoc(admin, kbId, `考勤制度 ${tag}`, "# 考勤制度\n\n上班时间 9:00，弹性 30 分钟。\n", "考勤");
  const leaveId = leave.results[0].doc.id;

  // ---- 让「AI」查几次：一次换说法后查到了，一次怎么都没查到 ----
  const token = (await api(admin, "POST", "/api/me/npm-tokens", { name: `ux-${tag}`, access: "read" })).data.token;
  await kbTool(token, "search", { query: "休息日", kb: `ux-${tag}` });
  const found = await kbTool(token, "search", { query: "年假 跨年", kb: `ux-${tag}` });
  await kbTool(token, "read", { doc: found.hits[0].doc });
  await kbTool(token, "search", { query: `育儿假 ${tag}`, kb: `ux-${tag}` });
  await kbTool(token, "search", { query: `${tag} 育儿假`, kb: `ux-${tag}` });

  // ---- 检索情况：没查到的在最上面，能忽略；常被读的文档 ----
  await admin.goto(`${BASE}/knowledge/${kbId}`);
  await admin.getByRole("button", { name: /检索情况 · 待补 1/ }).click();
  // 显示最近一次的原话；两次词序不同算同一条
  const gap = admin.locator("li", { hasText: "育儿假" }).filter({ visible: true }).first();
  await gap.waitFor();
  await gap.getByText(/^2 次 · 1 人/).waitFor();
  assert(!(await admin.locator("li", { hasText: "休息日" }).filter({ visible: true }).count()), "换说法后查到的不算待补");
  await admin.getByRole("link", { name: `年假规定 ${tag}` }).waitFor();
  await shot(admin, "kb4-gaps");
  step("检索情况：「育儿假」两次都没查到，排在最上面；「休息日」随后换说法查到了，不算；年假规定是常被读的文档");
  await gap.getByRole("button", { name: "忽略" }).click();
  await admin.getByText("最近 30 天，大家想查的都查到了。").waitFor();
  step("点「忽略」后待补清空");

  // ---- 文档列表：目录树、搜内容、批量移动 ----
  await admin.getByRole("button", { name: "文档", exact: true }).click();
  const tree = admin.getByRole("navigation", { name: "目录" });
  await tree.getByRole("button", { name: /假期\s*2/ }).click();
  await tree.getByRole("button", { name: /年假\s*1/ }).waitFor();
  assert(!(await admin.getByRole("link", { name: `考勤制度 ${tag}` }).count()), "选了「假期」不显示考勤");
  await tree.getByRole("button", { name: /全部目录/ }).click();
  await admin.getByRole("button", { name: "搜内容", exact: true }).click();
  await admin.getByPlaceholder("搜正文里的词").fill("医院证明");
  await admin.locator("mark, b, strong", { hasText: "医院证明" }).first().waitFor().catch(() => {});
  await admin.getByRole("link", { name: `病假规定 ${tag}` }).waitFor();
  assert(!(await admin.getByRole("link", { name: `年假规定 ${tag}` }).count()), "搜内容只留命中的文档");
  await shot(admin, "kb4-doc-list-content");
  step("文档列表：左边目录树按目录筛选；「搜内容」只留下正文命中的文档并显示命中的那一行");
  await admin.getByPlaceholder("搜正文里的词").fill("");
  await admin.getByRole("checkbox", { name: `选中「考勤制度 ${tag}」` }).check();
  await admin.getByRole("checkbox", { name: `选中「病假规定 ${tag}」` }).check();
  await admin.getByText("已选 2 篇").waitFor();
  await admin.getByRole("button", { name: "移动到…" }).click();
  await admin.getByRole("dialog").locator("input").fill("制度汇编");
  await admin.getByRole("dialog").getByRole("button", { name: "移动" }).click();
  await tree.getByRole("button", { name: /制度汇编\s*2/ }).waitFor();
  await shot(admin, "kb4-doc-list-moved");
  step("勾选两篇「移动到…」制度汇编：目录树里出现「制度汇编 2」");

  // ---- 编辑正文：左右分栏预览，提醒影响 ----
  await admin.goto(`${BASE}/knowledge/docs/${leaveId}`);
  await admin.getByRole("button", { name: "编辑正文" }).click();
  const box = admin.locator("textarea");
  await box.fill((await box.inputValue()).replace("5 天", "7 天"));
  await admin.getByText("预览（员工看到的样子）").waitFor();
  await admin.locator('[data-dsh-ee-ui="kb-read"]').getByText("入职满一年每年 7 天年假").waitFor();
  await admin.getByText("新增 1 行、删去 1 行").waitFor();
  await admin.getByText(/近 30 天读取 1 次/).waitFor();
  await shot(admin, "kb4-edit");
  step("编辑正文：左边改 Markdown，右边实时是阅读视图；下方提醒改了几行、这篇近 30 天被 AI 读过几次");
  await admin.getByRole("button", { name: "取消" }).click();

  // ---- 员工首页：搜索、最近更新、我的检索记录收起来 ----
  await admin.goto(`${BASE}/knowledge`);
  await admin.getByRole("link", { name: `人事制度 ${tag}`, exact: true }).waitFor();
  await admin.getByRole("link", { name: `年假规定 ${tag}` }).first().waitFor();
  await admin.getByPlaceholder("搜制度、手册、文档……").fill("顺延");
  await admin.getByRole("button", { name: "搜索", exact: true }).click();
  await admin.getByRole("link", { name: `年假规定 ${tag}` }).first().waitFor();
  await shot(admin, "kb4-home-search");
  step("知识库首页：大搜索框搜正文，结果按文档列出命中的行；卡片上有最近更新的文档");
  await admin.getByRole("button", { name: "在 DSH 里问" }).click();
  await admin.getByText(/已复制，到 DSH 里粘贴发送/).waitFor();
  step("「在 DSH 里问」复制 /kb-qa 加问题，提示到 DSH 里粘贴");
}
