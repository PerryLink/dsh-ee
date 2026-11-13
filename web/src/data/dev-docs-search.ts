/**
 * 站内开发文档的全文搜索（标题 + 摘要 + 标签 + 正文）。
 *
 * 正文来自构建期生成物（docs/ 单一事实源），本来就在站内包里；这里把 Markdown
 * 转成纯文本后做「子串 + 多词全部命中」匹配：中文两字词直接命中，英文按单词子串命中。
 * 纯函数、无依赖；索引按文档懒构建（首次搜索时才算，并缓存在模块内），
 * /docs 页过滤与 ⌘K 的「文档」分组共用这一套逻辑。
 */
import type { DevDoc } from "./dev-docs-types";

export interface DevDocHit {
  doc: DevDoc;
  /** 命中分：标题 > 摘要 > 标签/正文，用于排序。 */
  score: number;
  /** 正文里第一处命中的一小段（没有正文命中时用摘要）。 */
  snippet: string;
}

/** 去掉 Markdown 语法，保留可读正文（代码块内容也保留，方便搜 `ee_at_` 这类词）。 */
export function plainText(markdown: string): string {
  return markdown
    .replace(/```[^\n]*\n?/g, " ")
    .replace(/~~~[^\n]*\n?/g, " ")
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/<[^>]+>/g, " ")
    .replace(/^\s{0,3}#{1,6}\s+/gm, "")
    .replace(/^\s{0,3}>\s?/gm, "")
    .replace(/[`*_~]/g, "")
    .replace(/\|/g, " ")
    .replace(/\s+/g, " ")
    .trim();
}

const textCache = new Map<string, string>();

/** slug → 正文纯文本（懒构建并缓存；正文不变时只算一次）。 */
export function docBodyText(doc: DevDoc): string {
  const cached = textCache.get(doc.slug);
  if (cached !== undefined) return cached;
  const text = plainText(doc.body);
  textCache.set(doc.slug, text);
  return text;
}

/** 搜索用的小写 haystack：标题、摘要、标签、正文。 */
function haystack(doc: DevDoc): { title: string; summary: string; tags: string; body: string } {
  return {
    title: `${doc.title.zh} ${doc.title.en}`.toLowerCase(),
    summary: `${doc.summary.zh} ${doc.summary.en}`.toLowerCase(),
    tags: doc.tags.join(" ").toLowerCase(),
    body: docBodyText(doc).toLowerCase(),
  };
}

function countOccurrences(haystackText: string, needle: string): number {
  if (!needle) return 0;
  let count = 0;
  let index = haystackText.indexOf(needle);
  while (index !== -1 && count < 5) {
    count += 1;
    index = haystackText.indexOf(needle, index + needle.length);
  }
  return count;
}

/**
 * 是否命中：查询按空白拆成多个词，**每个词都要出现**（中文两字词直接子串匹配）。
 * 空查询返回 true（列表页显示全部）。
 */
export function matchesDevDoc(doc: DevDoc, query: string): boolean {
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  if (!words.length) return true;
  const hay = haystack(doc);
  return words.every((word) => hay.title.includes(word) || hay.summary.includes(word) || hay.tags.includes(word) || hay.body.includes(word));
}

/** 正文命中的上下文片段；找不到正文命中时退回摘要。 */
function bodySnippet(doc: DevDoc, word: string): string {
  if (!word) return doc.summary.zh;
  const body = docBodyText(doc);
  const index = body.toLowerCase().indexOf(word);
  if (index === -1) return doc.summary.zh;
  const start = Math.max(0, index - 24);
  const end = Math.min(body.length, index + word.length + 40);
  const prefix = start > 0 ? "…" : "";
  const suffix = end < body.length ? "…" : "";
  return `${prefix}${body.slice(start, end).trim()}${suffix}`;
}

/** 按命中质量排序的搜索结果；limit 默认 6（⌘K 分组用）。 */
export function searchDevDocs(docs: DevDoc[], query: string, limit = 6): DevDocHit[] {
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  if (!words.length) return [];
  const hits: DevDocHit[] = [];
  docs.forEach((doc, order) => {
    const hay = haystack(doc);
    let score = 0;
    for (const word of words) {
      const inTitle = hay.title.includes(word);
      const inSummary = hay.summary.includes(word);
      const inTags = hay.tags.includes(word);
      const bodyHits = countOccurrences(hay.body, word);
      if (!inTitle && !inSummary && !inTags && bodyHits === 0) return;
      if (inTitle) score += 6;
      if (inSummary) score += 4;
      if (inTags) score += 2;
      score += Math.min(bodyHits, 3);
    }
    if (!score) return;
    // 同分按白名单顺序（命中多的文档自然排在前面）
    hits.push({ doc, score: score * 1000 - order, snippet: bodySnippet(doc, words[0]) });
  });
  return hits.sort((a, b) => b.score - a.score).slice(0, limit);
}
