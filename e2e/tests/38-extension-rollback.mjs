// 扩展安装历史与回滚（网页）：连接详情看历史、预览回滚计划、确认回滚；
// 回滚后当前标记和工具清单都要刷新。API 层的细节（非法目标、权限、签名策略等）由 Go 测试覆盖。
import { ADMIN_PW, BASE, api, assert, assertEq, login, newPage, shot, step, tag } from "../lib.mjs";

function openapi(withCreate) {
  const doc = `openapi: 3.0.3
info: { title: Tickets, version: "1" }
paths:
  /api/tickets:
    get:
      operationId: list_tickets
      summary: List tickets
      responses:
        "200": { description: ok }
`;
  if (!withCreate) return doc;
  return (
    doc +
    `    post:
      operationId: create_ticket
      summary: Create a ticket
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                title: { type: string }
      responses: { "201": { description: created } }
`
  );
}

function manifest(id, version, withCreate) {
  const extra = withCreate ? `, { "name": "create_ticket", "description": "建工单" }` : "";
  return `{
  "id": ${JSON.stringify(id)}, "version": ${JSON.stringify(version)},
  "name": "回滚测试扩展", "description": "查工单、建工单", "kind": "declarative",
  "openapi": "openapi.yaml", "servers": ["https://itsm.example.com"],
  "credential": { "mode": "shared" },
  "tools": [ { "name": "list_tickets", "readOnly": true, "description": "按状态列出工单" }${extra} ]
}`;
}

const pkg = (id, version, withCreate) => ({
  files: { "dsh-ee.extension.json": manifest(id, version, withCreate), "openapi.yaml": openapi(withCreate) },
});

export default async function (t) {
  const page = await newPage(t);
  await login(page, "admin", ADMIN_PW);
  const extID = `acme/itsm-${tag}`;

  // 1. 装 1.0.0，再升级到 1.1.0（多一个写操作工具）
  const installed = await api(page, "POST", "/api/admin/extensions/install", pkg(extID, "1.0.0", false));
  assertEq(installed.status, 201, `安装扩展：${JSON.stringify(installed.data)}`);
  const assetID = installed.data.asset.id;
  const upgraded = await api(page, "POST", "/api/admin/extensions/install", pkg(extID, "1.1.0", true));
  assertEq(upgraded.status, 201, `升级扩展：${JSON.stringify(upgraded.data)}`);
  assertEq(upgraded.data.upgraded_from, "1.0.0", "升级来自 1.0.0");
  assertEq(upgraded.data.tools, 2, "升级后有两个工具");

  // 2. 连接详情：扩展卡片显示当前版本，工具清单里两个工具都在
  await page.goto(`${BASE}/assets/${assetID}`);
  await page.getByTestId("extension-panel").waitFor();
  await page.getByTestId("extension-version").getByText(`${extID}@1.1.0`).waitFor();
  await page.getByText("create_ticket", { exact: true }).waitFor();
  await step("连接详情显示扩展 1.1.0 和两个工具");

  // 3. 打开「安装历史」：两版，当前是 #2，旧版有回滚按钮
  await page.getByTestId("extension-history-open").click();
  const dialog = page.getByTestId("extension-history-dialog");
  await dialog.waitFor();
  await dialog.getByTestId("ext-history-2").waitFor();
  await dialog.getByTestId("ext-history-2").getByTestId("ext-history-current").waitFor();
  await dialog.getByTestId("ext-history-1").getByTestId("ext-rollback-1").waitFor();
  await shot(page, "extension-history");
  await step("安装历史列出两版，当前标记在 1.1.0");

  // 4. 回滚预览：展示变化（去掉升级时新增的工具），再确认回滚
  await dialog.getByTestId("ext-rollback-1").click();
  const plan = dialog.getByTestId("ext-rollback-plan");
  await plan.waitFor();
  await plan.getByText("去掉了工具 create_ticket", { exact: false }).waitFor();
  await shot(page, "extension-rollback-plan");
  await step("回滚计划列出「去掉了工具 create_ticket」");
  await dialog.getByTestId("ext-rollback-confirm").click();
  await dialog.waitFor({ state: "hidden" });

  // 5. 回滚后：扩展版本回到 1.0.0，工具清单只剩 list_tickets（页面数据已刷新）
  await page.getByTestId("extension-version").getByText(`${extID}@1.0.0`).waitFor();
  await page.waitForFunction(() => !document.body.innerText.includes("create_ticket"));
  assert((await page.getByText("list_tickets", { exact: true }).count()) > 0, "回滚后仍应有 list_tickets");
  await shot(page, "extension-rollback-done");
  await step("回滚后扩展版本 1.0.0、工具清单刷新为 1 个");

  // 6. 再打开历史：回滚追加的新版本是当前版本，之前两版仍可回滚
  await page.getByTestId("extension-history-open").click();
  await dialog.waitFor();
  await dialog.getByTestId("ext-history-3").getByTestId("ext-history-current").waitFor();
  await dialog.getByTestId("ext-history-3").getByText(`${extID}@1.0.0`, { exact: false }).waitFor();
  await shot(page, "extension-history-after-rollback");
  await step("回滚也进历史：当前是连接版本 #3（扩展 1.0.0）");
  await page.keyboard.press("Escape");
  await dialog.waitFor({ state: "hidden" });

  // 7. 只装过一版的扩展：历史只有当前一条，提示没有可回滚的历史版本
  const soloID = `acme/solo-${tag}`;
  const solo = await api(page, "POST", "/api/admin/extensions/install", pkg(soloID, "1.0.0", false));
  assertEq(solo.status, 201, `安装第二个扩展：${JSON.stringify(solo.data)}`);
  await page.goto(`${BASE}/assets/${solo.data.asset.id}`);
  await page.getByTestId("extension-panel").waitFor();
  await page.getByTestId("extension-history-open").click();
  await dialog.waitFor();
  await dialog.getByTestId("ext-history-1").getByTestId("ext-history-current").waitFor();
  await dialog.getByTestId("ext-history-empty").waitFor();
  assertEq(await dialog.locator("[data-testid^='ext-rollback-']").count(), 0, "没有历史时不应有回滚按钮");
  await shot(page, "extension-history-no-target");
  await step("只装过一版时历史显示没有可回滚的版本");
}
