package webui

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"mime"
	"strings"
)

// InlineScriptHashes 返回嵌入的 index.html 里内联脚本的 CSP sha256 源（形如 "sha256-…"）。
//
// 前端由 Vite 构建，index.html 的首屏主题脚本是内联的；启用 CSP 的页面必须把它的哈希
// 写进 script-src，不能退回 'unsafe-inline'。哈希按「实际嵌入、实际下发的那份 HTML」计算：
// webui.Handler 下发的就是同一份字节，构建产物变了哈希自动跟着变，不会出现硬编码失效。
//
// 没有嵌入前端（只编译了接口）时返回 nil：这时 Handler 返回纯文本 503，不是 HTML，
// 安全头中间件也不会给它加 CSP。
func InlineScriptHashes() []string {
	index, ok := IndexHTML()
	if !ok {
		return nil
	}
	return inlineScriptHashes(index)
}

// inlineScriptHashes 按 HTML 分词规则提取可执行内联脚本的 CSP sha256 源。
//
// 只处理执行型 <script>：跳过带 src 的（外链脚本由 'self' 覆盖）和 type 不是 JavaScript
// 的数据块（application/json、importmap 等）。脚本内容是 raw text，浏览器不对其实体解码，
// 遇到第一个后面跟空白、/ 或 > 的 </script 才结束；哈希因此按原始字节算，与浏览器一致。
func inlineScriptHashes(html []byte) []string {
	var hashes []string
	seen := make(map[string]bool)
	for i := 0; i < len(html); {
		lt := bytes.IndexByte(html[i:], '<')
		if lt < 0 {
			break
		}
		start := i + lt
		rest := html[start:]

		// 注释里的 <script> 是注释文本，不是元素。
		if hasPrefixFold(rest, "<!--") {
			end := bytes.Index(rest[4:], []byte("-->"))
			if end < 0 {
				break
			}
			i = start + 4 + end + 3
			continue
		}
		if !hasPrefixFold(rest, "<script") {
			i = start + 1
			continue
		}
		// <script 后面必须是属性区/标签结束：<scriptfoo> 不是 script 元素。
		nameEnd := start + len("<script")
		if nameEnd < len(html) && !isHTMLSpace(html[nameEnd]) && html[nameEnd] != '/' && html[nameEnd] != '>' {
			i = start + 1
			continue
		}
		tag, next, ok := scanStartTag(html, nameEnd)
		if !ok {
			break
		}
		// 不管带不带 src、是不是数据块，都要跳过元素内容，免得把内容里的 "<script>" 当元素。
		content, after, ok := rawTextUntilEndTag(html, next, "script")
		if !ok {
			break
		}
		i = after
		if tag.has("src") || !isExecutableScriptType(tag.attrOrEmpty("type")) {
			continue
		}
		sum := sha256.Sum256(content)
		source := "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
		if !seen[source] {
			seen[source] = true
			hashes = append(hashes, source)
		}
	}
	return hashes
}

// rawTextUntilEndTag 从 raw text 元素的开始标签之后找 "</name"（后面跟空白、/ 或 >）。
// 返回原始内容（不做实体解码）与结束标签之后的下标；找不到结束标签时按浏览器行为取到文末。
func rawTextUntilEndTag(html []byte, from int, name string) ([]byte, int, bool) {
	needle := "</" + name
	for i := from; i+len(needle) <= len(html); i++ {
		if html[i] != '<' || !hasPrefixFold(html[i:], needle) {
			continue
		}
		after := i + len(needle)
		// </scriptx> 不是结束标签；</script >、</script/>、</script> 都是。
		if after < len(html) && !isHTMLSpace(html[after]) && html[after] != '/' && html[after] != '>' {
			continue
		}
		gt := bytes.IndexByte(html[after:], '>')
		if gt < 0 {
			break
		}
		return html[from:i], after + gt + 1, true
	}
	// HTML 的 raw text 元素没有结束标签时，剩余内容都算脚本内容。
	return html[from:], len(html), true
}

// scanStartTag 从标签名之后解析属性区，返回属性（键小写；重复属性按 HTML 规则保留第一个）
// 和标签结束之后的下标。引号里的 > 不算标签结束。
func scanStartTag(html []byte, i int) (startTag, int, bool) {
	attrs := make(map[string]string, 4)
	for {
		for i < len(html) && isHTMLSpace(html[i]) {
			i++
		}
		if i >= len(html) {
			return startTag{}, 0, false
		}
		switch html[i] {
		case '>':
			return startTag{attrs: attrs}, i + 1, true
		case '/':
			// 自闭合的 "/>"；属性区里的孤立 / 按 HTML 规则忽略。
			if i+1 < len(html) && html[i+1] == '>' {
				return startTag{attrs: attrs}, i + 2, true
			}
			i++
			continue
		}

		nameStart := i
		for i < len(html) && !isHTMLSpace(html[i]) && html[i] != '=' && html[i] != '>' && html[i] != '/' {
			i++
		}
		name := strings.ToLower(string(html[nameStart:i]))
		if name == "" {
			i++
			continue
		}
		for i < len(html) && isHTMLSpace(html[i]) {
			i++
		}
		value := ""
		if i < len(html) && html[i] == '=' {
			i++
			for i < len(html) && isHTMLSpace(html[i]) {
				i++
			}
			if i >= len(html) {
				return startTag{}, 0, false
			}
			if quote := html[i]; quote == '"' || quote == '\'' {
				i++
				valueStart := i
				for i < len(html) && html[i] != quote {
					i++
				}
				value = string(html[valueStart:i])
				if i < len(html) {
					i++ // 跳过结束引号
				}
			} else {
				valueStart := i
				for i < len(html) && !isHTMLSpace(html[i]) && html[i] != '>' {
					i++
				}
				value = string(html[valueStart:i])
			}
		}
		if _, exists := attrs[name]; !exists {
			attrs[name] = value
		}
	}
}

type startTag struct {
	attrs map[string]string
}

func (t startTag) has(name string) bool {
	_, ok := t.attrs[name]
	return ok
}

func (t startTag) attrOrEmpty(name string) string { return t.attrs[name] }

// isExecutableScriptType 判断 type 属性是否属于浏览器会当作脚本执行的类型：
// 缺省 / 空串 / module / JavaScript MIME 是；JSON、importmap、模板等数据块不是。
func isExecutableScriptType(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(mediaType) {
	case "module",
		"text/javascript",
		"application/javascript",
		"application/ecmascript",
		"application/x-javascript",
		"text/ecmascript",
		"text/jscript",
		"text/livescript",
		"text/x-javascript":
		return true
	}
	return false
}

func isHTMLSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\f':
		return true
	}
	return false
}

func hasPrefixFold(b []byte, prefix string) bool {
	if len(b) < len(prefix) {
		return false
	}
	return strings.EqualFold(string(b[:len(prefix)]), prefix)
}
