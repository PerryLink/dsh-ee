import { useEffect, useMemo, useRef } from "react";
import { useLocation } from "react-router";
import {
  applyEmbedTheme,
  capEmbedHeight,
  isEmbedMessage,
  isTrustedParentMessage,
  parseEmbedOptions,
  postToHost,
  resolvedDocumentTheme,
  type EmbedError,
  type EmbedOptions,
} from "./embed";

/** 解析嵌入参数（?embed=1&host=…&fit=auto&theme=…）；非嵌入模式全部为默认值。 */
export function useEmbedOptions(): EmbedOptions {
  const { search } = useLocation();
  return useMemo(() => parseEmbedOptions(search), [search]);
}

/**
 * 主题双向：`?theme=` 是初始值，宿主还可以随时发 `dsh-ee:theme` 改；应用只改 DOM 不写 localStorage，
 * 每次应用后把实际主题回发给宿主。
 */
export function useEmbedTheme(options: EmbedOptions): void {
  useEffect(() => {
    if (!options.embed) return;
    if (options.theme) applyEmbedTheme(options.theme);
    // 先告诉宿主当前实际主题；宿主在 ready 之前收到也要容忍（协议里 ready 也带 theme）。
    postToHost(options, "dsh-ee:theme", { theme: resolvedDocumentTheme() });
  }, [options]);

  useEffect(() => {
    if (!options.embed || !options.host) return;
    const onMessage = (event: MessageEvent) => {
      if (!isTrustedParentMessage(event.origin, event.source, options.host, window.parent)) return;
      if (!isEmbedMessage(event.data, "dsh-ee:theme")) return;
      const theme = (event.data as { theme?: unknown }).theme;
      if (theme !== "light" && theme !== "dark") return;
      applyEmbedTheme(theme);
      postToHost(options, "dsh-ee:theme", { theme });
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [options]);
}

/**
 * `fit=auto`：用 ResizeObserver 上报内容高度，节流到一帧，高度上限 EMBED_MAX_HEIGHT。
 * 宿主收到 `dsh-ee:resize` 后自己决定是否按高度设置 iframe；默认 fill 不主动上报。
 */
export function useEmbedResize(options: EmbedOptions): void {
  useEffect(() => {
    if (!options.embed || options.fit !== "auto" || !options.host) return;
    let frame = 0;
    let lastHeight = -1;
    const report = () => {
      frame = 0;
      const height = capEmbedHeight(document.documentElement.scrollHeight);
      if (height === lastHeight) return;
      lastHeight = height;
      postToHost(options, "dsh-ee:resize", { height, width: document.documentElement.clientWidth });
    };
    const schedule = () => {
      if (!frame) frame = requestAnimationFrame(report);
    };
    schedule();
    const observer = new ResizeObserver(schedule);
    observer.observe(document.documentElement);
    window.addEventListener("resize", schedule);
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", schedule);
      if (frame) cancelAnimationFrame(frame);
    };
  }, [options]);
}

/** 加载失败 / 未登录 / 无权限：给宿主发 `dsh-ee:error`；同一条错误只发一次，避免刷新循环。 */
export function useEmbedError(options: EmbedOptions, error: EmbedError | null): void {
  const sent = useRef("");
  useEffect(() => {
    if (!options.embed || !error) return;
    const key = `${error.code}\n${error.message}`;
    if (sent.current === key) return;
    sent.current = key;
    postToHost(options, "dsh-ee:error", { code: error.code, message: error.message, retryable: !!error.retryable });
  }, [options, error, error?.code, error?.message, error?.retryable]);
}
