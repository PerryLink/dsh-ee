// 数据中台（M15）：管理员在「企业连接 → 数据中台」上传 CSV 生成表、预览，写一个带自动参数的数据集并试运行；
// 应用的工作台用这个数据集，员工在网页版里只看到自己负责的客户。
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { ADMIN_PW, BASE, api, assert, assertEq, login, newPage, shot, step, tag } from "../lib.mjs";

export default async function (t) {
  const admin = await newPage(t);
  await login(admin, "admin", ADMIN_PW);
  const table = `cust_${tag}`.slice(0, 40);
  const dataset = `my-cust-${tag}`;
  const owner = `小周${tag.slice(-3)}`;

  await admin.goto(`${BASE}/admin/connections?tab=data`);
  await admin.getByRole("heading", { name: "数据集", exact: true }).waitFor();
  step("「企业连接」页有「数据中台」页签");

  // ---- 上传 CSV ----
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "dshee-e2e-"));
  const file = path.join(dir, "客户.csv");
  fs.writeFileSync(file, `﻿客户,负责人,年合同额,续约日期\n华东精密,${owner},120000,2026-12-01\n瑞丰银行,林娜,80000,2027-01-15\n启明软件,${owner},30000,2026-11-20\n`);
  await admin.locator("input[type=file]").setInputFiles(file);
  await admin.getByPlaceholder("customers").fill(table);
  await admin.getByPlaceholder("客户主数据").fill(`客户 ${tag}`);
  await admin.getByRole("button", { name: "上传" }).click();
  await admin.getByText(`已写入 ${table}：这次 3 行，共 3 行`).waitFor();
  const row = admin.locator("li", { hasText: table });
  await row.getByText("年合同额（数字）").waitFor();
  await row.getByRole("button", { name: "预览" }).click();
  await row.getByRole("cell", { name: "华东精密" }).waitFor();
  await shot(admin, "data-hub-table");
  step("上传 CSV 生成表：列类型自动识别，能预览");

  // ---- 写数据集并试运行 ----
  await admin.getByRole("button", { name: "新建数据集" }).click();
  const editor = admin.locator("form", { hasText: "保存数据集" });
  await editor.getByPlaceholder("my-customers").fill(dataset);
  await editor.getByPlaceholder("我负责的客户").fill(`我的客户 ${tag}`);
  await editor.locator("textarea").fill(`SELECT 客户 AS name, 年合同额 AS arr FROM ${table} WHERE 负责人 = :me_name ORDER BY arr DESC`);
  await editor.getByRole("button", { name: "试运行" }).click();
  await editor.getByText(":me_name").first().waitFor();
  await editor.getByText("显示 0 行，共 0 行").or(editor.getByText("没有数据")).first().waitFor();
  step("试运行：:me_name 标成自动参数（管理员自己名下没有客户，所以是空的）");
  await editor.getByRole("button", { name: "保存数据集" }).click();
  await admin.locator("li", { hasText: dataset }).waitFor();
  await shot(admin, "data-hub-dataset");
  step("保存数据集，列表里给出工作台的写法");

  // ---- 应用用这个数据集，员工在网页版里只看到自己的 ----
  const slug = `cust-app-${tag}`;
  const created = await api(admin, "POST", "/api/hub", { kind: "app", slug, delivery: "auto", spec: {
    title: `客户本 ${tag}`, description: "e2e", persona: "x", web: true,
    workbench: { panes: [{ id: "main", type: "panel", title: "客户", widgets: [
      { type: "table", title: "我负责的客户", source: { dataset }, rows: "items", columns: [{ field: "name", title: "客户" }, { field: "arr", title: "年合同额", format: "money" }] },
    ] }] },
  } });
  assertEq(created.status, 201, "用数据集的应用");
  const user = `dh-${tag}`;
  assertEq((await api(admin, "POST", "/api/admin/users", { username: user, password: "employee-password-1", display_name: owner, department: "销售部" })).status, 201, "建员工");
  const emp = await newPage(t, { viewport: { width: 390, height: 844 } });
  await login(emp, user, "employee-password-1");
  await emp.goto(`${BASE}/a/${slug}`);
  const wb = emp.locator("[data-dsh-ee-ui=workbench]");
  await wb.getByRole("cell", { name: "华东精密" }).waitFor();
  await wb.getByRole("cell", { name: "启明软件" }).waitFor();
  assert(!(await wb.getByRole("cell", { name: "瑞丰银行" }).count()), "不应看到别人负责的客户");
  await shot(emp, "data-hub-web-app");
  step("应用工作台用 {dataset} 取数：员工只看到自己负责的客户（:me_name）");

  // 清理
  await api(admin, "DELETE", `/api/hub/${created.data.asset.id}`);
  await api(admin, "DELETE", `/api/admin/data/datasets/${dataset}`);
  await api(admin, "DELETE", `/api/admin/data/tables/${table}`);
}
