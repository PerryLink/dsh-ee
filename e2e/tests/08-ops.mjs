// 运营数据、审计日志与 LDAP 设置：总览与运营数据来自真实接口，审计日志记下登录，
// 没有权限的员工看不到；LDAP 配置页能试连并给出逐步结果，服务账号密码不回显
import { ADMIN_PW, BASE, api, assert, assertEq, login, newPage, shot, step, tag } from "../lib.mjs";

export default async function (t) {
  const page = await newPage(t);
  await login(page, "admin", ADMIN_PW);

  await page.goto(`${BASE}/admin`);
  await page.getByRole("heading", { name: "员工与终端" }).waitFor();
  await page.getByRole("heading", { name: "每日安装" }).waitFor();
  await page.getByRole("heading", { name: "最近操作" }).waitFor();
  const text = await page.locator("main").innerText();
  assert(!text.includes("统信 UOS") && !text.includes("截至 2026-10-26"), "总览里不应再有模拟数据");
  step("总览：员工与终端、每日安装、最近操作都来自接口");
  await shot(page, "ops-dashboard");

  await page.goto(`${BASE}/admin/stats`);
  await page.getByRole("heading", { name: "按部门" }).waitFor();
  const resp = page.waitForResponse((r) => r.url().includes("/api/admin/stats?days=30"));
  await page.getByRole("button", { name: "近 30 天" }).click();
  assertEq((await resp).status(), 200, "切换到近 30 天");
  const stats = (await api(page, "GET", "/api/admin/stats?days=30&tz=480")).data;
  assertEq(stats.daily.length, 30, "近 30 天的天数");
  step(`运营数据：近 30 天 ${stats.totals.installs} 次安装、${stats.totals.active_users} 位活跃员工`);
  await shot(page, "ops-stats");

  await page.goto(`${BASE}/admin/audit`);
  await page.getByRole("cell", { name: "登录", exact: true }).first().waitFor();
  const logs = (await api(page, "GET", "/api/admin/audit?action=auth.&limit=5")).data.logs;
  assert(logs.some((l) => l.action === "auth.login" && l.actor_name === "admin"), "审计日志里应有 admin 的登录");
  step("审计日志里有本次登录");
  await shot(page, "ops-audit");

  // 普通员工：工作台个人数据可读，运营数据与审计日志不可读
  const username = `ops-${tag}`;
  const created = await api(page, "POST", "/api/admin/users", { username, password: "ops-password-123", department: "测试部" });
  assertEq(created.status, 201, "建员工");
  const emp = await newPage(t);
  await login(emp, username, "ops-password-123");
  assertEq((await api(emp, "GET", "/api/me/overview")).status, 200, "员工读工作台数据");
  assertEq((await api(emp, "GET", "/api/admin/stats")).status, 403, "员工读运营数据");
  assertEq((await api(emp, "GET", "/api/admin/audit")).status, 403, "员工读审计日志");
  assertEq((await api(emp, "GET", "/api/admin/auth/ldap")).status, 403, "员工读 LDAP 配置");
  await emp.goto(`${BASE}/workbench`);
  await emp.getByRole("heading", { name: "在用的应用" }).waitFor();
  await emp.waitForLoadState("networkidle");
  // 空卡、值为 0 的指标、开发卡都不占位
  assertEq(await emp.getByRole("heading", { name: "装过的能力" }).count(), 0, "没装过能力时不显示空卡");
  assertEq(await emp.getByText("我的密钥", { exact: true }).count(), 0, "0 个密钥不显示指标");
  assertEq(await emp.locator("[data-dev-block]").count(), 0, "员工工作台不显示开发卡");
  step("员工：工作台隐藏空卡、0 值指标和开发卡，管理接口都是 403");
  await shot(emp, "ops-workbench-empty");

  // LDAP：连不上的地址 → 逐步结果里第一步失败；不保存
  await page.goto(`${BASE}/admin/auth`);
  await page.getByText("开启 LDAP 登录").waitFor();
  await page.getByLabel("目录地址").fill("ldap://127.0.0.1:1");
  await page.getByLabel(/^服务账号(?!密码)/).fill("cn=svc,dc=acme,dc=local");
  await page.getByLabel("服务账号密码").fill("svc-secret-e2e");
  await page.getByLabel("搜索起点（Base DN）").fill("dc=acme,dc=local");
  await page.getByText("有未保存的修改").waitFor();
  await page.getByRole("button", { name: "测试", exact: true }).click();
  await page.getByText("连接并绑定服务账号").waitFor();
  const before = (await api(page, "GET", "/api/admin/auth/ldap")).data;
  assert(!JSON.stringify(before).includes("svc-secret-e2e"), "没保存前接口里不应有密码");
  step("LDAP：试连失败时给出逐步结果，未保存的配置不落库");
  await shot(page, "ldap-test-failed");
}
