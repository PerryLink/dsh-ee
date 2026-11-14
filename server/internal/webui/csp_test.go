package webui

import (
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hashOf(script string) string {
	sum := sha256.Sum256([]byte(script))
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

func TestInlineScriptHashesClassicScript(t *testing.T) {
	script := `(function () { document.documentElement.classList.add("dark") })();`
	html := "<!doctype html><html><head><script>" + script + "</script></head></html>"
	got := inlineScriptHashes([]byte(html))
	if len(got) != 1 || got[0] != hashOf(script) {
		t.Fatalf("内联脚本哈希 = %v，希望 %q", got, hashOf(script))
	}
}

// 只有执行型内联脚本需要哈希：外链脚本由 'self' 覆盖，JSON/importmap 等数据块不执行。
func TestInlineScriptHashesSkipsNonExecutable(t *testing.T) {
	html := `<!doctype html><html><head>
<script src="/assets/index-abc123.js"></script>
<script type="application/json">{"a":1}</script>
<script type="application/ld+json">{}</script>
<script type="importmap">{"imports":{}}</script>
<script type="text/template"><p>模板</p></script>
<script type="text/javascript">window.ok = 1</script>
<script type="module">window.mod = 1</script>
</head></html>`
	got := inlineScriptHashes([]byte(html))
	want := []string{hashOf("window.ok = 1"), hashOf("window.mod = 1")}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("哈希 = %v，希望 %v", got, want)
	}
}

// HTML raw text：属性引号里的 >、内容里的 </scriptx>、注释里的 <script> 都不能干扰提取。
func TestInlineScriptHashesHTMLTokenization(t *testing.T) {
	script := `if (a > b) { console.log("</scriptx>") }`
	html := `<!doctype html><html><head>
<!-- <script>不该被算进去</script> -->
<SCRIPT data-note="a > b">` + script + `</SCRIPT >
<script>var s = "<script>嵌套文本</script>"</script>
</head></html>`
	got := inlineScriptHashes([]byte(html))
	want := []string{hashOf(script), hashOf(`var s = "<script>嵌套文本`)}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("哈希 = %v，希望 %v", got, want)
	}
}

func TestInlineScriptHashesDedupesIdenticalScripts(t *testing.T) {
	html := `<script>var a = 1</script><script>var a = 1</script><script>var b = 2</script>`
	got := inlineScriptHashes([]byte(html))
	if len(got) != 2 || got[0] != hashOf("var a = 1") || got[1] != hashOf("var b = 2") {
		t.Fatalf("哈希 = %v", got)
	}
}

func TestInlineScriptHashesNoScripts(t *testing.T) {
	for _, html := range []string{
		"",
		"<html><body>没有脚本</body></html>",
		`<script src="/index.js"></script>`,
	} {
		if got := inlineScriptHashes([]byte(html)); len(got) != 0 {
			t.Fatalf("%q 的内联脚本哈希 = %v，希望为空", html, got)
		}
	}
}

// 内联脚本内容是 HTML raw text：浏览器不解码实体，哈希必须按原始字节算。
func TestInlineScriptHashesDoesNotDecodeEntities(t *testing.T) {
	html := `<script>if (a &amp;&amp; b) {}</script>`
	got := inlineScriptHashes([]byte(html))
	if len(got) != 1 || got[0] != hashOf("if (a &amp;&amp; b) {}") {
		t.Fatalf("哈希 = %v", got)
	}
}

// 真实的入口 HTML 至少有一个内联脚本（首屏主题），并且能被可靠提取。
// 构建产物（web/dist/index.html）由 Vite 生成自同一份源码，CI 的 server job 只有源码，
// 因此这里校验源码；运行时哈希按实际嵌入的构建产物计算。
func TestRepoIndexHTMLHasExtractableInlineScript(t *testing.T) {
	path := filepath.Join("..", "..", "..", "web", "index.html")
	html, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("没有前端的 checkout（%v），跳过", err)
	}
	text := string(html)
	start := strings.Index(text, "<script>")
	if start < 0 {
		t.Fatal("web/index.html 里没有经典内联脚本：主题首屏脚本不能被 CSP 的 script-src 拦掉")
	}
	contentStart := start + len("<script>")
	end := strings.Index(text[contentStart:], "</script>")
	if end < 0 {
		t.Fatal("内联脚本没有结束标签")
	}
	script := text[contentStart : contentStart+end]
	if !strings.Contains(script, "dsh-ee:theme") {
		t.Fatalf("内联脚本不像首屏主题脚本：%q", script)
	}
	got := inlineScriptHashes(html)
	if len(got) != 1 || got[0] != hashOf(script) {
		t.Fatalf("哈希 = %v，希望 %q", got, hashOf(script))
	}
}

// InlineScriptHashes 读嵌入的构建产物；没嵌入前端（CI 的 server job）时为空。
func TestInlineScriptHashesFromEmbeddedBuild(t *testing.T) {
	index, ok := IndexHTML()
	if !ok {
		t.Skip("没有嵌入前端构建产物（make embed），跳过")
	}
	hashes := InlineScriptHashes()
	if len(hashes) != len(inlineScriptHashes(index)) {
		t.Fatalf("嵌入产物哈希 = %v", hashes)
	}
	for _, h := range hashes {
		if !strings.HasPrefix(h, "sha256-") {
			t.Fatalf("CSP 源表达式应以 sha256- 开头：%q", h)
		}
	}
}
