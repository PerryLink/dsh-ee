package api

// /mcp 的 tools/list 分页：给第三方 Harness 按页取工具，同时不改变 DSH 接入插件的行为。
//
// MCP 规范里 tools/list 用 cursor/nextCursor 翻页，但 DSH 接入插件一次 tools/list 就拿全量工具、
// 不跟 nextCursor（dsh-plugin/src/bridge.ts 的 sync）。为了让现有 DSH 客户端行为完全不变：
//   - 带 X-DshEE-Client: dsh-connect/… 的调用方，没给 cursor/limit 时照旧返回全量；
//   - 其它调用方（第三方 Harness）没给参数时按默认页大小分页，返回 nextCursor；
//   - 任何调用方显式给 cursor 或 limit 都按分页处理，limit=0 表示不分页（拿全量）。
//
// 分页顺序按工具名稳定排序；cursor 是不透明的 base64url，里面带工具清单摘要，翻页途中清单变了
// 会让旧 cursor 失效，避免悄悄漏掉或重复工具。

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	// mcpToolsPageSize 是分页时一页的默认工具数。第三方 Harness 可以用 limit 覆盖。
	mcpToolsPageSize = 100
	// mcpToolsPageMax 是一页最多返回的工具数，避免一次翻页把上下文拉爆。
	mcpToolsPageMax = 500
	// mcpToolsCursorVersion 是 cursor 的格式版本；格式变了旧 cursor 一律作废。
	mcpToolsCursorVersion = 1
)

var (
	errMCPToolsCursorInvalid = errors.New("cursor 无效：请原样使用上一页返回的 nextCursor")
	errMCPToolsListChanged   = errors.New("工具清单在翻页途中变了，请不带 cursor 从头开始")
)

// mcpToolsListParams 是 tools/list 接受的参数。cursor 是 MCP 规范字段；limit 是 dsh-ee 的扩展，
// 第三方用它在第一页开启分页（limit=0 表示不分页）。
type mcpToolsListParams struct {
	Cursor string `json:"cursor"`
	Limit  *int   `json:"limit"`
}

func parseMCPToolsListParams(raw json.RawMessage) (mcpToolsListParams, error) {
	var p mcpToolsListParams
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return p, nil
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, errors.New("参数格式不对：cursor 应是字符串，limit 应是整数")
	}
	if p.Limit != nil && *p.Limit < 0 {
		return p, errors.New("limit 不能是负数；不分页请传 limit=0")
	}
	return p, nil
}

// mcpToolsCursor 是 nextCursor 解码后的内容。调用方不该解析它，只该原样回传。
type mcpToolsCursor struct {
	// V 是 cursor 格式版本
	V int `json:"v"`
	// O 是下一页在稳定排序后的工具清单里的起始下标
	O int `json:"o"`
	// D 是清单摘要（工具名 sha256 前 16 个十六进制字符），清单变了 cursor 就作废
	D string `json:"d"`
}

func encodeMCPToolsCursor(offset int, digest string) string {
	b, _ := json.Marshal(mcpToolsCursor{V: mcpToolsCursorVersion, O: offset, D: digest})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeMCPToolsCursor(cursor string) (mcpToolsCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return mcpToolsCursor{}, errMCPToolsCursorInvalid
	}
	var c mcpToolsCursor
	if err := json.Unmarshal(b, &c); err != nil || c.V != mcpToolsCursorVersion || c.O < 0 || c.D == "" {
		return mcpToolsCursor{}, errMCPToolsCursorInvalid
	}
	return c, nil
}

func mcpToolDefName(t map[string]any) string {
	name, _ := t["name"].(string)
	return name
}

// mcpToolsDigest 对稳定排序后的工具名算摘要，用来发现翻页途中清单变化。
func mcpToolsDigest(tools []map[string]any) string {
	h := sha256.New()
	for _, t := range tools {
		fmt.Fprintf(h, "%s\n", mcpToolDefName(t))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// mcpToolsListPage 算 tools/list 的结果。tools 是当前主体的完整工具清单，rawParams 是请求的 params；
// asDSH 表示调用方是 DSH 接入插件（X-DshEE-Client: dsh-connect/…），它不跟 nextCursor。
//
// 返回的 page 是这一页的工具（稳定排序），next 为空表示没有下一页。
func mcpToolsListPage(tools []map[string]any, rawParams json.RawMessage, asDSH bool) (page []map[string]any, next string, err error) {
	p, err := parseMCPToolsListParams(rawParams)
	if err != nil {
		return nil, "", err
	}
	// DSH 插件一次要全量；显式给了 cursor/limit 才分页。
	if p.Cursor == "" && p.Limit == nil && asDSH {
		return tools, "", nil
	}
	ordered := make([]map[string]any, len(tools))
	copy(ordered, tools)
	sort.SliceStable(ordered, func(i, j int) bool { return mcpToolDefName(ordered[i]) < mcpToolDefName(ordered[j]) })
	digest := mcpToolsDigest(ordered)
	offset := 0
	if p.Cursor != "" {
		c, err := decodeMCPToolsCursor(p.Cursor)
		if err != nil {
			return nil, "", err
		}
		if c.D != digest {
			return nil, "", errMCPToolsListChanged
		}
		if c.O > len(ordered) {
			return nil, "", errMCPToolsCursorInvalid
		}
		offset = c.O
	}
	size := mcpToolsPageSize
	if p.Limit != nil {
		size = *p.Limit
	}
	if size == 0 {
		// limit=0：明确不要分页，把剩下的全给出去。
		return ordered[offset:], "", nil
	}
	if size > mcpToolsPageMax {
		size = mcpToolsPageMax
	}
	if offset >= len(ordered) {
		return []map[string]any{}, "", nil
	}
	end := offset + size
	if end >= len(ordered) {
		return ordered[offset:], "", nil
	}
	return ordered[offset:end], encodeMCPToolsCursor(end, digest), nil
}
