#!/usr/bin/env node
/**
 * dsh-ee-local：写端侧扩展时用的命令。
 *
 *   dsh-ee-local manifest <入口> [-o dsh-ee.extension.json]   按注册的工具生成清单（之后用 dsh-ee extension check 检查）
 *   dsh-ee-local tools <入口>                                 列出工具和参数
 *   dsh-ee-local call <入口> <工具> ['{"参数":"值"}']          调一次工具（写操作照样要经 dsh-ee 确认）
 *
 * 入口是 default export 一个 defineExtension(...) 结果的模块，例如 ./dist/extension.js。
 */
import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { Extension } from "./index.js";

const usage = `用法：
  dsh-ee-local manifest <入口> [-o 文件]
  dsh-ee-local tools <入口>
  dsh-ee-local call <入口> <工具> [参数 JSON]
`;

async function load(entry: string): Promise<Extension> {
  const mod = await import(pathToFileURL(path.resolve(entry)).href);
  const ext = mod.default ?? mod.extension;
  if (!(ext instanceof Extension) && !(ext && typeof ext.callTool === "function" && typeof ext.manifest === "function")) {
    throw new Error(`${entry} 要 export default 一个 defineExtension(...) 的结果`);
  }
  return ext as Extension;
}

export async function main(argv: string[], out: (s: string) => void = (s) => process.stdout.write(s)): Promise<number> {
  const [cmd, entry, ...rest] = argv;
  if (!cmd || !entry || cmd === "help") {
    out(usage);
    return cmd === "help" ? 0 : 2;
  }
  const ext = await load(entry);
  switch (cmd) {
    case "manifest": {
      const text = JSON.stringify(ext.manifest(), null, 2) + "\n";
      const o = rest.indexOf("-o");
      if (o >= 0 && rest[o + 1]) fs.writeFileSync(rest[o + 1], text);
      else out(text);
      return 0;
    }
    case "tools":
      out(JSON.stringify(ext.listTools(), null, 2) + "\n");
      return 0;
    case "call": {
      const [tool, raw] = rest;
      if (!tool) {
        out(usage);
        return 2;
      }
      let args: Record<string, unknown> = {};
      if (raw) {
        try {
          args = JSON.parse(raw);
        } catch {
          out("参数要是 JSON，例如 '{\"days\":2}'\n");
          return 2;
        }
      }
      const res = await ext.callTool(tool, args);
      out(JSON.stringify(res, null, 2) + "\n");
      return res.isError ? 1 : 0;
    }
  }
  out(usage);
  return 2;
}
