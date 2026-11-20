import fs from "node:fs";
import path from "node:path";
import { createHash } from "node:crypto";
import { setTimeout as sleep } from "node:timers/promises";
import { dshHome } from "./config.js";
import { createSession, sendToSession } from "./host-session.js";
import { createSessionSnapshotAdapter } from "./session-status.js";
import { t } from "./i18n/index.js";
import type { DshContext, PluginConfig } from "./dsh.js";
import type { HttpClient } from "./http.js";
import type { TokenManager } from "./token.js";

interface Message { id: number; text: string }
interface ActiveTurn { id: number; sessionId: string; replied: boolean }
interface Options {
  file?: string;
  intervalMs?: number;
  delay?: (ms: number, signal: AbortSignal) => Promise<void>;
}

function abortable<T>(work: () => Promise<T>, signal: AbortSignal): Promise<T> {
  return new Promise((resolve, reject) => {
    const abort = () => reject(signal.reason);
    if (signal.aborted) { reject(signal.reason); return; }
    signal.addEventListener("abort", abort, { once: true });
    work().then(resolve, reject).finally(() => signal.removeEventListener("abort", abort));
  });
}

export class FeishuChatRelay {
  private controller?: AbortController;
  private loop?: Promise<void>;
  private active?: ActiveTurn;
  private readonly file: string;
  private readonly delay: (ms: number, signal: AbortSignal) => Promise<void>;

  constructor(private ctx: DshContext, private config: PluginConfig, private tokens: Pick<TokenManager, "resolve">, private http: Pick<HttpClient, "fetch">, private options: Options = {}) {
    this.file = options.file ?? path.join(dshHome(), "dsh-ee", "feishu-chat.json");
    this.delay = options.delay ?? ((ms, signal) => sleep(ms, undefined, { signal, ref: false }));
    ctx.on("tools/pre-execute", (exec: any, next: () => Promise<unknown>) => {
      const turn = this.active;
      if (turn && !this.controller?.signal.aborted && exec.name === `mcp__${config.serverName}__ee_feishu`
        && exec.agent?.session?.id === turn.sessionId && exec.arguments?.action === "reply" && String(exec.arguments?.id) === String(turn.id)) {
        turn.replied = true;
      }
      return next();
    });
  }

  start(): void {
    if (this.loop) return;
    this.controller = new AbortController();
    this.loop = this.run(this.controller.signal).catch(() => undefined).finally(() => { this.loop = undefined; });
  }

  async stop(): Promise<void> {
    this.controller?.abort();
    this.active = undefined;
    await this.loop;
  }

  private async run(signal: AbortSignal): Promise<void> {
    while (!signal.aborted) {
      const token = await this.tokens.resolve().catch(() => undefined);
      if (!token?.value) { await this.delay(1000, signal); continue; }
      const auth = new AbortController();
      const current = AbortSignal.any([signal, auth.signal]);
      const base = this.config.base;
      let checking = false;
      const watch = setInterval(async () => {
        if (checking) return;
        checking = true;
        try {
          if ((await this.tokens.resolve())?.value !== token.value || this.config.base !== base) auth.abort();
        } catch { auth.abort(); }
        finally { checking = false; }
      }, this.options.intervalMs ?? 1000);
      watch.unref();
      try {
        const scope = createHash("sha256").update(base + "\n" + token.value).digest("hex");
        await this.poll(scope, current);
      } catch {
        if (!current.aborted) await this.delay(1000, signal);
      } finally {
        clearInterval(watch);
        this.active = undefined;
      }
    }
  }

  private async poll(scope: string, signal: AbortSignal): Promise<void> {
    let backoff = 1000;
    while (!signal.aborted) {
      try {
        const response = await this.http.fetch("/api/me/feishu-chat/next?wait=25", { signal });
        if (response.status === 204) { backoff = 1000; continue; }
        if (!response.ok) {
          await this.delay(response.status === 404 ? 60_000 : backoff, signal);
          backoff = Math.min(backoff * 2, 60_000);
          continue;
        }
        const message = await response.json() as Message;
        await this.assertScope(scope);
        if (!Number.isSafeInteger(message.id) || message.id <= 0 || typeof message.text !== "string") throw new Error("Invalid Feishu message");
        backoff = 1000;
        await this.deliver(scope, message, signal);
      } catch (error) {
        if (signal.aborted) throw error;
        await this.delay(backoff, signal);
        backoff = Math.min(backoff * 2, 60_000);
      }
    }
  }

  private async assertScope(scope: string): Promise<void> {
    const token = await this.tokens.resolve();
    if (!token?.value || createHash("sha256").update(this.config.base + "\n" + token.value).digest("hex") !== scope) throw new Error("Feishu login changed");
  }

  private async session(scope: string): Promise<string> {
    let saved: any;
    try { saved = JSON.parse(fs.readFileSync(this.file, "utf8")); } catch {}
    const snapshot = this.ctx.sessions?.list?.getSnapshot?.();
    if (saved?.scope === scope && typeof saved.sessionId === "string" && snapshot?.byId?.[saved.sessionId]) return saved.sessionId;
    const sessionId = await createSession(this.ctx);
    fs.mkdirSync(path.dirname(this.file), { recursive: true, mode: 0o700 });
    const temp = `${this.file}.${process.pid}.tmp`;
    try {
      fs.writeFileSync(temp, JSON.stringify({ scope, sessionId }), { mode: 0o600 });
      fs.renameSync(temp, this.file);
    } finally { fs.rmSync(temp, { force: true }); }
    return sessionId;
  }

  private async deliver(scope: string, message: Message, signal: AbortSignal): Promise<void> {
    let fallback = t("feishuChat.handled");
    let dispose = () => {};
    try {
      const sessionId = await abortable(() => this.session(scope), signal);
      signal.throwIfAborted();
      this.active = { id: message.id, sessionId, replied: false };
      const adapt = createSessionSnapshotAdapter();
      const statuses = this.ctx.get("uiSession")?.sessionStatus;
      const rowNow = () => adapt(this.ctx.sessions?.list?.getSnapshot?.(), statuses)?.byId?.[sessionId];
      while (rowNow()?.running === true) await this.delay(200, signal);
      let running = false;
      let complete = false;
      let previouslyCompleted = !!rowNow()?.completed;
      const check = () => {
        const row = rowNow();
        if (row?.running === true) running = true;
        if ((running && row?.running === false) || (row?.completed === true && !previouslyCompleted)) complete = true;
        previouslyCompleted = row?.completed === true;
      };
      const off = [this.ctx.sessions?.list?.subscribe?.(check), statuses?.subscribe?.(check)];
      dispose = () => { for (const fn of off) if (typeof fn === "function") fn(); };
      check();
      await this.assertScope(scope);
      await abortable(() => sendToSession(this.ctx, sessionId, t("feishuChat.prompt", { id: message.id, text: message.text })), signal);
      const deadline = Date.now() + 60_000;
      while (!complete) {
        signal.throwIfAborted();
        check();
        if (!running && Date.now() > deadline) throw new Error("Feishu session did not start");
        if (!complete) await this.delay(200, signal);
      }
    } catch (error) {
      if (signal.aborted) throw error;
      fallback = t("feishuChat.failed");
    } finally { dispose(); }
    try {
      signal.throwIfAborted();
      await this.assertScope(scope);
      if (!this.active?.replied) {
        const response = await this.http.fetch(`/api/me/feishu-chat/${message.id}/reply`, {
          method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ text: fallback }), signal,
        });
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
      }
    } finally { this.active = undefined; }
  }
}
