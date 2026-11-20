// DSH 里的 Agent 操作工作台（直接对 /mcp 发 JSON-RPC，模拟 DSH 里的模型）：
// 员工只看到员工侧工具 → 经 Agent 启用技能（网页上变成「已在 DSH 生效」）→ 经 Agent 分享提示词（进审核）
// → 吊销令牌要两步确认 → 审计页按来源「Agent」能筛到这些操作
import { ADMIN_PW, BASE, api, assert, assertEq, login, newPage, shot, step, tag } from "../lib.mjs";

let rpcId = 0;
async function mcp(token, method, params) {
  const r = await fetch(`${BASE}/mcp`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json", Accept: "application/json, text/event-stream" },
    body: JSON.stringify({ jsonrpc: "2.0", id: ++rpcId, method, params }),
  });
  assertEq(r.status, 200, `/mcp ${method}`);
  return (await r.json()).result;
}

async function tool(token, name, args) {
  const result = await mcp(token, "tools/call", { name, arguments: args });
  const text = result.content[0].text;
  return { isError: !!result.isError, text, json: () => JSON.parse(text) };
}

export default async function (t) {
  const admin = await newPage(t);
  await login(admin, "admin", ADMIN_PW);

  const skill = `e2e-agent-skill-${tag}`;
  const prompt = `e2e-agent-prompt-${tag}`;
  const skillTitle = `E2E Agent 报销制度 ${tag}`;
  const promptTitle = `E2E Agent 周报 ${tag}`;
  assertEq((await api(admin, "POST", "/api/hub", { kind: "skill", slug: skill, spec: { title: skillTitle, description: "差旅住宿标准", body: "住宿每晚 1300 元以内。" } })).status, 201, "管理员建技能");

  const emp = `agentemp-${tag}`;
  assertEq((await api(admin, "POST", "/api/admin/users", { username: emp, password: "agent-password-123", department: "研发效能部" })).status, 201, "建员工");
  const page = await newPage(t);
  await login(page, emp, "agent-password-123");
  const issued = await api(page, "POST", "/api/me/tokens", { name: "agent-e2e", scopes: ["models", "registry:read", "registry:publish", "hub", "connections", "agent"] });
  assertEq(issued.status, 201, "带 agent 范围的令牌");
  const token = issued.data.token;

  // 1. 员工只看到员工侧工具
  const listed = (await mcp(token, "tools/list")).tools.map((x) => x.name);
  for (const name of ["ee_me", "ee_hub", "ee_tokens", "ee_connections", "ee_doctor"]) assert(listed.includes(name), `员工应有 ${name}：${listed}`);
  assert(!listed.some((n) => /^ee_(users|roles|settings|admin_tokens)$/.test(n)), "员工不该看到管理工具：" + listed);
  step(`员工的工具：${listed.filter((n) => n.startsWith("ee_")).join("、")}`);

  // 2. 经 Agent 启用技能 → 网页上变成「已在 DSH 生效」
  const found = (await tool(token, "ee_hub", { action: "search", q: skillTitle })).json();
  assertEq(found.assets.length, 1, "搜到技能");
  assertEq((await tool(token, "ee_hub", { action: "enable", id: found.assets[0].id })).isError, false, "启用");
  await page.goto(`${BASE}/hub?tab=skill`);
  const card = page.locator("div.rounded-lg", { hasText: skillTitle }).last();
  await card.getByText("已在 DSH 生效").waitFor();
  await shot(page, "agent-hub-enabled");
  step("经 Agent 启用技能，能力中心页面显示已在 DSH 生效");

  // 3. 经 Agent 分享提示词 → 进审核队列
  const created = await tool(token, "ee_hub", { action: "create", kind: "prompt", slug: prompt, spec: { title: promptTitle, description: "把本周工作整理成周报", body: "按项目整理本周进展。" } });
  assertEq(created.isError, false, "分享提示词：" + created.text);
  assertEq(created.json().status, "pending", "进入待审核");
  await admin.goto(`${BASE}/admin/review`);
  await admin.getByRole("button", { name: /技能、提示词与微应用/ }).click();
  await admin.getByRole("button", { name: new RegExp(promptTitle) }).waitFor();
  await shot(admin, "agent-review");
  step("经 Agent 分享的提示词出现在审核队列");

  // 4. 吊销令牌：先预览，确认后才执行
  const victim = await api(page, "POST", "/api/me/tokens", { name: `victim-${tag}`, scopes: ["hub"] });
  const vid = victim.data.info.id;
  const alive = async () => (await fetch(`${BASE}/api/dsh/whoami`, { headers: { Authorization: `Bearer ${victim.data.token}` } })).status === 200;
  const preview = await tool(token, "ee_tokens", { action: "revoke", id: vid });
  assert(preview.json().confirm_required && /将吊销令牌/.test(preview.json().preview), "第一步应返回预览：" + preview.text);
  assert(await alive(), "只看预览不该吊销");
  const replay = await tool(token, "ee_tokens", { action: "revoke", id: vid, confirm: preview.json().confirm });
  assertEq(replay.isError, false, "带确认码执行：" + replay.text);
  assert(!(await alive()), "确认后令牌应已吊销");
  assertEq((await tool(token, "ee_tokens", { action: "revoke", id: vid, confirm: preview.json().confirm })).isError, true, "确认码只能用一次");
  step("吊销令牌：预览无副作用，确认后生效，确认码不可重放");

  // 5. 审计页按来源筛选
  await admin.goto(`${BASE}/admin/audit`);
  await admin.getByLabel("全部来源").click();
  await admin.getByRole("option", { name: "Agent", exact: true }).click();
  await admin.getByText(prompt).first().waitFor();
  await shot(admin, "agent-audit");
  step("审计日志按来源「Agent」筛到这些操作");

  // 收尾
  const assets = (await api(admin, "GET", "/api/hub")).data.assets;
  for (const a of assets.filter((x) => [skill, prompt].includes(x.slug))) await api(admin, "DELETE", `/api/hub/${a.id}`);
}
