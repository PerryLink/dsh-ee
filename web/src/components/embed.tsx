import { useTranslation } from "react-i18next";
import { nonEmbedURL, type EmbedError } from "../lib/embed";
import { useEmbedBridge } from "../lib/embed-provider";
import { Button, Spinner } from "./ui";

/**
 * 嵌入模式等待握手（跨站 iframe）：宿主拿到 ready 后 mint 一次性 code 并发 connect，
 * 这里只显示「正在连接」，一直等不到会由 EmbedProvider 超时转成失败态。
 * iframe 内不跳登录页，兜底是顶层打开的链接。
 */
export function EmbedGate() {
  const { t } = useTranslation();
  const { phase, error } = useEmbedBridge();
  if (phase === "failed" && error) return <EmbedFailure error={error} />;
  return (
    <div className="flex min-h-screen items-center justify-center bg-neutral-50 px-4">
      <div className="w-full max-w-md rounded-xl bg-card p-6 text-center shadow-card ring-1 ring-neutral-200/80 dark:ring-white/10">
        <div role="status" className="flex items-center justify-center gap-2 text-sm text-neutral-600">
          <Spinner size={16} className="text-neutral-400" />
          {t("embed.connecting")}
        </div>
        <a href={nonEmbedURL()} target="_top" rel="noreferrer" className="mt-4 inline-block text-sm font-medium text-brand-600 hover:underline dark:text-brand-300">
          {t("embed.openInBrowser")}
        </a>
        <p className="mt-3 text-xs leading-5 text-neutral-500">{t("embed.connectHint")}</p>
      </div>
    </div>
  );
}

/**
 * 嵌入模式的失败态（未登录 / 会话过期 / 应用不存在或没权限 / 网络错误）：
 * 不在 iframe 里跳登录页，而是由调用方把 `dsh-ee:error` 发给宿主，这里只负责展示与「在浏览器打开」兜底。
 * 宿主收到错误后可以在顶层窗口走登录，或直接打开这个地址。
 */
export function EmbedFailure({ error, onRetry }: { error: EmbedError; onRetry?: () => void }) {
  const { t } = useTranslation();
  return (
    <div className="flex min-h-screen items-center justify-center bg-neutral-50 px-4">
      <div className="w-full max-w-md rounded-xl bg-card p-6 shadow-card ring-1 ring-neutral-200/80 dark:ring-white/10">
        <p className="text-sm leading-6 text-neutral-700">{error.message}</p>
        <div className="mt-4 flex flex-wrap items-center gap-3">
          {onRetry && (
            <Button size="sm" variant="secondary" onClick={onRetry}>
              {t("common.retry")}
            </Button>
          )}
          <a href={nonEmbedURL()} target="_top" rel="noreferrer" className="text-sm font-medium text-brand-600 hover:underline dark:text-brand-300">
            {t("embed.openInBrowser")}
          </a>
        </div>
        <p className="mt-4 text-xs leading-5 text-neutral-500">{t("embed.failureHint")}</p>
      </div>
    </div>
  );
}
