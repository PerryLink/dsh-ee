package sdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// MCP 的服务端（Streamable HTTP，只回 JSON，不开事件流）：initialize / ping / tools/list / tools/call。
// 工具的参数定义按 Params 生成：只有模型填的参数出现在里面。

var mcpVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26"}

// MetaError 是工具结果的 _meta 里放错误码的键。
const MetaError = "dsh-ee/error"

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func rpcReply(w http.ResponseWriter, id json.RawMessage, result any, code int, msg string) {
	resp := map[string]any{"jsonrpc": "2.0", "id": id}
	if msg != "" {
		resp["error"] = map[string]any{"code": code, "message": msg}
	} else {
		resp["result"] = result
	}
	writeJSON(w, http.StatusOK, resp)
}

// InputSchema 是给模型看的参数定义。
func (t *Tool) InputSchema() map[string]any {
	props := map[string]any{}
	required := []string{}
	for _, p := range t.Params {
		if p.Fill != "" && p.Fill != "model" {
			continue
		}
		s := map[string]any{}
		if p.Type != "" {
			s["type"] = p.Type
		}
		if d := p.Description.any(); d != "" {
			s["description"] = d
		}
		if len(p.Enum) > 0 {
			s["enum"] = p.Enum
		}
		props[p.Name] = s
		if p.Required {
			required = append(required, p.Name)
		}
	}
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func textResult(text string, isError bool, code string) map[string]any {
	out := map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
	if isError {
		out["isError"] = true
	}
	if code != "" {
		out["_meta"] = map[string]any{MetaError: map[string]string{"code": code}}
	}
	return out
}

func (e *Extension) handleMCP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
	case http.MethodDelete:
		w.WriteHeader(http.StatusOK)
		return
	default:
		w.Header().Set("Allow", "POST, DELETE")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	c, ok := e.call(w, r)
	if !ok {
		return
	}
	var msg rpcMsg
	if !readBody(w, r, &msg) {
		return
	}
	if msg.Method == "" {
		rpcReply(w, json.RawMessage("null"), nil, -32700, "不是合法的 JSON-RPC 请求")
		return
	}
	if len(msg.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	switch msg.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		version := mcpVersions[0]
		for _, v := range mcpVersions {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		rpcReply(w, msg.ID, map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": e.m.ID, "version": e.m.Version},
			"instructions":    e.m.Description.any(),
		}, 0, "")
	case "ping":
		rpcReply(w, msg.ID, map[string]any{}, 0, "")
	case "tools/list":
		tools := []map[string]any{}
		for _, t := range e.tools {
			tools = append(tools, map[string]any{
				"name": t.Name, "description": t.Description.any(), "inputSchema": t.InputSchema(),
				"annotations": map[string]any{"readOnlyHint": t.ReadOnly},
			})
		}
		rpcReply(w, msg.ID, map[string]any{"tools": tools}, 0, "")
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			rpcReply(w, msg.ID, nil, -32602, "参数格式不对")
			return
		}
		var tool *Tool
		for _, t := range e.tools {
			if t.Name == p.Name {
				tool = t
			}
		}
		if tool == nil {
			rpcReply(w, msg.ID, nil, -32602, "没有这个工具："+p.Name)
			return
		}
		if p.Arguments == nil {
			p.Arguments = map[string]any{}
		}
		// 必填的参数缺了直接报，不进处理函数
		for _, prm := range tool.Params {
			if _, has := p.Arguments[prm.Name]; prm.Required && !has {
				rpcReply(w, msg.ID, textResult(fmt.Sprintf("缺少参数 %s", prm.Name), true, CodeInvalid), 0, "")
				return
			}
		}
		c.Args = p.Arguments
		out, err := tool.Handler(r.Context(), c)
		if err != nil {
			code := CodeInternal
			var ee *Error
			if errors.As(err, &ee) {
				code = ee.Code
			}
			rpcReply(w, msg.ID, textResult(err.Error(), true, code), 0, "")
			return
		}
		text, isText := out.(string)
		if !isText {
			raw, err := json.Marshal(out)
			if err != nil {
				rpcReply(w, msg.ID, textResult("结果编不成 JSON："+err.Error(), true, CodeInternal), 0, "")
				return
			}
			text = string(raw)
		}
		rpcReply(w, msg.ID, textResult(text, false, ""), 0, "")
	case "resources/list":
		rpcReply(w, msg.ID, map[string]any{"resources": []any{}}, 0, "")
	case "prompts/list":
		rpcReply(w, msg.ID, map[string]any{"prompts": []any{}}, 0, "")
	default:
		rpcReply(w, msg.ID, nil, -32601, "不支持的方法："+msg.Method)
	}
}
