import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useLocation, useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import { api, setEmbedToken, setEmbedUnauthorizedHandler } from "./api";
import { useAuth } from "./auth";
import {
  EMBED_PROTOCOL_V2,
  EmbedExchangeError,
  exchangeEmbedCode,
  isAppPath,
  isEmbedMessage,
  isTrustedParentMessage,
  normalizeAppPath,
  parseEmbedOptions,
  postToHost,
  postToHostV2,
  resolvedDocumentTheme,
  type EmbedError,
  type EmbedOptions,
} from "./embed";
import { useEmbedResize, useEmbedTheme } from "./use-embed";

/**
 * 微应用嵌入的握手与状态（跨站 v1 + 协议 v2；同站 v0 继续用现有 cookie 会话）。
 *
 * 时序：
 *  1. 进入嵌入页先发 v2 `dsh-ee:hello`（支持的消息/协议版本）与 v1 `dsh-ee:ready`，
 *     要早于认证，因为跨站宿主需要据此再 mint code 并回 `dsh-ee:connect`；
 *  2. 没有 cookie 会话（跨站）时发 `dsh-ee:auth-required`，等宿主 connect；
 *  3. connect 的 code 只在本 iframe 同源 fetch 里换 `ee_emb_` 内存 Bearer，再 refresh 认证；
 *     mint 时绑定的 next 在这里消费（deeplink，pushState 不整页刷新）；
 *  4. 认证成功发 `dsh-ee:session`（不含 token）；之后所有 /api 请求自动带 Bearer；
 *  5. 会话过期/被吊销（401）时清 token、重新发 auth-required，不跳登录页。
 *
 * v2 增量：navigate（宿主切本应用路径）、navigate-request / open-external（iframe 请求宿主跳转）、
 * logout / logged-out（只撤销本次嵌入会话）。所有消息仍只发往 URL 的 host origin，不用 "*"。
 */
export type EmbedPhase = "inactive" | "waiting" | "connecting" | "connected" | "failed";

export interface EmbedBridge {
  options: EmbedOptions;
  /** 当前在嵌入页（?embed=1 且路径是 /a/*） */
  active: boolean;
  phase: EmbedPhase;
  error: EmbedError | null;
}

const EMPTY_OPTIONS: EmbedOptions = { embed: false, host: null, fit: "fill", theme: null };
const Ctx = createContext<EmbedBridge>({ options: EMPTY_OPTIONS, active: false, phase: "inactive", error: null });

export function useEmbedBridge() {
  return useContext(Ctx);
}

const HANDSHAKE_TIMEOUT_MS = 10_000;
const MAX_CONNECT_ATTEMPTS = 2;

/** v2 hello：iframe 声明支持的消息（老宿主按未知消息忽略；不降级授权）。 */
const SUPPORTS_V2 = ["connect-v1", "navigate", "navigate-request", "open-external", "logout", "deeplink", "theme", "resize"];

export function EmbedProvider({ children }: { children: ReactNode }) {
  const { search, pathname } = useLocation();
  const navigate = useNavigate();
  const { t } = useTranslation();
  const options = useMemo(() => parseEmbedOptions(search), [search]);
  const active = options.embed && pathname.startsWith("/a/");
  const slug = pathname.split("/")[2] ?? "";
  const { user, loading, refresh, clear } = useAuth();
  const [phase, setPhase] = useState<EmbedPhase>("inactive");
  const [error, setError] = useState<EmbedError | null>(null);
  const readySent = useRef(false);
  const helloKey = useRef("");
  const sessionSent = useRef(false);
  const authRequested = useRef(false);
  const attempts = useRef(0);
  const expiresAt = useRef<number | null>(null);
  const timer = useRef<number | null>(null);
  const errorSent = useRef("");

  // 主题与尺寸（v0 起的行为）：嵌入页统一在 provider 处理，覆盖登录前的等待/失败态。
  useEmbedTheme(options);
  useEmbedResize(options);

  // 1) hello（protocol v2，带 supports）与 ready：嵌入页一挂载就发，宿主据此安排握手。
  useEffect(() => {
    if (!active) {
      readySent.current = false;
      helloKey.current = "";
      return;
    }
    if (helloKey.current !== slug) {
      helloKey.current = slug;
      postToHostV2(options, "dsh-ee:hello", {
        protocol: EMBED_PROTOCOL_V2,
        app: { slug },
        supports: SUPPORTS_V2,
        theme: resolvedDocumentTheme(),
      });
    }
    if (!readySent.current) {
      readySent.current = true;
      postToHost(options, "dsh-ee:ready", {
        app: { slug },
        theme: resolvedDocumentTheme(),
        supports: ["connect-v1"],
      });
    }
  }, [active, options, slug]);

  // 离开嵌入页时清掉内存 Bearer（不写 localStorage / cookie）。
  useEffect(() => {
    if (!active) return;
    return () => setEmbedToken(null);
  }, [active]);

  // 401：嵌入会话过期或被吊销。清 token，让宿主重新 mint；AuthProvider 同时会清 user。
  useEffect(() => {
    if (!active) return;
    setEmbedUnauthorizedHandler(() => {
      setEmbedToken(null);
      expiresAt.current = null;
      sessionSent.current = false;
      authRequested.current = false;
      attempts.current = 0;
      setPhase("waiting");
    });
    return () => setEmbedUnauthorizedHandler(null);
  }, [active]);

  const clearTimer = () => {
    if (timer.current !== null) {
      window.clearTimeout(timer.current);
      timer.current = null;
    }
  };

  /**
   * 站内导航必须保留 URL 上的嵌入参数（embed/host/fit/theme）：否则路由一跳就退出嵌入模式，
   * 内存 Bearer 虽然还在，页面却会回到普通登录流程。
   */
  const navigateInEmbed = useCallback(
    (path: string) => {
      const target = new URL(path, window.location.origin);
      const current = new URL(window.location.href);
      for (const key of ["embed", "host", "fit", "theme"]) {
        const value = current.searchParams.get(key);
        if (value !== null) target.searchParams.set(key, value);
      }
      navigate(target.pathname + target.search + target.hash);
    },
    [navigate],
  );

  /** 消费 mint 时绑定的 next（deeplink）：只走本应用路径，pushState 不整页刷新，内存 token 不丢。 */
  const applyDeeplink = useCallback(
    (next: string | undefined) => {
      const path = normalizeAppPath(next, slug);
      if (!path) return;
      navigateInEmbed(path);
      postToHostV2(options, "dsh-ee:deeplink", { path });
    },
    [navigateInEmbed, options, slug],
  );

  const handleConnect = useCallback(
    async (code: string) => {
      if (!active) return;
      if (attempts.current >= MAX_CONNECT_ATTEMPTS) {
        setError({ code: "auth_required", message: t("embed.authRequired"), retryable: false });
        setPhase("failed");
        return;
      }
      attempts.current += 1;
      setPhase("connecting");
      try {
        const res = await exchangeEmbedCode(code);
        setEmbedToken(res.token);
        expiresAt.current = Date.now() + res.expires_in * 1000;
        sessionSent.current = false;
        setError(null);
        await refresh();
        applyDeeplink(res.next);
        setPhase("connected");
      } catch (e) {
        setEmbedToken(null);
        expiresAt.current = null;
        const code2 = e instanceof EmbedExchangeError ? e.code : "embed_code_invalid";
        const message = e instanceof Error ? e.message : String(e);
        postToHost(options, "dsh-ee:error", { code: code2, message, retryable: code2 === "embed_code_expired" });
        if (attempts.current < MAX_CONNECT_ATTEMPTS) {
          // 只允许宿主重试一次：重新 mint 后应该发新的 connect。
          authRequested.current = false;
          setPhase("waiting");
          postToHost(options, "dsh-ee:auth-required", { reason: "expired" });
        } else {
          setError({ code: "auth_required", message: t("embed.authRequired"), retryable: false });
          setPhase("failed");
        }
      }
    },
    [active, applyDeeplink, options, refresh, t],
  );

  /** 宿主 -> iframe 的 dsh-ee:logout：只撤销本次嵌入会话，不碰用户的网页登录。 */
  const handleLogout = useCallback(async () => {
    const wasEmbed = !!user?.embed;
    let revoked = false;
    if (wasEmbed) {
      try {
        await api.embedLogout();
        revoked = true;
      } catch {
        // 会话已经失效/网络失败：本地照样清掉，宿主拿到 revoked:false
      }
    }
    setEmbedToken(null);
    expiresAt.current = null;
    sessionSent.current = false;
    clear();
    postToHostV2(options, "dsh-ee:logged-out", { revoked });
    setError({ code: "auth_required", message: t("embed.loggedOut"), retryable: false });
    setPhase("failed");
  }, [clear, options, t, user]);

  // 2) 宿主 -> iframe：connect / navigate / logout（校验 host origin + window.parent）。
  useEffect(() => {
    if (!active || !options.host) return;
    const onMessage = (event: MessageEvent) => {
      if (!isTrustedParentMessage(event.origin, event.source, options.host, window.parent)) return;
      const data = event.data as Record<string, unknown> | null;
      if (!data || typeof data !== "object") return;
      if (isEmbedMessage(data, "dsh-ee:connect")) {
        const code = data.code;
        if (typeof code === "string" && code) void handleConnect(code);
        return;
      }
      if (isEmbedMessage(data, "dsh-ee:navigate")) {
        // 只允许本应用路径：pushState 切换，不整页刷新（刷新会丢内存 Bearer）；保留嵌入参数。
        const path = normalizeAppPath(data.path, slug);
        if (path) navigateInEmbed(path);
        return;
      }
      if (isEmbedMessage(data, "dsh-ee:logout")) {
        void handleLogout();
      }
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [active, options, handleConnect, handleLogout, navigateInEmbed, slug]);

  // 3) 认证状态：同站 cookie 直接可用；跨站没有会话时请宿主 mint 并握手。
  useEffect(() => {
    if (!active || loading) return;
    if (user) {
      clearTimer();
      if (phase !== "connected") setPhase("connected");
      return;
    }
    if (phase === "failed" || phase === "connecting") return;
    if (!options.host) {
      // 没有 host 就没法握手：直接给失败态（宿主会收到 hello/ready 但没有 connect 通道）。
      setError({ code: "auth_required", message: t("embed.authRequired"), retryable: false });
      setPhase("failed");
      return;
    }
    setPhase("waiting");
    if (!authRequested.current) {
      authRequested.current = true;
      postToHost(options, "dsh-ee:auth-required", { reason: "missing" });
    }
    if (timer.current === null) {
      timer.current = window.setTimeout(() => {
        timer.current = null;
        setError({ code: "auth_required", message: t("embed.authRequired"), retryable: false });
        setPhase("failed");
      }, HANDSHAKE_TIMEOUT_MS);
    }
  }, [active, loading, user, phase, options, t]);

  // 卸载时清计时器。
  useEffect(() => clearTimer, []);

  // 失败态：给宿主一个可判断的 dsh-ee:error（同一个错误只发一次）。
  useEffect(() => {
    if (!active || phase !== "failed" || !error) return;
    const key = `${error.code}\n${error.message}`;
    if (errorSent.current === key) return;
    errorSent.current = key;
    postToHost(options, "dsh-ee:error", { code: error.code, message: error.message, retryable: !!error.retryable });
  }, [active, phase, error, options]);

  // 4) 认证成功：通知宿主 session（只带展示信息，不含 token）。
  useEffect(() => {
    if (!active || !user || sessionSent.current) return;
    sessionSent.current = true;
    const expiresIn = expiresAt.current ? Math.max(0, Math.round((expiresAt.current - Date.now()) / 1000)) : undefined;
    postToHost(options, "dsh-ee:session", {
      user: { display_name: user.display_name || user.username },
      limited: !!user.limited || !!user.embed,
      ...(expiresIn !== undefined ? { expires_in: expiresIn } : {}),
    });
  }, [active, user, options]);

  // 5) 点击拦截（v2）：本应用内链接交给前端路由；同源站外路径发 navigate-request；
  //    外部 https / dsh:// 链接发 open-external，由宿主确认后再打开。
  useEffect(() => {
    if (!active) return;
    const onClick = (event: MouseEvent) => {
      if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
      const target = event.target as Element | null;
      const anchor = target?.closest?.("a[href]") as HTMLAnchorElement | null;
      if (!anchor) return;
      const raw = anchor.getAttribute("href") || "";
      if (!raw || raw.startsWith("#")) return;
      let url: URL;
      try {
        url = new URL(raw, window.location.href);
      } catch {
        return;
      }
      if (url.origin === window.location.origin) {
        const path = url.pathname + url.search + url.hash;
        if (isAppPath(path, slug)) return; // 本应用内：前端路由处理
        event.preventDefault();
        postToHostV2(options, "dsh-ee:navigate-request", { path });
        return;
      }
      // 只把 https 与登记过的 dsh:// 交给宿主；宿主负责确认后再打开。
      if (url.protocol === "https:" || url.protocol === "dsh:") {
        event.preventDefault();
        postToHostV2(options, "dsh-ee:open-external", { url: url.href });
      }
    };
    document.addEventListener("click", onClick, true);
    return () => document.removeEventListener("click", onClick, true);
  }, [active, options, slug]);

  const value = useMemo<EmbedBridge>(() => ({ options, active, phase, error }), [options, active, phase, error]);
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}
