/**
 * 开发文档的数据结构。
 *
 * 页面用 `DevDoc`（正文叫 body）；构建期从 docs/ 生成的产物用 `GeneratedDevDoc`
 * （字段与 docs/ 里的 front matter 对齐，正文叫 markdown）。
 * 生成器 scripts/docs/build.mjs 只依赖这里的类型形状，不引第三方依赖。
 */

/** 文档分类：列表页按它展示标签，顺序即导航顺序。 */
export type DocCategory = "start" | "harness" | "plugin" | "extension" | "access" | "publish" | "quality";

/** 顺序即列表页分类筛选的展示顺序。 */
export const DOC_CATEGORIES: DocCategory[] = ["start", "harness", "plugin", "extension", "access", "publish", "quality"];

/** 站内页面使用的文档结构。 */
export interface DevDoc {
  /** 稳定 slug：详情路由 /docs/:slug */
  slug: string;
  category: DocCategory;
  title: { zh: string; en: string };
  summary: { zh: string; en: string };
  /** 中文正文（Markdown）。docs/ 是唯一事实源，不复制上游正文。 */
  body: string;
  /** 内容复核日期，展示在列表与详情页；空字符串表示未标注。 */
  updatedAt: string;
  tags: string[];
  /** 仓库内源文件路径（相对 docs/，如 dev/overview.md）；旧静态兜底数据没有时为空。 */
  sourcePath?: string;
}

/** 构建期生成物（web/src/generated/dev-docs.ts）的元素结构。 */
export interface GeneratedDevDoc {
  slug: string;
  category: DocCategory;
  title: { zh: string; en: string };
  summary: { zh: string; en: string };
  tags: string[];
  /** front matter 的 updated_at，YYYY-MM-DD；未标注为空字符串。 */
  updated_at: string;
  /** 仓库内源文件路径（相对 docs/），详情页「查看源文件」用。 */
  source: string;
  /** 中文正文（Markdown），已由生成器去掉 front matter、改写站内链接。 */
  markdown: string;
}
