/**
 * 机器人对话（/bot-chat）：员工在网页里与管理员为自己开通的云端机器人对话。
 *
 * 三栏，以机器人为中心：
 * - 左：机器人列表（没有「会话」这一层）。每个机器人对当前员工只有一条连续的对话；
 *   预览与未读点来自真实的回复状态，已读位置只记在本机（按账号 + 机器人）。
 * - 中：选中机器人的那条对话。选中项写在地址里（?bot=<id>），刷新、前进后退都能回到原处；
 *   旧的 ?conversation=<id> 链接会换算成对应的机器人。
 * - 右：机器人资料（动态 / 文件库），可关闭；定时任务只读，开关仍由管理员在后台操作。
 *
 * 沿用原有的保护：有排队 / 运行中的回复时才轮询，且上一次请求回来之后才发下一次；
 * 本地追加消息之前发出的读取结果一律丢弃；发送失败时内容留在输入框里，重试复用同一个幂等键。
 */
import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent, type ReactNode, type RefObject } from "react";
import { useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import { ArrowUp, Bot, CalendarClock, ChevronDown, CircleAlert, Download, FileDown, FileText, Lock, Maximize2, Minimize2, Monitor, RefreshCw, Users, X } from "lucide-react";
import { Badge, Button, Card, Empty, Notice, Spinner, cx, tabCls, tabListCls } from "../components/ui";
import { ChatMarkdown } from "../components/chat-markdown";
import { BotWhale } from "../components/bot-whale";
import { WhaleMark } from "../components/whale-mark";
import { FilePreviewDialog, type PreviewFile } from "../components/bot-file-preview";
import { useAuth } from "../lib/auth";
import { formatBytes, formatDateOnly, formatDateTime, formatNumber, formatTimeOnly, resolveLang } from "../lib/format";
import {
  CHAT_CONTENT_MAX_BYTES,
  chatApi,
  getLastRead,
  isPendingTurn,
  newIdempotencyKey,
  sameId,
  setLastRead,
  toChatError,
  useChatBots,
  utf8Bytes,
  type ChatArtifact,
  type ChatBot,
  type ChatBotProfile,
  type ChatError,
  type ChatErrorScope,
  type ChatId,
  type ChatTurn,
  type ChatTurnStatus,
} from "../lib/bot-chat";

/** 有回复在排队 / 运行时的轮询间隔：上一次请求结束后再等这么久。 */
const POLL_MS = 2500;
/** 资料栏开着时刷新资料（云端电脑状态、文件库）的间隔。 */
const PROFILE_POLL_MS = 5000;
/** 离底部这么近就算「在看最新消息」，新内容到了跟着滚下去。 */
const STICK_PX = 96;
/** 滚到离顶部这么近就去读更早的消息。 */
const EARLIER_PX = 48;

/** 一个机器人的那条对话。 */
type Thread = {
  status: "loading" | "ready" | "error";
  convId: string | null;
  messages: ChatTurn[];
  /** 还有更早的消息时，读上一页要带的 before。 */
  nextBefore: ChatId | null;
  earlierLoading: boolean;
  earlierError: ChatError | null;
  error: ChatError | null;
  /** 已经有内容之后的刷新 / 轮询失败：保留对话记录，只提示一下。 */
  refreshError: ChatError | null;
};

const LOADING_THREAD: Thread = {
  status: "loading",
  convId: null,
  messages: [],
  nextBefore: null,
  earlierLoading: false,
  earlierError: null,
  error: null,
  refreshError: null,
};

/** 每个机器人的请求控制：不进渲染，只用来去重和丢弃过期结果。 */
type ThreadCtl = {
  convId: string | null;
  /** 列表里的 thread_id 读不到了（404）：下次改用幂等的创建接口重新取得。 */
  skipListed: boolean;
  inflight: Promise<void> | null;
  /** 本地追加过消息（发送成功）就 +1：在那之前发出的读取结果不含新消息，回来后丢弃。 */
  version: number;
  earlier: boolean;
};

type ProfileState = {
  status: "loading" | "ready" | "error";
  data: ChatBotProfile | null;
  error: ChatError | null;
  /** 已有资料后的刷新失败：保留上一次的内容。 */
  refreshError: ChatError | null;
};

function useAlive() {
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  return alive;
}

const isWide = (query: string) => typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia(query).matches;
/** 与下面的 lg: / xl: 断点一致：更窄时列表 / 资料是盖在对话区上的浮层。 */
const LIST_STATIC = "(min-width: 1024px)";
const PROFILE_STATIC = "(min-width: 1280px)";

function TypingDots() {
  return (
    <span aria-hidden className="inline-flex items-center gap-1">
      {[0, 150, 300].map((delay) => (
        <span
          key={delay}
          style={{ animationDelay: `${delay}ms` }}
          className="h-1.5 w-1.5 animate-bounce rounded-full bg-neutral-400 motion-reduce:animate-none"
        />
      ))}
    </span>
  );
}

// ---------- 小工具 ----------

/** 产物名可能带子目录（reports/周报.md）：下载时的文件名只取最后一段。 */
const baseName = (name: string) => name.split("/").pop() || name;

const reducedMotion = () => typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

/** 面板收起时先播完退场动画再隐藏；用户要求减少动态时立即隐藏。 */
const PANEL_OUT_MS = 160;
function usePresence(open: boolean): { shown: boolean; leaving: boolean } {
  // 上一次打开过、还没等到退场结束：关闭后的这段时间里照常渲染，只是标成 leaving
  const [shownState, setShownState] = useState(open);
  useEffect(() => {
    if (open) {
      setShownState(true);
      return;
    }
    if (reducedMotion()) {
      setShownState(false);
      return;
    }
    const timer = setTimeout(() => setShownState(false), PANEL_OUT_MS);
    return () => clearTimeout(timer);
  }, [open]);
  return { shown: open || shownState, leaving: !open && shownState };
}

/** 全屏状态只记在本机，按账号分开；存储不可用时就是不全屏。 */
const fullscreenKey = (userId: number) => `dsh-ee:bot-chat:fullscreen:${userId}`;
function getFullscreen(userId: number): boolean {
  try {
    return localStorage.getItem(fullscreenKey(userId)) === "1";
  } catch {
    return false;
  }
}
function saveFullscreen(userId: number, on: boolean) {
  try {
    if (on) localStorage.setItem(fullscreenKey(userId), "1");
    else localStorage.removeItem(fullscreenKey(userId));
  } catch {
    // 隐私模式等存不进去：只是刷新后不再保持全屏
  }
}

/** 预览用：把 Markdown 压成一行纯文字。 */
function plainText(markdown: string): string {
  return markdown
    .replace(/```[\s\S]*?```/g, " ")
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/[#>*_`~|]/g, "")
    .replace(/\s+/g, " ")
    .trim()
    .slice(0, 160);
}

const dayStart = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();

function dayKey(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : String(dayStart(d));
}

/** 日期分隔：今天 / 昨天用各语言自己的说法，其余是完整日期。 */
function dayLabel(iso: string, lang: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const diff = Math.round((dayStart(d) - dayStart(new Date())) / 86_400_000);
  if (diff === 0 || diff === -1) {
    const text = new Intl.RelativeTimeFormat(resolveLang(lang), { numeric: "auto" }).format(diff, "day");
    return text.charAt(0).toLocaleUpperCase(resolveLang(lang)) + text.slice(1);
  }
  return formatDateOnly(d, lang);
}

/** 每日 cron（UTC，「分 时 * * *」）换成本地时间；不是这种写法就返回 null，只显示原规则。 */
function dailyLocalTime(cron: string, lang: string): string | null {
  const m = /^(\d{1,2})\s+(\d{1,2})\s+\*\s+\*\s+\*$/.exec(cron.trim());
  if (!m) return null;
  const minute = Number(m[1]);
  const hour = Number(m[2]);
  if (minute > 59 || hour > 23) return null;
  const d = new Date();
  d.setUTCHours(hour, minute, 0, 0);
  return formatTimeOnly(d, lang);
}

// ---------- 对话里的一轮 ----------

/** 成功回复生成的文件：员工点了才读列表，点了文件名才下载。 */
function TurnFiles({ taskId, onPreview }: { taskId: ChatId; onPreview: (file: PreviewFile) => void }) {
  const { t } = useTranslation();
  const alive = useAlive();
  const [state, setState] = useState<{ status: "idle" | "loading" | "ready" | "error"; files: ChatArtifact[]; error: ChatError | null }>({
    status: "idle",
    files: [],
    error: null,
  });

  const load = () => {
    if (state.status === "loading") return;
    setState({ status: "loading", files: [], error: null });
    chatApi.artifacts(taskId).then(
      (files) => {
        if (alive.current) setState({ status: "ready", files, error: null });
      },
      (err: unknown) => {
        if (alive.current) setState({ status: "error", files: [], error: toChatError(err, "files") });
      },
    );
  };

  if (state.status === "ready") {
    if (!state.files.length) return <p className="mt-2 text-xs text-neutral-500">{t("botChat.files.none")}</p>;
    return (
      <div className="mt-3">
        <div className="mb-1.5 text-xs font-medium text-neutral-500">{t("botChat.files.title")}</div>
        <ul className="flex flex-col gap-1.5">
          {state.files.map((file) => (
            <li key={String(file.id)} className="bc-rise flex max-w-full min-w-0 items-stretch">
              <button
                type="button"
                onClick={() => onPreview({ name: file.name, url: chatApi.artifactUrl(taskId, file.id) })}
                aria-label={t("botChat.files.preview", { name: file.name })}
                className="inline-flex min-w-0 items-center gap-2 rounded-l-lg border border-neutral-200 bg-card px-3 py-1.5 text-left text-[13px] font-medium text-neutral-800 shadow-control transition-colors hover:border-neutral-300 hover:bg-neutral-50 dark:border-white/10 dark:hover:border-white/20"
              >
                <FileText size={14} aria-hidden className="shrink-0 text-neutral-500" />
                <span className="min-w-0 break-all">{file.name}</span>
              </button>
              <a
                href={chatApi.artifactUrl(taskId, file.id)}
                download={baseName(file.name)}
                aria-label={t("botChat.files.download", { name: file.name })}
                title={t("botChat.files.download", { name: file.name })}
                className="-ml-px inline-flex shrink-0 items-center rounded-r-lg border border-neutral-200 bg-card px-2.5 text-neutral-500 shadow-control transition-colors hover:border-neutral-300 hover:bg-neutral-50 hover:text-neutral-900 dark:border-white/10 dark:hover:border-white/20"
              >
                <Download size={14} aria-hidden />
              </a>
            </li>
          ))}
        </ul>
      </div>
    );
  }
  return (
    <div className="mt-2 flex flex-wrap items-center gap-2">
      <Button type="button" variant="ghost" size="sm" className="-ml-2 h-7 px-2 text-xs font-medium" disabled={state.status === "loading"} onClick={load}>
        {state.status === "loading" ? <Spinner size={13} /> : <FileDown size={13} aria-hidden />}
        {state.status === "loading" ? t("botChat.files.loading") : t("botChat.files.show")}
      </Button>
      {state.error && (
        <span role="alert" className="text-xs text-red-600 dark:text-red-400">
          {t("botChat.files.failed", { reason: t(`botChat.errors.${state.error.key}`) })}
        </span>
      )}
    </div>
  );
}

function Turn({ turn, bot, onReuse, onPreview }: { turn: ChatTurn; bot: ChatBot; onReuse?: (content: string) => void; onPreview: (file: PreviewFile) => void }) {
  const { t, i18n } = useTranslation();
  const pending = isPendingTurn(turn);
  const stopped = turn.status === "failed" || turn.status === "canceled";
  return (
    <li className="bc-rise min-w-0" data-turn={String(turn.id)}>
      <div className="flex flex-col items-end">
        <span className="sr-only">{t("botChat.thread.you")}</span>
        <div className="max-w-[88%] rounded-2xl rounded-br-md bg-neutral-100 px-4 py-2.5 text-sm leading-6 break-words whitespace-pre-wrap text-neutral-900 [overflow-wrap:anywhere] sm:max-w-[80%]">
          {turn.content}
        </div>
        <time dateTime={turn.created_at} className="mt-1 text-[11px] text-neutral-400">
          {formatTimeOnly(turn.created_at, i18n.language)}
        </time>
      </div>
      <div className="mt-3 flex min-w-0 gap-2.5 sm:gap-3">
        <BotWhale seed={bot.id} size={28} active={pending} />
        {/* key 跟着状态走：排队 → 处理中 → 完成时淡入新内容，而不是生硬地换掉 */}
        <div key={turn.status} className="bc-fade min-w-0 flex-1 pt-0.5 text-sm leading-6 text-neutral-800">
          <span className="sr-only">{bot.name}</span>
          {pending && (
            <div role="status" className="flex items-center gap-2.5 text-sm text-neutral-500">
              <TypingDots />
              <span>{t(turn.status === "queued" ? "botChat.reply.queued" : "botChat.reply.running")}</span>
            </div>
          )}
          {turn.status === "succeeded" &&
            (turn.summary.trim() ? <ChatMarkdown markdown={turn.summary} /> : <p className="text-sm text-neutral-500">{t("botChat.reply.noText")}</p>)}
          {turn.status === "succeeded" && <TurnFiles taskId={turn.id} onPreview={onPreview} />}
          {stopped && (
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
              <span className={cx("inline-flex items-start gap-1.5", turn.status === "failed" ? "text-red-600 dark:text-red-400" : "text-neutral-500")}>
                <CircleAlert size={15} aria-hidden className="mt-[3px] shrink-0" />
                {t(turn.status === "failed" ? "botChat.reply.failed" : "botChat.reply.canceled")}
              </span>
              {onReuse && (
                <button
                  type="button"
                  onClick={() => onReuse(turn.content)}
                  className="rounded text-[13px] font-medium text-brand-600 underline-offset-2 hover:underline dark:text-brand-300"
                >
                  {t("botChat.reply.reuse")}
                </button>
              )}
            </div>
          )}
        </div>
      </div>
    </li>
  );
}

// ---------- 右栏：机器人资料 ----------

type ProfileTab = "activity" | "library";
const PROFILE_TABS: ProfileTab[] = ["activity", "library"];

function ProfilePane({
  id,
  bot,
  state,
  open,
  replying,
  closeRef,
  onClose,
  onRetry,
  onPreview,
}: {
  id: string;
  bot: ChatBot;
  state: ProfileState | undefined;
  open: boolean;
  replying: boolean;
  closeRef: RefObject<HTMLButtonElement | null>;
  onClose: () => void;
  onRetry: () => void;
  onPreview: (file: PreviewFile) => void;
}) {
  const { shown, leaving } = usePresence(open);
  const { t, i18n } = useTranslation();
  const lang = i18n.language;
  const [tab, setTab] = useState<ProfileTab>("activity");
  const tabRefs = useRef<Partial<Record<ProfileTab, HTMLButtonElement | null>>>({});
  const errText = (e: ChatError) => t(`botChat.errors.${e.key}`);
  const data = state?.data ?? null;
  const localTime = data?.schedule ? dailyLocalTime(data.schedule, lang) : null;

  const onTabKey = (e: KeyboardEvent<HTMLButtonElement>) => {
    if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
    e.preventDefault();
    const next = PROFILE_TABS[(PROFILE_TABS.indexOf(tab) + (e.key === "ArrowRight" ? 1 : PROFILE_TABS.length - 1)) % PROFILE_TABS.length];
    setTab(next);
    tabRefs.current[next]?.focus();
  };

  return (
    <aside
      id={id}
      aria-label={t("botChat.profile.label")}
      data-bot-profile=""
      // 收起时退场动画播放期间已经不可交互，也不在无障碍树里
      aria-hidden={leaving || undefined}
      inert={leaving || undefined}
      className={cx(
        "absolute inset-0 z-20 min-h-0 flex-col bg-card xl:static xl:z-auto xl:w-72 xl:shrink-0 xl:border-l xl:border-neutral-200 dark:xl:border-white/10",
        shown ? "flex" : "hidden",
        open && "bc-panel-in",
        leaving && "bc-panel-out",
      )}
    >
      <div className="flex justify-end px-3 pt-3">
        <button
          ref={closeRef}
          type="button"
          aria-label={t("botChat.profile.close")}
          title={t("botChat.profile.close")}
          onClick={onClose}
          className="rounded-md p-1.5 text-neutral-500 transition-colors hover:bg-neutral-100 hover:text-neutral-900"
        >
          <X size={18} aria-hidden />
        </button>
      </div>
      <div className="flex flex-col items-center px-5 pb-4 text-center">
        <BotWhale key={String(bot.id)} seed={bot.id} size={96} active={replying} className="bc-pop" />
        <h2 className="mt-3 max-w-full text-lg leading-7 font-bold tracking-[-0.01em] break-words text-neutral-900">{bot.name}</h2>
      </div>
      <div role="tablist" aria-label={t("botChat.profile.label")} className={cx(tabListCls, "justify-center px-5 dark:border-white/10")}>
        {PROFILE_TABS.map((key) => (
          <button
            key={key}
            ref={(el) => {
              tabRefs.current[key] = el;
            }}
            type="button"
            role="tab"
            id={`${id}-tab-${key}`}
            aria-selected={tab === key}
            aria-controls={`${id}-panel-${key}`}
            tabIndex={tab === key ? 0 : -1}
            onClick={() => setTab(key)}
            onKeyDown={onTabKey}
            className={tabCls(tab === key)}
          >
            {t(`botChat.profile.tabs.${key}`)}
          </button>
        ))}
      </div>
      <div role="tabpanel" id={`${id}-panel-${tab}`} aria-labelledby={`${id}-tab-${tab}`} className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
        {(!state || state.status === "loading") && (
          <div role="status" className="flex items-center justify-center gap-2 py-8 text-[13px] text-neutral-500">
            <Spinner size={14} className="text-neutral-400" />
            {t("botChat.profile.loading")}
          </div>
        )}
        {state?.status === "error" && state.error && (
          <div className="py-6 text-center">
            <p role="alert" className="text-[13px] leading-5 text-neutral-600">
              {t("botChat.profile.failed", { reason: errText(state.error) })}
            </p>
            <Button type="button" variant="secondary" size="sm" className="mt-3" onClick={onRetry}>
              {t("botChat.retry")}
            </Button>
          </div>
        )}
        {state?.status === "ready" && data && (
          <>
            {state.refreshError && (
              <Notice tone="warn" role="status" className="mb-3 text-[13px]">
                <span className="mr-2">{t("botChat.profile.refreshFailed", { reason: errText(state.refreshError) })}</span>
                <button type="button" className="font-medium underline-offset-2 hover:underline" onClick={onRetry}>
                  {t("botChat.retry")}
                </button>
              </Notice>
            )}
            {tab === "activity" && (
              <div className="flex flex-col gap-3">
                <section className="rounded-lg border border-neutral-200 p-3.5 dark:border-white/10" data-profile-schedule="">
                  <div className="flex items-start gap-2.5">
                    <CalendarClock size={16} aria-hidden className="mt-0.5 shrink-0 text-neutral-500" />
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <h3 className="text-[13px] font-semibold text-neutral-900">{t("botChat.profile.activity.scheduleTitle")}</h3>
                        {data.schedule && (
                          <Badge tone={data.schedule_enabled ? "green" : "slate"}>
                            {t(data.schedule_enabled ? "botChat.profile.activity.on" : "botChat.profile.activity.off")}
                          </Badge>
                        )}
                      </div>
                      {data.schedule ? (
                        <>
                          {localTime && <p className="mt-1 text-sm text-neutral-800">{t("botChat.profile.activity.scheduleAt", { time: localTime })}</p>}
                          <p className="mt-1 text-xs break-words text-neutral-500">
                            {t("botChat.profile.activity.scheduleCron", { cron: data.schedule })}
                          </p>
                        </>
                      ) : (
                        <p className="mt-1 text-[13px] text-neutral-500">{t("botChat.profile.activity.scheduleNone")}</p>
                      )}
                    </div>
                  </div>
                  <p className="mt-3 flex items-start gap-1.5 border-t border-neutral-200 pt-2.5 text-xs leading-5 text-neutral-500 dark:border-white/10">
                    <Lock size={12} aria-hidden className="mt-1 shrink-0" />
                    {t("botChat.profile.activity.readonly")}
                  </p>
                </section>
                <section className="rounded-lg border border-neutral-200 p-3.5 dark:border-white/10" data-profile-computer="">
                  <div className="flex items-start gap-2.5">
                    <Monitor size={16} aria-hidden className="mt-0.5 shrink-0 text-neutral-500" />
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <h3 className="text-[13px] font-semibold text-neutral-900">{t("botChat.profile.activity.computerTitle")}</h3>
                        <span className="inline-flex items-center gap-1.5 text-xs font-medium text-neutral-700">
                          <span aria-hidden className={cx("h-2 w-2 rounded-full", data.running ? "bg-emerald-500" : "bg-neutral-300 dark:bg-white/25")} />
                          {t(data.running ? "botChat.profile.activity.running" : "botChat.profile.activity.asleep")}
                        </span>
                      </div>
                      <p className="mt-1 text-xs leading-5 text-neutral-500">
                        {t(data.running ? "botChat.profile.activity.runningDesc" : "botChat.profile.activity.asleepDesc")}
                      </p>
                    </div>
                  </div>
                </section>
              </div>
            )}
            {tab === "library" && (
              <>
                <p className="mb-3 text-xs leading-5 text-neutral-500">{t("botChat.profile.library.hint")}</p>
                {data.artifacts.length ? (
                  <ul className="flex flex-col gap-2" data-profile-library="">
                    {data.artifacts.map((file) => {
                      const url = chatApi.artifactUrl(file.task_id, file.id);
                      return (
                        <li
                          key={`${String(file.task_id)}:${String(file.id)}`}
                          className="bc-rise flex min-w-0 items-stretch rounded-lg border border-neutral-200 bg-card shadow-control transition-colors hover:border-neutral-300 dark:border-white/10 dark:hover:border-white/20"
                        >
                          <button
                            type="button"
                            onClick={() => onPreview({ name: file.name, url, size: file.size })}
                            aria-label={t("botChat.files.preview", { name: file.name })}
                            className="flex min-w-0 flex-1 items-start gap-2.5 rounded-l-lg px-3 py-2.5 text-left transition-colors hover:bg-neutral-50"
                          >
                            <FileText size={16} aria-hidden className="mt-0.5 shrink-0 text-brand-500" />
                            <span className="min-w-0 flex-1">
                              <span className="block text-[13px] font-medium break-all text-neutral-800">{file.name}</span>
                              <span className="mt-0.5 flex flex-wrap gap-x-2 text-xs text-neutral-500">
                                <span>{t(file.source === "task" ? "botChat.profile.library.fromTask" : "botChat.profile.library.fromChat")}</span>
                                {typeof file.size === "number" && <span>{formatBytes(file.size, lang)}</span>}
                                {file.created_at && <time dateTime={file.created_at}>{formatDateTime(file.created_at, lang)}</time>}
                              </span>
                            </span>
                          </button>
                          <a
                            href={url}
                            download={baseName(file.name)}
                            aria-label={t("botChat.files.download", { name: file.name })}
                            title={t("botChat.files.download", { name: file.name })}
                            className="flex shrink-0 items-center rounded-r-lg border-l border-neutral-200 px-2.5 text-neutral-400 transition-colors hover:bg-neutral-50 hover:text-neutral-900 dark:border-white/10"
                          >
                            <Download size={14} aria-hidden />
                          </a>
                        </li>
                      );
                    })}
                  </ul>
                ) : (
                  <p className="py-8 text-center text-[13px] leading-5 text-neutral-500">{t("botChat.profile.library.empty")}</p>
                )}
              </>
            )}
          </>
        )}
      </div>
    </aside>
  );
}

// ---------- 工作区 ----------

function Workspace({
  userId,
  bots,
  reloadBots,
  botsRefreshing,
  botsError,
}: {
  userId: number;
  bots: ChatBot[];
  reloadBots: () => void;
  botsRefreshing: boolean;
  botsError: ChatError | null;
}) {
  const { t, i18n } = useTranslation();
  const lang = i18n.language;
  const alive = useAlive();
  const composerId = useId();
  const profileId = useId();
  const [params, setParams] = useSearchParams();
  const botParam = params.get("bot");
  const convParam = params.get("conversation");
  const botsRef = useRef(bots);
  botsRef.current = bots;

  // ---------- 选中的机器人 ----------
  // 旧链接 ?conversation=<id>：先问一次它属于哪个机器人，再换成 ?bot=；读不到就回到默认的第一个。
  const [resolving, setResolving] = useState(() => !botParam && !!convParam);
  useEffect(() => {
    if (!resolving || !convParam) return;
    let stopped = false;
    chatApi
      .conversation(convParam)
      .then(
        (d) => String(d.conversation.bot_id),
        () => null,
      )
      .then((botId) => {
        if (stopped || !alive.current) return;
        const target = botId !== null && botsRef.current.some((b) => sameId(b.id, botId)) ? botId : null;
        setParams(target !== null ? { bot: target } : {}, { replace: true });
        setResolving(false);
      });
    return () => {
      stopped = true;
    };
  }, [resolving, convParam, alive, setParams]);

  const selected = resolving ? null : (bots.find((b) => sameId(b.id, botParam)) ?? bots[0] ?? null);
  const selKey = selected ? String(selected.id) : "";
  const selKeyRef = useRef(selKey);
  selKeyRef.current = selKey;

  const [listOpen, setListOpen] = useState(false);
  const [preview, setPreview] = useState<PreviewFile | null>(null);
  // 全屏：盖住系统侧栏和顶栏，只留机器人列表、对话和资料；刷新后保持（本机、按账号）
  const [fullscreen, setFullscreen] = useState(() => getFullscreen(userId));
  useEffect(() => saveFullscreen(userId, fullscreen), [userId, fullscreen]);
  useEffect(() => {
    if (!fullscreen) return;
    // 后面的页面不再跟着滚动
    const root = document.documentElement;
    const prev = root.style.overflow;
    root.style.overflow = "hidden";
    return () => {
      root.style.overflow = prev;
    };
  }, [fullscreen]);
  const [profileOpen, setProfileOpen] = useState(() => isWide(PROFILE_STATIC));
  const profileOpenRef = useRef(profileOpen);
  profileOpenRef.current = profileOpen;
  const pillRef = useRef<HTMLButtonElement>(null);
  const listOpenRef = useRef<HTMLButtonElement>(null);
  const profileCloseRef = useRef<HTMLButtonElement>(null);
  const radioRefs = useRef<Record<string, HTMLButtonElement | null>>({});

  const selectBot = (bot: ChatBot) => {
    if (!sameId(bot.id, selKey)) setParams({ bot: String(bot.id) });
    setListOpen(false);
  };

  // ---------- 每个机器人的对话 ----------
  const [threads, setThreads] = useState<Record<string, Thread>>({});
  const threadsRef = useRef(threads);
  threadsRef.current = threads;
  const ctls = useRef<Record<string, ThreadCtl>>({});
  const ctlOf = useCallback((key: string) => (ctls.current[key] ??= { convId: null, skipListed: false, inflight: null, version: 0, earlier: false }), []);

  const stick = useRef(true);
  const scrollRef = useRef<HTMLDivElement>(null);
  /** 往前补了更早的消息：记下离底部的距离，渲染后恢复，画面不跳。 */
  const anchor = useRef<number | null>(null);

  /** 读最近一页（首次打开、刷新、轮询都走这里）；同一个机器人同时只有一个请求。 */
  const loadThread = useCallback(
    (bot: ChatBot): Promise<void> => {
      const key = String(bot.id);
      const ctl = ctlOf(key);
      if (ctl.inflight) return ctl.inflight;
      const version = ctl.version;
      setThreads((s) => (s[key] ? s : { ...s, [key]: LOADING_THREAD }));
      const run = async () => {
        let scope: ChatErrorScope = "runtime";
        try {
          let convId = ctl.convId ?? (!ctl.skipListed && bot.thread_id != null ? String(bot.thread_id) : null);
          if (!convId) {
            // 还没聊过：用幂等的创建接口取得这条对话，不会多出第二条
            const c = await chatApi.createConversation(bot.id);
            if (c?.id == null) throw new Error("missing thread id");
            convId = String(c.id);
          }
          scope = "conversation";
          const d = await chatApi.conversation(convId);
          ctl.convId = convId;
          if (!alive.current || version !== ctl.version) return;
          const id = convId;
          setThreads((s) => {
            const prev = s[key];
            // 已经往前翻过的更早消息留着，最近一页用新结果覆盖
            const kept = prev?.status === "ready" ? prev.messages.filter((m) => !d.messages.some((x) => sameId(x.id, m.id))) : [];
            return {
              ...s,
              [key]: {
                status: "ready",
                convId: id,
                messages: [...kept, ...d.messages],
                nextBefore: kept.length && prev ? prev.nextBefore : (d.next_before ?? null),
                earlierLoading: prev?.earlierLoading ?? false,
                earlierError: prev?.earlierError ?? null,
                error: null,
                refreshError: null,
              },
            };
          });
        } catch (err) {
          if (!alive.current) return;
          const error = toChatError(err, scope);
          if (scope === "conversation" && error.key === "notFound") {
            ctl.convId = null;
            ctl.skipListed = true;
          }
          if (error.key === "botUnavailable") reloadBots();
          setThreads((s) => {
            const prev = s[key];
            return { ...s, [key]: prev?.status === "ready" ? { ...prev, refreshError: error } : { ...LOADING_THREAD, status: "error", error } };
          });
        }
      };
      const promise: Promise<void> = run().finally(() => {
        if (ctl.inflight === promise) ctl.inflight = null;
      });
      ctl.inflight = promise;
      return promise;
    },
    [alive, ctlOf, reloadBots],
  );

  /** 读更早的一页，接在前面。 */
  const loadEarlier = useCallback(
    (key: string) => {
      const ctl = ctlOf(key);
      const th = threadsRef.current[key];
      if (ctl.earlier || !th || th.status !== "ready" || th.nextBefore == null || !th.convId) return;
      ctl.earlier = true;
      setThreads((s) => (s[key] ? { ...s, [key]: { ...s[key], earlierLoading: true, earlierError: null } } : s));
      chatApi
        .conversation(th.convId, th.nextBefore)
        .then(
          (d) => {
            if (!alive.current) return;
            const el = scrollRef.current;
            if (el && selKeyRef.current === key) anchor.current = el.scrollHeight - el.scrollTop;
            setThreads((s) => {
              const prev = s[key];
              if (!prev || prev.status !== "ready") return s;
              const older = d.messages.filter((m) => !prev.messages.some((x) => sameId(x.id, m.id)));
              // 这一页没带来任何新内容就当作到头了，避免对着同一个 before 反复请求
              return { ...s, [key]: { ...prev, messages: [...older, ...prev.messages], nextBefore: older.length ? (d.next_before ?? null) : null, earlierLoading: false } };
            });
          },
          (err: unknown) => {
            if (!alive.current) return;
            const error = toChatError(err, "conversation");
            setThreads((s) => (s[key] ? { ...s, [key]: { ...s[key], earlierLoading: false, earlierError: error } } : s));
          },
        )
        .finally(() => {
          ctl.earlier = false;
        });
    },
    [alive, ctlOf],
  );

  // 选中机器人：没读过就读；读过的先显示原样（同一条对话），后台刷新一次。
  useEffect(() => {
    if (!selKey) return;
    const bot = botsRef.current.find((b) => sameId(b.id, selKey));
    if (bot) void loadThread(bot);
  }, [selKey, loadThread]);

  // 机器人列表刷新后：已经读过的对话如果和列表里的最新回复对不上，补读一次。
  useEffect(() => {
    for (const b of bots) {
      const th = threadsRef.current[String(b.id)];
      if (!th || th.status !== "ready" || b.last_task_id == null) continue;
      const last = th.messages[th.messages.length - 1];
      if (!last || !sameId(last.id, b.last_task_id) || (b.last_status && b.last_status !== last.status)) void loadThread(b);
    }
  }, [bots, loadThread]);

  // 只在有排队 / 运行中的回复时轮询（切到别的机器人后，原来那条也继续等回复）；
  // 上一轮读取都结束后才排下一轮，页面在后台时跳过。
  const pendingKeys = bots
    .filter((b) => {
      const th = threads[String(b.id)];
      return th?.status === "ready" && th.messages.some(isPendingTurn);
    })
    .map((b) => String(b.id))
    .join("\n");
  useEffect(() => {
    if (!pendingKeys) return;
    const keys = pendingKeys.split("\n");
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const tick = async () => {
      if (stopped) return;
      if (!document.hidden) {
        await Promise.all(
          keys.map((key) => {
            const bot = botsRef.current.find((b) => sameId(b.id, key));
            return bot ? loadThread(bot) : undefined;
          }),
        );
      }
      if (!stopped) timer = setTimeout(() => void tick(), POLL_MS);
    };
    timer = setTimeout(() => void tick(), POLL_MS);
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [pendingKeys, loadThread]);

  const thread = selKey ? threads[selKey] : undefined;
  const messages = thread?.messages ?? [];
  const ready = thread?.status === "ready";
  const replying = ready && messages.some(isPendingTurn);
  const botName = selected?.name || t("botChat.unknownBot");

  /** 打开失败后的重试：先回到加载态，再读一次。 */
  const reopen = () => {
    if (!selected) return;
    setThreads((s) => ({ ...s, [selKey]: LOADING_THREAD }));
    void loadThread(selected);
  };

  // ---------- 预览与未读 ----------
  const [reads, setReads] = useState<Record<string, string>>({});
  const readOf = (key: string) => reads[key] ?? getLastRead(userId, key);

  /** 最近一条：读过对话就以对话为准，否则用机器人列表里带的。 */
  const latestOf = (b: ChatBot): { id: ChatId | null; status: ChatTurnStatus | null; text: string } | null => {
    const th = threads[String(b.id)];
    const last = th?.status === "ready" ? th.messages[th.messages.length - 1] : undefined;
    if (last) return { id: last.id, status: last.status, text: last.status === "succeeded" ? last.summary : last.content };
    if (b.last_task_id != null || b.last_message) return { id: b.last_task_id ?? null, status: b.last_status ?? null, text: b.last_message ?? "" };
    return null;
  };
  const previewOf = (b: ChatBot): string => {
    const latest = latestOf(b);
    if (!latest) return t("botChat.preview.empty");
    if (latest.status === "queued" || latest.status === "running") return t("botChat.preview.replying");
    if (latest.status === "failed") return t("botChat.preview.failed");
    if (latest.status === "canceled") return t("botChat.preview.canceled");
    return plainText(latest.text) || t(latest.status === "succeeded" ? "botChat.preview.noText" : "botChat.preview.empty");
  };
  /** 未读：有一条真实完成的回复，且本机这个账号还没在这个机器人的对话里看到过它。 */
  const unreadOf = (b: ChatBot): boolean => {
    if (sameId(b.id, selKey)) return false;
    const latest = latestOf(b);
    return !!latest && latest.id != null && latest.status === "succeeded" && readOf(String(b.id)) !== String(latest.id);
  };

  // 正在看的对话：把读到的位置记到本机
  const lastDone = ready ? [...messages].reverse().find((m) => !isPendingTurn(m)) : undefined;
  const lastDoneId = lastDone ? String(lastDone.id) : "";
  useEffect(() => {
    if (!selKey || !lastDoneId) return;
    setLastRead(userId, selKey, lastDoneId);
    setReads((r) => (r[selKey] === lastDoneId ? r : { ...r, [selKey]: lastDoneId }));
  }, [userId, selKey, lastDoneId]);

  // ---------- 机器人资料 ----------
  const [profiles, setProfiles] = useState<Record<string, ProfileState>>({});
  const profilesRef = useRef(profiles);
  profilesRef.current = profiles;
  const profileInflight = useRef<Record<string, Promise<void> | undefined>>({});

  /** 同一个机器人同时只读一次：已有读取在途时返回那一次。 */
  const loadProfile = useCallback(
    (bot: ChatBot): Promise<void> => {
      const key = String(bot.id);
      const pending = profileInflight.current[key];
      if (pending) return pending;
      setProfiles((s) => (s[key]?.status === "ready" ? s : { ...s, [key]: { status: "loading", data: null, error: null, refreshError: null } }));
      const p: Promise<void> = chatApi
        .profile(bot.id)
        .then(
          (data) => {
            if (!alive.current) return;
            setProfiles((s) => {
              // 内容没变就不动状态，轮询时不引起重绘
              const prev = s[key];
              if (prev?.status === "ready" && !prev.refreshError && JSON.stringify(prev.data) === JSON.stringify(data)) return s;
              return { ...s, [key]: { status: "ready", data, error: null, refreshError: null } };
            });
          },
          (err: unknown) => {
            if (!alive.current) return;
            const error = toChatError(err, "files");
            setProfiles((s) => ({
              ...s,
              [key]: s[key]?.status === "ready" ? { ...s[key], refreshError: error } : { status: "error", data: null, error, refreshError: null },
            }));
          },
        )
        .finally(() => {
          if (profileInflight.current[key] === p) profileInflight.current[key] = undefined;
        });
      profileInflight.current[key] = p;
      return p;
    },
    [alive],
  );

  // 打开资料、或换了机器人：读一次（已有内容时原地刷新）；之后只要资料还开着就定时刷新，
  // 这样回复进行中、或定时任务在跑时，云端电脑状态不会一直停在打开那一刻。
  // 和对话轮询一样：上一轮结束后才排下一轮，页面在后台时跳过；首次读取失败停在错误态时不自动重试。
  useEffect(() => {
    if (!profileOpen || !selKey) return;
    const current = () => botsRef.current.find((b) => sameId(b.id, selKey));
    const first = current();
    if (first) void loadProfile(first);
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const tick = async () => {
      if (stopped) return;
      const bot = current();
      if (bot && !document.hidden && profilesRef.current[selKey]?.status === "ready") await loadProfile(bot);
      if (!stopped) timer = setTimeout(() => void tick(), PROFILE_POLL_MS);
    };
    timer = setTimeout(() => void tick(), PROFILE_POLL_MS);
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [profileOpen, selKey, loadProfile]);

  // 一次回复刚结束：云端电脑状态和文件库可能变了，资料开着就刷新一下
  const wasReplying = useRef("");
  useEffect(() => {
    if (replying) {
      wasReplying.current = selKey;
      return;
    }
    if (!selKey || wasReplying.current !== selKey) return;
    wasReplying.current = "";
    const bot = botsRef.current.find((b) => sameId(b.id, selKey));
    if (bot && profileOpenRef.current) void loadProfile(bot);
  }, [replying, selKey, loadProfile]);

  const openList = () => {
    setProfileOpen((v) => (isWide(PROFILE_STATIC) ? v : false));
    setListOpen(true);
  };
  const closeList = () => {
    setListOpen(false);
    listOpenRef.current?.focus();
  };
  const toggleProfile = () => {
    setListOpen(false);
    setProfileOpen((v) => !v);
  };
  const closeProfile = () => {
    setProfileOpen(false);
    pillRef.current?.focus();
  };

  // 窄屏上资料是浮层：打开后把焦点交给关闭按钮
  useEffect(() => {
    if (profileOpen && selKey && !isWide(PROFILE_STATIC)) profileCloseRef.current?.focus();
  }, [profileOpen, selKey]);

  // Esc 只关浮层；宽屏上常驻的栏不受影响（输入法里按 Esc 也不该把它关掉）
  useEffect(() => {
    if (!listOpen && !profileOpen && !fullscreen) return;
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.key !== "Escape" || e.isComposing || e.defaultPrevented) return;
      if (listOpen && !isWide(LIST_STATIC)) {
        setListOpen(false);
        listOpenRef.current?.focus();
      } else if (profileOpen && !isWide(PROFILE_STATIC)) {
        setProfileOpen(false);
        pillRef.current?.focus();
      } else if (fullscreen && !(e.target instanceof HTMLTextAreaElement) && !document.querySelector("[data-file-preview]")) {
        setFullscreen(false);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [listOpen, profileOpen, fullscreen]);

  // ---------- 输入与发送 ----------
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [sending, setSending] = useState<string | null>(null);
  const [sendError, setSendError] = useState<{ bot: string; error: ChatError } | null>(null);
  /** 发送时后端明确说这个机器人已停用（bot_disabled）。 */
  const [blocked, setBlocked] = useState<string[]>([]);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const idem = useRef<{ bot: string; content: string; key: string } | null>(null);

  const draft = drafts[selKey] ?? "";
  const setDraft = (value: string) => setDrafts((d) => ({ ...d, [selKey]: value }));
  const content = draft.trim();
  const bytes = useMemo(() => utf8Bytes(content), [content]);
  const tooLong = bytes > CHAT_CONTENT_MAX_BYTES;

  const botDisabled = ready && blocked.includes(selKey);
  const isSending = sending === selKey && !!selKey;
  const canSend = ready && !!thread?.convId && !botDisabled && sending === null && !replying && content.length > 0 && !tooLong;

  const send = async () => {
    if (!canSend || !selected || !thread?.convId) return;
    const bot = selected;
    const key = selKey;
    const conv = thread.convId;
    const ctl = ctlOf(key);
    const reuse = idem.current;
    const idemKey = reuse && reuse.bot === key && reuse.content === content ? reuse.key : newIdempotencyKey();
    idem.current = { bot: key, content, key: idemKey };
    setSending(key);
    setSendError(null);
    try {
      const turn = await chatApi.send(conv, content, idemKey);
      if (idem.current?.key === idemKey) idem.current = null;
      if (!alive.current) return;
      // 等待期间输入框里换了内容（例如点了「重新编辑」）：那是新草稿，原样留着
      setDrafts((d) => ((d[key] ?? "").trim() === content ? { ...d, [key]: "" } : d));
      // 发送前发出的读取不含这条消息，回来后会被 version 丢弃；等它结束后补读一次
      // （loadThread 自带去重，不会和轮询重叠）。
      const pendingRead = ctl.inflight;
      ctl.version++;
      if (selKeyRef.current === key) stick.current = true;
      setThreads((s) => {
        const prev = s[key];
        if (!prev || prev.status !== "ready") return s;
        return { ...s, [key]: { ...prev, refreshError: null, messages: [...prev.messages.filter((m) => !sameId(m.id, turn.id)), turn] } };
      });
      if (pendingRead) {
        void pendingRead.then(() => {
          if (alive.current) void loadThread(bot);
        });
      }
    } catch (err) {
      const error = toChatError(err, "conversation");
      // 这个键后端不再接受：下次重试换一个新的
      if (error.key === "conflict" && idem.current?.key === idemKey) idem.current = null;
      if (!alive.current) return;
      setSendError({ bot: key, error });
      if (error.key === "botDisabled") {
        setBlocked((b) => (b.includes(key) ? b : [...b, key]));
        reloadBots();
      }
      // 已有一条在处理（例如另一个标签页发的）：读一次最新记录，轮询随之接上
      if (error.key === "busy") void loadThread(bot);
    } finally {
      if (alive.current) setSending(null);
    }
  };

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    void send();
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    // 输入法组词时的回车是「上屏」，不是发送
    if (e.key !== "Enter" || e.shiftKey || e.nativeEvent.isComposing || e.keyCode === 229) return;
    e.preventDefault();
    void send();
  };

  // 输入框随内容长高，超过上限后内部滚动
  useLayoutEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 192)}px`;
  }, [draft, selKey, ready]);

  const reuseContent = (text: string) => {
    setDraft(text);
    inputRef.current?.focus();
  };

  // ---------- 滚动 ----------
  const first = messages[0];
  const last = messages[messages.length - 1];
  const tail = `${selKey}:${thread?.status ?? ""}:${messages.length}:${first ? String(first.id) : ""}:${last ? String(last.id) : ""}:${last?.status ?? ""}:${last?.summary.length ?? 0}`;
  const scrolledFor = useRef("");
  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (scrolledFor.current !== selKey) {
      // 换了机器人：回到「跟随最新消息」
      scrolledFor.current = selKey;
      stick.current = true;
      anchor.current = null;
    }
    if (!el) return;
    if (anchor.current != null) {
      el.scrollTop = el.scrollHeight - anchor.current;
      anchor.current = null;
    } else if (stick.current) {
      el.scrollTop = el.scrollHeight;
    }
  }, [selKey, tail]);
  const onScroll = () => {
    const el = scrollRef.current;
    if (!el) return;
    stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < STICK_PX;
    if (selKey && el.scrollTop < EARLIER_PX && el.scrollHeight > el.clientHeight) loadEarlier(selKey);
  };

  const errText = (e: ChatError) => t(`botChat.errors.${e.key}`);
  const activeSendError = sendError && sendError.bot === selKey ? sendError.error : null;

  // 机器人列表的手动刷新：左栏和「暂无机器人」的欢迎区各放一个，没有机器人时也看得到
  const botsRefresh = (className?: string) => (
    <Button type="button" variant="ghost" size="sm" className={className} disabled={botsRefreshing} onClick={reloadBots} data-bot-refresh="">
      <RefreshCw size={14} aria-hidden className={cx(botsRefreshing && "animate-spin motion-reduce:animate-none")} />
      {botsRefreshing ? t("botChat.list.refreshingBots") : t("botChat.list.refreshBots")}
    </Button>
  );
  const botsRefreshError = botsError && !botsRefreshing && (
    <p role="alert" className="text-[13px] leading-5 text-red-600 dark:text-red-400">
      {t("botChat.list.refreshBotsFailed", { reason: errText(botsError) })}
    </p>
  );

  /** 单选组的方向键：移到上 / 下一个机器人并选中。 */
  const onRadioKey = (e: KeyboardEvent<HTMLButtonElement>, index: number) => {
    const step = e.key === "ArrowDown" || e.key === "ArrowRight" ? 1 : e.key === "ArrowUp" || e.key === "ArrowLeft" ? -1 : 0;
    if (!step || !bots.length) return;
    e.preventDefault();
    const next = bots[(index + step + bots.length) % bots.length];
    if (!sameId(next.id, selKey)) setParams({ bot: String(next.id) });
    radioRefs.current[String(next.id)]?.focus();
  };

  // 日期分隔：每条消息前，日期和上一条不同就插一行
  let prevDay = "";

  return (
    <div
      data-bot-workspace=""
      data-fullscreen={fullscreen ? "true" : "false"}
      className={cx(
        "flex overflow-hidden bg-card",
        // 只能有一个定位类：fixed 和 relative 同时出现时后者会赢
        fullscreen
          ? "fixed inset-0 z-40 h-dvh"
          : "relative h-[calc(100dvh-8rem)] min-h-[30rem] rounded-xl border border-neutral-200 shadow-card lg:h-[calc(100dvh-6.5rem)] dark:border-white/10",
      )}
    >
      {/* 左：机器人列表。宽屏常驻，窄屏盖在对话区上 */}
      <aside
        aria-label={t("botChat.list.label")}
        className={cx(
          "absolute inset-0 z-20 min-h-0 flex-col bg-card lg:static lg:z-auto lg:w-64 lg:shrink-0 lg:border-r lg:border-neutral-200 dark:lg:border-white/10",
          listOpen ? "flex" : "hidden lg:flex",
        )}
      >
        <div className="flex items-center gap-2 px-4 pt-4 pb-1">
          <WhaleMark size={16} className="shrink-0 text-brand-500" />
          <h1 className="min-w-0 flex-1 truncate text-xs font-semibold tracking-[0.04em] text-neutral-500/80">{t("botChat.title")}</h1>
          <button
            type="button"
            aria-label={t("botChat.list.close")}
            onClick={closeList}
            className="rounded-md p-1.5 text-neutral-500 transition-colors hover:bg-neutral-100 hover:text-neutral-900 lg:hidden"
          >
            <X size={18} aria-hidden />
          </button>
        </div>
        {selected && (
          <div key={selKey} className="bc-pop flex flex-col items-center px-4 pt-2 pb-4 text-center" data-bot-current="">
            <BotWhale seed={selected.id} size={72} active={replying} />
            <p className="mt-2.5 max-w-full truncate text-base leading-6 font-bold tracking-[-0.01em] text-neutral-900">
              <span className="sr-only">{t("botChat.list.current")}: </span>
              {selected.name}
            </p>
          </div>
        )}
        {bots.length ? (
          <div role="radiogroup" aria-label={t("botChat.list.pick")} className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto border-t border-neutral-200 px-2 py-2 dark:border-white/10">
            {bots.map((b, index) => {
              const key = String(b.id);
              const picked = sameId(b.id, selKey);
              const unread = unreadOf(b);
              const descId = `${composerId}-bot-${index}`;
              return (
                <button
                  key={key}
                  ref={(el) => {
                    radioRefs.current[key] = el;
                  }}
                  type="button"
                  role="radio"
                  aria-checked={picked}
                  aria-label={b.name}
                  aria-describedby={descId}
                  tabIndex={picked || (!selKey && index === 0) ? 0 : -1}
                  data-bot={key}
                  data-unread={unread ? "true" : "false"}
                  onClick={() => selectBot(b)}
                  onKeyDown={(e) => onRadioKey(e, index)}
                  className={cx(
                    "flex w-full min-w-0 items-center gap-2.5 rounded-lg px-2.5 py-2 text-left transition-colors",
                    picked ? "bg-brand-50 dark:bg-brand-500/15" : "hover:bg-neutral-100",
                  )}
                >
                  <BotWhale seed={b.id} size={36} active={pendingKeys.split("\n").includes(key)} />
                  <span className="min-w-0 flex-1">
                    <span className={cx("block truncate text-sm font-medium", picked ? "text-brand-600 dark:text-brand-200" : "text-neutral-800")}>{b.name}</span>
                    <span id={descId} className="mt-0.5 block truncate text-xs text-neutral-500">
                      {previewOf(b)}
                      {unread && <span className="sr-only"> · {t("botChat.list.unread")}</span>}
                    </span>
                  </span>
                  {unread && <span aria-hidden title={t("botChat.list.unread")} className="h-2 w-2 shrink-0 rounded-full bg-brand-500" />}
                </button>
              );
            })}
          </div>
        ) : (
          <p className="min-h-0 flex-1 border-t border-neutral-200 px-4 py-4 text-[13px] leading-5 text-neutral-500 dark:border-white/10">{t("botChat.list.noBots")}</p>
        )}
        <div className="space-y-1.5 border-t border-neutral-200 px-3 py-2.5 dark:border-white/10">
          {botsRefresh("w-full")}
          {botsRefreshError}
        </div>
      </aside>

      {/* 中：选中机器人的对话 */}
      <section className="flex min-w-0 flex-1 flex-col" aria-label={selected ? t("botChat.header.with", { bot: botName }) : t("botChat.title")}>
        <header className="flex items-center gap-2 border-b border-neutral-200 px-3 py-2.5 sm:px-4 dark:border-white/10">
          <button
            ref={listOpenRef}
            type="button"
            aria-label={t("botChat.list.open")}
            title={t("botChat.list.open")}
            onClick={openList}
            className="shrink-0 rounded-md p-2 text-neutral-600 transition-colors hover:bg-neutral-100 hover:text-neutral-900 lg:hidden"
          >
            <Users size={18} aria-hidden />
          </button>
          <div className="flex min-w-0 flex-1 items-center gap-2">
            {selected ? (
              <button
                ref={pillRef}
                type="button"
                aria-label={t("botChat.header.profile", { bot: botName })}
                aria-expanded={profileOpen}
                aria-controls={profileId}
                onClick={toggleProfile}
                data-bot-pill=""
                className={cx(
                  "inline-flex max-w-full min-w-0 items-center gap-2 rounded-full border py-1 pr-2.5 pl-1 text-sm font-semibold text-neutral-900 transition-colors",
                  profileOpen
                    ? "border-brand-200 bg-brand-50 dark:border-brand-400/30 dark:bg-brand-500/15"
                    : "border-neutral-200 bg-card hover:border-neutral-300 hover:bg-neutral-50 dark:border-white/10 dark:hover:border-white/20",
                )}
              >
                <BotWhale seed={selected.id} size={26} active={replying} />
                <span className="min-w-0 truncate">{botName}</span>
                <ChevronDown size={14} aria-hidden className={cx("shrink-0 text-neutral-500 transition-transform", profileOpen && "-rotate-90")} />
              </button>
            ) : (
              <h2 className="min-w-0 truncate px-1 text-base font-bold text-neutral-900">{t("botChat.title")}</h2>
            )}
            {botDisabled && <Badge tone="amber">{t("botChat.header.disabled")}</Badge>}
            {replying && (
              <span className="hidden sm:inline-flex">
                <span className="bc-fade inline-flex">
                  <Badge tone="brand">{t("botChat.header.replying")}</Badge>
                </span>
              </span>
            )}
          </div>
          {selected && (
            <button
              type="button"
              aria-label={t("botChat.refresh")}
              title={t("botChat.refresh")}
              disabled={thread?.status === "loading"}
              onClick={() => void loadThread(selected)}
              className="shrink-0 rounded-md p-2 text-neutral-500 transition-colors hover:bg-neutral-100 hover:text-neutral-900 disabled:opacity-50"
            >
              <RefreshCw size={16} aria-hidden className={cx(thread?.status === "loading" && "animate-spin motion-reduce:animate-none")} />
            </button>
          )}
          <button
            type="button"
            aria-pressed={fullscreen}
            aria-label={t(fullscreen ? "botChat.header.exitFullscreen" : "botChat.header.fullscreen")}
            title={t(fullscreen ? "botChat.header.exitFullscreen" : "botChat.header.fullscreen")}
            onClick={() => setFullscreen((v) => !v)}
            data-bot-fullscreen=""
            className="shrink-0 rounded-md p-2 text-neutral-500 transition-colors hover:bg-neutral-100 hover:text-neutral-900"
          >
            {fullscreen ? <Minimize2 size={16} aria-hidden /> : <Maximize2 size={16} aria-hidden />}
          </button>
        </header>

        <div ref={scrollRef} onScroll={onScroll} className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain px-3 py-5 sm:px-6">
          {!selected && !resolving && (
            <div className="mx-auto flex min-h-full max-w-xl flex-col items-center justify-center py-6 text-center">
              <span aria-hidden className="flex h-14 w-14 items-center justify-center rounded-full bg-neutral-100 text-neutral-400 ring-1 ring-neutral-200/70 ring-inset">
                <Bot size={26} strokeWidth={1.6} />
              </span>
              <h2 className="mt-5 text-lg font-bold text-neutral-900">{t("botChat.welcome.noBotsTitle")}</h2>
              <p className="mt-2 text-sm leading-6 text-neutral-600">{t("botChat.welcome.noBots")}</p>
              {botsRefresh("mt-4")}
              {botsRefreshError && <div className="mt-2">{botsRefreshError}</div>}
            </div>
          )}

          {(resolving || (selected && (!thread || thread.status === "loading"))) && (
            <div role="status" className="flex min-h-full items-center justify-center gap-2 text-sm text-neutral-500">
              <Spinner size={18} className="text-neutral-400" />
              {t("botChat.thread.loading")}
            </div>
          )}

          {selected && thread?.status === "error" && thread.error && (
            <Empty
              icon={CircleAlert}
              title={t("botChat.thread.loadFailedTitle")}
              className="min-h-full"
              action={
                <Button type="button" onClick={reopen}>
                  {t("botChat.retry")}
                </Button>
              }
            >
              <span role="alert">{errText(thread.error)}</span>
            </Empty>
          )}

          {selected && ready && thread && (
            <div key={selKey} className="bc-fade mx-auto max-w-3xl">
              {thread.nextBefore != null && (
                <div className="mb-5 flex flex-col items-center gap-1.5">
                  <Button type="button" variant="ghost" size="sm" disabled={thread.earlierLoading} onClick={() => loadEarlier(selKey)} data-load-earlier="">
                    {thread.earlierLoading && <Spinner size={13} />}
                    {thread.earlierLoading ? t("botChat.thread.earlierLoading") : t("botChat.thread.earlier")}
                  </Button>
                  {thread.earlierError && (
                    <p role="alert" className="text-xs text-red-600 dark:text-red-400">
                      {t("botChat.thread.earlierFailed", { reason: errText(thread.earlierError) })}
                    </p>
                  )}
                </div>
              )}
              {!messages.length && (
                <div className="flex flex-col items-center px-4 py-10 text-center">
                  <BotWhale seed={selected.id} size={64} className="bc-pop" />
                  <p className="mt-4 text-sm leading-6 text-neutral-600">{t("botChat.thread.empty", { bot: botName })}</p>
                </div>
              )}
              <ol role="log" aria-live="polite" aria-label={t("botChat.thread.label")} className="flex flex-col gap-6">
                {messages.flatMap((m) => {
                  const day = dayKey(m.created_at);
                  const items: ReactNode[] = [];
                  if (day && day !== prevDay) {
                    items.push(
                      <li key={`day-${day}`} className="flex justify-center" data-day-separator="">
                        <time dateTime={m.created_at} className="rounded-full bg-neutral-100 px-3 py-0.5 text-[11px] font-medium text-neutral-500">
                          {dayLabel(m.created_at, lang)}
                        </time>
                      </li>,
                    );
                  }
                  if (day) prevDay = day;
                  items.push(<Turn key={String(m.id)} turn={m} bot={selected} onReuse={botDisabled ? undefined : reuseContent} onPreview={setPreview} />);
                  return items;
                })}
              </ol>
            </div>
          )}
        </div>

        {/* 输入区 */}
        {selected && ready && thread && (
          <form onSubmit={onSubmit} className="px-3 pt-2 pb-3 sm:px-6">
            <div className="mx-auto max-w-3xl">
              {thread.refreshError && (
                <Notice tone="warn" role="status" className="mb-2">
                  <span className="mr-2">{t("botChat.thread.refreshFailed", { reason: errText(thread.refreshError) })}</span>
                  <button type="button" className="font-medium underline-offset-2 hover:underline" onClick={() => void loadThread(selected)}>
                    {t("botChat.retry")}
                  </button>
                </Notice>
              )}
              {botDisabled && (
                <Notice tone="warn" role="status" className="mb-2">
                  {t("botChat.composer.disabledBot")}
                </Notice>
              )}
              {activeSendError && activeSendError.key !== "botDisabled" && (
                <Notice tone="danger" role="alert" className="mb-2">
                  {t("botChat.composer.failed", { reason: errText(activeSendError) })}
                </Notice>
              )}
              <div
                className={cx(
                  "flex items-end gap-2 rounded-3xl border bg-card py-1.5 pr-1.5 pl-4 shadow-control transition-[border-color,box-shadow] duration-150 focus-within:border-brand-500 focus-within:ring-[3px] focus-within:ring-brand-500/20 dark:focus-within:border-brand-400",
                  tooLong ? "border-red-400" : "border-neutral-300 dark:border-white/15",
                  botDisabled && "bg-neutral-50 opacity-70",
                )}
              >
                <label htmlFor={composerId} className="sr-only">
                  {t("botChat.composer.label")}
                </label>
                <textarea
                  id={composerId}
                  ref={inputRef}
                  rows={1}
                  value={draft}
                  disabled={botDisabled}
                  readOnly={isSending}
                  aria-invalid={tooLong || undefined}
                  aria-describedby={`${composerId}-hint`}
                  placeholder={botDisabled ? t("botChat.composer.placeholderDisabled") : t("botChat.composer.placeholder", { bot: botName })}
                  onChange={(e) => setDraft(e.target.value)}
                  onKeyDown={onKeyDown}
                  className="max-h-48 min-h-9 min-w-0 flex-1 resize-none border-0 bg-transparent py-1.5 text-base leading-6 text-neutral-900 shadow-none ring-0 outline-none placeholder:text-neutral-400 focus:ring-0 focus:outline-none disabled:cursor-not-allowed sm:text-sm dark:placeholder:text-neutral-500"
                />
                <button
                  type="submit"
                  disabled={!canSend}
                  aria-label={isSending ? t("botChat.composer.sending") : t("botChat.composer.send")}
                  title={replying ? t("botChat.composer.waiting") : t("botChat.composer.send")}
                  className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-brand-500 text-white shadow-primary transition-[background-color,opacity,transform] duration-150 hover:bg-brand-600 active:translate-y-px disabled:cursor-not-allowed disabled:opacity-40 disabled:shadow-none"
                >
                  {isSending ? <Spinner size={16} /> : <ArrowUp size={18} aria-hidden />}
                </button>
              </div>
              <div id={`${composerId}-hint`} className="mt-1.5 flex min-h-4 flex-wrap items-center justify-between gap-x-3 gap-y-0.5 px-1 text-xs text-neutral-500">
                <span aria-live="polite">
                  {tooLong ? (
                    <span className="text-red-600 dark:text-red-400">{t("botChat.composer.tooLong")}</span>
                  ) : isSending ? (
                    t("botChat.composer.sending")
                  ) : replying ? (
                    t("botChat.composer.waiting")
                  ) : botDisabled ? null : (
                    <span className="hidden sm:inline">{t("botChat.composer.hint")}</span>
                  )}
                </span>
                {bytes > CHAT_CONTENT_MAX_BYTES * 0.8 && (
                  <span className={cx("ml-auto tabular-nums", tooLong && "font-medium text-red-600 dark:text-red-400")}>
                    {t("botChat.composer.bytes", { used: formatNumber(bytes), max: formatNumber(CHAT_CONTENT_MAX_BYTES) })}
                  </span>
                )}
              </div>
            </div>
          </form>
        )}
      </section>

      {/* 右：机器人资料。超宽屏常驻，其余盖在对话区上；都可以关 */}
      {selected && (
        <ProfilePane
          id={profileId}
          bot={selected}
          state={profiles[selKey]}
          open={profileOpen}
          replying={replying}
          closeRef={profileCloseRef}
          onClose={closeProfile}
          onRetry={() => void loadProfile(selected)}
          onPreview={setPreview}
        />
      )}
      <FilePreviewDialog file={preview} onClose={() => setPreview(null)} />
    </div>
  );
}

export default function BotChatPage() {
  const { t } = useTranslation();
  const { user } = useAuth();
  const { status, bots, error, refreshing, reload } = useChatBots();

  // 共享的机器人列表不会自己过期：每次打开本页重新拉一次（reload 引用稳定，只在挂载和换账号时触发；
  // 与侧栏的首次探测撞上时共用同一个在途请求）。
  useEffect(() => {
    reload();
  }, [reload]);

  if (status === "idle" || status === "loading") {
    return (
      <div role="status" className="flex items-center justify-center gap-2 py-24 text-sm text-neutral-500">
        <Spinner size={18} className="text-neutral-400" />
        {t("botChat.loading")}
      </div>
    );
  }
  if (status === "unavailable") {
    return (
      <Card>
        <Empty icon={Bot} title={t("botChat.gate.unavailableTitle")}>
          {t("botChat.gate.unavailable")}
        </Empty>
      </Card>
    );
  }
  if (status === "forbidden") {
    return (
      <Card>
        <Empty icon={Lock} title={t("botChat.gate.forbiddenTitle")}>
          {t("botChat.gate.forbidden")}
        </Empty>
      </Card>
    );
  }
  if (status === "error") {
    return (
      <Card>
        <Empty
          icon={CircleAlert}
          title={t("botChat.gate.errorTitle")}
          action={
            <Button type="button" onClick={reload}>
              {t("botChat.retry")}
            </Button>
          }
        >
          <span role="alert">{t(`botChat.errors.${error?.key ?? "generic"}`)}</span>
        </Empty>
      </Card>
    );
  }
  const userId = user?.id ?? 0;
  // 换账号就整个重建：上一个人的对话、草稿和已读位置都不带过来
  return <Workspace key={userId} userId={userId} bots={bots} reloadBots={reload} botsRefreshing={refreshing} botsError={error} />;
}
