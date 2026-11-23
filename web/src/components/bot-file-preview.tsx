/**
 * 机器人生成的文件在网页里的预览（/bot-chat 的文件库和对话里的「生成的文件」共用）。
 *
 * - 文件走原有的产物下载接口（/api/runtime/tasks/{task}/artifacts/{id}），带登录态读取；
 *   能不能读由后端按所有者判断，这里不做任何放行。
 * - Markdown 按回复同样的安全口径排版（不渲染 HTML、不自动加载图片），也可以切到原文；
 *   其它文本原样显示；图片、PDF 用本地 blob 地址显示，并强制成对应的类型，
 *   一个改了后缀的 HTML 文件不会被当成网页打开。其余类型只给下载。
 * - 只在员工点开时才读文件。
 */
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Download } from "lucide-react";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/shadcn/dialog";
import { ChatMarkdown } from "./chat-markdown";
import { Segmented, Spinner, buttonCls } from "./ui";
import { ApiError } from "../lib/api";
import { formatBytes } from "../lib/format";
import { toChatError, type ChatError } from "../lib/bot-chat";

export interface PreviewFile {
  name: string;
  url: string;
  size?: number | null;
}

type Kind = "markdown" | "text" | "image" | "pdf" | "other";

const TEXT_EXT = new Set([
  "txt", "csv", "tsv", "json", "jsonl", "log", "yaml", "yml", "xml", "html", "htm", "ini", "toml", "conf", "sql",
  "py", "js", "ts", "tsx", "jsx", "sh", "go", "java", "rb", "rs", "c", "h", "cpp", "css", "env",
]);
const IMAGE_TYPES: Record<string, string> = {
  png: "image/png", jpg: "image/jpeg", jpeg: "image/jpeg", gif: "image/gif", webp: "image/webp", bmp: "image/bmp", svg: "image/svg+xml",
};
/** 文本超过这个大小就不在网页里显示（浏览器排版很慢），只给下载。 */
const TEXT_MAX = 1 << 20;
const BLOB_MAX = 8 << 20;

export function previewKind(name: string): Kind {
  const ext = name.toLowerCase().split(".").pop() ?? "";
  if (ext === "md" || ext === "markdown") return "markdown";
  if (TEXT_EXT.has(ext)) return "text";
  if (ext in IMAGE_TYPES) return "image";
  if (ext === "pdf") return "pdf";
  return "other";
}

const mimeOf = (name: string, kind: Kind) => (kind === "pdf" ? "application/pdf" : IMAGE_TYPES[name.toLowerCase().split(".").pop() ?? ""] ?? "");

type State =
  | { status: "loading" }
  | { status: "text"; text: string }
  | { status: "blob"; url: string }
  | { status: "unsupported" | "tooLarge" }
  | { status: "error"; error: ChatError };

export function FilePreviewDialog({ file, onClose }: { file: PreviewFile | null; onClose: () => void }) {
  const { t, i18n } = useTranslation();
  const [state, setState] = useState<State>({ status: "loading" });
  const [view, setView] = useState<"rendered" | "source">("rendered");
  const kind = file ? previewKind(file.name) : "other";

  useEffect(() => {
    if (!file) return;
    setView("rendered");
    if (kind === "other") {
      setState({ status: "unsupported" });
      return;
    }
    const limit = kind === "markdown" || kind === "text" ? TEXT_MAX : BLOB_MAX;
    if (typeof file.size === "number" && file.size > limit) {
      setState({ status: "tooLarge" });
      return;
    }
    let stopped = false;
    let objectUrl = "";
    setState({ status: "loading" });
    (async () => {
      let res: Response;
      try {
        res = await fetch(file.url, { credentials: "same-origin" });
      } catch {
        throw new ApiError(0, "");
      }
      if (!res.ok) {
        let data: unknown = null;
        try {
          data = await res.json();
        } catch {
          // 不是 JSON（代理错误页等）：按状态码提示
        }
        throw new ApiError(res.status, "", data);
      }
      const bytes = await res.arrayBuffer();
      if (bytes.byteLength > limit) return { status: "tooLarge" } as const;
      if (kind === "markdown" || kind === "text") return { status: "text", text: new TextDecoder().decode(bytes) } as const;
      objectUrl = URL.createObjectURL(new Blob([bytes], { type: mimeOf(file.name, kind) }));
      return { status: "blob", url: objectUrl } as const;
    })().then(
      (next) => {
        if (!stopped) setState(next);
      },
      (err: unknown) => {
        if (!stopped) setState({ status: "error", error: toChatError(err, "files") });
      },
    );
    return () => {
      stopped = true;
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [file, kind]);

  return (
    <Dialog open={!!file} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="flex max-h-[calc(100dvh-2rem)] flex-col gap-3 sm:max-w-3xl" data-file-preview="">
        <DialogHeader className="min-w-0 pr-8">
          <DialogTitle className="truncate text-base">{file?.name}</DialogTitle>
          <DialogDescription className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
            <span>{t("botChat.filePreview.title")}</span>
            {typeof file?.size === "number" && <span>{formatBytes(file.size, i18n.language)}</span>}
          </DialogDescription>
        </DialogHeader>
        {kind === "markdown" && state.status === "text" && (
          <div>
            <Segmented
            value={view}
            onChange={setView}
            options={[
              { value: "rendered", label: t("botChat.filePreview.rendered") },
                { value: "source", label: t("botChat.filePreview.source") },
              ]}
            />
          </div>
        )}
        <div className="min-h-40 min-w-0 flex-1 overflow-auto rounded-lg border border-neutral-200 bg-card dark:border-white/10">
          {state.status === "loading" && (
            <div role="status" className="flex h-40 items-center justify-center gap-2 text-[13px] text-neutral-500">
              <Spinner size={14} className="text-neutral-400" />
              {t("botChat.filePreview.loading")}
            </div>
          )}
          {state.status === "error" && (
            <p role="alert" className="p-4 text-[13px] text-red-600 dark:text-red-400">
              {t("botChat.filePreview.failed", { reason: t(`botChat.errors.${state.error.key}`) })}
            </p>
          )}
          {(state.status === "unsupported" || state.status === "tooLarge") && (
            <p className="p-4 text-[13px] leading-5 text-neutral-600">{t(`botChat.filePreview.${state.status}`)}</p>
          )}
          {state.status === "text" &&
            (kind === "markdown" && view === "rendered" ? (
              <div className="px-5 py-4 text-sm leading-6 text-neutral-800">
                <ChatMarkdown markdown={state.text} />
              </div>
            ) : (
              <pre className="p-4 font-mono text-[12.5px] leading-5 break-words whitespace-pre-wrap text-neutral-800">{state.text}</pre>
            ))}
          {state.status === "blob" &&
            (kind === "image" ? (
              <div className="flex justify-center p-4">
                <img src={state.url} alt={file?.name ?? ""} className="max-h-[70dvh] max-w-full object-contain" />
              </div>
            ) : (
              <iframe title={file?.name ?? ""} src={state.url} className="h-[70dvh] w-full" />
            ))}
        </div>
        {file && (
          <div className="flex justify-end">
            <a href={file.url} download={file.name.split("/").pop()} className={buttonCls("secondary", "sm")}>
              <Download size={14} aria-hidden />
              {t("botChat.filePreview.download")}
            </a>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
