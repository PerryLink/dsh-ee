/**
 * 机器人对话（/bot-chat）的接口层与共享状态。
 *
 * - 接口固定在 /api/runtime/chat 下，统一走 request()；
 * - 可用机器人列表做成模块级共享状态：侧栏入口与页面共用一次探测，不重复请求；
 * - 错误不直接显示服务端原文（那是中文默认文案，也可能带运行细节），
 *   按 runtime_chat_* 错误码或 HTTP 状态码映射到 botChat.errors.* 的本地化文案。
 */
import { useEffect, useSyncExternalStore } from "react";
import { ApiError, request, type Me } from "./api";
import { useAuth } from "./auth";

export type ChatId = number | string;

export interface ChatBot {
  id: ChatId;
  name: string;
}

export interface ChatConversation {
  id: ChatId;
  bot_id: ChatId;
  bot_name: string;
  title: string;
  updated_at: string;
}

export type ChatTurnStatus = "queued" | "running" | "succeeded" | "failed" | "canceled";

/** 一轮对话：员工的一条消息 + 机器人的回复；id 就是云任务 id。 */
export interface ChatTurn {
  id: ChatId;
  content: string;
  status: ChatTurnStatus;
  summary: string;
  created_at: string;
}

export interface ChatConversationDetail {
  conversation: ChatConversation;
  messages: ChatTurn[];
}

export interface ChatArtifact {
  id: ChatId;
  name: string;
}

/** 单条消息的内容上限（UTF-8 字节），与后端一致。 */
export const CHAT_CONTENT_MAX_BYTES = 8000;

const base = "/api/runtime/chat";
const tasksBase = "/api/runtime/tasks";
const enc = (id: ChatId) => encodeURIComponent(String(id));

export const chatApi = {
  bots: () => request<ChatBot[] | null>("GET", `${base}/bots`).then((x) => x ?? []),
  conversations: () => request<ChatConversation[] | null>("GET", `${base}/conversations`).then((x) => x ?? []),
  createConversation: (botId: ChatId) => request<ChatConversation>("POST", `${base}/conversations`, { bot_id: botId }),
  conversation: (id: ChatId) =>
    request<ChatConversationDetail>("GET", `${base}/conversations/${enc(id)}`).then((d) => ({ ...d, messages: d.messages ?? [] })),
  send: (id: ChatId, content: string, idempotencyKey: string) =>
    request<ChatTurn>("POST", `${base}/conversations/${enc(id)}/messages`, { content, idempotency_key: idempotencyKey }),
  artifacts: (taskId: ChatId) => request<ChatArtifact[] | null>("GET", `${tasksBase}/${enc(taskId)}/artifacts`).then((x) => x ?? []),
  /** 产物下载地址：只在员工点击时访问，页面不会自动下载。 */
  artifactUrl: (taskId: ChatId, artifactId: ChatId) => `${tasksBase}/${enc(taskId)}/artifacts/${enc(artifactId)}`,
};

export const sameId = (a: ChatId | null | undefined, b: ChatId | null | undefined) => a != null && b != null && String(a) === String(b);

export const isPendingTurn = (m: Pick<ChatTurn, "status">) => m.status === "queued" || m.status === "running";

export function utf8Bytes(text: string): number {
  return new TextEncoder().encode(text).length;
}

/** 幂等键：同一条内容重试时复用；非安全上下文（http 内网）没有 randomUUID 时退回随机串。 */
export function newIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") return crypto.randomUUID();
  const rand = () => Math.random().toString(36).slice(2);
  return `${Date.now().toString(36)}-${rand()}-${rand()}`;
}

// ---------- 错误映射 ----------

/** botChat.errors.* 下的文案键。 */
export type ChatErrorKey =
  | "network"
  | "server"
  | "forbidden"
  | "unavailable"
  | "notFound"
  | "botUnavailable"
  | "botDisabled"
  | "busy"
  | "conflict"
  | "queueFull"
  | "tooLong"
  | "invalid"
  | "rateLimited"
  | "generic";

/** 请求出在哪一类资源上：决定 404 说的是「功能没开」还是「会话不存在」。 */
export type ChatErrorScope = "runtime" | "conversation" | "files";

export interface ChatError {
  key: ChatErrorKey;
  status: number;
}

// 后端错误码 runtime_chat.<后缀>（server/internal/api/errcodes.go，也兼容下划线写法）→ 文案键。
// 没登记的后缀按 HTTP 状态码兜底，不显示服务端原文。
const CODE_KEYS: Record<string, ChatErrorKey> = {
  bot_unavailable: "botUnavailable",
  bot_disabled: "botDisabled",
  not_found: "notFound",
  invalid_message: "invalid",
  invalid_key: "conflict",
  busy: "busy",
  idempotency_conflict: "conflict",
  queue_full: "queueFull",
  disabled: "unavailable",
  forbidden: "forbidden",
};

function keyOfStatus(status: number, scope: ChatErrorScope): ChatErrorKey {
  if (status === 0) return "network";
  if (status === 400 || status === 422) return "invalid";
  if (status === 403) return "forbidden";
  if (status === 404) return scope === "runtime" ? "unavailable" : scope === "conversation" ? "notFound" : "generic";
  if (status === 409) return "busy";
  if (status === 413) return "tooLong";
  if (status === 429) return "rateLimited";
  if (status >= 500) return "server";
  return "generic";
}

export function toChatError(err: unknown, scope: ChatErrorScope): ChatError {
  if (!(err instanceof ApiError)) return { key: "generic", status: -1 };
  const data = err.data && typeof err.data === "object" ? (err.data as Record<string, unknown>) : {};
  const code = typeof data.code === "string" ? data.code : "";
  const suffix = /^runtime_chat[._]/.test(code) ? code.replace(/^runtime_chat[._]/, "") : "";
  const mapped = suffix ? CODE_KEYS[suffix] : undefined;
  return { key: mapped ?? keyOfStatus(err.status, scope), status: err.status };
}

// ---------- 可用机器人：侧栏与页面共用 ----------

export type ChatBotsStatus = "idle" | "loading" | "ready" | "unavailable" | "forbidden" | "error";

export interface ChatBotsState {
  status: ChatBotsStatus;
  bots: ChatBot[];
  error: ChatError | null;
}

const INELIGIBLE: ChatBotsState = { status: "forbidden", bots: [], error: null };
const IDLE: ChatBotsState = { status: "idle", bots: [], error: null };

let state: ChatBotsState = IDLE;
let owner: number | null = null;
let inflight: Promise<void> | null = null;
const listeners = new Set<() => void>();

function setState(next: ChatBotsState) {
  state = next;
  for (const fn of listeners) fn();
}

function subscribe(fn: () => void) {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

function loadBots(userId: number, force: boolean): Promise<void> {
  if (owner !== userId) {
    owner = userId;
    inflight = null;
    state = IDLE;
  }
  if (inflight) return inflight;
  if (!force && state.status !== "idle") return Promise.resolve();
  setState({ ...state, status: state.status === "ready" ? "ready" : "loading", error: null });
  const p: Promise<void> = chatApi
    .bots()
    .then((bots) => {
      if (owner === userId && inflight === p) setState({ status: "ready", bots, error: null });
    })
    .catch((err: unknown) => {
      if (owner !== userId || inflight !== p) return;
      const error = toChatError(err, "runtime");
      const status: ChatBotsStatus = error.key === "unavailable" ? "unavailable" : error.key === "forbidden" ? "forbidden" : "error";
      setState({ status, bots: [], error });
    })
    .finally(() => {
      if (inflight === p) inflight = null;
    });
  inflight = p;
  return p;
}

/**
 * 网页对话面向普通员工：真人账号、非管理员，且同时有 portal.view 与 llm.use。
 * 这里只用来决定要不要发探测请求；真正的鉴权在后端，超级管理员身份不当作放行依据。
 */
export function chatEligible(user: Me | null): user is Me {
  if (!user) return false;
  if (user.account_type !== "human" || user.is_super) return false;
  return user.permissions.includes("portal.view") && user.permissions.includes("llm.use");
}

/** 可用机器人与功能可用性；不符合条件的账号不发请求，直接是 forbidden。 */
export function useChatBots(): ChatBotsState & { reload: () => void } {
  const { user } = useAuth();
  const eligible = chatEligible(user);
  const userId = eligible ? user.id : null;
  const snapshot = useSyncExternalStore(subscribe, () => state);
  useEffect(() => {
    if (userId !== null) void loadBots(userId, false);
  }, [userId]);
  if (userId === null) return { ...INELIGIBLE, reload: () => {} };
  // 换了登录用户、探测还没发出去的那一帧：不要把上一个人的结果露出来
  const current = owner === userId ? snapshot : IDLE;
  return { ...current, reload: () => void loadBots(userId, true) };
}
