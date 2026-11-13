// 「用 / 造 / 管」：员工的侧边栏没有管理入口和「插件」；新建应用默认自己用、免审核；
// 改范围分享 → 管理员在「分享申请」里通过；从样板复制一个应用。
import { ADMIN_PW, BASE, api, assert, assertEq, login, newPage, shot, step, tag, nav } from "../lib.mjs";

async function dsh(token, path) {
  const r = await fetch(BASE + path, { headers: { Authorization: `Bearer ${token}` } });
  return r.json();
}

export default async function (t) {
  const admin = await newPage(t);
  await login(admin, "admin", ADMIN_PW);
  const emp = `builder-${tag}`;
  const mine = `e2e-mine-${tag}`;
  const tpl = `e2e-tpl-${tag}`;
  const created = [];
  assertEq((await api(admin, "POST", "/api/admin/users", { username: emp, password: "build-password-1", department: "销售部" })).status, 201, "建员工");
  const tplRes = await api(admin, "POST", "/api/hub", {
    kind: "app",
    slug: tpl,
    scope: { all: true, departments: [], roles: [], users: [] },
    spec: { title: `E2E 样板 ${tag}`, description: "客户拜访前的准备", persona: "你是销售助理，先列出客户近况。", starters: ["帮我准备明天的拜访"], icon: "🤝", template: true },
  });
  assertEq(tplRes.status, 201, "管理员建样板");
  created.push(tplRes.data.asset.id);

  try {
    const page = await newPage(t);
    await login(page, emp, "build-password-1");
    const sideText = await page.locator("aside").innerText();
    for (const g of ["用", "造", "接入", "企业应用", "知识问答", "新建应用", "我的应用", "从样板复制", "原料"]) assert(sideText.includes(g), `侧边栏缺少「${g}」：${sideText}`);
    for (const no of ["管理", "插件", "能力中心", "待审核"]) assert(!sideText.includes(no), `员工侧边栏不应出现「${no}」：${sideText}`);
    step("员工侧边栏只有「用 / 造 / 接入 / 开发」，没有管理入口，也没有「插件」");
    await shot(page, "build-nav");
    const tok = await api(page, "POST", "/api/me/tokens", { name: "dsh", scopes: ["models", "registry:read", "hub", "connections"] });
    assertEq(tok.status, 201, "接入令牌");
    const token = tok.data.token;

    // ---- 新建应用：默认只有我自己，保存即生效 ----
    await nav(page, { name: "新建应用" });
    await page.waitForURL(`${BASE}/build/new`);
    // 新建默认进「让 AI 帮我做」页签，手动表单在「手动配置」里（页内还有「手动配置 →」链接，要 exact 匹配）
    await page.getByRole("button", { name: "手动配置", exact: true }).click();
    await page.getByText("只有你自己用，保存后直接能在 DSH 里打开，不用审核。").waitFor();
    await page.getByLabel("标识").fill(mine);
    await page.getByLabel("标题").fill(`E2E 我的助手 ${tag}`);
    await page.getByLabel("说明").fill("整理每周客户跟进");
    await page.getByLabel("人设与工作方式").fill("你是我的销售助理。");
    await shot(page, "build-new");
    await page.getByRole("button", { name: "提交", exact: true }).click();
    await page.waitForURL(/\/assets\/\d+$/);
    const mineId = Number(new URL(page.url()).pathname.split("/").pop());
    created.push(mineId);
    let cat = await dsh(token, "/api/dsh/catalog");
    assert(cat.apps.some((a) => a.name === mine), "自己用的应用应直接出现在作者的 DSH 里：" + JSON.stringify(cat.apps));
    step("新建应用默认「只有我自己」，提交后不用审核，作者的 DSH 里马上有");

    // ---- 我的应用：分享给部门 → 审核中 ----
    await nav(page, { name: "我的应用" });
    const row = page.locator("li", { hasText: `E2E 我的助手 ${tag}` });
    await row.getByText("自己用", { exact: true }).waitFor();
    await row.getByRole("button", { name: "分享…" }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByRole("button", { name: "指定范围", exact: true }).click();
    await dialog.getByRole("combobox", { name: "部门" }).fill("销售部");
    await dialog.getByRole("combobox", { name: "部门" }).press("Enter");
    await dialog.getByRole("button", { name: "保存", exact: true }).click();
    await page.getByText("已提交分享申请，等管理员审核").first().waitFor();
    await row.getByText("分享审核中", { exact: true }).waitFor();
    await shot(page, "build-sharing");
    cat = await dsh(token, "/api/dsh/catalog");
    assert(cat.apps.some((a) => a.name === mine), "审核期间作者照常能用");
    step("改范围分享给部门：进入审核，作者照常能用");

    // ---- 管理员：分享申请 → 通过 ----
    await admin.goto(`${BASE}/admin/review`);
    await admin.getByRole("button", { name: /分享申请/ }).click();
    await admin.getByRole("button", { name: new RegExp(`E2E 我的助手 ${tag}`) }).click();
    await admin.getByText("申请改为").waitFor();
    await shot(admin, "build-scope-review");
    await admin.getByRole("button", { name: "通过并上架" }).click();
    await admin.getByText(`已通过「E2E 我的助手 ${tag}」的分享申请`).waitFor();
    await page.reload();
    await row.getByText("已分享", { exact: true }).waitFor();
    step("管理员在「分享申请」里通过，作者看到「已分享」");

    // ---- 原料：插件收在高级选项里 ----
    await nav(page, { name: "原料" });
    await page.getByRole("tab", { name: "知识库" }).waitFor();
    assert(!(await page.locator("main").innerText()).includes("插件"), "原料页默认不应出现「插件」");
    await page.getByRole("button", { name: "显示高级选项" }).click();
    await page.getByText("上传或申请引入插件").waitFor();
    step("原料页列出知识库、连接、技能、提示词；插件在高级选项里");

    // ---- 从样板复制 ----
    await nav(page, { name: "从样板复制" });
    const card = page.locator("div.rounded-lg", { hasText: `E2E 样板 ${tag}` }).last();
    await card.getByRole("button", { name: "复制为我的应用" }).click();
    await page.waitForURL(/\/build\/new\?from=\d+/);
    assertEq(await page.getByLabel("标识").inputValue(), `${tpl}-copy`, "复制后的标识");
    assertEq(await page.getByLabel("标题").inputValue(), `E2E 样板 ${tag}（副本）`, "复制后的标题");
    assert((await page.getByLabel("人设与工作方式").inputValue()).includes("销售助理"), "人设应复制过来");
    await page.getByRole("button", { name: "提交", exact: true }).click();
    await page.waitForURL(/\/assets\/\d+$/);
    created.push(Number(new URL(page.url()).pathname.split("/").pop()));
    cat = await dsh(token, "/api/dsh/catalog");
    assert(cat.apps.some((a) => a.name === `${tpl}-copy`), "复制出来的应用自己用、直接生效");
    step("从样板复制：内容带过来，复制后是自己用的应用");
    await shot(page, "build-copied");

    // 旧链接
    await page.goto(`${BASE}/hub`);
    await page.waitForURL(`${BASE}/apps`);
    step("旧的 /hub 跳到企业应用");
  } finally {
    for (const id of created) await api(admin, "DELETE", `/api/hub/${id}`);
  }
}
