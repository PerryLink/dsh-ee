/**
 * 机器人对话（/bot-chat）的接口层与共享状态。
 *
 * - 接口固定在 /api/runtime/chat 下，统一走 request()；
 * - 一个机器人对当前员工只有一条连续的会话（thread）：列表带 thread_id，没有时用幂等的创建接口取得；
 * - 团队共享：机器人共享给员工所在的部门后，列表里另有一个团队入口（shared），整个部门共用一条团队会话，
 *   每条消息带发送人；前端把团队入口的 id 换成 team-<机器人 id>，和员工自己的入口区分开，接口调用用 botRef 取回真实 id；
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
  /** 团队入口：机器人共享给了当前员工所在的部门 department，所有者是 owner_name；bot_id 是真实的机器人 id。 */
  shared?: boolean;
  department?: string | null;
  owner_name?: string | null;
  bot_id?: ChatId | null;
  /** 只出现在自己的机器人上：当前共享给了哪个部门（没共享时没有）。 */
  shared_department?: string | null;
  /** 写操作审批模式（管理员设置）；旧后端没有这个字段，按只读处理。 */
  approval_mode?: ApprovalMode | null;
  /** 管理员是否允许本人在对话页切换审批模式。 */
  approval_editable?: boolean | null;
}

/**
 * 云端机器人的写操作审批模式：
 * read_only 只读；manual 逐个审批；auto 按规则自动审批，其余逐个审批；full 完全放行（不再审批，仍有审计）。
 */
export type ApprovalMode = "read_only" | "manual" | "auto" | "full";
export const APPROVAL_MODES: ApprovalMode[] = ["read_only", "manual", "auto", "full"];
export const approvalModeOf = (b: Pick<ChatBot, "approval_mode"> | null | undefined): ApprovalMode =>
  b?.approval_mode && APPROVAL_MODES.includes(b.approval_mode) ? b.approval_mode : "read_only";

/** 一张审批卡：机器人在某一轮（runtime_task_id）里要做的写操作，等员工批准或拒绝。复用写操作确认（/api/me/confirmations）。 */
export interface ChatApproval {
  id: ChatId;
  tool: string;
  /** 规整后的参数 JSON。 */
  args: string;
  status: string;
  /** 企业系统名、工具说明（后端按连接补上）。 */
  system?: string;
  description?: string;
  runtime_task_id?: ChatId | null;
  expires_at: string;
}

export interface ChatConversation {
  id: ChatId;
  bot_id: ChatId;
  bot_name: string;
  title: string;
  updated_at: string;
  shared?: boolean;
  department?: string | null;
}

/** 一轮对话：员工的一条消息 + 机器人的回复；id 就是云任务 id。 */
export interface ChatTurn {
  id: ChatId;
  content: string;
  status: ChatTurnStatus;
  summary: string;
  created_at: string;
  /** 团队会话里才有：发这条消息的员工（这一轮也以他自己的身份运行）。 */
  sender_id?: ChatId | null;
  sender_name?: string | null;
  /** 员工随这条消息上传的文档（只有文件名、大小，内容在机器人的工作目录里）。 */
  attachments?: ChatAttachment[] | null;
}

/** 拖进对话框的一个文档：上传时服务端已转好正文，发消息时带上 id。 */
export interface ChatAttachment {
  id: number;
  name: string;
  size: number;
  format: string;
  pages?: number;
  text_bytes: number;
  created_at: string;
}

/**
 * 机器人之间的一次委派（派任务 / 发消息）。status：pending（发起的那一轮还没结束）、failed（没派出去）、canceled，
 * 派出去之后就是目标那一轮的状态（queued / running / succeeded / failed / canceled）。
 * 一轮里：parent_task_id 是它就是这一轮派出的；child_task_id 是它就是别的机器人派来的；followup_task_id 是它就是结果回来的那一轮。
 */
export interface ChatDelegation {
  id: ChatId;
  parent_task_id: ChatId;
  parent_bot_id: ChatId;
  parent_bot_name: string;
  target_bot_id: ChatId;
  target_bot_name: string;
  kind: "task" | "message";
  content: string;
  status: "pending" | "failed" | "canceled" | ChatTurnStatus;
  error?: string | null;
  child_task_id?: ChatId | null;
  child_conversation_id?: ChatId | null;
  followup_task_id?: ChatId | null;
  result?: string | null;
  depth: number;
  created_at: string;
}

export const isOpenDelegation = (d: ChatDelegation) =>
  d.status === "pending" || d.status === "queued" || d.status === "running" || (d.kind === "task" && d.status === "succeeded" && d.followup_task_id == null);

export interface ChatConversationDetail {
  conversation: ChatConversation;
  messages: ChatTurn[];
  /** 这一页的轮次相关的委派（旧后端没有）。 */
  delegations?: ChatDelegation[] | null;
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
 * 常驻云电脑（我的电脑 / 团队电脑）。none：还没有（下一轮新建）；waking：正在新建或从休眠恢复；running：开着；
 * hibernating：正在休眠；hibernated：已休眠（文件保留，下一轮自动唤醒）。
 */
export type ChatComputerState = "none" | "waking" | "running" | "hibernating" | "hibernated";

export interface ChatComputer {
  /** 为假：这个运行时每轮都用一台新电脑，没有休眠 / 唤醒。 */
  persistent: boolean;
  /** user：我的电脑（员工的所有个人机器人共用）；team：团队电脑。旧后端没有，按 user 显示。 */
  owner_kind?: "user" | "team";
  department?: string | null;
  /** 共用这台电脑的个人机器人数。 */
  shared_bots?: number | null;
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
  /** 团队机器人的资料：没有所有者的定时设置，文件库只有团队会话里的文件。 */
  shared?: boolean;
  department?: string | null;
  schedule: string;
  schedule_enabled: boolean;
  running: boolean;
  /** 旧后端没有这个字段。 */
  computer?: ChatComputer | null;
  artifacts: ChatBotArtifact[];
}

/** 单条消息的内容上限（UTF-8 字节），与后端一致。 */
export const CHAT_CONTENT_MAX_BYTES = 8000;

/** 对话框附件：与服务端 runtimeChatUploadMax、RuntimeChatUploadsPerMessage、知识库的转换格式一致。 */
export const CHAT_UPLOAD_MAX_BYTES = 50 << 20;
export const CHAT_UPLOADS_PER_MESSAGE = 5;
export const CHAT_UPLOAD_FORMATS = [".pdf", ".docx", ".xlsx", ".pptx", ".csv", ".md", ".markdown", ".txt"];

export function chatUploadSupported(name: string): boolean {
  const dot = name.lastIndexOf(".");
  return dot > 0 && CHAT_UPLOAD_FORMATS.includes(name.slice(dot).toLowerCase());
}

const base = "/api/runtime/chat";
const tasksBase = "/api/runtime/tasks";
const enc = (id: ChatId) => encodeURIComponent(String(id));

/** 团队入口在前端的 id：和同一个机器人的个人入口区分开。 */
export const teamKey = (botId: ChatId) => `team-${String(botId)}`;
/** 列表项对应的真实机器人 id（团队入口的 id 是 teamKey）。 */
export const botRef = (bot: Pick<ChatBot, "id" | "bot_id">): ChatId => bot.bot_id ?? bot.id;

export const chatApi = {
  bots: () =>
    request<ChatBot[] | null>("GET", `${base}/bots`).then((x) =>
      (x ?? []).map((b) => (b.shared ? { ...b, bot_id: b.id, id: teamKey(b.id) } : b)),
    ),
  profile: (bot: Pick<ChatBot, "id" | "bot_id" | "shared">) =>
    request<ChatBotProfile>("GET", `${base}/bots/${enc(botRef(bot))}/profile${bot.shared ? "?shared=1" : ""}`).then((p) => ({
      ...p,
      schedule: p.schedule ?? "",
      artifacts: p.artifacts ?? [],
    })),
  /** 幂等：同一个机器人（团队入口是同一个部门）总是返回同一个会话；只在列表里没有 thread_id 时用来取得它。 */
  createConversation: (bot: Pick<ChatBot, "id" | "bot_id" | "shared">) =>
    request<ChatConversation>("POST", `${base}/conversations`, bot.shared ? { bot_id: botRef(bot), shared: true } : { bot_id: botRef(bot) }),
  /** 所有者把自己的机器人共享给自己所在的部门，或取消共享；返回现在共享给的部门（空串是没共享）。 */
  share: (botId: ChatId, shared: boolean) =>
    request<{ shared_department: string }>("PUT", `${base}/bots/${enc(botId)}/share`, { shared }),
  /** 最近一页消息；带 before（上一页给的 next_before）时读更早的一页。 */
  conversation: (id: ChatId, before?: ChatId | null) =>
    request<ChatConversationDetail>("GET", `${base}/conversations/${enc(id)}${before != null ? `?before=${enc(before)}` : ""}`).then((d) => ({
      ...d,
      messages: d.messages ?? [],
      delegations: d.delegations ?? [],
    })),
  send: (id: ChatId, content: string, idempotencyKey: string, uploads: number[] = []) =>
    request<ChatTurn>("POST", `${base}/conversations/${enc(id)}/messages`, {
      content,
      idempotency_key: idempotencyKey,
      ...(uploads.length ? { uploads } : {}),
    }),
  /** 上传一个拖进对话框的文档；服务端当场转成正文，读不出来时直接报错。 */
  upload: (id: ChatId, file: File) => {
    const form = new FormData();
    form.set("file", file, file.name);
    return request<ChatAttachment>("POST", `${base}/conversations/${enc(id)}/uploads`, undefined, { raw: { body: form } });
  },
  /** 移除一个还没发出的附件。 */
  removeUpload: (id: number) => request<void>("DELETE", `${base}/uploads/${enc(id)}`),
  artifacts: (taskId: ChatId) => request<ChatArtifact[] | null>("GET", `${tasksBase}/${enc(taskId)}/artifacts`).then((x) => x ?? []),
  /** 我待审批的云端机器人写操作（只取带 runtime_task_id 的）。 */
  approvals: () =>
    request<{ items: ChatApproval[] | null }>("GET", "/api/me/confirmations").then((x) => (x.items ?? []).filter((a) => a.runtime_task_id != null)),
  /** 批准或拒绝一张审批卡；机器人那一轮在等这个决定，批准后才执行。 */
  decide: (id: ChatId, approve: boolean) =>
    request<ChatApproval>("POST", `/api/me/confirmations/${enc(id)}`, {
      approve,
    }),
  /** 本人切换审批模式（需管理员允许）；切到完全放行必须带 confirmFullAccess。 */
  setApprovalMode: (botId: ChatId, mode: ApprovalMode, confirmFullAccess: boolean) =>
    request<{ approval_mode: ApprovalMode; approval_editable: boolean }>("PUT", `${base}/bots/${enc(botId)}/approval`, {
      approval_mode: mode,
      confirm_full_access: confirmFullAccess,
    }),
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
  | "teamForbidden"
  | "noDepartment"
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
  team_forbidden: "teamForbidden",
  no_department: "noDepartment",
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

const INELIGIBLE: ChatBotsState = {
  status: "forbidden",
  bots: [],
  error: null,
  refreshing: false,
};
const IDLE: ChatBotsState = {
  status: "idle",
  bots: [],
  error: null,
  refreshing: false,
};

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
