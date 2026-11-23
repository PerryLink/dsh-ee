/**
 * 机器人对话（/bot-chat）的接口层与共享状态。
 *
 * - 接口固定在 /api/runtime/chat 下，统一走 request()；
 * - 一个机器人对当前员工只有一条连续的会话（thread）：列表带 thread_id，没有时用幂等的创建接口取得；
 * - 可用机器人列表做成模块级共享状态：侧栏入口与页面共用同一份结果和同一个在途请求；
 *   对话页每次打开会重新拉一次，页面上也能手动刷新；
 * - 错误不直接显示服务端原文（那是中文默认文案，也可能带运行细节），
 *   按 runtime_chat_* 错误码或 HTTP 状态码映射到 botChat.errors.* 的本地化文案。
 */
import { useCallback, useEffect, useSyncExternalStore } from "react";
import { ApiError, request, type Me } from "./api";
import { useAuth } from "./auth";

export type ChatId = number | string;

export type ChatTurnStatus = "queued" | "running" | "succeeded" | "failed" | "canceled";

export interface ChatBot {
  id: ChatId;
  name: string;
  /** 这个机器人与当前员工的唯一会话；还没聊过时没有，用 createConversation（幂等）取得。 */
  thread_id?: ChatId | null;
  /** 最近一条可预览的文字、它所属的云任务与回复状态（都来自后端，没有就不显示）。 */
  last_message?: string | null;
  last_task_id?: ChatId | null;
  last_status?: ChatTurnStatus | null;
}

export interface ChatConversation {
  id: ChatId;
  bot_id: ChatId;
  bot_name: string;
  title: string;
  updated_at: string;
}

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
  /** 还有更早的消息时给出：作为 before 传回去读上一页。 */
  next_before?: ChatId | null;
}

export interface ChatArtifact {
  id: ChatId;
  name: string;
}

/** 机器人资料里的文件：带所属云任务，下载走 artifactUrl(task_id, id)。 */
export interface ChatBotArtifact extends ChatArtifact {
  task_id: ChatId;
  /** 来自对话轮次（chat）还是定时 / 手动任务（task）。 */
  source?: "chat" | "task" | null;
  size?: number | null;
  created_at?: string | null;
}

/**
 * 机器人自己的常驻云电脑。none：还没有（下一轮新建）；waking：正在新建或从休眠恢复；running：开着；
 * hibernating：正在休眠；hibernated：已休眠（文件保留，下一轮自动唤醒）。
 */
export type ChatComputerState = "none" | "waking" | "running" | "hibernating" | "hibernated";

export interface ChatComputer {
  /** 为假：这个运行时每轮都用一台新电脑，没有休眠 / 唤醒。 */
  persistent: boolean;
  state: ChatComputerState;
  /** 正被一个任务或操作占用。 */
  busy: boolean;
  disk_bytes: number;
  disk_limit_bytes: number;
  idle_timeout_seconds: number;
  last_used_at?: string | null;
  hibernated_at?: string | null;
}

/** 机器人资料：每日定时任务（UTC cron）、是否开启、是否有任务在跑、云电脑状态、生成过的文件。 */
export interface ChatBotProfile {
  schedule: string;
  schedule_enabled: boolean;
  running: boolean;
  /** 旧后端没有这个字段。 */
  computer?: ChatComputer | null;
  artifacts: ChatBotArtifact[];
}

/** 单条消息的内容上限（UTF-8 字节），与后端一致。 */
export const CHAT_CONTENT_MAX_BYTES = 8000;

const base = "/api/runtime/chat";
const tasksBase = "/api/runtime/tasks";
const enc = (id: ChatId) => encodeURIComponent(String(id));

export const chatApi = {
  bots: () => request<ChatBot[] | null>("GET", `${base}/bots`).then((x) => x ?? []),
  profile: (botId: ChatId) =>
    request<ChatBotProfile>("GET", `${base}/bots/${enc(botId)}/profile`).then((p) => ({ ...p, schedule: p.schedule ?? "", artifacts: p.artifacts ?? [] })),
  /** 幂等：同一个机器人总是返回同一个会话；只在列表里没有 thread_id 时用来取得它。 */
  createConversation: (botId: ChatId) => request<ChatConversation>("POST", `${base}/conversations`, { bot_id: botId }),
  /** 最近一页消息；带 before（上一页给的 next_before）时读更早的一页。 */
  conversation: (id: ChatId, before?: ChatId | null) =>
    request<ChatConversationDetail>("GET", `${base}/conversations/${enc(id)}${before != null ? `?before=${enc(before)}` : ""}`).then((d) => ({
      ...d,
      messages: d.messages ?? [],
    })),
  send: (id: ChatId, content: string, idempotencyKey: string) =>
    request<ChatTurn>("POST", `${base}/conversations/${enc(id)}/messages`, { content, idempotency_key: idempotencyKey }),
  artifacts: (taskId: ChatId) => request<ChatArtifact[] | null>("GET", `${tasksBase}/${enc(taskId)}/artifacts`).then((x) => x ?? []),
  /** 立即休眠（文件保留）或重置（删除整台电脑和其中的文件）。 */
  computer: (botId: ChatId, action: "hibernate" | "reset") => request<ChatComputer>("POST", `${base}/bots/${enc(botId)}/computer/${action}`),
  /** 产物下载地址：只在员工点击时访问，页面不会自动下载。 */
  artifactUrl: (taskId: ChatId, artifactId: ChatId) => `${tasksBase}/${enc(taskId)}/artifacts/${enc(artifactId)}`,
};

export const sameId = (a: ChatId | null | undefined, b: ChatId | null | undefined) => a != null && b != null && String(a) === String(b);

export const isPendingTurn = (m: Pick<ChatTurn, "status">) => m.status === "queued" || m.status === "running";

export function utf8Bytes(text: string): number {
  return new TextEncoder().encode(text).length;
}

// ---------- 已读位置：只存在本机，按账号 + 机器人分开 ----------

const readKey = (userId: number, botId: ChatId) => `dsh-ee:bot-chat:read:${userId}:${String(botId)}`;

/** 这个账号在本机上读到了该机器人的哪一条回复（云任务 id）；没读过或存储不可用时为 null。 */
export function getLastRead(userId: number, botId: ChatId): string | null {
  try {
    return localStorage.getItem(readKey(userId, botId));
  } catch {
    return null;
  }
}

export function setLastRead(userId: number, botId: ChatId, taskId: ChatId) {
  try {
    localStorage.setItem(readKey(userId, botId), String(taskId));
  } catch {
    // 隐私模式等存不进去：未读点只是提示，丢了不影响对话
  }
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
  /** 已有结果、后台正在重新拉取：界面保持原样，只让刷新按钮转起来。 */
  refreshing: boolean;
}

const INELIGIBLE: ChatBotsState = { status: "forbidden", bots: [], error: null, refreshing: false };
const IDLE: ChatBotsState = { status: "idle", bots: [], error: null, refreshing: false };

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
  // 已有确定结果（可用 / 未启用 / 无权限）时原地刷新，不退回 loading：页面和侧栏入口都不闪。
  // 还没结果、或上次失败后重试，才显示加载中。
  const prev = state;
  const settled = prev.status === "ready" || prev.status === "unavailable" || prev.status === "forbidden";
  setState(settled ? { ...prev, error: null, refreshing: true } : { status: "loading", bots: [], error: null, refreshing: false });
  const p: Promise<void> = chatApi
    .bots()
    .then((bots) => {
      if (owner === userId && inflight === p) setState({ status: "ready", bots, error: null, refreshing: false });
    })
    .catch((err: unknown) => {
      if (owner !== userId || inflight !== p) return;
      const error = toChatError(err, "runtime");
      if (error.key === "unavailable") setState({ status: "unavailable", bots: [], error, refreshing: false });
      else if (error.key === "forbidden") setState({ status: "forbidden", bots: [], error, refreshing: false });
      // 刷新时的临时故障（断网、5xx）：留着上一次的结果并带上错误，不把正在用的页面换成报错页
      else if (settled) setState({ ...prev, error, refreshing: false });
      else setState({ status: "error", bots: [], error, refreshing: false });
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

const noop = () => {};

/**
 * 可用机器人与功能可用性；不符合条件的账号不发请求，直接是 forbidden。
 *
 * 结果是模块级共享的，本身不会过期：reload 强制重新拉取（引用稳定，可以放进 effect 依赖），
 * 对话页每次打开时调一次，页面上另有手动刷新；侧栏入口只做首次探测。
 */
export function useChatBots(): ChatBotsState & { reload: () => void } {
  const { user } = useAuth();
  const eligible = chatEligible(user);
  const userId = eligible ? user.id : null;
  const snapshot = useSyncExternalStore(subscribe, () => state);
  useEffect(() => {
    if (userId !== null) void loadBots(userId, false);
  }, [userId]);
  const reload = useCallback(() => {
    if (userId !== null) void loadBots(userId, true);
  }, [userId]);
  if (userId === null) return { ...INELIGIBLE, reload: noop };
  // 换了登录用户、探测还没发出去的那一帧：不要把上一个人的结果露出来
  const current = owner === userId ? snapshot : IDLE;
  return { ...current, reload };
}
