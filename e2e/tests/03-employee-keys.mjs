// 员工：管理员建用户并分配额度 → 员工登录自助创建统一令牌 → 调网关 → 用量计入令牌与账号额度
import http from "node:http";
import { ADMIN_PW, BASE, api, assert, assertEq, login, logout, newPage, shot, step, tag } from "../lib.mjs";

export default async function (t) {
  const upstream = http.createServer((req, res) => {
    req.resume();
    req.on("end", () => {
      res.setHeader("Content-Type", "application/json");
      res.end(JSON.stringify({ id: "x", choices: [{ message: { role: "assistant", content: "你好" } }], usage: { prompt_tokens: 1200, completion_tokens: 800, total_tokens: 2000 } }));
    });
  });
  await new Promise((r) => upstream.listen(0, "127.0.0.1", r));
  const model = `e2e-chat-${tag}`;
  const emp = `li.na.${tag}`;
  const page = await newPage(t);
  let providerId;

  try {
    await login(page, "admin", ADMIN_PW);
    const p = await api(page, "POST", "/api/admin/providers", {
      name: `fake-${tag}`, base_url: `http://127.0.0.1:${upstream.address().port}`, api_key: "sk-company-secret-9999", models: [model],
    });
    assertEq(p.status, 201, "创建上游");
    providerId = p.data.id;

    await page.goto(`${BASE}/admin/users`);
    await page.getByRole("button", { name: "新建用户" }).click();
    await page.getByPlaceholder("zhang.wei").fill(emp);
    await page.getByLabel("初始密码").fill("li-na-password-1");
    await page.getByLabel("显示名", { exact: true }).fill("李娜");
    await page.getByLabel("部门", { exact: true }).fill("平台架构组");
    await page.getByRole("button", { name: "创建用户" }).click();
    await page.getByPlaceholder("搜索用户名、显示名、部门或角色").fill(emp);
    await page.getByText(emp, { exact: true }).first().waitFor();
    step("管理员创建员工 " + emp);

    const users = await api(page, "GET", "/api/admin/users");
    const u = users.data.users.find((x) => x.username === emp);
    assertEq(u.monthly_token_limit, 0, "新用户默认额度");
    const q = await api(page, "PATCH", `/api/admin/users/${u.id}/quota`, { monthly_token_limit: 10000, allowed_models: [model], quota_expires_at: null });
    assertEq(q.status, 200, "分配额度");
    step("分配每月 1 万 token，只允许 " + model);
    await logout(page);

    await login(page, emp, "li-na-password-1");
    await page.goto(`${BASE}/tokens`);
    await page.getByRole("button", { name: "生成令牌" }).first().click();
    // 新表单默认按第三方 Harness 处理；DSH 用户可显式改成 DSH 接入
    assertEq(await page.locator("form select").inputValue(), "third_party", "令牌默认客户端类型");
    await page.getByLabel("名称", { exact: true }).fill("李娜的 DSH");
    await page.locator("form").getByRole("button", { name: "生成令牌" }).click();
    const pre = page.locator("pre").filter({ hasText: /^\s*ee_at_\S+\s*$/ }).first();
    await pre.waitFor();
    const key = (await pre.innerText()).trim();
    assert(key.startsWith("ee_at_"), "没拿到统一令牌：" + key);
    step("员工自助创建统一令牌：" + key.slice(0, 12) + "…");
    await shot(page, "keys-issued");

    const call = (m) =>
      fetch(`${BASE}/v1/chat/completions`, {
        method: "POST",
        headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/json" },
        body: JSON.stringify({ model: m, messages: [{ role: "user", content: "你好" }] }),
      }).then((r) => r.status);
    assertEq(await call(model), 200, "允许的模型");
    assertEq(await call("deepseek-reasoner"), 403, "不在允许列表里的模型");
    step("允许的模型 → 200，其它模型 → 403");

    await page.reload();
    await page.getByRole("heading", { name: "我的令牌" }).waitFor();
    const mine = await api(page, "GET", "/api/me/tokens");
    const current = mine.data.tokens.find((item) => item.prefix === key.slice(0, 12));
    assertEq(current.month_used, 2000, "统一令牌本月用量");
    assertEq(current.client_kind, "third_party", "网页新建令牌默认第三方 Harness");
    step("令牌本月已用 2000 token，客户端类型为第三方 Harness");

    // 显式选 DSH 接入：给不用设备码登录、手工填进 DSH 的令牌
    await page.getByRole("button", { name: "生成令牌" }).first().click();
    await page.getByLabel("名称", { exact: true }).fill("李娜的 DSH 手工");
    await page.locator("form select").selectOption("dsh");
    await page.locator("form").getByRole("button", { name: "生成令牌" }).click();
    await page.locator("form").waitFor({ state: "hidden" }); // 等 POST 完成、表单收起
    const mine2 = await api(page, "GET", "/api/me/tokens");
    const dshManual = mine2.data.tokens.find((item) => item.name === "李娜的 DSH 手工");
    assertEq(dshManual?.client_kind, "dsh", "可显式选择 DSH 接入");
    // 列表里能看出两类客户端
    await page.getByText("第三方 Harness").first().waitFor();
    await page.getByText("DSH 接入").first().waitFor();
    await shot(page, "keys-client-kind");
    step("网页可选 DSH / 第三方 Harness，列表显示客户端类型");
    await shot(page, "keys-mine");

    await page.goto(`${BASE}/admin/users`);
    await page.getByText("页面不存在，或你没有访问权限").waitFor();
    assertEq((await api(page, "GET", "/api/admin/users")).status, 403, "员工调管理接口");
    step("员工打不开 /admin/users，接口返回 403");
  } finally {
    upstream.close();
    if (providerId) {
      await logout(page);
      await login(page, "admin", ADMIN_PW);
      await api(page, "DELETE", `/api/admin/providers/${providerId}`);
    }
  }
}
