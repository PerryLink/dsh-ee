package datahub

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// 上传的表格：第一行是列名，其余是数据。支持 CSV（UTF-8）和 Excel（.xlsx 的第一个工作表）。
//
// 公开演示站允许任意访客上传（api/demo_site.go 没关数据中台上传），所以这里必须假设输入是恶意的：
//   - .xlsx 是 zip，解压后可以放大几百倍：所有部件共用一个解压字节预算，超了立即报错（防 zip 炸弹）；
//   - 工作表 XML 用 xml.Decoder.Token 流式读，行数 / 列数超限立即报错，内存 O(行×列) 而不是 O(XML)；
//   - 共享字符串同样流式读，并限条数和文本总字节；
//   - CSV 每读一条记录就检查字段数和单条记录字节数，不再先把整份文件 append 成 [][]string 再检查。

const (
	MaxColumns    = 100
	MaxUploadRows = 200000
	maxCell       = 4000
)

// parseLimits 是一次解析的资源上限。默认值按「正常表格够用、恶意输入打不爆内存」取；
// 测试里会调小这些值来验证边界，不用造几百 MB 的夹具。
type parseLimits struct {
	// maxUnzipBytes 是 .xlsx 所有部件解压后的合计字节上限。
	maxUnzipBytes int64
	// maxMetaBytes 是 workbook.xml / rels / styles.xml 这类要整段读进内存的小部件单个上限。
	maxMetaBytes int64
	// maxSharedStrings / maxSharedStringBytes 是 xl/sharedStrings.xml 的条数和文本总字节上限。
	maxSharedStrings     int
	maxSharedStringBytes int64
	// maxCSVRecordBytes 是 CSV 单条记录（含引号里的换行）的字节上限。
	maxCSVRecordBytes int64
}

var defaultParseLimits = parseLimits{
	maxUnzipBytes:        64 << 20,
	maxMetaBytes:         16 << 20,
	maxSharedStrings:     1_000_000,
	maxSharedStringBytes: 64 << 20,
	maxCSVRecordBytes:    1 << 20,
}

var (
	// errTooBig 是各种「表格太大」错误的哨兵，上层（api）原样展示 message 即可。
	errTooBig = errors.New("表格太大")
	// errCSVRecordTooBig 单独一个，方便 ParseCSV 区分「单条记录太长」和 CSV 格式错误。
	errCSVRecordTooBig = errors.New("CSV 单条记录太长")
)

// Sheet 是解析出来的表格：列名与每行的文字。
type Sheet struct {
	Header []string
	Rows   [][]string
}

// ParseCSV 读 UTF-8 的 CSV（带不带 BOM 都行）。
func ParseCSV(data []byte) (*Sheet, error) { return parseCSV(data, defaultParseLimits) }

func parseCSV(data []byte, l parseLimits) (*Sheet, error) {
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	if !utf8.Valid(data) {
		return nil, errors.New("CSV 不是 UTF-8 编码：请在 Excel 里另存为「CSV UTF-8」，或者直接上传 .xlsx")
	}
	// 单条记录字节上限：csv.Reader 会先把整条记录读进内存再返回，所以要在它下面垫一层计数 reader。
	// 先按 csv 规则扫一遍记录长度：超大记录在分配内存之前就拒绝。
	if err := checkCSVRecords(data, l.maxCSVRecordBytes); err != nil {
		return nil, err
	}
	limited := &recordLimitReader{r: bytes.NewReader(data), max: l.maxCSVRecordBytes}
	r := csv.NewReader(limited)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	var all [][]string
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			if errors.Is(err, errCSVRecordTooBig) {
				return nil, err
			}
			return nil, fmt.Errorf("CSV 格式不对：%w", err)
		}
		// 字段数要一读完就查，不能先 append 全部记录、最后才在 toSheet 里查表头列数。
		if len(rec) > MaxColumns {
			return nil, fmt.Errorf("最多 %d 列", MaxColumns)
		}
		all = append(all, rec)
		if len(all) > MaxUploadRows+1 {
			return nil, fmt.Errorf("最多 %d 行", MaxUploadRows)
		}
		limited.reset()
	}
	return toSheet(all)
}

// recordLimitReader 是 csv.Reader 底下的一层：一条记录累计读超过 max 字节就报错，
// 免得一行 20MB 的 CSV 被解析成上千万个字段、先分配几百 MB 再报错。
// 读完一条记录后调用 reset。
type recordLimitReader struct {
	r   io.Reader
	max int64
	n   int64
}

func (r *recordLimitReader) reset() { r.n = 0 }

func (r *recordLimitReader) Read(p []byte) (int, error) {
	// 允许多读 1 字节来判断「刚好到上限」还是「还没完」。
	remain := r.max + 1 - r.n
	if remain <= 0 {
		return 0, fmt.Errorf("CSV 单条记录超过 %d KB：%w", r.max>>10, errCSVRecordTooBig)
	}
	if int64(len(p)) > remain {
		p = p[:remain]
	}
	n, err := r.r.Read(p)
	r.n += int64(n)
	if r.n > r.max {
		return 0, fmt.Errorf("CSV 单条记录超过 %d KB：%w", r.max>>10, errCSVRecordTooBig)
	}
	return n, err
}

// checkCSVRecords 先在原始字节上按 csv.Reader 的规则（LazyQuotes）扫一遍记录长度。
// csv.Reader 会先把整条记录读进内存再返回，一行 20MB 的 CSV 会被解析成上千万个字段、
// 分配几百 MB 之后才轮到报错；先扫一遍就能在解析前直接拒绝。这里只需要「宁松勿紧」：
// 判断比解析宽松没关系，真正的兜底是 recordLimitReader。
func checkCSVRecords(data []byte, max int64) error {
	tooBig := func() error {
		return fmt.Errorf("CSV 单条记录超过 %d KB：%w", max>>10, errCSVRecordTooBig)
	}
	var record int64
	atFieldStart := true
	inQuotes := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		record++
		if record > max {
			return tooBig()
		}
		if inQuotes {
			if c != '"' {
				continue
			}
			// 双引号是转义；引号后跟 , / 换行是字段结束；LazyQuotes 下其它情况当普通字符。
			if i+1 < len(data) && data[i+1] == '"' {
				i++
				record++
				if record > max {
					return tooBig()
				}
				continue
			}
			if i+1 >= len(data) {
				inQuotes = false
				continue
			}
			switch data[i+1] {
			case ',', '\n', '\r':
				inQuotes = false
			}
			continue
		}
		switch c {
		case '"':
			if atFieldStart {
				inQuotes = true
			}
			atFieldStart = false
		case ',':
			atFieldStart = true
		case '\n':
			record = 0
			atFieldStart = true
		case '\r':
			// \r\n 里的 \r；记录结束由后面的 \n 处理。
		default:
			atFieldStart = false
		}
	}
	return nil
}

// ParseXLSX 读 .xlsx 的第一个工作表：共享字符串、内联字符串、数字、布尔；日期按 Excel 序号转换（只认常见的日期格式）。
func ParseXLSX(data []byte) (*Sheet, error) { return parseXLSX(data, defaultParseLimits) }

func parseXLSX(data []byte, l parseLimits) (*Sheet, error) {
	a, err := newXLSXArchive(data, l)
	if err != nil {
		return nil, err
	}
	sheetPath, err := firstSheet(a)
	if err != nil {
		return nil, err
	}
	shared, err := sharedStrings(a)
	if err != nil {
		return nil, err
	}
	dateStyles, err := dateStyleIndexes(a)
	if err != nil {
		return nil, err
	}
	rows, err := readSheet(a, sheetPath, shared, dateStyles)
	if err != nil {
		return nil, err
	}
	return toSheet(rows)
}

// xlsxArchive 是一次解析的 zip 访问器：所有部件共用一个解压字节预算。
type xlsxArchive struct {
	files map[string]*zip.File
	left  int64 // 还能解压多少字节
	l     parseLimits
}

func newXLSXArchive(data []byte, l parseLimits) (*xlsxArchive, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("不是有效的 .xlsx 文件")
	}
	files := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		files[f.Name] = f
	}
	return &xlsxArchive{files: files, left: l.maxUnzipBytes, l: l}, nil
}

func (a *xlsxArchive) budgetErr() error {
	return fmt.Errorf("表格解压后太大（所有部件解压合计超过 %d MB，可能是压缩炸弹）：%w", a.l.maxUnzipBytes>>20, errTooBig)
}

// openPart 打开一个 zip 部件。maxPart > 0 时限制单个部件大小（workbook.xml 这类要整段读进内存的）；
// maxPart == 0 表示流式读取、只受总预算限制。先看 zip 头里声明的大小做快速拒绝，
// 真正防伪造的是 budgetReader 在读取时累计的字节数。
func (a *xlsxArchive) openPart(name string, maxPart int64) (io.ReadCloser, error) {
	f := a.files[name]
	if f == nil {
		return nil, nil
	}
	declared := f.UncompressedSize64
	if declared > uint64(a.l.maxUnzipBytes) {
		return nil, a.budgetErr()
	}
	if maxPart > 0 && declared > uint64(maxPart) {
		return nil, fmt.Errorf("表格解压后太大（%s 超过 %d MB）：%w", name, maxPart>>20, errTooBig)
	}
	if declared > uint64(a.left) {
		return nil, a.budgetErr()
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("表格里的 %s 读不了：%w", name, err)
	}
	return &budgetReader{rc: rc, a: a}, nil
}

// readMeta 把一个小部件整段读进内存（workbook.xml / rels / styles.xml 要按属性解析）。
func (a *xlsxArchive) readMeta(name string) ([]byte, error) {
	rc, err := a.openPart(name, a.l.maxMetaBytes)
	if err != nil || rc == nil {
		return nil, err
	}
	defer rc.Close()
	raw, err := io.ReadAll(rc)
	if err != nil {
		if errors.Is(err, errTooBig) {
			return nil, err
		}
		return nil, fmt.Errorf("表格里的 %s 读不了：%w", name, err)
	}
	return raw, nil
}

// openStream 打开要流式解析的部件（共享字符串、工作表），只受总预算限制。
func (a *xlsxArchive) openStream(name string) (io.ReadCloser, error) {
	return a.openPart(name, 0)
}

// budgetReader 读取时累计解压预算：超了立即报错，不依赖 zip 头里声明的大小（那个可以伪造）。
type budgetReader struct {
	rc io.ReadCloser
	a  *xlsxArchive
}

func (r *budgetReader) Read(p []byte) (int, error) {
	if r.a.left < 0 {
		return 0, r.a.budgetErr()
	}
	if r.a.left == 0 {
		// 预算正好用完：多读 1 字节判断是刚好结束还是还有内容。
		var one [1]byte
		n, err := r.rc.Read(one[:])
		if n > 0 {
			return 0, r.a.budgetErr()
		}
		return 0, err
	}
	if int64(len(p)) > r.a.left {
		p = p[:r.a.left]
	}
	n, err := r.rc.Read(p)
	r.a.left -= int64(n)
	return n, err
}

func (r *budgetReader) Close() error { return r.rc.Close() }

// sheetErr 统一包装工作表解析错误；预算错误原样往上抛，上层能看到「解压后太大」。
func sheetErr(err error) error {
	if errors.Is(err, errTooBig) {
		return err
	}
	return fmt.Errorf("工作表格式不对：%w", err)
}

func firstSheet(a *xlsxArchive) (string, error) {
	wb, err := a.readMeta("xl/workbook.xml")
	if err != nil {
		return "", err
	}
	if wb == nil {
		return "", errors.New("不是有效的 .xlsx 文件（缺 workbook.xml）")
	}
	var w struct {
		Sheets []struct {
			RID string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal(wb, &w); err != nil || len(w.Sheets) == 0 {
		return "", errors.New("表格里没有工作表")
	}
	rels, err := a.readMeta("xl/_rels/workbook.xml.rels")
	if err != nil {
		return "", err
	}
	var r struct {
		Items []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	_ = xml.Unmarshal(rels, &r)
	for _, it := range r.Items {
		if it.ID == w.Sheets[0].RID {
			t := strings.TrimPrefix(it.Target, "/")
			if !strings.HasPrefix(t, "xl/") {
				t = "xl/" + t
			}
			return t, nil
		}
	}
	return "xl/worksheets/sheet1.xml", nil
}

// sharedStrings 流式读共享字符串：每读一条就查条数和文本总字节，不把整份 XML 反序列化。
func sharedStrings(a *xlsxArchive) ([]string, error) {
	rc, err := a.openStream("xl/sharedStrings.xml")
	if err != nil || rc == nil {
		return nil, err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	var out []string
	var total int64
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			if errors.Is(err, errTooBig) {
				return nil, err
			}
			return nil, fmt.Errorf("共享字符串格式不对：%w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "si" {
			continue
		}
		if len(out) >= a.l.maxSharedStrings {
			return nil, fmt.Errorf("共享字符串太多（最多 %d 条）", a.l.maxSharedStrings)
		}
		var item struct {
			Text string `xml:"t"`
			Runs []struct {
				Text string `xml:"t"`
			} `xml:"r"`
		}
		if err := dec.DecodeElement(&item, &se); err != nil {
			if errors.Is(err, errTooBig) {
				return nil, err
			}
			return nil, fmt.Errorf("共享字符串格式不对：%w", err)
		}
		s := item.Text
		for _, run := range item.Runs {
			s += run.Text
		}
		total += int64(len(s))
		if total > a.l.maxSharedStringBytes {
			return nil, fmt.Errorf("共享字符串太大（合计超过 %d MB）", a.l.maxSharedStringBytes>>20)
		}
		out = append(out, s)
	}
}

// dateStyleIndexes 找出单元格样式里哪些是日期格式（内置的 14～22、45～47，或自定义格式里有 y / d）。
func dateStyleIndexes(a *xlsxArchive) (map[int]bool, error) {
	raw, err := a.readMeta("xl/styles.xml")
	if err != nil || raw == nil {
		return map[int]bool{}, err
	}
	var st struct {
		NumFmts []struct {
			ID   int    `xml:"numFmtId,attr"`
			Code string `xml:"formatCode,attr"`
		} `xml:"numFmts>numFmt"`
		Xfs []struct {
			NumFmt int `xml:"numFmtId,attr"`
		} `xml:"cellXfs>xf"`
	}
	if err := xml.Unmarshal(raw, &st); err != nil {
		return map[int]bool{}, nil
	}
	custom := map[int]bool{}
	for _, f := range st.NumFmts {
		code := strings.ToLower(f.Code)
		custom[f.ID] = (strings.Contains(code, "y") || strings.Contains(code, "d")) && !strings.Contains(code, "[h]")
	}
	out := map[int]bool{}
	for i, x := range st.Xfs {
		id := x.NumFmt
		out[i] = (id >= 14 && id <= 22) || (id >= 45 && id <= 47) || custom[id]
	}
	return out, nil
}

// readSheet 流式读第一个工作表：边读边填 rows，行数 / 列数超限立即报错。
// 内存是 O(行×列)，和 sheet XML 有多大无关。
func readSheet(a *xlsxArchive, path string, shared []string, dateStyles map[int]bool) ([][]string, error) {
	rc, err := a.openStream(path)
	if err != nil {
		return nil, err
	}
	if rc == nil {
		return nil, errors.New("读不到第一个工作表")
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	var rows [][]string
	inSheetData := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return rows, nil
		}
		if err != nil {
			return nil, sheetErr(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "sheetData":
				inSheetData = true
			case "row":
				if !inSheetData {
					continue
				}
				// 和 CSV 一样：header + MaxUploadRows 行数据，再多一行就报错。
				if len(rows) >= MaxUploadRows+1 {
					return nil, fmt.Errorf("最多 %d 行", MaxUploadRows)
				}
				row, err := readSheetRow(dec, shared, dateStyles)
				if err != nil {
					return nil, err
				}
				rows = append(rows, row)
			}
		case xml.EndElement:
			if t.Name.Local == "sheetData" {
				inSheetData = false
			}
		}
	}
}

// readSheetRow 读 <row> 到 </row>：每读完一个 <c> 就检查列号，超过 MaxColumns 立即报错。
func readSheetRow(dec *xml.Decoder, shared []string, dateStyles map[int]bool) ([]string, error) {
	var cells []string
	seq := 0 // 没有 r 属性的单元格按出现顺序算列号
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, sheetErr(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local != "c" {
				continue
			}
			value, ref, err := readCell(dec, t, shared, dateStyles)
			if err != nil {
				return nil, err
			}
			col := seq
			if ref != "" {
				if n := colIndex(ref); n >= 0 {
					col = n
				}
			}
			seq++
			if col >= MaxColumns {
				return nil, fmt.Errorf("最多 %d 列", MaxColumns)
			}
			for len(cells) <= col {
				cells = append(cells, "")
			}
			cells[col] = value
		case xml.EndElement:
			if t.Name.Local == "row" {
				return cells, nil
			}
		}
	}
}

// readCell 读一个 <c> 到 </c>：<v> 是值，<is> 里的 <t>（含 <r> 分片）是内联字符串；
// 公式 <f> 之类的其它子元素丢掉不看。返回值和 ref（单元格坐标，可能为空）。
func readCell(dec *xml.Decoder, start xml.StartElement, shared []string, dateStyles map[int]bool) (value, ref string, err error) {
	typ := ""
	style := 0
	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "r":
			ref = attr.Value
		case "t":
			typ = attr.Value
		case "s":
			style, _ = strconv.Atoi(attr.Value)
		}
	}
	var raw, inline []byte
	inV, inIs, inT := false, false, false
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", ref, sheetErr(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "v":
				inV = true
			case "is":
				inIs = true
			case "t":
				if inIs {
					inT = true
				}
			}
		case xml.CharData:
			if inV {
				raw = append(raw, t...)
			} else if inT {
				inline = append(inline, t...)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inV = false
			case "t":
				inT = false
			case "is":
				inIs = false
			case "c":
				switch typ {
				case "s":
					n, _ := strconv.Atoi(string(raw))
					if n >= 0 && n < len(shared) {
						return shared[n], ref, nil
					}
					return "", ref, nil
				case "inlineStr":
					return string(inline), ref, nil
				case "b":
					switch string(raw) {
					case "1":
						return "TRUE", ref, nil
					case "0":
						return "FALSE", ref, nil
					}
					return "", ref, nil
				default:
					v := string(raw)
					if dateStyles[style] {
						if f, e := strconv.ParseFloat(v, 64); e == nil {
							return excelDate(f), ref, nil
						}
					}
					return v, ref, nil
				}
			}
		}
	}
}

func excelDate(f float64) string {
	// Excel 的 1900 日期系统：1899-12-30 是 0
	t := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).Add(time.Duration(math.Round(f*86400)) * time.Second)
	if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 {
		return t.Format("2006-01-02")
	}
	return t.Format("2006-01-02 15:04:05")
}

func colIndex(ref string) int {
	n := 0
	for _, r := range ref {
		if r < 'A' || r > 'Z' {
			break
		}
		n = n*26 + int(r-'A'+1)
	}
	return n - 1
}

// toSheet 去掉空行，规范列名（去空白、去反引号、去重、空的补成「列N」）。
func toSheet(all [][]string) (*Sheet, error) {
	var rows [][]string
	for _, r := range all {
		empty := true
		for _, c := range r {
			if strings.TrimSpace(c) != "" {
				empty = false
				break
			}
		}
		if !empty {
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		return nil, errors.New("表格是空的")
	}
	header := rows[0]
	if len(header) > MaxColumns {
		return nil, fmt.Errorf("最多 %d 列", MaxColumns)
	}
	seen := map[string]bool{}
	names := make([]string, len(header))
	for i, h := range header {
		h = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(h, "`", ""), "\n", " "))
		if utf8.RuneCountInString(h) > 60 {
			h = string([]rune(h)[:60])
		}
		if h == "" || strings.HasPrefix(h, "_") {
			h = fmt.Sprintf("列%d", i+1)
		}
		base := h
		for k := 2; seen[strings.ToLower(h)]; k++ {
			h = fmt.Sprintf("%s_%d", base, k)
		}
		seen[strings.ToLower(h)] = true
		names[i] = h
	}
	out := &Sheet{Header: names}
	for _, r := range rows[1:] {
		line := make([]string, len(names))
		for i := range names {
			if i < len(r) {
				v := strings.TrimSpace(r[i])
				if utf8.RuneCountInString(v) > maxCell {
					v = string([]rune(v)[:maxCell])
				}
				line[i] = v
			}
		}
		out.Rows = append(out.Rows, line)
	}
	return out, nil
}

var (
	numberRe = regexp.MustCompile(`^[-+]?(\d{1,3}(,\d{3})+|\d+)(\.\d+)?$`)
	dateRe   = regexp.MustCompile(`^(\d{4})[-/.](\d{1,2})[-/.](\d{1,2})$`)
)

// numeric 判断一个单元格能不能当数值存。长得像数字、其实是编号的不行：number 列是 DOUBLE，
// 前导零会丢（工号 00123 → 123），超过 15 位有效数字的末几位会被改写（18 位身份证号、银行卡号、订单号）。
func numeric(v string) bool {
	if !numberRe.MatchString(v) {
		return false
	}
	digits := strings.TrimLeft(v, "+-")
	if len(digits) > 1 && digits[0] == '0' && digits[1] != '.' {
		return false
	}
	n := 0
	for _, c := range digits {
		if c >= '0' && c <= '9' {
			n++
		}
	}
	return n <= 15
}

// Infer 按内容推断每列的类型：全是数值的是 number，全是日期的是 date，其余是 text；空单元格不影响判断。
// 一列里只要有一个值是编号（见 numeric），整列按 text 原样保存。
// 返回列定义和转换好的行（number 是 float64，date 是 2006-01-02，空是 nil）。
func Infer(s *Sheet) ([]Column, [][]any) {
	cols := make([]Column, len(s.Header))
	for i, name := range s.Header {
		number, date, any := true, true, false
		for _, r := range s.Rows {
			v := r[i]
			if v == "" {
				continue
			}
			any = true
			if number && !numeric(v) {
				number = false
			}
			if date && !dateRe.MatchString(v) {
				date = false
			}
		}
		t := "text"
		switch {
		case !any:
		case number:
			t = "number"
		case date:
			t = "date"
		}
		cols[i] = Column{Name: name, Type: t}
	}
	rows := make([][]any, len(s.Rows))
	for ri, r := range s.Rows {
		line := make([]any, len(cols))
		for i, c := range cols {
			v := r[i]
			if v == "" {
				continue
			}
			switch c.Type {
			case "number":
				f, _ := strconv.ParseFloat(strings.ReplaceAll(v, ",", ""), 64)
				line[i] = f
			case "date":
				m := dateRe.FindStringSubmatch(v)
				y, _ := strconv.Atoi(m[1])
				mo, _ := strconv.Atoi(m[2])
				d, _ := strconv.Atoi(m[3])
				line[i] = fmt.Sprintf("%04d-%02d-%02d", y, mo, d)
			default:
				line[i] = v
			}
		}
		rows[ri] = line
	}
	return cols, rows
}

// Align 把新上传的表按列名对齐到已有的列（追加时用）：列名要一样（顺序可以不同），值按已有的类型转换。
func Align(existing []Column, s *Sheet) ([][]any, error) {
	idx := map[string]int{}
	for i, h := range s.Header {
		idx[h] = i
	}
	if len(s.Header) != len(existing) {
		return nil, fmt.Errorf("列数不一样：原表 %d 列，这次 %d 列", len(existing), len(s.Header))
	}
	for _, c := range existing {
		if _, ok := idx[c.Name]; !ok {
			return nil, fmt.Errorf("这次的表格里没有「%s」这一列；追加时列名要和原表一样", c.Name)
		}
	}
	rows := make([][]any, len(s.Rows))
	for ri, r := range s.Rows {
		line := make([]any, len(existing))
		for i, c := range existing {
			v := r[idx[c.Name]]
			if v == "" {
				continue
			}
			switch c.Type {
			case "number":
				if !numberRe.MatchString(v) {
					return nil, fmt.Errorf("第 %d 行「%s」应该是数字：%s", ri+2, c.Name, v)
				}
				if !numeric(v) {
					return nil, fmt.Errorf("第 %d 行「%s」的 %s 是编号（有前导零或超过 15 位），按数值存会被改写；请用「替换」重新上传整张表，这一列会按文本保存", ri+2, c.Name, v)
				}
				f, _ := strconv.ParseFloat(strings.ReplaceAll(v, ",", ""), 64)
				line[i] = f
			case "date":
				m := dateRe.FindStringSubmatch(v)
				if m == nil {
					return nil, fmt.Errorf("第 %d 行「%s」应该是日期：%s", ri+2, c.Name, v)
				}
				y, _ := strconv.Atoi(m[1])
				mo, _ := strconv.Atoi(m[2])
				d, _ := strconv.Atoi(m[3])
				line[i] = fmt.Sprintf("%04d-%02d-%02d", y, mo, d)
			default:
				line[i] = v
			}
		}
		rows[ri] = line
	}
	return rows, nil
}
