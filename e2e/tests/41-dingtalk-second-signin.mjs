// 钉钉组织同步与第二登录入口的页面：主登录方式是飞书时再开钉钉，登录页出现两个入口，个人账号页显示身份绑定。
// 不向钉钉发任何请求（保存配置、渲染页面都不联网）；真实的同步、登录与绑定流程由
// server/internal/api 的 TestDingTalkOrganizationSync / TestFeishuDingTalkIdentityBinding 用假钉钉覆盖。
import { ADMIN_PW, BASE, api, assert, assertEq, login, logout, newPage, shot, step } from "../lib.mjs";

// ToggleRow 的开关没有可访问名字，按所在行的标题找
const toggle = (scope, label) => scope.locator("div.items-start").filter({ has: scope.page().getByText(label, { exact: true }) }).getByRole("switch");

export default async function (t) {
  const admin = await newPage(t, { viewport: { width: 1280, height: 1100 } });
  await login(admin, "admin", ADMIN_PW);

  await admin.goto(`${BASE}/admin/auth`);
  const second = admin.locator("#auth-oidc-secondary");
  await second.getByText("先把上面的主登录方式选成飞书或钉钉并开启保存").waitFor();
  assertEq(await second.getByRole("switch").count(), 0, "主登录方式没开时第二入口不可配");
  step("主登录方式不是飞书 / 钉钉时，第二登录入口只给提示");

  // 主登录方式：飞书（保存不联网，Issuer 指向一个不会被访问的本机地址）
  const oidc = admin.locator("#auth-oidc");
  await oidc.getByRole("button", { name: "飞书", exact: true }).click();
  await oidc.getByRole("switch").first().click();
  await oidc.getByLabel("Issuer").fill("http://127.0.0.1:9/feishu");
  await oidc.getByLabel("Client ID").fill("cli_feishu");
  await oidc.getByLabel("Client Secret").fill("feishu-secret");
  await oidc.getByRole("button", { name: "保存统一登录配置" }).click();
  await admin.getByText("已保存统一登录配置").first().waitFor();

  // 第二登录入口：只能选钉钉
  await second.getByRole("button", { name: "钉钉", exact: true }).waitFor();
  assertEq(await second.getByRole("button", { name: "飞书", exact: true }).count(), 0, "第二入口不能和主登录方式是同一个来源");
  await second.getByRole("button", { name: "钉钉", exact: true }).click();
  await toggle(second, "开启第二登录入口").click();
  await second.getByLabel("钉钉企业 CorpId").fill("ding-corp-e2e");
  await second.getByLabel("Client ID").fill("ding-app-e2e");
  await second.getByLabel("Client Secret").fill("ding-secret-e2e");
  await toggle(second, "按核实过的手机号 / 企业邮箱自动关联身份").click();
  const sync = second.locator("[data-dingtalk-sync]");
  await sync.getByText("钉钉组织同步", { exact: true }).waitFor();
  await toggle(sync, "每小时自动同步").click();
  await toggle(sync, "已离职的成员同步禁用").click();
  assertEq(await second.getByText("钉钉机器人对话").count(), 0, "第二入口不带机器人对话");
  assert(await sync.getByRole("button", { name: "立即同步" }).isDisabled(), "没保存前不能同步");
  await second.getByRole("button", { name: "保存统一登录配置" }).click();
  await admin.getByText("已保存统一登录配置").first().waitFor();
  await sync.getByRole("button", { name: "立即同步" }).waitFor();
  await shot(admin, "dingtalk-second-signin-config");

  const saved = await api(admin, "GET", "/api/admin/auth/oidc?slot=secondary");
  assert(!JSON.stringify(saved.data).includes("ding-secret-e2e"), "不能回显钉钉 Client Secret");
  assertEq(saved.data.preset, "dingtalk", "第二入口是钉钉");
  assertEq(saved.data.dingTalkSync, true, "每小时同步已保存");
  assertEq(saved.data.dingTalkSyncDisable, true, "离职禁用已保存");
  assertEq(saved.data.linkByContact, true, "自动关联已保存");
  const primary = await api(admin, "GET", "/api/admin/auth/oidc");
  assertEq(primary.data.preset, "feishu", "保存第二入口不动主登录方式");
  step("第二登录入口选钉钉：凭据、组织同步开关与自动关联开关保存成功，secret 不回显");

  // 个人账号页：本地管理员能看到两个身份都未绑定，但没有绑定入口
  await admin.goto(`${BASE}/account`);
  const card = admin.locator("[data-identity-bindings]");
  await card.getByText("飞书 / 钉钉身份").waitFor();
  assertEq(await card.locator("[data-identity]").count(), 2, "飞书和钉钉各一行");
  assertEq(await card.getByRole("link").count(), 0, "本地账号不能绑定飞书 / 钉钉身份");
  const mine = await api(admin, "GET", "/api/me/identities");
  assertEq(mine.data.identities.map((i) => i.provider).join(","), "feishu,dingtalk", "身份列表顺序");
  await shot(admin, "dingtalk-identity-card");
  step("个人账号页显示飞书 / 钉钉身份；本地账号不提供绑定");

  const english = await newPage(t, { lang: "en" });
  await login(english, "admin", ADMIN_PW);
  await english.goto(`${BASE}/account?identity_error=${encodeURIComponent("身份冲突")}&identity_error_code=identity.already_linked`);
  await english.locator("[data-identity-bindings]").getByText("This account already has a linked identity from this provider. Unlink it before switching.").waitFor();
  step("账号页按机器码翻译绑定冲突，不直接显示回调的中文文案");

  // 登录页：两个入口
  await logout(admin);
  await admin.goto(`${BASE}/login`);
  await admin.getByRole("link", { name: "用飞书登录" }).waitFor();
  const ding = admin.getByRole("link", { name: "用钉钉登录" });
  await ding.waitFor();
  assert((await ding.getAttribute("href")).startsWith("/api/auth/oidc/start?provider=dingtalk&"), "钉钉按钮走第二登录入口");
  await shot(admin, "dingtalk-second-signin-login");
  step("登录页同时给出飞书和钉钉两个入口");
}
