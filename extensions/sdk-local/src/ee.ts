/**
 * 端侧扩展和 dsh-ee 之间的两件事（docs/plugins.md §8；服务端是 server/internal/api/local_ext.go）：
 *   - 写操作前问 dsh-ee 要确认（POST /api/local/confirm）：员工在网页上同意后，同样的参数再调一次才执行；
 *   - 每次调用后回报摘要（POST /api/local/audit）：扩展、版本、工具、耗时、成败，不带返回内容。
 * 用的是员工自己的接入令牌。
 */

export interface EEOptions {
  /** dsh-ee 的地址，例如 https://ee.acme.com；默认取环境变量 DSH_EE_URL */
  url?: string;
  /** 员工的接入令牌；默认取环境变量 DSH_EE_TOKEN */
  token?: string;
  /** 调用方是哪个 Agent：dsh / claude-code / codex …；默认取环境变量 DSH_EE_HARNESS。DSH 自己有审批卡 */
  harness?: string;
  /** 测试用 */
  fetch?: typeof fetch;
}

export interface ConfirmAnswer {
  approved: boolean;
  rejected?: boolean;
  /** 原样交给模型的说明（带确认链接） */
  message?: string;
  url?: string;
}

export class EEClient {
  readonly url: string;
  readonly token: string;
  readonly harness: string;
  private readonly http: typeof fetch;

  constructor(opts: EEOptions = {}) {
    this.url = (opts.url ?? process.env.DSH_EE_URL ?? "").replace(/\/+$/, "");
    this.token = opts.token ?? process.env.DSH_EE_TOKEN ?? "";
    this.harness = opts.harness ?? process.env.DSH_EE_HARNESS ?? "";
    this.http = opts.fetch ?? fetch;
  }

  get configured() {
    return !!this.url && !!this.token;
  }

  private async post(path: string, body: unknown, timeoutMs: number): Promise<Response> {
    return this.http(`${this.url}${path}`, {
      method: "POST",
      headers: { "content-type": "application/json", authorization: `Bearer ${this.token}`, "x-dshee-client": "dsh-ee-local/0.1" },
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(timeoutMs),
    });
  }

  /** 问 dsh-ee 这个写操作能不能执行。出错时抛错：确认不了就不执行。 */
  async confirm(extension: string, version: string, tool: string, args: unknown): Promise<ConfirmAnswer> {
    const r = await this.post("/api/local/confirm", { extension, version, tool, args: args ?? {}, harness: this.harness }, 15_000);
    const body = (await r.json().catch(() => null)) as (ConfirmAnswer & { error?: string }) | null;
    if (!r.ok || !body) throw new Error(body?.error ? `dsh-ee：${body.error}` : `dsh-ee 返回 HTTP ${r.status}`);
    return body;
  }

  /** 回报一次调用。尽力而为：失败不影响调用本身。 */
  async audit(extension: string, version: string, tool: string, ok: boolean, durationMs: number, code?: string): Promise<void> {
    if (!this.configured) return;
    try {
      await this.post("/api/local/audit", { extension, version, tool, ok, duration_ms: Math.round(durationMs), code: code ?? "", harness: this.harness }, 5_000);
    } catch {
      // 网络不通时不重试；本机回报本来就是弱保证（docs/plugins.md §13 待定 2）
    }
  }
}
