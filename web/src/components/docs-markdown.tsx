/**
 * 开发文档的 Markdown 渲染器（开发文档 / 开发样例详情页共用）。
 *
 * 用 react-markdown + remark-gfm + rehype-slug：
 * - GFM：表格、任务列表、删除线（文档里大量用表格）；
 * - rehype-slug：标题按 GitHub 同款算法生成 id，`/docs/<slug>#<锚点>` 可以直接深链；
 * - 标题旁给一个 # 链接，页内目录从渲染后的 h2/h3 里取，避免自己再实现一遍 slug 算法。
 *
 * 站内绝对路径（/docs/...）用 react-router 的 Link 走无刷新跳转，其它站外链接新窗口打开。
 */
import { useEffect, useRef, useState, type ReactNode } from "react";
import { Link } from "react-router";
import { Link2 } from "lucide-react";
import ReactMarkdown, { type Components } from "react-markdown";
import rehypeSlug from "rehype-slug";
import remarkGfm from "remark-gfm";
import { useTranslation } from "react-i18next";
import { cx } from "./ui";

interface TocItem {
  id: string;
  text: string;
  level: 2 | 3;
}

const anchorCls = "ml-1 inline-flex shrink-0 items-center rounded p-0.5 text-neutral-300 no-underline opacity-0 transition-opacity group-hover:opacity-100 focus:opacity-100 hover:text-brand-600 dark:text-neutral-600 dark:hover:text-brand-300";

function Heading({
  level,
  id,
  children,
}: {
  level: 1 | 2 | 3 | 4;
  id?: string;
  children?: ReactNode;
}) {
  const { t } = useTranslation();
  const text = typeof children === "string" ? children : undefined;
  const sizes = {
    1: "mt-8 mb-3 text-xl font-bold text-neutral-900",
    2: "mt-8 mb-3 text-lg font-semibold text-neutral-900",
    3: "mt-6 mb-2 text-base font-semibold text-neutral-900",
    4: "mt-5 mb-2 text-sm font-semibold text-neutral-800",
  } as const;
  const Tag = `h${level}` as "h1" | "h2" | "h3" | "h4";
  return (
    <Tag id={id} data-toc-title={text} className={cx("group scroll-mt-24", sizes[level])}>
      {children}
      {id && (
        <a href={`#${id}`} aria-label={t("devdocs.anchor")} className={anchorCls}>
          <Link2 size={14} />
        </a>
      )}
    </Tag>
  );
}

const components: Components = {
  h1: ({ id, children }) => (
    <Heading level={1} id={id}>
      {children}
    </Heading>
  ),
  h2: ({ id, children }) => (
    <Heading level={2} id={id}>
      {children}
    </Heading>
  ),
  h3: ({ id, children }) => (
    <Heading level={3} id={id}>
      {children}
    </Heading>
  ),
  h4: ({ id, children }) => (
    <Heading level={4} id={id}>
      {children}
    </Heading>
  ),
  p: ({ children }) => <p className="my-3 text-sm leading-6 text-neutral-700">{children}</p>,
  ul: ({ children }) => <ul className="my-3 list-disc space-y-1 pl-5 text-sm leading-6 text-neutral-700">{children}</ul>,
  ol: ({ children }) => <ol className="my-3 list-decimal space-y-1 pl-5 text-sm leading-6 text-neutral-700">{children}</ol>,
  li: ({ children }) => <li className="pl-0.5 marker:text-neutral-400">{children}</li>,
  blockquote: ({ children }) => (
    <blockquote className="my-4 border-l-2 border-brand-300 bg-neutral-50 px-4 py-1.5 text-neutral-600 dark:border-brand-400/40 dark:bg-white/5">{children}</blockquote>
  ),
  hr: () => <hr className="my-8 border-neutral-200 dark:border-white/10" />,
  table: ({ children }) => (
    <div className="my-4 overflow-x-auto rounded-lg border border-neutral-200 dark:border-white/10">
      <table className="tbl">{children}</table>
    </div>
  ),
  thead: ({ children }) => <thead>{children}</thead>,
  tbody: ({ children }) => <tbody>{children}</tbody>,
  tr: ({ children }) => <tr>{children}</tr>,
  th: ({ children }) => <th className="px-3.5 py-2.5">{children}</th>,
  td: ({ children }) => <td className="px-3.5 py-2.5 align-top">{children}</td>,
  pre: ({ children }) => (
    <pre className="my-4 overflow-x-auto rounded-lg bg-neutral-900 px-4 py-3 font-mono text-[13px] leading-6 text-neutral-100 shadow-card ring-1 ring-white/5 ring-inset">
      {children}
    </pre>
  ),
  code: ({ className, children }) => {
    // 围栏代码块：react-markdown 会给 language-* 类；行内代码保持浅色底
    if (className?.includes("language-")) return <code className={cx("font-mono", className)}>{children}</code>;
    return (
      <code className="rounded bg-neutral-100 px-1 py-0.5 font-mono text-[0.85em] text-neutral-800 dark:bg-white/10 dark:text-neutral-200">{children}</code>
    );
  },
  a: ({ href, children }) => {
    if (!href) return <span>{children}</span>;
    if (href.startsWith("#")) {
      return (
        <a href={href} className="font-medium text-brand-600 underline-offset-2 hover:underline dark:text-brand-300">
          {children}
        </a>
      );
    }
    if (href.startsWith("/")) {
      return (
        <Link to={href} className="font-medium text-brand-600 underline-offset-2 hover:underline dark:text-brand-300">
          {children}
        </Link>
      );
    }
    return (
      <a href={href} target="_blank" rel="noreferrer" className="font-medium text-brand-600 underline-offset-2 hover:underline dark:text-brand-300">
        {children}
        <span className="sr-only"> ↗</span>
      </a>
    );
  },
  img: ({ src, alt }) => <img src={typeof src === "string" ? src : undefined} alt={alt ?? ""} className="my-4 max-w-full rounded-lg border border-neutral-200" />,
  strong: ({ children }) => <strong className="font-semibold text-neutral-900">{children}</strong>,
};

/** 从渲染后的标题里提取页内目录（h2 / h3），移动端不显示，正文的 # 锚点仍可用。 */
function useToc(markdown: string) {
  const ref = useRef<HTMLDivElement>(null);
  const [items, setItems] = useState<TocItem[]>([]);
  useEffect(() => {
    const root = ref.current;
    if (!root) return;
    const next: TocItem[] = [];
    for (const el of root.querySelectorAll<HTMLHeadingElement>("h2[id], h3[id]")) {
      next.push({ id: el.id, text: el.dataset.tocTitle ?? el.textContent ?? "", level: el.tagName === "H3" ? 3 : 2 });
    }
    setItems(next);
  }, [markdown]);
  return { ref, items };
}

export function DocsMarkdown({ markdown, className }: { markdown: string; className?: string }) {
  const { t } = useTranslation();
  const { ref, items } = useToc(markdown);
  return (
    <div ref={ref} className={cx("lg:grid lg:grid-cols-[200px_minmax(0,1fr)] lg:items-start lg:gap-10", className)}>
      {items.length > 1 && (
        <nav aria-label={t("devdocs.toc")} className="sticky top-24 hidden max-h-[calc(100vh-8rem)] overflow-y-auto text-sm lg:block">
          <div className="mb-2 text-xs font-semibold tracking-wide text-neutral-500 uppercase">{t("devdocs.toc")}</div>
          <ul className="space-y-1 border-l border-neutral-200 dark:border-white/10">
            {items.map((it) => (
              <li key={it.id} className={it.level === 3 ? "pl-3" : ""}>
                <a
                  href={`#${it.id}`}
                  className="-ml-px block border-l border-transparent py-0.5 pl-3 text-neutral-600 transition-colors hover:border-brand-400 hover:text-brand-700 dark:text-neutral-400 dark:hover:text-brand-300"
                >
                  {it.text}
                </a>
              </li>
            ))}
          </ul>
        </nav>
      )}
      <div className="min-w-0" data-dev-markdown="">
        <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeSlug]} components={components}>
          {markdown}
        </ReactMarkdown>
      </div>
    </div>
  );
}
