// 网页版应用（M14）：员工用手机宽度打开 /a/<标识>，和 DSH 一样的工作台：表单直接存进表、表格刷新，
// 发给 AI 的按钮提示到 DSH 里继续，AI 看板换成说明；「数据」里能改自己的行。没开网页版的应用员工打不开。
import { ADMIN_PW, BASE, api, assert, assertEq, login, newPage, shot, step, tag } from "../lib.mjs";

export default async function (t) {
  const admin = await newPage(t);
  await login(admin, "admin", ADMIN_PW);
  const slug = `webapp-${tag}`;
  const tables = [{ name: "signups", title: "报名", visibility: "own", fields: [
    { name: "who", title: "姓名", kind: "user" },
    { name: "session", title: "场次", kind: "select", options: ["上午", "下午"], required: true },
    { name: "people", title: "人数", kind: "number" },
  ] }];
  const workbench = { panes: [{ id: "main", type: "panel", title: "报名", widgets: [
    { type: "heading", title: "报名" },
    { type: "metric", title: "我报了几场", source: { table: "signups" }, field: "total" },
    { type: "form", title: "我要报名", submit: { label: "报名", table: "signups" } },
    { type: "table", title: "我的报名", source: { table: "signups" }, rows: "items", columns: [{ field: "session", title: "场次" }, { field: "people", title: "人数" }],
      row_actions: [{ label: "问 AI", prompt: "第 {{row.id}} 条报名能改吗" }] },
    { type: "actions", title: "快捷", actions: [{ label: "帮我报名", prompt: "帮我报名这次团建" }] },
    { type: "board", title: "AI 看板" },
  ] }] };
  const spec = (web) => ({ title: `网页版 ${tag}`, description: "e2e 网页版应用", persona: "你是报名助手。", icon: "🏕", web, tables, workbench });
  assertEq((await api(admin, "POST", "/api/hub", { kind: "app", slug, delivery: "auto", spec: spec(true) })).status, 201, "建网页版应用");
  assertEq((await api(admin, "POST", "/api/hub", { kind: "app", slug: `${slug}-off`, delivery: "auto", spec: spec(false) })).status, 201, "建没开网页版的应用");

  const user = `web-${tag}`;
  assertEq((await api(admin, "POST", "/api/admin/users", { username: user, password: "employee-password-1", display_name: "小李", department: "研发部" })).status, 201, "建员工");
  const phone = await newPage(t, { viewport: { width: 390, height: 844 } });
  await login(phone, user, "employee-password-1");

  // ---- 打开网页版：和 DSH 同一套渲染 ----
  await phone.goto(`${BASE}/a/${slug}`);
  await phone.getByRole("heading", { name: `网页版 ${tag}` }).waitFor();
  const wb = phone.locator("[data-dsh-ee-ui=workbench]");
  await wb.getByText("我的报名").waitFor();
  await wb.getByText("AI 看板只在 DSH 里显示").waitFor();
  await wb.locator("[data-value]").first().waitFor();
  await shot(phone, "web-app-phone");
  step("手机宽度打开 /a/标识：工作台和 DSH 里一样，AI 看板换成说明");

  // ---- 表单直接存进表 ----
  await wb.locator("select").first().selectOption("下午");
  await wb.locator("input[type=number]").first().fill("2");
  await wb.getByRole("button", { name: "报名", exact: true }).click();
  await phone.getByText("已保存：报名").waitFor();
  await wb.getByRole("cell", { name: "下午" }).waitFor();
  assertEq((await wb.locator("[data-value]").first().innerText()).trim(), "1", "指标跟着刷新");
  await shot(phone, "web-app-saved");
  step("表单提交直接存进应用的表，表格和指标跟着刷新");

  // ---- 发给 AI 的按钮：到 DSH 里继续 ----
  await wb.getByRole("button", { name: "帮我报名 ↗" }).click();
  await phone.getByText("网页上没有 AI").waitFor();
  const handoff = await phone.locator("[role=status] code").innerText();
  assert(handoff.startsWith(`/${slug} 帮我报名这次团建`), "带上 /应用名 的那段话：" + handoff);
  await phone.getByRole("link", { name: "打开 DSH ↗" }).waitFor();
  step("发给 AI 的按钮标成「↗」，点了复制「/应用名 + 那段话」，并给出打开 DSH 的链接");

  // ---- 「数据」：改自己的行 ----
  await phone.getByRole("button", { name: "数据", exact: true }).click();
  const data = phone.locator("section").filter({ has: phone.getByRole("heading", { name: "数据", exact: true }) });
  await data.getByRole("cell", { name: "下午" }).waitFor();
  await data.getByRole("button", { name: "编辑" }).click();
  await data.getByText(/编辑第 \d+ 行/).waitFor();
  await data.locator("select").first().selectOption("上午");
  await data.getByRole("button", { name: "保存" }).click();
  await data.getByRole("cell", { name: "上午" }).waitFor();
  await shot(phone, "web-app-data-edit");
  step("「数据」里能编辑自己的行");

  // ---- 没开网页版：员工打不开，作者能预览 ----
  await phone.goto(`${BASE}/a/${slug}-off`);
  await phone.getByText("这个应用没有开放网页版").waitFor();
  await admin.goto(`${BASE}/a/${slug}-off`);
  await admin.getByText("网页版未开放，仅作者和管理员可预览。").waitFor();
  step("没开网页版的应用：员工看到说明，作者能预览");

  for (const s of [slug, `${slug}-off`]) {
    const list = await api(admin, "GET", "/api/hub");
    const a = list.data.assets.find((x) => x.slug === s);
    if (a) await api(admin, "DELETE", `/api/hub/${a.id}`);
  }
}
