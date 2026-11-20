// 收尾功能：企业统一登录（OIDC，用一个本地的假 IdP 自动同意）与月度 AI 落地报告。
import crypto from "node:crypto";
import http from "node:http";
import { ADMIN_PW, BASE, api, assert, assertEq, login, logout, newPage, shot, step, tag } from "../lib.mjs";

/** 最小的 OIDC 身份提供方：/authorize 直接带着授权码跳回去（相当于用户已登录并同意）。 */
async function fakeIdP(user) {
  const { publicKey, privateKey } = crypto.generateKeyPairSync("rsa", { modulusLength: 2048 });
  const jwk = { ...publicKey.export({ format: "jwk" }), kid: "e2e", use: "sig", alg: "RS256" };
  const codes = new Map();
  const b64 = (x) => Buffer.from(typeof x === "string" ? x : JSON.stringify(x)).toString("base64url");
  let issuer = "";
  const server = http.createServer(async (req, res) => {
    const u = new URL(req.url, issuer);
    const json = (v, code = 200) => {
      res.writeHead(code, { "Content-Type": "application/json" });
      res.end(JSON.stringify(v));
    };
    if (u.pathname === "/.well-known/openid-configuration") {
      return json({ issuer, authorization_endpoint: `${issuer}/authorize`, token_endpoint: `${issuer}/token`, jwks_uri: `${issuer}/jwks`, userinfo_endpoint: `${issuer}/userinfo` });
    }
    if (u.pathname === "/jwks") return json({ keys: [jwk] });
    if (u.pathname === "/authorize") {
      const code = crypto.randomBytes(16).toString("hex");
      codes.set(code, { nonce: u.searchParams.get("nonce"), challenge: u.searchParams.get("code_challenge") });
      const back = new URL(u.searchParams.get("redirect_uri"));
      back.searchParams.set("code", code);
      back.searchParams.set("state", u.searchParams.get("state"));
      res.writeHead(302, { Location: back.toString() });
      return res.end();
    }
    if (u.pathname === "/token") {
      let body = "";
      for await (const c of req) body += c;
      const f = new URLSearchParams(body);
      const g = codes.get(f.get("code"));
      codes.delete(f.get("code"));
      const sum = crypto.createHash("sha256").update(f.get("code_verifier") ?? "").digest("base64url");
      if (!g || sum !== g.challenge || f.get("client_secret") !== "e2e-secret") return json({ error: "invalid_grant" }, 400);
      const now = Math.floor(Date.now() / 1000);
      const head = b64({ alg: "RS256", kid: "e2e", typ: "JWT" });
      const payload = b64({ iss: issuer, aud: "dsh-ee", sub: user.sub, iat: now, exp: now + 300, nonce: g.nonce, preferred_username: user.username, name: user.name, groups: user.groups });
      const sig = crypto.sign("sha256", Buffer.from(`${head}.${payload}`), privateKey).toString("base64url");
      return json({ id_token: `${head}.${payload}.${sig}`, access_token: "at", token_type: "Bearer" });
    }
    if (u.pathname === "/userinfo") return json({ sub: user.sub, email: `${user.username}@xinghe.example`, department: user.department });
    res.writeHead(404);
    res.end();
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  issuer = `http://127.0.0.1:${server.address().port}`;
  return { issuer, close: () => server.close() };
}

export default async function (t) {
  const admin = await newPage(t);
  await login(admin, "admin", ADMIN_PW);
  const username = `sso.${tag}`;
  const idp = await fakeIdP({ sub: `sub-${tag}`, username, name: "单点 测试", department: "研发部", groups: ["/xinghe/dev"] });
  try {
    // ---- 系统设置：配置企业统一登录 ----
    await admin.goto(`${BASE}/admin/auth`);
    await admin.getByText("开启企业统一登录").waitFor();
    await admin.locator("text=开启企业统一登录").locator("xpath=../..").getByRole("switch").click();
    await admin.getByRole("button", { name: "Keycloak", exact: true }).click();
    await admin.getByLabel("登录按钮上的名字").fill("星河 SSO");
    await admin.getByRole("textbox", { name: /^Issuer/ }).fill(idp.issuer);
    await admin.getByLabel("Client ID", { exact: true }).fill("dsh-ee");
    await admin.getByLabel("Client Secret", { exact: true }).fill("e2e-secret");
    await admin.getByRole("button", { name: "试连", exact: true }).click();
    await admin.getByText("读到了身份提供方的配置").waitFor();
    await admin.getByRole("button", { name: "保存统一登录配置" }).click();
    await admin.getByText("已保存统一登录配置").first().waitFor();
    await admin.getByText(`${BASE}/api/auth/oidc/callback`).waitFor();
    await shot(admin, "sso-settings");
    step("系统设置里配置企业统一登录：选预设、试连读到端点、保存，页面给出回调地址");

    // ---- 员工：登录页点按钮 → IdP → 回来已登录 ----
    const page = await newPage(t);
    await page.goto(`${BASE}/login`);
    await page.getByRole("link", { name: "用星河 SSO登录" }).waitFor();
    await shot(page, "sso-login");
    await page.getByRole("link", { name: "用星河 SSO登录" }).click();
    await page.waitForURL((u) => !u.pathname.startsWith("/login") && !u.pathname.startsWith("/api/"));
    const me = await api(page, "GET", "/api/auth/me");
    assertEq(me.status, 200, "统一登录后已登录");
    assert(me.data.username === username && me.data.auth_source === "oidc" && me.data.department === "研发部" && me.data.display_name === "单点 测试", "资料：" + JSON.stringify(me.data));
    step("员工点「用星河 SSO登录」，经 IdP 回来就登录了，资料来自声明");
    await page.goto(`${BASE}/account`);
    await page.getByText("企业统一登录（OIDC）").first().waitFor();

    // ---- 月度报告 ----
    const now = new Date();
    const month = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}`;
    await admin.goto(`${BASE}/admin`);
    await admin.getByRole("link", { name: "导出月度报告" }).waitFor();
    await admin.goto(`${BASE}/report/monthly?month=${month}`);
    await admin.getByRole("heading", { name: "AI 落地月度报告" }).waitFor();
    await admin.getByText("活跃率").waitFor();
    await admin.getByText("估算方法").waitFor();
    await admin.getByLabel("每次打开应用节省（分钟）").fill("12");
    await admin.getByRole("button", { name: "保存参数" }).click();
    await admin.getByText("已保存估算参数").first().waitFor();
    await admin.getByText("应用次数 × 12").first().waitFor();
    await shot(admin, "report-monthly");
    const rep = await api(admin, "GET", `/api/admin/report/monthly?month=${month}&tz=480`);
    // 隔离模式下这个库是全新的，没有其它测试留下的调用记录；这里只断言报告结构、
    // 用户数（admin + 刚通过 OIDC 登录的用户）和估算参数，不依赖前面的测试数据。
    assert(rep.data.report.users >= 1 && rep.data.params.minutesPerAppRun === 12, "报告数据：" + JSON.stringify(rep.data.report).slice(0, 300));
    step("管理总览「导出月度报告」：可打印的报告页，估算参数可调");
  } finally {
    await api(admin, "PUT", "/api/admin/auth/oidc", { enabled: false, preset: "custom", name: "企业统一登录", issuer: "", clientId: "", clientSecret: "", scopes: "profile email", usernameClaim: "preferred_username", nameClaim: "name", emailClaim: "email", departmentClaim: "", groupsClaim: "groups", roleMappings: [], autoCreate: true });
    await api(admin, "PUT", "/api/admin/report/params", { minutesPerAppRun: 10, minutesPerToolCall: 3, minutesPerKbHit: 5, hourlyCost: 0 });
    await logout(admin);
    idp.close();
  }
}
