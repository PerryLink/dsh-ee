/**
 * 同站嵌入（v0）的前端参数与 postMessage 协议。
 *
 * 宿主用 iframe 打开微应用网页版：
 *   /a/<标识>?embed=1&host=<宿主 origin>[&fit=auto][&theme=light|dark]
 *
 * 约定：
 * - 消息只发给 URL 里的 host origin，绝不使用 "*"；
 * - 只接受来自 host origin、且 source 是 window.parent 的消息；
 * - 不传 host 或 host 非法时页面照常显示，但不发任何消息（宿主自己加了 sandbox 也能用）；
 * - 同站用现有 Lax cookie；跨站（v1）由宿主后端 mint 一次性 code，iframe 用 postMessage
 *   `dsh-ee:connect` 换取只存内存的 ee_emb_ Bearer（见 use-embed.ts 的握手与 docs/embedding.md）。
 */

export const EMBED_VERSION = 1;
/** 协议 v2（增量）：hello/supports、navigate、navigate-request、open-external、logout、deeplink。 */
export const EMBED_PROTOCOL_V2 = 2;

/** fit=auto 上报内容高度的上限：宿主按消息设高度时，避免异常页面把宿主布局撑爆。 */
export const EMBED_MAX_HEIGHT = 4000;

export type EmbedTheme = "light" | "dark";
export type EmbedFit = "fill" | "auto";
export type EmbedErrorCode = "auth_required" | "forbidden" | "load_failed";

export interface EmbedOptions {
  embed: boolean;
  /** 宿主 origin（校验后的 `scheme://host[:port]`）；缺省或非法时为 null。 */
  host: string | null;
  fit: EmbedFit;
  theme: EmbedTheme | null;
}

export interface EmbedError {
  code: EmbedErrorCode;
  message: string;
  retryable?: boolean;
}

const LOOPBACK_HOSTS = new Set(["localhost", "127.0.0.1", "::1", "[::1]"]);

export function isEmbedSearch(search: string): boolean {
  return new URLSearchParams(search).get("embed") === "1";
}

export function parseEmbedOptions(search: string): EmbedOptions {
  const params = new URLSearchParams(search);
  const embed = isEmbedSearch(search);
  return {
    embed,
    host: embed ? parseHostOrigin(params.get("host")) : null,
    fit: params.get("fit") === "auto" ? "auto" : "fill",
    theme: embed ? parseTheme(params.get("theme")) : null,
  };
}

function parseTheme(raw: string | null): EmbedTheme | null {
  return raw === "light" || raw === "dark" ? raw : null;
}

/** host 必须是完整 origin：https，或回环地址上的 http（本地联调）。 */
export function parseHostOrigin(raw: string | null): string | null {
  if (!raw) return null;
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return null;
  }
  const loopback = LOOPBACK_HOSTS.has(u.hostname.toLowerCase());
  if (u.protocol !== "https:" && !(u.protocol === "http:" && loopback)) return null;
  if (u.username || u.password || u.pathname !== "/" || u.search || u.hash) return null;
  return u.origin;
}

export function embedMessage(type: string, payload: Record<string, unknown> = {}): Record<string, unknown> {
  return { v: EMBED_VERSION, type, ...payload };
}

export function isEmbedMessageType(data: unknown, type: string): data is Record<string, unknown> {
  if (!data || typeof data !== "object") return false;
  const m = data as Record<string, unknown>;
  return m.v === EMBED_VERSION && m.type === type;
}

/** v1/v2 兼容的入站消息判断：新宿主可以带 v:2，老宿主 v:1 仍然接受。 */
export function isEmbedMessage(data: unknown, type: string): boolean {
  if (!data || typeof data !== "object") return false;
  const m = data as Record<string, unknown>;
  return (m.v === EMBED_VERSION || m.v === EMBED_PROTOCOL_V2) && m.type === type;
}

/** v2 消息（增量，老宿主按未知 type 忽略）。 */
export function embedMessageV2(type: string, payload: Record<string, unknown> = {}): Record<string, unknown> {
  return { v: EMBED_PROTOCOL_V2, type, ...payload };
}

/** 宿主 → iframe 的消息必须是 host origin 的父窗口发的。 */
export function isTrustedParentMessage(origin: string, source: unknown, host: string | null, parent: unknown): boolean {
  return !!host && origin === host && source === parent;
}

/** 宿主 -> iframe 的 connect 消息：一次性 code，只用于交换内存 Bearer。 */
export interface EmbedConnectPayload {
  code: string;
}

/** exchange 返回：ee_emb_ 内存 Bearer（只存内存，不回传给宿主，不写 localStorage）。 */
export interface EmbedExchangeResult {
  token: string;
  expires_in: number;
  limited: true;
  /** v2：mint 时绑定的站内路径（deeplink），exchange 后由 iframe 消费。 */
  next?: string;
  app: { slug: string; title: string };
  user: { username: string; display_name: string };
}

export class EmbedExchangeError extends Error {
  constructor(
    /** embed_code_invalid / embed_code_expired / embed_disabled / rate_limited */
    public code: string,
    message: string,
  ) {
    super(message);
  }
}

/**
 * 用宿主给的一次性 code 换 ee_emb_ 内存 Bearer。必须同源调用（iframe 自己发），
 * 不带 cookie、响应也不会有 Set-Cookie。
 */
export async function exchangeEmbedCode(code: string): Promise<EmbedExchangeResult> {
  let res: Response;
  try {
    res = await fetch("/api/auth/embed/exchange", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ code }),
    });
  } catch {
    throw new EmbedExchangeError("network", "无法连接 dsh-ee");
  }
  const text = await res.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    // 代理错误页之类的纯文本
  }
  const body = (data ?? {}) as { code?: string; error?: string };
  if (!res.ok) {
    throw new EmbedExchangeError(body.code || "embed_code_invalid", body.error || "登录码无效");
  }
  return data as EmbedExchangeResult;
}

/** 给宿主发消息；没配 host 或 host 非法时什么都不发（绝不用 "*"）。 */
export function postToHost(options: EmbedOptions, type: string, payload: Record<string, unknown> = {}): void {
  if (!options.embed || !options.host || typeof window === "undefined") return;
  window.parent.postMessage(embedMessage(type, payload), options.host);
}

/** v2 消息发送（hello/navigate-request/open-external/logged-out/deeplink）。 */
export function postToHostV2(options: EmbedOptions, type: string, payload: Record<string, unknown> = {}): void {
  if (!options.embed || !options.host || typeof window === "undefined") return;
  window.parent.postMessage(embedMessageV2(type, payload), options.host);
}

/**
 * 校验宿主发来的站内路径：只允许本应用 `/a/<slug>`（可带 query/hash），
 * 拒绝 `//`、反斜杠、控制字符、`..`/`.` 段。返回原 path 或 null。
 */
export function normalizeAppPath(path: unknown, slug: string): string | null {
  if (typeof path !== "string" || !slug) return null;
  if (path.length > 2048 || /[\\\u0000-\u001f\u007f]/.test(path)) return null;
  const prefix = `/a/${slug}`;
  if (!path.startsWith(prefix)) return null;
  const rest = path.slice(prefix.length);
  if (rest && !rest.startsWith("/") && !rest.startsWith("?") && !rest.startsWith("#")) return null;
  const pathOnly = path.split(/[?#]/, 1)[0];
  for (const seg of pathOnly.split("/")) {
    if (seg === ".." || seg === ".") return null;
  }
  return path;
}

/** 浏览器点击的链接是否留在本应用内（交给前端路由，不跳整页）。 */
export function isAppPath(path: string, slug: string): boolean {
  return normalizeAppPath(path, slug) !== null;
}

export function capEmbedHeight(height: number, max = EMBED_MAX_HEIGHT): number {
  if (!Number.isFinite(height)) return 0;
  return Math.max(0, Math.min(Math.round(height), max));
}

/** 当前实际渲染的主题：嵌入页只改 DOM，不写 localStorage。 */
export function resolvedDocumentTheme(): EmbedTheme {
  return document.documentElement.classList.contains("dark") ? "dark" : "light";
}

export function applyEmbedTheme(theme: EmbedTheme): void {
  const root = document.documentElement;
  root.classList.toggle("dark", theme === "dark");
  root.style.colorScheme = theme;
}

/** 失败态的「在浏览器打开」：去掉嵌入参数，用顶层窗口打开，避免在 iframe 里跳登录。 */
export function nonEmbedURL(): string {
  const u = new URL(window.location.href);
  for (const k of ["embed", "host", "fit", "theme"]) u.searchParams.delete(k);
  return `${u.pathname}${u.search}${u.hash}`;
}

/** 接口错误 → 宿主可判断的失败码。 */
export function embedErrorForStatus(status: number | null, message: string): EmbedError {
  if (status === 401) return { code: "auth_required", message, retryable: false };
  if (status === 403 || status === 404) return { code: "forbidden", message, retryable: false };
  return { code: "load_failed", message, retryable: true };
}
