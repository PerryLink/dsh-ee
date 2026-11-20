// 应用自带数据（M13）：作者在编辑器里插入示例数据表并保存 → 员工在应用详情里填一行、只看到自己的 →
// 作者看到全部并能导出 CSV；员工能删自己的行。
import { ADMIN_PW, BASE, api, assert, assertEq, login, newPage, shot, step, tag } from "../lib.mjs";

export default async function (t) {
  const admin = await newPage(t);
  await login(admin, "admin", ADMIN_PW);
  const slug = `outing-${tag}`;

  // ---- 作者：编辑器里插入示例数据表 ----
  await admin.goto(`${BASE}/assets/new?kind=app&mode=manual`);
  // 按钮在 Field 的 <label> 里，可访问名称是整段标签文字，所以按文字找
  const sample = admin.locator("button", { hasText: "插入示例（团建报名）" });
  await sample.waitFor();
  await sample.click();
  const text = await admin.locator("textarea").filter({ hasText: '"signups"' }).inputValue();
  const tables = JSON.parse(text);
  assertEq(tables[0].name, "signups", "示例数据表");
  await shot(admin, "app-data-editor");
  step("编辑器的「数据表」一栏能插入示例（团建报名）");

  // 用接口保存（编辑器其余字段由别的测试覆盖）
  const created = await api(admin, "POST", "/api/hub", {
    kind: "app", slug, delivery: "auto",
    spec: {
      title: `团建报名 ${tag}`, description: "e2e", persona: "你是报名助手。", tables,
      workbench: { panes: [{ id: "main", type: "panel", title: "报名", widgets: [{ type: "form", title: "报名", submit: { label: "报名", table: "signups" } }] }] },
    },
  });
  assertEq(created.status, 201, "保存带数据表的应用");
  const id = created.data.asset.id;

  // ---- 员工：在应用详情里填一行 ----
  const user = `appdata-${tag}`;
  const u = await api(admin, "POST", "/api/admin/users", { username: user, password: "employee-password-1", display_name: "小王", department: "研发部" });
  assertEq(u.status, 201, "建员工");
  const emp = await newPage(t);
  await login(emp, user, "employee-password-1");
  await emp.goto(`${BASE}/assets/${id}`);
  const card = emp.locator("section").filter({ has: emp.getByRole("heading", { name: "数据", exact: true }) });
  await card.getByText("还没有数据").waitFor();
  await card.locator("select").first().selectOption("周六下午");
  await card.getByRole("button", { name: "新增一行" }).click();
  await card.getByRole("cell", { name: "周六下午" }).waitFor();
  await card.getByRole("cell", { name: "小王" }).first().waitFor();
  // 有「姓名」这种自动填写的字段时，不再单独显示「填写人」一列
  await shot(emp, "app-data-employee");
  step("员工在应用详情的「数据」里填一行：姓名自动填成本人");

  // 管理员也报一行；员工仍只看到自己的
  assertEq((await api(admin, "POST", `/api/apps/${slug}/data/signups`, { row: { session: "周六上午" } })).status, 201, "管理员报名");
  await emp.reload();
  await card.getByRole("cell", { name: "周六下午" }).waitFor();
  assert(!(await card.getByRole("cell", { name: "周六上午" }).count()), "员工不应看到别人的报名");
  step("各自填写的表：员工只看到自己的行");

  // ---- 作者看全部、导出 CSV ----
  await admin.goto(`${BASE}/assets/${id}`);
  const adminCard = admin.locator("section").filter({ has: admin.getByRole("heading", { name: "数据", exact: true }) });
  await adminCard.getByRole("cell", { name: "周六上午" }).waitFor();
  await adminCard.getByRole("cell", { name: "周六下午" }).waitFor();
  const [download] = await Promise.all([admin.waitForEvent("download"), adminCard.getByRole("link", { name: "导出 CSV" }).click()]);
  const csv = await (await download.createReadStream()).toArray().then((chunks) => Buffer.concat(chunks).toString("utf8"));
  assert(csv.includes("场次") && csv.includes("周六下午") && csv.includes("小王"), "导出的 CSV：" + csv);
  await shot(admin, "app-data-admin");
  step("作者看到全部行，导出 CSV 带表头和填写人");

  // ---- 员工删自己的行 ----
  await card.getByRole("button", { name: "删除" }).click();
  await emp.getByRole("alertdialog").getByRole("button", { name: "删除" }).click();
  await card.getByText("还没有数据").waitFor();
  step("员工删掉自己的行（保留 30 天）");

  await api(admin, "DELETE", `/api/hub/${id}`);
}
