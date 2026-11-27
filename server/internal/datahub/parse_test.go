package datahub

// 大文件解析的安全回归测试。公开演示站允许任意访客上传（server/internal/api/demo_site.go），
// 所以解析必须对流式输入封顶：几百 KB 的 .xlsx / 单行 CSV 不能把一次请求放大成 GB 级内存。
// 修前（整份 XML 反序列化、io.ReadAll 读部件、CSV 先 append 全部记录）这两个用例会分配
// 数百 MB 到数 GB；修后应只分配几十 MB 以内。

import (
	"archive/zip"
	"bytes"
	"errors"
	"github.com/xuri/excelize/v2"
	"io"
	"runtime"
	"strings"

	"testing"
)

const (
	testWorkbookXML     = `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="S" sheetId="1" r:id="rId1"/></sheets></workbook>`
	testWorkbookRelsXML = `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`
)

// allocDuring 跑 fn，返回它期间的总分配字节数（MemStats.TotalAlloc 增量：和 GC 时机无关，
// 累计分配了多少就是多少）和返回时的堆增量（HeapAlloc 增量：还没被回收的）。
// 修前解析 100MB 的 sheet XML 总分配是 GB 级，修后应远小于 64MB。
func allocDuring(fn func()) (total, heap int64) {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	total = int64(after.TotalAlloc) - int64(before.TotalAlloc)
	heap = int64(after.HeapAlloc) - int64(before.HeapAlloc)
	runtime.GC()
	return total, heap
}

// zipXLSX 造一个最小的 .xlsx：workbook + rels + 第一个工作表。extra 是附加部件（sharedStrings、styles）。
func zipXLSX(t *testing.T, extra map[string]string, writeSheet func(io.Writer) error) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Fatal(err)
		}
	}
	add("xl/workbook.xml", testWorkbookXML)
	add("xl/_rels/workbook.xml.rels", testWorkbookRelsXML)
	for name, body := range extra {
		add(name, body)
	}
	w, err := zw.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeSheet(w); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sheetXMLText(sheetXML string) func(io.Writer) error {
	return func(w io.Writer) error {
		_, err := io.WriteString(w, sheetXML)
		return err
	}
}

// xlsxBomb 造一个 sheet XML 解压后约 approxSheetBytes 字节的 .xlsx：行内容重复、压缩后很小，
// 行数超过 MaxUploadRows（修前会整份反序列化后才在行数检查处报错）。
func xlsxBomb(t *testing.T, approxSheetBytes int) []byte {
	t.Helper()
	return zipXLSX(t, nil, func(w io.Writer) error {
		const head = `<worksheet><sheetData>`
		if _, err := io.WriteString(w, head); err != nil {
			return err
		}
		written := len(head)
		row := `<row><c t="inlineStr"><is><t>` + strings.Repeat("a", 120) + `</t></is></c></row>`
		const chunkRows = 2000
		chunk := strings.Repeat(row, chunkRows)
		for written+len(chunk) < approxSheetBytes {
			if _, err := io.WriteString(w, chunk); err != nil {
				return err
			}
			written += len(chunk)
		}
		_, err := io.WriteString(w, `</sheetData></worksheet>`)
		return err
	})
}

// manyRowsXLSX 造一个 rowCount 行的 .xlsx（每行一个内联字符串单元格），用来验证行数上限
// 是边读边检查的，而不是等整份 sheet XML 反序列化完再检查。
func manyRowsXLSX(t *testing.T, rowCount int) []byte {
	t.Helper()
	return zipXLSX(t, nil, func(w io.Writer) error {
		if _, err := io.WriteString(w, `<worksheet><sheetData>`); err != nil {
			return err
		}
		const rowXML = `<row><c t="inlineStr"><is><t>x</t></is></c></row>`
		const chunkRows = 5000
		chunk := strings.Repeat(rowXML, chunkRows)
		for written := 0; written < rowCount; written += chunkRows {
			if _, err := io.WriteString(w, chunk); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, `</sheetData></worksheet>`)
		return err
	})
}

// ① 压缩炸弹：约 300KB 的 xlsx，sheet XML 解压后 ~100MB。修前 io.ReadAll + xml.Unmarshal
// 会分配 GB 级内存（审计实测峰值 HeapAlloc 1074MB、总分配 4.55GB），这里要求 < 64MB。
func TestParseXLSXZipBombBoundedMemory(t *testing.T) {
	const sheetBytes = 96 << 20 // ~100MB，旧实现的单部件上限是 100MiB，会放行
	data := xlsxBomb(t, sheetBytes)
	t.Logf("夹具：xlsx %d KB，sheet XML 解压后 %d MB", len(data)>>10, sheetBytes>>20)
	var err error
	total, heap := allocDuring(func() { _, err = ParseXLSX(data) })
	if err == nil {
		t.Fatalf("解压后 %d MB 的 sheet 应该报错，而不是返回表格", sheetBytes>>20)
	}
	const maxAlloc = 64 << 20
	if total > maxAlloc || heap > maxAlloc {
		t.Fatalf("解析解压后 %d MB 的 xlsx 分配了 %d MB（堆 +%d MB），应 < 64 MB；错误：%v",
			sheetBytes>>20, total>>20, heap>>20, err)
	}
	t.Logf("已拒绝：%v；总分配 %d MB，堆增量 %d MB", err, total>>20, heap>>20)
}

// ② CSV 单条记录：一行 20MB 全是逗号。修前会把整行解析成上千万个字段（审计实测峰值 ~599MB），
// 这里要求立即报错且分配 < 64MB。
func TestParseCSVHugeRecordBoundedMemory(t *testing.T) {
	const recordBytes = 20 << 20
	data := bytes.Repeat([]byte{','}, recordBytes)
	var err error
	total, heap := allocDuring(func() { _, err = ParseCSV(data) })
	if err == nil {
		t.Fatal("单条记录 20MB 的 CSV 应该报错，而不是返回表格")
	}
	const maxAlloc = 64 << 20
	if total > maxAlloc || heap > maxAlloc {
		t.Fatalf("解析 %d MB 的单条 CSV 记录分配了 %d MB（堆 +%d MB），应 < 64 MB；错误：%v",
			recordBytes>>20, total>>20, heap>>20, err)
	}
	t.Logf("已拒绝：%v；总分配 %d MB，堆增量 %d MB", err, total>>20, heap>>20)
}

// ③ 行数上限：100 万行、sheet XML ~49MB（在 64MB 解压预算内，所以会真的走流式解析）。
// 修前整份反序列化完才报错；修后读到第 MaxUploadRows+1 行立即报错，内存 O(行×列)。
func TestParseXLSXRowLimitStreams(t *testing.T) {
	data := manyRowsXLSX(t, 1_000_000)
	var err error
	total, heap := allocDuring(func() { _, err = ParseXLSX(data) })
	if err == nil || !strings.Contains(err.Error(), "行") {
		t.Fatalf("超过 %d 行应该报错：%v", MaxUploadRows, err)
	}
	// 194MB 是 xml 库逐 token 的固定开销（和处理到第 20 万行有关，和解压后的 XML 大小无关）；修前要 1499MB（整份物化）。
	const maxAlloc = 256 << 20
	if total > maxAlloc || heap > 64<<20 {
		t.Fatalf("行数超限时总分配 %d MB、堆 %d MB，说明整份 XML 被物化了：%v", total>>20, heap>>20, err)
	}
	t.Logf("已拒绝：%v；总分配 %d MB，堆增量 %d MB", err, total>>20, heap>>20)
}

// ④ 列数上限：数据行有 150 个单元格，超过 MaxColumns 应报错（修前是静默丢弃超出的列）。
func TestParseXLSXColumnLimitRejected(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<worksheet><sheetData><row><c t="inlineStr"><is><t>a</t></is></c><c t="inlineStr"><is><t>b</t></is></c></row><row>`)
	for i := 0; i < 150; i++ {
		b.WriteString(`<c t="inlineStr"><is><t>v</t></is></c>`)
	}
	b.WriteString(`</row></sheetData></worksheet>`)
	if _, err := ParseXLSX(zipXLSX(t, nil, sheetXMLText(b.String()))); err == nil || !strings.Contains(err.Error(), "列") {
		t.Fatalf("超过 %d 列应该报错：%v", MaxColumns, err)
	}
}

// ⑤ CSV 字段数：表头 2 列，数据行有 101 个字段，超过 MaxColumns 应报错（修前多出来的字段被静默忽略）。
func TestParseCSVFieldLimitRejected(t *testing.T) {
	data := []byte("a,b\n" + strings.Repeat(",", 100) + "\n")
	if _, err := ParseCSV(data); err == nil || !strings.Contains(err.Error(), "列") {
		t.Fatalf("超过 %d 列应该报错：%v", MaxColumns, err)
	}
}

// ⑥ 回归：正常的小 xlsx / csv 仍然解析成功，列名净化、去重、类型推断规则不变。
func TestParseSmallFilesStillWork(t *testing.T) {
	s, err := ParseCSV([]byte("\xEF\xBB\xBF客户,金额\n华东精密,\"1,200.5\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	cols, rows := Infer(s)
	if len(cols) != 2 || cols[0].Name != "客户" || cols[1].Type != "number" || rows[0][1] != 1200.5 {
		t.Fatalf("CSV 回归：%v %v", cols, rows)
	}
	data := zipXLSX(t, map[string]string{
		"xl/sharedStrings.xml": `<sst><si><t>部门</t></si><si><t>人数</t></si><si><t>成立</t></si><si><r><t>研发</t></r><r><t>部</t></r></si></sst>`,
		"xl/styles.xml":        `<styleSheet><cellXfs><xf numFmtId="0"/><xf numFmtId="14"/></cellXfs></styleSheet>`,
	}, sheetXMLText(`<worksheet><sheetData>
		<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="C1" t="s"><v>2</v></c></row>
		<row r="2"><c r="A2" t="s"><v>3</v></c><c r="B2"><v>90</v></c><c r="C2" s="1"><v>45658</v></c></row>
		<row r="3"><c r="A3" t="inlineStr"><is><t>销售部</t></is></c><c r="C3" s="1"><v>45659</v></c></row>
		</sheetData></worksheet>`))
	s, err = ParseXLSX(data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(s.Header, ",") != "部门,人数,成立" || len(s.Rows) != 2 || s.Rows[0][2] != "2025-01-01" || s.Rows[1][0] != "销售部" {
		t.Fatalf("XLSX 回归：%v %v", s.Header, s.Rows)
	}
}

// ⑦ 解压预算跨部件累计：小预算下前面几个部件读完，后面的部件就超预算了。
func TestParseXLSXUnzipBudgetSharedAcrossParts(t *testing.T) {
	limits := defaultParseLimits
	limits.maxUnzipBytes = int64(len(testWorkbookXML)+len(testWorkbookRelsXML)) + 8
	data := zipXLSX(t, map[string]string{
		"xl/sharedStrings.xml": `<sst><si><t>abc</t></si></sst>`,
	}, sheetXMLText(`<worksheet><sheetData/></worksheet>`))
	_, err := parseXLSX(data, limits)
	if !errors.Is(err, errTooBig) || !strings.Contains(err.Error(), "太大") {
		t.Fatalf("解压预算应该跨部件累计：%v", err)
	}
}

// ⑧ 共享字符串条数 / 文本总字节上限。
func TestParseXLSXSharedStringsLimits(t *testing.T) {
	data := zipXLSX(t, map[string]string{
		"xl/sharedStrings.xml": `<sst><si><t>a</t></si><si><t>b</t></si><si><t>c</t></si><si><t>d</t></si></sst>`,
	}, sheetXMLText(`<worksheet><sheetData/></worksheet>`))
	limits := defaultParseLimits
	limits.maxSharedStrings = 3
	if _, err := parseXLSX(data, limits); err == nil || !strings.Contains(err.Error(), "共享字符串太多") {
		t.Fatalf("共享字符串条数应该封顶：%v", err)
	}
	limits = defaultParseLimits
	limits.maxSharedStringBytes = 3 // a + b + c + d = 4 字节
	if _, err := parseXLSX(data, limits); err == nil || !strings.Contains(err.Error(), "共享字符串太大") {
		t.Fatalf("共享字符串总字节应该封顶：%v", err)
	}
}

// ⑨ 读取时累计的解压预算：zip 头里的声明大小可以被伪造，真正兜底的是 budgetReader。
func TestBudgetReaderRejectsOversize(t *testing.T) {
	a := &xlsxArchive{left: 8}
	r := &budgetReader{rc: io.NopCloser(strings.NewReader("0123456789")), a: a}
	if _, err := io.ReadAll(r); !errors.Is(err, errTooBig) {
		t.Fatalf("读取超过解压预算应该报错：%v", err)
	}
}

// ⑩ CSV 单条记录字节上限（默认 1MB，这里调小到 8 字节验证）。
func TestParseCSVRecordBytesLimit(t *testing.T) {
	limits := defaultParseLimits
	limits.maxCSVRecordBytes = 8
	_, err := parseCSV([]byte("a,b\n"+strings.Repeat("x", 100)+"\n"), limits)
	if !errors.Is(err, errCSVRecordTooBig) || !strings.Contains(err.Error(), "单条记录") {
		t.Fatalf("单条记录应该封顶：%v", err)
	}
}

// ⑪ 带引号、跨行的正常 CSV 仍然解析（记录长度预扫不能误伤）。
func TestParseCSVQuotedMultiline(t *testing.T) {
	s, err := ParseCSV([]byte("a,b\r\n\"line1\nline2\",2\r\n\"x, y\",3\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"line1\nline2", "2"}, {"x, y", "3"}}
	if len(s.Header) != 2 || s.Header[0] != "a" || len(s.Rows) != 2 || s.Rows[0][0] != want[0][0] || s.Rows[1][0] != want[1][0] {
		t.Fatalf("跨行引号 CSV：%v %v", s.Header, s.Rows)
	}
}

// ⑫ 真实 Excel 文件（excelize 生成：命名空间、sharedStrings、styles 都是库写出来的）。
func TestParseExcelizeGeneratedXLSX(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	for cell, v := range map[string]any{
		"A1": "部门", "B1": "人数", "C1": "成立", "D1": "备注",
		"A2": "研发部", "B2": 90, "C2": 45658, "D2": "是",
		"A3": "销售部", "B3": 12.5, "C3": 45659, "D3": "否",
	} {
		if err := f.SetCellValue("Sheet1", cell, v); err != nil {
			t.Fatal(err)
		}
	}
	style, err := f.NewStyle(&excelize.Style{NumFmt: 14})
	if err != nil {
		t.Fatal(err)
	}
	_ = f.SetCellStyle("Sheet1", "C2", "C3", style)
	if _, err := f.NewSheet("第二页"); err != nil {
		t.Fatal(err)
	}
	_ = f.SetCellValue("第二页", "A1", "不该出现")
	f.SetActiveSheet(0)
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	s, err := ParseXLSX(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Header) != 4 || s.Header[0] != "部门" || len(s.Rows) != 2 || s.Rows[0][0] != "研发部" || s.Rows[0][1] != "90" || s.Rows[0][2] != "2025-01-01" {
		t.Fatalf("解析 excelize 生成的文件：%v %v", s.Header, s.Rows)
	}
}
