/**
 * @dsh-ee/extension-local：端侧扩展 SDK（docs/plugins.md §8、§10）。
 *
 * 端侧扩展是跑在员工电脑上的扩展：一个 npm 包，起一个本机的 MCP 服务（stdio），Agent（DSH、Claude Code、Codex）
 * 直接调它，它用员工自己的登录态访问只能在内网、只能用员工身份的老系统。dsh-ee 服务端不运行它。
 *
 * SDK 负责：
 *   - MCP 服务端（stdio，按行收发 JSON-RPC）：initialize / ping / tools/list / tools/call；
 *   - 没标 readOnly 的工具执行前先问 dsh-ee 要员工确认，确认不了就不执行（漏标只会多确认一次）；
 *   - 每次调用后把摘要回报给 dsh-ee 记审计（不含返回内容）；
 *   - 清单（dsh-ee.extension.json）按注册的工具生成；
 *   - webSignIn：在单独的浏览器窗口里让员工登录，取走要的 cookie（见 ./web-signin）。
 *
 *   import { defineExtension } from "@dsh-ee/extension-local";
 *   const ext = defineExtension({ id: "acme/legacy-oa", version: "0.1.0", name: "老 OA", package: "@acme/legacy-oa-local", tools: [...] });
 *   ext.serveStdio();
 */
import { createInterface } from "node:readline";
import { Codes, ExtError, codeOf } from "./errors.js";
import { EEClient, type EEOptions } from "./ee.js";

export { Codes, ExtError, codeOf } from "./errors.js";
export { EEClient, type EEOptions, type ConfirmAnswer } from "./ee.js";
export { webSignIn, CookieStore, cookieHeader, type WebSignInOptions, type StoredCookie } from "./web-signin.js";

/** 给人看的文字：字符串，或中英两种。 */
export type Text = string | { zh?: string; en?: string };

export interface Param {
  name: string;
  type?: "string" | "number" | "integer" | "boolean" | "array" | "object";
  description?: Text;
  required?: boolean;
  enum?: (string | number)[];
}

export interface CallContext {
  /** 和 dsh-ee 打交道的客户端（一般用不到） */
  ee: EEClient;
  /** 这次调用的超时信号 */
  signal: AbortSignal;
}

/** 处理函数可以返回字符串、任意 JSON 值（会被序列化成文本），或完整的 MCP 结果。 */
export type ToolResult = string | number | boolean | null | Record<string, unknown> | unknown[] | { content: { type: string; text?: string }[]; isError?: boolean };

export interface ToolDef {
  name: string;
  description: Text;
  /** 只读的才标 true；没标的一律当写操作，执行前要员工确认（docs/plugins.md §5.2、§7.5） */
  readOnly?: boolean;
  params?: Param[];
  handler: (args: Record<string, unknown>, ctx: CallContext) => Promise<ToolResult> | ToolResult;
}

export interface ExtensionDef {
  /** 作者/名字，例如 acme/legacy-oa */
  id: string;
  version: string;
  name: Text;
  description?: Text;
  /** npm 包名 */
  package: string;
  /** 兼容的最低 dsh-ee 版本 */
  minVersion?: string;
  tools: ToolDef[];
  /** 一次调用最长多久，默认 60 秒 */
  timeoutMs?: number;
  /** 默认从环境变量 DSH_EE_URL / DSH_EE_TOKEN / DSH_EE_HARNESS 取 */
  ee?: EEOptions | EEClient;
}

export interface McpResult {
  content: { type: string; text?: string }[];
  isError?: boolean;
  _meta?: Record<string, unknown>;
}

const ID_RE = /^[a-z0-9][a-z0-9._-]{0,40}\/[a-z0-9][a-z0-9._-]{0,60}$/;
const TOOL_RE = /^[A-Za-z0-9_.-]{1,64}$/;
const MCP_VERSIONS = ["2025-11-25", "2025-06-18", "2025-03-26"];
/** 工具结果 _meta 里放错误码的键，和 Go SDK 相同 */
export const META_ERROR = "dsh-ee/error";

function textOf(t: Text | undefined, lang = "zh"): string {
  if (!t) return "";
  if (typeof t === "string") return t;
  return (lang === "en" ? t.en || t.zh : t.zh || t.en) ?? "";
}

function result(text: string, isError = false, code?: string): McpResult {
  const out: McpResult = { content: [{ type: "text", text }] };
  if (isError) out.isError = true;
  if (code) out._meta = { [META_ERROR]: { code } };
  return out;
}

export class Extension {
  readonly ee: EEClient;
  private readonly tools = new Map<string, ToolDef>();

  constructor(readonly def: ExtensionDef) {
    if (!ID_RE.test(def.id)) throw new Error(`扩展 id ${JSON.stringify(def.id)} 不对：要是「作者/名字」，小写字母、数字、. _ -`);
    for (const t of def.tools) {
      if (!TOOL_RE.test(t.name)) throw new Error(`工具名 ${JSON.stringify(t.name)} 不对`);
      if (this.tools.has(t.name)) throw new Error(`工具 ${t.name} 重复了`);
      this.tools.set(t.name, t);
    }
    this.ee = def.ee instanceof EEClient ? def.ee : new EEClient(def.ee);
  }

  /** 给模型看的参数定义。 */
  inputSchema(t: ToolDef) {
    const properties: Record<string, unknown> = {};
    const required: string[] = [];
    for (const p of t.params ?? []) {
      properties[p.name] = { ...(p.type ? { type: p.type } : {}), ...(p.description ? { description: textOf(p.description) } : {}), ...(p.enum ? { enum: p.enum } : {}) };
      if (p.required) required.push(p.name);
    }
    return { type: "object", properties, ...(required.length ? { required } : {}) };
  }

  /** 清单 dsh-ee.extension.json（docs/plugins.md §5.2）。用 `dsh-ee-local manifest` 写出来，再用 dsh-ee extension check 检查。 */
  manifest() {
    const d = this.def;
    return {
      id: d.id,
      version: d.version,
      ...(d.minVersion ? { minVersion: d.minVersion } : {}),
      name: d.name,
      ...(d.description ? { description: d.description } : {}),
      kind: "local",
      package: d.package,
      permissions: { approval: true, audit: true },
      tools: d.tools.map((t) => ({
        name: t.name,
        description: t.description,
        ...(t.readOnly ? { readOnly: true } : {}),
        ...(t.params?.length ? { params: t.params.map((p) => ({ ...p, fill: "model" })) } : {}),
      })),
    };
  }

  listTools() {
    return [...this.tools.values()].map((t) => ({
      name: t.name,
      description: textOf(t.description),
      inputSchema: this.inputSchema(t),
      annotations: { readOnlyHint: !!t.readOnly },
    }));
  }

  /** 调一个工具：写操作先问 dsh-ee，执行完回报摘要。 */
  async callTool(name: string, args: Record<string, unknown> = {}): Promise<McpResult> {
    const t = this.tools.get(name);
    if (!t) return result(`没有工具 ${name}`, true, Codes.not_found);
    const started = Date.now();
    let ok = false;
    let code: string | undefined;
    try {
      if (!t.readOnly) {
        if (!this.ee.configured) {
          code = Codes.setup;
          return result("这个工具会修改数据，要先经 dsh-ee 由员工确认，但没有配置 DSH_EE_URL 和 DSH_EE_TOKEN，所以没有执行。请让员工在 dsh-ee「我的令牌」里建一个令牌并配置好。", true, code);
        }
        let answer;
        try {
          answer = await this.ee.confirm(this.def.id, this.def.version, t.name, args);
        } catch (e) {
          code = Codes.unavailable;
          return result(`要先经 dsh-ee 确认，但现在连不上 dsh-ee（${e instanceof Error ? e.message : String(e)}），所以没有执行。`, true, code);
        }
        if (!answer.approved) {
          code = Codes.confirm;
          return result(answer.message ?? "要先由员工在 dsh-ee 网页上确认，现在还没有执行。", true, code);
        }
      }
      const ctrl = new AbortController();
      const timer = setTimeout(() => ctrl.abort(new ExtError(Codes.unavailable, "超时")), this.def.timeoutMs ?? 60_000);
      try {
        const out = await Promise.race([
          Promise.resolve(t.handler(args, { ee: this.ee, signal: ctrl.signal })),
          new Promise<never>((_, reject) => ctrl.signal.addEventListener("abort", () => reject(ctrl.signal.reason), { once: true })),
        ]);
        ok = !(out && typeof out === "object" && "content" in out && (out as McpResult).isError);
        if (out && typeof out === "object" && "content" in out && Array.isArray((out as McpResult).content)) return out as McpResult;
        return result(typeof out === "string" ? out : JSON.stringify(out, null, 2));
      } finally {
        clearTimeout(timer);
      }
    } catch (e) {
      code = codeOf(e);
      // 不把内部异常的堆栈交给模型
      const msg = e instanceof ExtError ? e.message : "扩展内部出错了";
      if (!(e instanceof ExtError)) console.error(`[${this.def.id}] ${name}:`, e);
      return result(msg, true, code);
    } finally {
      void this.ee.audit(this.def.id, this.def.version, name, ok, Date.now() - started, ok ? undefined : code);
    }
  }

  /** 处理一条 JSON-RPC 消息；通知（没有 id）返回 undefined。 */
  async handle(msg: { jsonrpc?: string; id?: string | number | null; method?: string; params?: any }): Promise<object | undefined> {
    const id = msg.id;
    const reply = (res: unknown) => ({ jsonrpc: "2.0", id, result: res });
    const fail = (code: number, message: string) => ({ jsonrpc: "2.0", id, error: { code, message } });
    if (id === undefined || id === null) return undefined;
    switch (msg.method) {
      case "initialize": {
        const asked = String(msg.params?.protocolVersion ?? "");
        return reply({
          protocolVersion: MCP_VERSIONS.includes(asked) ? asked : MCP_VERSIONS[0],
          capabilities: { tools: {} },
          serverInfo: { name: this.def.id, version: this.def.version },
          instructions: textOf(this.def.description),
        });
      }
      case "ping":
        return reply({});
      case "tools/list":
        return reply({ tools: this.listTools() });
      case "tools/call": {
        const name = String(msg.params?.name ?? "");
        const args = msg.params?.arguments && typeof msg.params.arguments === "object" ? msg.params.arguments : {};
        return reply(await this.callTool(name, args));
      }
    }
    return fail(-32601, `不支持 ${msg.method}`);
  }

  /** 在 stdin / stdout 上提供 MCP 服务（Agent 的本机 MCP 配置里写 command: npx, args: [包名]）。 */
  serveStdio(input: NodeJS.ReadableStream = process.stdin, output: NodeJS.WritableStream = process.stdout) {
    const rl = createInterface({ input });
    rl.on("line", async (line) => {
      if (!line.trim()) return;
      let msg;
      try {
        msg = JSON.parse(line);
      } catch {
        output.write(JSON.stringify({ jsonrpc: "2.0", id: null, error: { code: -32700, message: "不是 JSON" } }) + "\n");
        return;
      }
      const res = await this.handle(msg);
      if (res) output.write(JSON.stringify(res) + "\n");
    });
    return rl;
  }
}

export function defineExtension(def: ExtensionDef): Extension {
  return new Extension(def);
}
