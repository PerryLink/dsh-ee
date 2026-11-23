/**
 * 机器人回复的 Markdown 渲染（对话气泡里用，比开发文档的渲染器紧凑）。
 *
 * 安全口径：
 * - 不启用 rehype-raw：回复里的 HTML 只会按纯文本显示，不会变成真实节点；
 * - 链接只放行 http(s) 与 mailto，一律新窗口打开；其它协议、站内相对路径按纯文本显示；
 * - 图片不自动加载（避免回复触发对外请求），显示成可点击的链接。
 */
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { cx } from "./ui";

const linkCls = "font-medium break-words text-brand-600 underline-offset-2 hover:underline dark:text-brand-300";
const safeHref = (href: unknown): string | null => (typeof href === "string" && /^(https?:\/\/|mailto:)/i.test(href) ? href : null);

const components: Components = {
  h1: ({ children }) => <h3 className="mt-4 mb-2 text-base font-semibold text-neutral-900 first:mt-0">{children}</h3>,
  h2: ({ children }) => <h3 className="mt-4 mb-2 text-base font-semibold text-neutral-900 first:mt-0">{children}</h3>,
  h3: ({ children }) => <h4 className="mt-3 mb-1.5 text-sm font-semibold text-neutral-900 first:mt-0">{children}</h4>,
  h4: ({ children }) => <h5 className="mt-3 mb-1.5 text-sm font-semibold text-neutral-800 first:mt-0">{children}</h5>,
  p: ({ children }) => <p className="my-2 first:mt-0 last:mb-0">{children}</p>,
  ul: ({ children }) => <ul className="my-2 list-disc space-y-1 pl-5 first:mt-0 last:mb-0">{children}</ul>,
  ol: ({ children }) => <ol className="my-2 list-decimal space-y-1 pl-5 first:mt-0 last:mb-0">{children}</ol>,
  li: ({ children }) => <li className="pl-0.5 marker:text-neutral-400">{children}</li>,
  blockquote: ({ children }) => (
    <blockquote className="my-3 border-l-2 border-neutral-300 pl-3 text-neutral-600 dark:border-white/20">{children}</blockquote>
  ),
  hr: () => <hr className="my-4 border-neutral-200 dark:border-white/10" />,
  table: ({ children }) => (
    <div className="my-3 max-w-full overflow-x-auto rounded-lg border border-neutral-200 dark:border-white/10">
      <table className="tbl">{children}</table>
    </div>
  ),
  th: ({ children }) => <th className="px-3 py-2">{children}</th>,
  td: ({ children }) => <td className="px-3 py-2 align-top">{children}</td>,
  pre: ({ children }) => (
    <pre className="my-3 max-w-full overflow-x-auto rounded-lg bg-neutral-900 px-3.5 py-3 font-mono text-[13px] leading-6 text-neutral-100 ring-1 ring-white/5 ring-inset">
      {children}
    </pre>
  ),
  code: ({ className, children }) => {
    if (className?.includes("language-")) return <code className={cx("font-mono", className)}>{children}</code>;
    return (
      <code className="rounded bg-neutral-100 px-1 py-0.5 font-mono text-[0.85em] break-words text-neutral-800 dark:bg-white/10 dark:text-neutral-200">
        {children}
      </code>
    );
  },
  a: ({ href, children }) => {
    const safe = safeHref(href);
    if (!safe) return <span>{children}</span>;
    return (
      <a href={safe} target="_blank" rel="noopener noreferrer" className={linkCls}>
        {children}
      </a>
    );
  },
  img: ({ src, alt }) => {
    const safe = safeHref(src);
    const label = alt || (typeof src === "string" ? src : "");
    if (!safe) return <span>{label}</span>;
    return (
      <a href={safe} target="_blank" rel="noopener noreferrer" className={linkCls}>
        {label}
      </a>
    );
  },
  strong: ({ children }) => <strong className="font-semibold text-neutral-900">{children}</strong>,
};

export function ChatMarkdown({ markdown, className }: { markdown: string; className?: string }) {
  return (
    <div className={cx("min-w-0 text-sm leading-6 text-neutral-800 [overflow-wrap:anywhere]", className)}>
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {markdown}
      </ReactMarkdown>
    </div>
  );
}
