/**
 * 机器人对话（/bot-chat）：员工在网页里与管理员为自己开通的云端机器人对话。
 *
 * - 当前会话写在地址里（?conversation=<id>），刷新、前进后退、分享给自己都能回到原处；
 * - 一轮 = 员工的一条消息 + 机器人的回复（回复状态来自云任务）；有排队 / 运行中的回复时
 *   才轮询，且上一次请求回来之后才发下一次，不会重叠；
 * - 切换会话后才回来的旧响应一律丢弃；发送失败时内容留在输入框里，重试复用同一个幂等键。
 */
import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import { ArrowUp, Bot, CircleAlert, Download, FileDown, History, Lock, MessageSquare, Plus, RefreshCw, X } from "lucide-react";
import { Badge, Button, Card, Empty, Notice, Spinner, cx, inputCls } from "../components/ui";
import { ChatMarkdown } from "../components/chat-markdown";
import { formatTime } from "../lib/api";
import { formatNumber, formatRelativeTime } from "../lib/format";
import {
  CHAT_CONTENT_MAX_BYTES,
  chatApi,
  isPendingTurn,
  newIdempotencyKey,
  sameId,
  toChatError,
  useChatBots,
  utf8Bytes,
  type ChatArtifact,
  type ChatBot,
  type ChatConversation,
  type ChatError,
  type ChatId,
  type ChatTurn,
} from "../lib/bot-chat";

/** 有回复在排队 / 运行时的轮询间隔：上一次请求结束后再等这么久。 */
const POLL_MS = 2500;
/** 离底部这么近就算「在看最新消息」，新内容到了跟着滚下去。 */
const STICK_PX = 96;

type ConvsState = { status: "loading" | "ready" | "error"; items: ChatConversation[]; error: ChatError | null };

type Detail = {
  id: string;
  status: "loading" | "ready" | "error";
  conversation: ChatConversation | null;
  messages: ChatTurn[];
  error: ChatError | null;
  /** 已经有内容之后的刷新 / 轮询失败：保留对话记录，只提示一下。 */
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

function BotAvatar({ size = "md" }: { size?: "sm" | "md" | "lg" }) {
  const box = size === "lg" ? "h-14 w-14" : size === "md" ? "h-10 w-10" : "h-7 w-7";
  return (
    <span
      aria-hidden
      className={cx(
        "flex shrink-0 items-center justify-center rounded-full bg-gradient-to-br from-[#73c8ff] via-brand-500 to-[#6d5dfc] text-white shadow-control",
        box,
      )}
    >
      <Bot size={size === "lg" ? 28 : size === "md" ? 20 : 15} strokeWidth={1.8} />
    </span>
  );
}

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

/** 成功回复生成的文件：员工点了才读列表，点了文件名才下载。 */
function TurnFiles({ taskId }: { taskId: ChatId }) {
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
            <li key={String(file.id)} className="min-w-0">
              <a
                href={chatApi.artifactUrl(taskId, file.id)}
                download={file.name}
                aria-label={t("botChat.files.download", { name: file.name })}
                className="inline-flex max-w-full items-center gap-2 rounded-lg border border-neutral-200 bg-card px-3 py-1.5 text-[13px] font-medium text-neutral-800 shadow-control transition-colors hover:border-neutral-300 hover:bg-neutral-50 dark:border-white/10 dark:hover:border-white/20"
              >
                <Download size={14} aria-hidden className="shrink-0 text-neutral-500" />
                <span className="min-w-0 break-all">{file.name}</span>
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

function Turn({ turn, botName, onReuse }: { turn: ChatTurn; botName: string; onReuse?: (content: string) => void }) {
  const { t } = useTranslation();
  const pending = isPendingTurn(turn);
  const stopped = turn.status === "failed" || turn.status === "canceled";
  return (
    <li className="min-w-0">
      <div className="flex flex-col items-end">
        <span className="sr-only">{t("botChat.thread.you")}</span>
        <div className="max-w-[88%] rounded-2xl rounded-br-md bg-neutral-100 px-4 py-2.5 text-sm leading-6 break-words whitespace-pre-wrap text-neutral-900 [overflow-wrap:anywhere] sm:max-w-[80%]">
          {turn.content}
        </div>
        <time dateTime={turn.created_at} className="mt-1 text-[11px] text-neutral-400">
          {formatTime(turn.created_at)}
        </time>
      </div>
      <div className="mt-3 flex min-w-0 gap-2.5 sm:gap-3">
        <BotAvatar size="sm" />
        <div className="min-w-0 flex-1 pt-0.5">
          <div className="mb-1 truncate text-xs font-medium text-neutral-500">{botName}</div>
          {pending && (
            <div role="status" className="flex items-center gap-2.5 text-sm text-neutral-500">
              <TypingDots />
              <span>{t(turn.status === "queued" ? "botChat.reply.queued" : "botChat.reply.running")}</span>
            </div>
          )}
          {turn.status === "succeeded" &&
            (turn.summary.trim() ? <ChatMarkdown markdown={turn.summary} /> : <p className="text-sm text-neutral-500">{t("botChat.reply.noText")}</p>)}
          {turn.status === "succeeded" && <TurnFiles taskId={turn.id} />}
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

function Workspace({
  bots,
  reloadBots,
  botsRefreshing,
  botsError,
}: {
  bots: ChatBot[];
  reloadBots: () => void;
  botsRefreshing: boolean;
  botsError: ChatError | null;
}) {
  const { t, i18n } = useTranslation();
  const alive = useAlive();
  const composerId = useId();
  const [params, setParams] = useSearchParams();
  const activeId = params.get("conversation") ?? "";
  const activeRef = useRef(activeId);
  activeRef.current = activeId;

  const openConversation = useCallback(
    (id: string) => {
      setParams(id ? { conversation: id } : {});
    },
    [setParams],
  );

  // ---------- 会话列表 ----------
  const [convs, setConvs] = useState<ConvsState>({ status: "loading", items: [], error: null });
  const convsInflight = useRef<Promise<void> | null>(null);

  const upsertConv = useCallback((c: ChatConversation) => {
    setConvs((s) => ({ ...s, items: [c, ...s.items.filter((x) => !sameId(x.id, c.id))] }));
  }, []);

  const loadConvs = useCallback(() => {
    if (convsInflight.current) return;
    setConvs((s) => ({ ...s, status: s.status === "ready" ? "ready" : "loading", error: null }));
    const p: Promise<void> = chatApi
      .conversations()
      .then(
        (items) => {
          if (!alive.current) return;
          // 请求在路上时本地新建 / 刚打开的会话可能还不在结果里：留着，不要被旧结果冲掉
          setConvs((s) => ({ status: "ready", error: null, items: [...items, ...s.items.filter((x) => !items.some((y) => sameId(x.id, y.id)))] }));
        },
        (err: unknown) => {
          if (alive.current) setConvs((s) => ({ ...s, status: s.status === "ready" ? "ready" : "error", error: toChatError(err, "runtime") }));
        },
      )
      .finally(() => {
        if (convsInflight.current === p) convsInflight.current = null;
      });
    convsInflight.current = p;
  }, [alive]);

  useEffect(() => {
    loadConvs();
  }, [loadConvs]);

  const sortedConvs = useMemo(
    () => [...convs.items].sort((a, b) => (Date.parse(b.updated_at) || 0) - (Date.parse(a.updated_at) || 0)),
    [convs.items],
  );

  // ---------- 当前会话 ----------
  const [detail, setDetail] = useState<Detail | null>(null);
  const detailInflight = useRef<{ id: string; promise: Promise<void> } | null>(null);
  const detailSeq = useRef(0);
  /** 本地追加过消息（发送成功）就 +1：在那之前发出的读取结果不含新消息，回来后丢弃。 */
  const localVersion = useRef(0);

  const loadDetail = useCallback(
    (id: string): Promise<void> => {
      const current = detailInflight.current;
      if (current && current.id === id) return current.promise;
      const seq = ++detailSeq.current;
      const version = localVersion.current;
      const fresh = () => alive.current && activeRef.current === id && seq === detailSeq.current;
      const promise: Promise<void> = chatApi
        .conversation(id)
        .then(
          (d) => {
            if (!fresh() || version !== localVersion.current) return;
            setDetail({ id, status: "ready", conversation: d.conversation, messages: d.messages, error: null, refreshError: null });
            upsertConv(d.conversation);
          },
          (err: unknown) => {
            if (!fresh()) return;
            const error = toChatError(err, "conversation");
            setDetail((prev) =>
              prev && prev.id === id && prev.status === "ready"
                ? { ...prev, refreshError: error }
                : { id, status: "error", conversation: null, messages: [], error, refreshError: null },
            );
          },
        )
        .finally(() => {
          if (detailInflight.current?.promise === promise) detailInflight.current = null;
        });
      detailInflight.current = { id, promise };
      return promise;
    },
    [alive, upsertConv],
  );

  const [listOpen, setListOpen] = useState(false);
  const idem = useRef<{ conv: string; content: string; key: string } | null>(null);
  const stick = useRef(true);

  useEffect(() => {
    // 换了会话：幂等键作废，回到「跟随最新消息」
    idem.current = null;
    stick.current = true;
    setListOpen(false);
    if (!activeId) {
      setDetail(null);
      return;
    }
    setDetail((prev) =>
      prev?.id === activeId ? prev : { id: activeId, status: "loading", conversation: null, messages: [], error: null, refreshError: null },
    );
    void loadDetail(activeId);
  }, [activeId, loadDetail]);

  /** 打开失败后的重试：先回到加载态，再读一次。 */
  const reopen = () => {
    setDetail({ id: activeId, status: "loading", conversation: null, messages: [], error: null, refreshError: null });
    void loadDetail(activeId);
  };

  const current = detail && detail.id === activeId ? detail : null;
  const conversation = current?.conversation ?? convs.items.find((c) => sameId(c.id, activeId)) ?? null;
  const messages = current?.messages ?? [];
  const replying = current?.status === "ready" && messages.some(isPendingTurn);

  // 只在有排队 / 运行中的回复时轮询；上一次读取结束后才排下一次，页面在后台时跳过。
  useEffect(() => {
    if (!replying || !activeId) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const tick = async () => {
      if (stopped) return;
      if (!document.hidden) await loadDetail(activeId);
      if (!stopped) timer = setTimeout(() => void tick(), POLL_MS);
    };
    timer = setTimeout(() => void tick(), POLL_MS);
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [replying, activeId, loadDetail]);

  // ---------- 新建会话 ----------
  const [selectedBot, setSelectedBot] = useState("");
  const bot = bots.find((b) => sameId(b.id, selectedBot)) ?? bots[0] ?? null;
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<ChatError | null>(null);

  const create = async () => {
    if (!bot || creating) return;
    const from = activeRef.current;
    setCreating(true);
    setCreateError(null);
    try {
      const c = await chatApi.createConversation(bot.id);
      if (!alive.current) return;
      upsertConv(c);
      // 等待期间员工自己点开了别的会话，就不把他拉走
      if (activeRef.current === from) openConversation(String(c.id));
    } catch (err) {
      if (!alive.current) return;
      const error = toChatError(err, "runtime");
      setCreateError(error);
      if (error.key === "botUnavailable") reloadBots();
    } finally {
      if (alive.current) setCreating(false);
    }
  };

  // ---------- 输入与发送 ----------
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [sending, setSending] = useState<string | null>(null);
  const [sendError, setSendError] = useState<{ conv: string; error: ChatError } | null>(null);
  /** 发送时后端明确说这个会话的机器人已停用（bot_disabled）。 */
  const [blocked, setBlocked] = useState<string[]>([]);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  const draft = drafts[activeId] ?? "";
  const setDraft = (value: string) => setDrafts((d) => ({ ...d, [activeId]: value }));
  const content = draft.trim();
  const bytes = useMemo(() => utf8Bytes(content), [content]);
  const tooLong = bytes > CHAT_CONTENT_MAX_BYTES;

  const ready = current?.status === "ready";
  const botName = conversation?.bot_name || t("botChat.unknownBot");
  const botDisabled = !!ready && !!conversation && (!bots.some((b) => sameId(b.id, conversation.bot_id)) || blocked.includes(activeId));
  const isSending = sending !== null;
  const canSend = !!ready && !botDisabled && !isSending && !replying && content.length > 0 && !tooLong;

  const send = async () => {
    if (!canSend) return;
    const conv = activeId;
    const reuse = idem.current;
    const key = reuse && reuse.conv === conv && reuse.content === content ? reuse.key : newIdempotencyKey();
    idem.current = { conv, content, key };
    setSending(conv);
    setSendError(null);
    try {
      const turn = await chatApi.send(conv, content, key);
      if (idem.current?.key === key) idem.current = null;
      if (!alive.current) return;
      // 等待期间输入框里换了内容（例如点了「重新编辑」）：那是新草稿，原样留着
      setDrafts((d) => ((d[conv] ?? "").trim() === content ? { ...d, [conv]: "" } : d));
      setConvs((s) => ({ ...s, items: s.items.map((c) => (sameId(c.id, conv) ? { ...c, updated_at: turn.created_at || c.updated_at } : c)) }));
      if (activeRef.current === conv) {
        // 发送前发出的读取不含这条消息，回来后会被 localVersion 丢弃。切走又切回时会话可能还停在
        // 加载态，没有内容可追加：等那次读取结束后补读一次（loadDetail 自带去重，不会和轮询重叠）。
        const pendingRead = detailInflight.current?.id === conv ? detailInflight.current.promise : null;
        localVersion.current++;
        stick.current = true;
        setDetail((prev) =>
          prev && prev.id === conv && prev.status === "ready"
            ? { ...prev, refreshError: null, messages: [...prev.messages.filter((m) => !sameId(m.id, turn.id)), turn] }
            : prev,
        );
        if (pendingRead) {
          void pendingRead.then(() => {
            if (alive.current && activeRef.current === conv) void loadDetail(conv);
          });
        }
      }
    } catch (err) {
      const error = toChatError(err, "conversation");
      // 这个键后端不再接受：下次重试换一个新的
      if (error.key === "conflict" && idem.current?.key === key) idem.current = null;
      if (!alive.current) return;
      setSendError({ conv, error });
      if (error.key === "botDisabled") {
        setBlocked((b) => (b.includes(conv) ? b : [...b, conv]));
        reloadBots();
      }
      // 已有一条在处理（例如另一个标签页发的）：读一次最新记录，轮询随之接上
      if (error.key === "busy" && activeRef.current === conv) void loadDetail(conv);
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
  }, [draft, activeId, ready]);

  const reuseContent = (text: string) => {
    setDraft(text);
    inputRef.current?.focus();
  };

  // ---------- 滚动 ----------
  const scrollRef = useRef<HTMLDivElement>(null);
  const last = messages[messages.length - 1];
  const tail = `${current?.status ?? ""}:${messages.length}:${last?.status ?? ""}:${last?.summary.length ?? 0}`;
  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [activeId, tail]);
  const onScroll = () => {
    const el = scrollRef.current;
    if (el) stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < STICK_PX;
  };

  useEffect(() => {
    if (!listOpen) return;
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.key === "Escape") setListOpen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [listOpen]);

  const errText = (e: ChatError) => t(`botChat.errors.${e.key}`);
  const refreshing = current?.status === "loading";
  const activeSendError = sendError && sendError.conv === activeId ? sendError.error : null;
  const title = conversation?.title?.trim() || t("botChat.untitled");

  const botPicker = (
    <select
      aria-label={t("botChat.list.botLabel")}
      className={cx(inputCls, "min-w-0")}
      value={bot ? String(bot.id) : ""}
      disabled={creating || !bots.length}
      onChange={(e) => setSelectedBot(e.target.value)}
    >
      {bots.map((b) => (
        <option key={String(b.id)} value={String(b.id)}>
          {b.name}
        </option>
      ))}
    </select>
  );

  // 机器人列表的手动刷新：会话栏和「暂无机器人」的欢迎区各放一个，没有机器人时也看得到
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

  return (
    <div className="relative flex h-[calc(100dvh-8rem)] min-h-[30rem] overflow-hidden rounded-xl border border-neutral-200 bg-card shadow-card lg:h-[calc(100dvh-6.5rem)] dark:border-white/10">
      {/* 会话栏：宽屏常驻左侧，窄屏盖在对话区上 */}
      <aside
        aria-label={t("botChat.list.open")}
        className={cx(
          "absolute inset-0 z-20 min-h-0 flex-col bg-card lg:static lg:z-auto lg:w-72 lg:shrink-0 lg:border-r lg:border-neutral-200 dark:lg:border-white/10",
          listOpen ? "flex" : "hidden lg:flex",
        )}
      >
        <div className="flex items-center gap-2 px-4 pt-4 pb-3">
          <h1 className="min-w-0 flex-1 truncate text-base font-bold tracking-[-0.01em] text-neutral-900">{t("botChat.title")}</h1>
          <button
            type="button"
            aria-label={t("botChat.list.close")}
            onClick={() => setListOpen(false)}
            className="rounded-md p-1.5 text-neutral-500 transition-colors hover:bg-neutral-100 hover:text-neutral-900 lg:hidden"
          >
            <X size={18} />
          </button>
        </div>
        <div className="space-y-2 border-b border-neutral-200 px-4 pb-4 dark:border-white/10">
          {bots.length ? (
            <>
              {botPicker}
              <Button type="button" className="w-full" disabled={creating || !bot} onClick={() => void create()}>
                {creating ? <Spinner size={15} /> : <Plus size={15} aria-hidden />}
                {creating ? t("botChat.list.creating") : t("botChat.list.new")}
              </Button>
            </>
          ) : (
            <p className="text-[13px] leading-5 text-neutral-500">{t("botChat.list.noBots")}</p>
          )}
          {botsRefresh("w-full")}
          {botsRefreshError}
          {createError && (
            <p role="alert" className="text-[13px] leading-5 text-red-600 dark:text-red-400">
              {t("botChat.list.createFailed", { reason: errText(createError) })}
            </p>
          )}
        </div>
        <div className="flex items-center justify-between px-4 pt-3 pb-1.5">
          <h2 className="text-xs font-semibold tracking-[0.04em] text-neutral-500/80">{t("botChat.list.title")}</h2>
          {convs.status === "ready" && convs.error && (
            <button type="button" onClick={loadConvs} className="text-xs font-medium text-red-600 hover:underline dark:text-red-400">
              {t("botChat.retry")}
            </button>
          )}
        </div>
        <nav aria-label={t("botChat.list.title")} className="min-h-0 flex-1 overflow-y-auto px-2 pb-3">
          {convs.status === "loading" && (
            <div role="status" className="flex items-center gap-2 px-2 py-3 text-[13px] text-neutral-500">
              <Spinner size={14} className="text-neutral-400" />
              {t("botChat.list.loading")}
            </div>
          )}
          {convs.status === "error" && (
            <div role="alert" className="px-2 py-3 text-[13px] leading-5 text-neutral-600">
              <p>
                {t("botChat.list.failed")} {convs.error && errText(convs.error)}
              </p>
              <Button type="button" variant="secondary" size="sm" className="mt-2" onClick={loadConvs}>
                {t("botChat.retry")}
              </Button>
            </div>
          )}
          {convs.status === "ready" && !sortedConvs.length && <p className="px-2 py-3 text-[13px] leading-5 text-neutral-500">{t("botChat.list.empty")}</p>}
          <ul className="flex flex-col gap-0.5">
            {sortedConvs.map((c) => {
              const active = sameId(c.id, activeId);
              return (
                <li key={String(c.id)}>
                  <button
                    type="button"
                    aria-current={active ? "page" : undefined}
                    onClick={() => (active ? setListOpen(false) : openConversation(String(c.id)))}
                    className={cx(
                      "block w-full min-w-0 rounded-md px-2.5 py-2 text-left transition-colors",
                      active ? "bg-brand-50 dark:bg-brand-500/15" : "hover:bg-neutral-100",
                    )}
                  >
                    <span className={cx("block truncate text-sm font-medium", active ? "text-brand-600 dark:text-brand-200" : "text-neutral-800")}>
                      {c.title?.trim() || t("botChat.untitled")}
                    </span>
                    <span className="mt-0.5 flex min-w-0 items-center gap-1.5 text-xs text-neutral-500">
                      <span className="min-w-0 truncate">{c.bot_name || t("botChat.unknownBot")}</span>
                      <span aria-hidden>·</span>
                      <span className="shrink-0">{formatRelativeTime(c.updated_at, i18n.language)}</span>
                    </span>
                  </button>
                </li>
              );
            })}
          </ul>
        </nav>
      </aside>

      <section className="flex min-w-0 flex-1 flex-col" aria-label={activeId ? t("botChat.header.with", { bot: botName }) : t("botChat.title")}>
        {/* 机器人头部 */}
        <header className="flex items-center gap-3 border-b border-neutral-200 px-3 py-3 sm:px-5 dark:border-white/10">
          <button
            type="button"
            aria-label={t("botChat.list.open")}
            onClick={() => setListOpen(true)}
            className="-ml-1 shrink-0 rounded-md p-1.5 text-neutral-600 transition-colors hover:bg-neutral-100 hover:text-neutral-900 lg:hidden"
          >
            <History size={19} />
          </button>
          <BotAvatar />
          <div className="min-w-0 flex-1">
            <div className="flex min-w-0 items-center gap-2">
              <h2 className="min-w-0 truncate text-base leading-6 font-bold tracking-[-0.01em] text-neutral-900 sm:text-lg">
                {activeId ? botName : (bot?.name ?? t("botChat.title"))}
              </h2>
              {botDisabled && <Badge tone="amber">{t("botChat.header.disabled")}</Badge>}
              {replying && (
                <span className="hidden sm:inline-flex">
                  <Badge tone="brand">{t("botChat.header.replying")}</Badge>
                </span>
              )}
            </div>
            <p className="truncate text-xs leading-5 text-neutral-500">{activeId ? title : t("botChat.welcome.pick")}</p>
          </div>
          {activeId && (
            <button
              type="button"
              aria-label={t("botChat.refresh")}
              title={t("botChat.refresh")}
              disabled={refreshing}
              onClick={() => void loadDetail(activeId)}
              className="shrink-0 rounded-md p-2 text-neutral-500 transition-colors hover:bg-neutral-100 hover:text-neutral-900 disabled:opacity-50"
            >
              <RefreshCw size={16} className={cx(refreshing && "animate-spin motion-reduce:animate-none")} />
            </button>
          )}
        </header>

        {/* 对话区 */}
        <div ref={scrollRef} onScroll={onScroll} className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain px-3 py-5 sm:px-6">
          {!activeId && (
            <div className="mx-auto flex min-h-full max-w-xl flex-col items-center justify-center py-6 text-center">
              <BotAvatar size="lg" />
              {bots.length ? (
                <>
                  <h2 className="mt-5 text-xl leading-7 font-bold tracking-[-0.02em] text-neutral-900 sm:text-2xl sm:leading-8">{t("botChat.welcome.title")}</h2>
                  <p className="mt-2 text-sm leading-6 text-neutral-600">{t("botChat.welcome.desc")}</p>
                  <div role="radiogroup" aria-label={t("botChat.welcome.pick")} className="mt-6 flex max-w-full flex-wrap justify-center gap-2">
                    {bots.map((b) => {
                      const picked = sameId(b.id, bot?.id);
                      return (
                        <button
                          key={String(b.id)}
                          type="button"
                          role="radio"
                          aria-checked={picked}
                          disabled={creating}
                          onClick={() => setSelectedBot(String(b.id))}
                          className={cx(
                            "inline-flex max-w-full items-center gap-2 rounded-full border px-3.5 py-1.5 text-sm font-medium transition-colors",
                            picked
                              ? "border-brand-500 bg-brand-50 text-brand-700 dark:bg-brand-500/15 dark:text-brand-200"
                              : "border-neutral-200 bg-card text-neutral-700 hover:border-neutral-300 hover:bg-neutral-50 dark:border-white/10 dark:hover:border-white/20",
                          )}
                        >
                          <Bot size={15} aria-hidden className="shrink-0" />
                          <span className="min-w-0 truncate">{b.name}</span>
                        </button>
                      );
                    })}
                  </div>
                  <Button type="button" size="lg" className="mt-6 rounded-full" disabled={creating || !bot} onClick={() => void create()}>
                    {creating ? <Spinner size={16} /> : <Plus size={16} aria-hidden />}
                    {creating ? t("botChat.list.creating") : t("botChat.welcome.start")}
                  </Button>
                  {createError && (
                    <p role="alert" className="mt-3 text-sm text-red-600 dark:text-red-400">
                      {t("botChat.list.createFailed", { reason: errText(createError) })}
                    </p>
                  )}
                </>
              ) : (
                <>
                  <h2 className="mt-5 text-lg font-bold text-neutral-900">{t("botChat.welcome.noBotsTitle")}</h2>
                  <p className="mt-2 text-sm leading-6 text-neutral-600">{t("botChat.welcome.noBots")}</p>
                  {botsRefresh("mt-4")}
                  {botsRefreshError && <div className="mt-2">{botsRefreshError}</div>}
                </>
              )}
            </div>
          )}

          {activeId && current?.status === "loading" && (
            <div role="status" className="flex min-h-full items-center justify-center gap-2 text-sm text-neutral-500">
              <Spinner size={18} className="text-neutral-400" />
              {t("botChat.thread.loading")}
            </div>
          )}

          {activeId && current?.status === "error" && current.error && (
            <Empty
              icon={current.error.key === "notFound" ? MessageSquare : CircleAlert}
              title={t(current.error.key === "notFound" ? "botChat.thread.notFoundTitle" : "botChat.thread.loadFailedTitle")}
              className="min-h-full"
              action={
                <>
                  {current.error.key !== "notFound" && (
                    <Button type="button" onClick={reopen}>
                      {t("botChat.retry")}
                    </Button>
                  )}
                  <Button type="button" variant="secondary" onClick={() => openConversation("")}>
                    {t("botChat.thread.back")}
                  </Button>
                </>
              }
            >
              <span role="alert">{current.error.key === "notFound" ? t("botChat.thread.notFound") : errText(current.error)}</span>
            </Empty>
          )}

          {activeId && ready && (
            <div className="mx-auto max-w-3xl">
              {!messages.length && (
                <div className="flex flex-col items-center px-4 py-10 text-center">
                  <BotAvatar size="lg" />
                  <p className="mt-4 text-sm leading-6 text-neutral-600">{t("botChat.thread.empty", { bot: botName })}</p>
                </div>
              )}
              <ol role="log" aria-live="polite" aria-label={t("botChat.thread.label")} className="flex flex-col gap-7">
                {messages.map((m) => (
                  <Turn key={String(m.id)} turn={m} botName={botName} onReuse={botDisabled ? undefined : reuseContent} />
                ))}
              </ol>
            </div>
          )}
        </div>

        {/* 输入区 */}
        {activeId && ready && (
          <form onSubmit={onSubmit} className="border-t border-neutral-200 px-3 pt-3 pb-3 sm:px-6 dark:border-white/10">
            <div className="mx-auto max-w-3xl">
              {current.refreshError && (
                <Notice tone="warn" role="status" className="mb-2">
                  <span className="mr-2">{t("botChat.thread.refreshFailed", { reason: errText(current.refreshError) })}</span>
                  <button type="button" className="font-medium underline-offset-2 hover:underline" onClick={() => void loadDetail(activeId)}>
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
    </div>
  );
}

export default function BotChatPage() {
  const { t } = useTranslation();
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
  return <Workspace bots={bots} reloadBots={reload} botsRefreshing={refreshing} botsError={error} />;
}
