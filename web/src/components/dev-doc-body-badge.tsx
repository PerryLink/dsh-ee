/**
 * 正文语言徽标：当前界面语言还没有正文时，提示「正文为中文」或「显示的是回退语言正文」。
 *
 * 正文按语言懒加载（见 lib/dev-docs-i18n.ts）：列表页只读同步的 hasBody 元数据（不加载 chunk）；
 * 详情页在正文加载完成后把实际拿到的语言（bodyLang）传进来，避免元数据与 chunk 内容不一致时误报。
 * 有请求语言的正文时不渲染任何东西。
 */
import { useTranslation } from "react-i18next";
import type { DevDoc, DocLang } from "../data/dev-docs-types";
import { docLang, docLangName, fallbackBodyLang, hasBodyFor } from "../lib/dev-docs-i18n";
import { Badge } from "./ui";

export function DevDocBodyBadge({ doc, lang, bodyLang }: { doc: DevDoc; lang: string; bodyLang?: DocLang }) {
  const { t, i18n } = useTranslation();
  const requested = docLang(lang);
  const actual = bodyLang ?? (hasBodyFor(doc, requested) ? requested : fallbackBodyLang(doc, requested));
  if (actual === requested) return null;
  // 回退到中文：沿用已有的「正文为中文」；回退到其它语言（如 es 界面回退英文）时带上实际语言名
  return (
    <Badge tone="amber">
      {actual === "zh" ? t("devdocs.zhOnly") : t("devdocs.bodyFallback", { lang: docLangName(actual, i18n.resolvedLanguage) })}
    </Badge>
  );
}
