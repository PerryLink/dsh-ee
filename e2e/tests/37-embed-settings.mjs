// 管理端嵌入设置（E1）：设置页分区可见、保存后 CSP 立即生效、非管理员看不到；
// 以及 env 只读 + DB 覆盖的展示（环境基线来自 DSH_EE_EMBED_ORIGINS）。
import { ADMIN_PW, BASE, api, assert, assertEq, login, newPage, step, tag } from "../lib.mjs";

const envOrigins = (process.env.DSH_EE_EMBED_ORIGINS || "").split(",").map((x) => x.trim()).filter(Boolean);

export default async function (t) {
  const admin = await newPage(t);
  await login(admin, "admin", ADMIN_PW);

  // ---- 设置页分区与 env 基线 ----
  await admin.goto(`${BASE}/admin/settings#settings-embed`);
  const section = admin.locator("#settings-embed");
  await section.waitFor({ timeout: 15000 });
  await section.getByText("跨站嵌入", { exact: true }).waitFor();
  await admin.waitForTimeout(800); // 等 settings 与 embed-settings 两个请求回来
  for (const origin of envOrigins) {
    assert((await section.innerText()).includes(origin), "设置页应展示部署环境来源 " + origin);
  }
  step("系统设置里的「跨站嵌入」分区可见，并展示只读的部署来源：" + envOrigins.join(", "));

  // ---- 追加来源：保存后 CSP 立即生效（/a/* 加来源；/admin 仍 'self'） ----
  const dynamicOrigin = "https://dynamic.example.com";
  const originsArea = section.locator("textarea").first();
  await originsArea.fill(dynamicOrigin);
  await section.getByText("跨站嵌入", { exact: true }).click(); // blur 触发保存
  await admin.waitForTimeout(2200); // StoreProvider 600ms 合并保存 + 服务端返回
  const csp = async (path) => (await fetch(`${BASE}${path}`)).headers.get("content-security-policy");
  const appCSP = await csp("/a/some-app");
  assert(appCSP.includes(dynamicOrigin), "保存后 /a/* 的 CSP 应立即包含新来源：" + appCSP);
  for (const origin of envOrigins) assert(appCSP.includes(origin), "env 来源仍在 CSP 里：" + origin);
  assertEq(await csp("/admin/menus"), "frame-ancestors 'self'", "管理页不能放行嵌入来源");
  const saved = await api(admin, "GET", "/api/admin/embed-settings");
  assertEq(saved.status, 200, "读嵌入有效配置");
  assert(saved.data.effective.allowedOrigins.includes(dynamicOrigin), "有效来源应包含 DB 追加值：" + JSON.stringify(saved.data.effective.allowedOrigins));
  step("管理页追加来源保存后：CSP 立即带上新来源，/admin 仍只允许同站");

  // ---- 会话 TTL（分钟）：保存后 mint 使用 DB 值 ----
  const ttlInput = section.locator("input[type=number]").first();
  await ttlInput.fill("90");
  await section.getByText("跨站嵌入", { exact: true }).click();
  await admin.waitForTimeout(2200);
  const settings = await api(admin, "GET", "/api/settings");
  assertEq(settings.status, 200, "读设置");
  assertEq(settings.data.embed?.sessionMinutes, 90, "DB 里应保存 sessionMinutes=90");
  const effective = await api(admin, "GET", "/api/admin/embed-settings");
  assertEq(effective.data.effective.sessionSeconds, 90 * 60, "有效 TTL 应为 90 分钟");
  step("会话有效期改为 90 分钟：保存成功，有效配置立即变化");

  // ---- 非管理员看不到这一段 ----
  const user = `embed-settings-ui-${tag}`;
  assertEq((await api(admin, "POST", "/api/admin/users", { username: user, password: "employee-password-1", department: "研发部" })).status, 201, "建员工");
  const emp = await newPage(t);
  await login(emp, user, "employee-password-1");
  await emp.goto(`${BASE}/admin/settings#settings-embed`);
  await emp.waitForTimeout(800);
  assertEq(await emp.locator("#settings-embed").count(), 0, "非管理员不应看到嵌入设置分区");
  const empSettings = await api(emp, "GET", "/api/settings");
  assertEq(empSettings.data.embed?.allowedOrigins?.length ?? 0, 0, "非管理员读设置时嵌入段应隐藏");
  step("非管理员：设置页没有嵌入分区，/api/settings 也不返回来源");
}
