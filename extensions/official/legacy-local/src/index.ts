#!/usr/bin/env node
// 本机 MCP 服务的入口：Agent 的 MCP 配置里写 { "command": "npx", "args": ["@acme/legacy-oa-local"] }
import extension from "./extension.js";

extension.serveStdio();
