// 知识库第二期：Word（自动编号、合并单元格）、Excel、PPT 上传后的转换体检；同名文件更新成新版本；
// 文档页的阅读视图（标题、表格、工作表、每页卡片）、引用摘句精确高亮、切到原文看行号。
import { ADMIN_PW, BASE, api, assert, assertEq, login, newPage, shot, step, tag } from "../lib.mjs";

// ---- 测试文件：现场生成，只存储不压缩的 zip ----

const crcTable = Array.from({ length: 256 }, (_, n) => {
  let c = n;
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  return c >>> 0;
});
function crc32(buf) {
  let c = 0xffffffff;
  for (const b of buf) c = crcTable[(c ^ b) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}
function zip(files) {
  const parts = [];
  const central = [];
  let offset = 0;
  for (const [name, text] of Object.entries(files)) {
    const data = Buffer.from(text, "utf8");
    const fname = Buffer.from(name, "utf8");
    const crc = crc32(data);
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(20, 4);
    local.writeUInt16LE(0x0800, 6); // 文件名是 UTF-8
    local.writeUInt32LE(crc, 14);
    local.writeUInt32LE(data.length, 18);
    local.writeUInt32LE(data.length, 22);
    local.writeUInt16LE(fname.length, 26);
    parts.push(local, fname, data);
    const cd = Buffer.alloc(46);
    cd.writeUInt32LE(0x02014b50, 0);
    cd.writeUInt16LE(20, 4);
    cd.writeUInt16LE(20, 6);
    cd.writeUInt16LE(0x0800, 8);
    cd.writeUInt32LE(crc, 16);
    cd.writeUInt32LE(data.length, 20);
    cd.writeUInt32LE(data.length, 24);
    cd.writeUInt16LE(fname.length, 28);
    cd.writeUInt32LE(offset, 42);
    central.push(cd, fname);
    offset += local.length + fname.length + data.length;
  }
  const cdBuf = Buffer.concat(central);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(Object.keys(files).length, 8);
  end.writeUInt16LE(Object.keys(files).length, 10);
  end.writeUInt32LE(cdBuf.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...parts, cdBuf, end]);
}

const W = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`;
function docx() {
  const p = (style, text, num = "") => `<w:p><w:pPr>${style ? `<w:pStyle w:val="${style}"/>` : ""}${num}</w:pPr><w:r><w:t>${text}</w:t></w:r></w:p>`;
  const cell = (pr, text) => `<w:tc><w:tcPr>${pr}</w:tcPr><w:p><w:r><w:t>${text}</w:t></w:r></w:p></w:tc>`;
  const body =
    p("Title", "差旅管理办法") +
    p("Heading2", "总则") +
    p("Clause", "为规范员工出差，制定本办法。") +
    p("Clause", "本办法适用于全体正式员工和实习生。") +
    p("Heading2", "住宿") +
    p("Clause", "一线城市每晚不超过 600 元，其他城市每晚不超过 400 元。") +
    p("Clause", "超出标准的部分由员工自理。") +
    `<w:tbl><w:tr>${cell("", "部门")}${cell("", "城市")}${cell("", "标准（元/晚）")}</w:tr>` +
    `<w:tr>${cell('<w:vMerge w:val="restart"/>', "销售部")}${cell("", "北京")}${cell("", "600")}</w:tr>` +
    `<w:tr>${cell("<w:vMerge/>", "")}${cell("", "上海")}${cell("", "550")}</w:tr></w:tbl>` +
    p("Heading2", "交通") +
    p("Clause", "乘坐高铁二等座；单程超过 6 小时可以乘坐飞机经济舱。");
  return zip({
    "word/document.xml": `<w:document ${W}><w:body>${body}</w:body></w:document>`,
    "word/styles.xml": `<w:styles ${W}><w:style w:styleId="Heading2"><w:name w:val="heading 2"/><w:pPr><w:numPr><w:numId w:val="1"/></w:numPr></w:pPr></w:style><w:style w:styleId="Clause"><w:name w:val="条款"/><w:pPr><w:numPr><w:numId w:val="2"/></w:numPr></w:pPr></w:style></w:styles>`,
    "word/numbering.xml": `<w:numbering ${W}><w:abstractNum w:abstractNumId="0"><w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="chineseCounting"/><w:lvlText w:val="第%1章"/><w:pStyle w:val="Heading2"/></w:lvl></w:abstractNum><w:abstractNum w:abstractNumId="1"><w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="chineseCounting"/><w:lvlText w:val="第%1条"/></w:lvl></w:abstractNum><w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num><w:num w:numId="2"><w:abstractNumId w:val="1"/></w:num></w:numbering>`,
  });
}

function xlsx() {
  const esc = (s) => String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;");
  const sheet = (rows) =>
    `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>${rows
      .map((r, i) => `<row r="${i + 1}">${r.map((v, j) => {
        const ref = String.fromCharCode(65 + j) + (i + 1);
        return typeof v === "number" ? `<c r="${ref}"><v>${v}</v></c>` : `<c r="${ref}" t="inlineStr"><is><t>${esc(v)}</t></is></c>`;
      }).join("")}</row>`)
      .join("")}</sheetData></worksheet>`;
  const price = [["产品", "地区", "单价"], ...Array.from({ length: 60 }, (_, i) => [`型号-${String(i).padStart(3, "0")}`, i % 2 ? "华东" : "华南", 100 + i * 5])];
  const phone = [["姓名", "部门", "分机"], ["王五", "财务部", 8001], ["赵六", "人事部", 8002]];
  return zip({
    "[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/xl/worksheets/sheet2.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`,
    "_rels/.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`,
    "xl/workbook.xml": `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="价目表" sheetId="1" r:id="rId1"/><sheet name="通讯录" sheetId="2" r:id="rId2"/></sheets></workbook>`,
    "xl/_rels/workbook.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet2.xml"/></Relationships>`,
    "xl/worksheets/sheet1.xml": sheet(price),
    "xl/worksheets/sheet2.xml": sheet(phone),
  });
}

function pptx() {
  const ns = `xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`;
  const sp = (ph, y, ...paras) =>
    `<p:sp><p:nvSpPr><p:cNvPr id="1" name="s"/><p:cNvSpPr/><p:nvPr>${ph ? `<p:ph type="${ph}"/>` : ""}</p:nvPr></p:nvSpPr><p:spPr><a:xfrm><a:off x="0" y="${y}"/></a:xfrm></p:spPr><p:txBody>${paras.map((t) => `<a:p><a:r><a:t>${t}</a:t></a:r></a:p>`).join("")}</p:txBody></p:sp>`;
  const slide = (...shapes) => `<p:sld ${ns}><p:cSld><p:spTree>${shapes.join("")}</p:spTree></p:cSld></p:sld>`;
  const rel = (id, target, type = "slide") => `<Relationship Id="${id}" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/${type}" Target="${target}"/>`;
  return zip({
    "ppt/presentation.xml": `<p:presentation ${ns}><p:sldIdLst><p:sldId id="256" r:id="rId2"/><p:sldId id="257" r:id="rId3"/></p:sldIdLst></p:presentation>`,
    "ppt/_rels/presentation.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">${rel("rId2", "slides/slide1.xml")}${rel("rId3", "slides/slide2.xml")}</Relationships>`,
    "ppt/slides/slide1.xml": slide(sp("title", 0, "2026 年度目标"), sp("", 2000, "营收增长 30%", "新签客户 200 家")),
    "ppt/slides/slide2.xml": slide(sp("title", 0, "季度节奏"), sp("", 2000, "Q1 招聘到位", "Q2 新产品上线")),
    "ppt/slides/_rels/slide1.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">${rel("rId9", "../notesSlides/notesSlide1.xml", "notesSlide")}</Relationships>`,
    "ppt/notesSlides/notesSlide1.xml": `<p:notes ${ns}><p:cSld><p:spTree>${sp("body", 0, "讲到这里先停一下，问大家有没有问题")}</p:spTree></p:cSld></p:notes>`,
  });
}

/** 多页 PDF：每页有页眉页脚；第 2 页是扫描页（只有一张大图）。 */
function pdf() {
  const objs = [];
  const add = (o) => objs.push(o) && objs.length;
  add("<< /Type /Catalog /Pages 2 0 R >>");
  add("");
  const font = add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>");
  const img = add("<< /Type /XObject /Subtype /Image /Width 300 /Height 300 /ColorSpace /DeviceGray /BitsPerComponent 8 /Length 0 >>\nstream\n\nendstream");
  const bodies = ["Travel policy: hotel limit is 600 per night.", null, "Flights need approval from the manager.", "Reimburse within 30 days."];
  const kids = bodies.map((text, i) => {
    let c = "";
    if (text === null) c = "q 300 0 0 300 0 0 cm /Im1 Do Q\n";
    else {
      const lines = ["Galaxy Tech Internal", text, `Section ${i + 1} details follow.`, `Page ${i + 1} of 4`];
      c = "BT /F1 12 Tf\n" + lines.map((l, k) => `1 0 0 1 72 ${750 - k * 16} Tm (${l}) Tj\n`).join("") + "ET\n";
    }
    const content = add(`<< /Length ${c.length} >>\nstream\n${c}endstream`);
    return add(`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 ${font} 0 R >> /XObject << /Im1 ${img} 0 R >> >> /Contents ${content} 0 R >>`);
  });
  objs[1] = `<< /Type /Pages /Kids [${kids.map((k) => `${k} 0 R`).join(" ")}] /Count ${kids.length} >>`;
  let out = "%PDF-1.4\n";
  const offsets = objs.map((o, i) => {
    const at = out.length;
    out += `${i + 1} 0 obj\n${o}\nendobj\n`;
    return at;
  });
  const xref = out.length;
  out += `xref\n0 ${objs.length + 1}\n0000000000 65535 f \n` + offsets.map((o) => `${String(o).padStart(10, "0")} 00000 n \n`).join("");
  out += `trailer\n<< /Size ${objs.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  return Buffer.from(out, "latin1");
}

const policy = "# 报销须知\n\n## 发票\n\n住宿发票要写明入住日期和离店日期，\n酒店发票抬头写公司全称。\n\n## 时限\n\n出差回来 30 天内提交报销。\n";

export default async function (t) {
  const admin = await newPage(t);
  await login(admin, "admin", ADMIN_PW);
  const created = await api(admin, "POST", "/api/kb", { slug: `docs-${tag}`, title: `制度样本 ${tag}`, description: "第二期格式验证" });
  assertEq(created.status, 201, "建库");
  const kbId = created.data.id;
  try {
    await run(admin, kbId);
  } finally {
    await api(admin, "DELETE", `/api/kb/${kbId}`);
  }
}

async function run(admin, kbId) {

  // ---- 上传：选文件后先看有没有同名，上传完每个文件一张体检卡 ----
  await admin.goto(`${BASE}/knowledge/${kbId}`);
  await admin.getByText("还没有文档。把 PDF、Word、Excel、PPT 等文件拖到这里").waitFor();
  await admin.getByRole("button", { name: "上传文档" }).click();
  const dialog = admin.getByRole("dialog");
  await dialog.getByText("把文件或文件夹拖到这里").waitFor();
  await dialog.locator("input[type=file][accept]").setInputFiles([
    { name: "差旅管理办法.docx", mimeType: "application/octet-stream", buffer: docx() },
    { name: "价目表.xlsx", mimeType: "application/octet-stream", buffer: xlsx() },
    { name: "年度目标.pptx", mimeType: "application/octet-stream", buffer: pptx() },
    { name: "报销须知.md", mimeType: "text/markdown", buffer: Buffer.from(policy) },
    { name: "旧制度.doc", mimeType: "application/msword", buffer: Buffer.from("x") },
    { name: "travel.pdf", mimeType: "application/pdf", buffer: pdf() },
  ]);
  await dialog.getByText("新文档").first().waitFor();
  await dialog.getByRole("button", { name: "上传 6 个文件" }).click();
  await dialog.getByText("补回 8 个条款编号 · 1 个表格").waitFor();
  await dialog.getByText("2 个工作表 · 62 行").waitFor();
  await dialog.getByText("收录 2 页幻灯片 · 1 页带备注").waitFor();
  await dialog.getByText("请在 Word 里另存为 .docx 后再上传").waitFor();
  await dialog.getByText("收录 3 页").waitFor();
  await dialog.getByText("第 2 页是扫描页，没有收录；员工问到这些页的内容会查不到").waitFor();
  await dialog.getByText("去掉了每页都重复的页眉页脚").waitFor();
  await shot(admin, "kb2-upload-report");
  step("上传 Word、Excel、PPT、PDF、Markdown 和一个旧版 .doc：每个文件一张体检卡（PDF 说清第 2 页是扫描页），.doc 告诉人怎么办");
  await dialog.getByRole("button", { name: "完成" }).click();
  await admin.getByText("差旅管理办法").first().waitFor();
  await shot(admin, "kb2-doc-list");

  // ---- 同名文件：默认更新成新版本 ----
  await admin.getByRole("button", { name: "上传文档" }).click();
  await dialog.locator("input[type=file][accept]").setInputFiles([{ name: "报销须知.md", mimeType: "text/markdown", buffer: Buffer.from(policy.replace("30 天", "45 天")) }]);
  await dialog.getByText("更新为第 2 版").waitFor();
  await dialog.getByText("更新「报销须知」，保留旧版本与引用").waitFor();
  await shot(admin, "kb2-upload-same-name");
  await dialog.getByRole("button", { name: "上传 1 个文件（其中 1 个更新）" }).click();
  await dialog.getByText("已更新为第 2 版").waitFor();
  await dialog.getByRole("button", { name: "完成" }).click();
  const detail = await api(admin, "GET", `/api/kb/${kbId}`);
  assertEq(detail.data.docs.length, 5, "同名文件不新建文档");
  step("再传一次「报销须知.md」：预检认出同名，默认更新成第 2 版，库里还是 5 篇");

  const docOf = (title) => detail.data.docs.find((d) => d.title === title);

  // ---- 阅读视图：Word 的条款编号补回来了，表格正常排版 ----
  await admin.goto(`${BASE}/knowledge/docs/${docOf("差旅管理办法").id}`);
  const reader = admin.locator('[data-dsh-ee-ui="kb-read"]');
  await reader.getByRole("heading", { name: "第二章 住宿" }).waitFor();
  await reader.getByText("第三条 一线城市每晚不超过 600 元").waitFor();
  const rows = await reader.locator("tbody tr").allInnerTexts();
  assert(rows.some((r) => r.includes("销售部") && r.includes("上海")), "纵向合并的格子应抄下上一格：" + rows.join(" / "));
  await admin.getByText("转换情况").waitFor();
  await shot(admin, "kb2-read-docx");
  step("Word 文档页：「第二章 住宿」「第三条 …」都补回来了，合并单元格每行完整，右侧是转换情况");

  // 原文视图：带行号
  await admin.getByRole("button", { name: "原文（带行号）" }).click();
  await admin.locator("[data-line] a", { hasText: /^1$/ }).first().waitFor();
  await admin.getByRole("button", { name: "阅读" }).click();

  // ---- Excel：按工作表切换，数字右对齐 ----
  await admin.goto(`${BASE}/knowledge/docs/${docOf("价目表").id}`);
  await admin.getByRole("tab", { name: "价目表" }).waitFor();
  await reader.getByText("型号-000").waitFor();
  const align = await reader.locator("tbody td").nth(2).evaluate((el) => getComputedStyle(el).textAlign);
  assertEq(align, "right", "单价一列右对齐");
  await shot(admin, "kb2-read-xlsx");
  await admin.getByRole("tab", { name: "通讯录" }).click();
  await reader.getByText("王五").waitFor();
  assert(!(await reader.getByText("型号-000").count()), "切到通讯录后不显示价目表");
  step("Excel 文档页：顶部按工作表切换，每段带表头，数字列右对齐");

  // ---- PPT：每页一张卡片 ----
  await admin.goto(`${BASE}/knowledge/docs/${docOf("年度目标").id}`);
  await reader.locator("section.kbr-slide").first().waitFor();
  assertEq(await reader.locator("section.kbr-slide").count(), 2, "两页幻灯片");
  await reader.getByText("备注：讲到这里先停一下").waitFor();
  await shot(admin, "kb2-read-pptx");
  step("PPT 文档页：每页一张卡片，备注在卡片底部");

  // ---- PDF：分页线，扫描页是提示块，页眉页脚去掉了 ----
  await admin.goto(`${BASE}/knowledge/docs/${docOf("travel").id}`);
  await reader.getByRole("separator").first().waitFor();
  assertEq(await reader.getByRole("separator").count(), 4, "四页四条分页线");
  await reader.getByText("第 2 页是扫描页，没有收录文字，请看原件。").waitFor();
  assert(!(await reader.getByText("Galaxy Tech Internal").count()), "页眉应去掉");
  await shot(admin, "kb2-read-pdf");
  step("PDF 文档页：每页一条分页线，扫描页显示成灰色提示，页眉页脚去掉了");
  await admin.goto(`${BASE}/knowledge/${kbId}`);
  await admin.getByText("1 页未收录").waitFor();
  step("文档列表上这篇 PDF 标着「1 页未收录」");

  // ---- 引用：摘句精确标出来，滚到中间 ----
  const md = docOf("报销须知");
  await admin.goto(`${BASE}/knowledge/docs/${md.id}?q=${encodeURIComponent("入住日期和离店日期，酒店发票抬头")}#L5-6`);
  const mark = reader.locator("mark.kbr-mark");
  await mark.first().waitFor();
  const marked = (await mark.allInnerTexts()).join("");
  // 跨行时行尾的标点不标，两行各标一段
  assertEq(marked, "入住日期和离店日期酒店发票抬头", "摘句跨行精确高亮");
  assertEq(await reader.locator(".kbr-hit").count(), 2, "引用的两行左边有竖线");
  await shot(admin, "kb2-read-citation");
  step("从出处链接打开（?q=摘句#L5-6）：两行左边一条竖线，摘句跨行精确标黄");

  // 暗色（跟主题切换一样写 localStorage 再刷新）
  await admin.evaluate(() => localStorage.setItem("dsh-ee:theme", "dark"));
  await admin.reload();
  await mark.first().waitFor();
  await shot(admin, "kb2-read-citation-dark");
  await admin.evaluate(() => localStorage.removeItem("dsh-ee:theme"));
}
