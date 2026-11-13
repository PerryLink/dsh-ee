/**
 * /examples：开发样例目录（固定路由，不随菜单 path 变化）。
 *
 * 8 个精选 DSH 插件样例，字段来自 web/src/data/dev-examples.ts：
 * 标题、一句摘要、难度、涉及的 Cordis 面、固定版本、安装命令、来源 repo@tag、许可。
 * 页面不展示任何检查结论，也不做排序或评级。
 */
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { ArrowUpRight, ExternalLink } from "lucide-react";
import { DEV_EXAMPLES, exampleInstallCommand, exampleSourceUrl } from "../data/dev-examples";
import { isZh } from "../i18n";
import { api } from "../lib/api";
import { useApi } from "../lib/use-api";
import { Badge, Card, cardHover, CopyBox, cx, PageHeader, Notice, type Tone } from "../components/ui";

const difficultyTone: Record<string, Tone> = { starter: "green", intermediate: "brand", advanced: "slate" };

export default function DevExamples() {
  const { t } = useTranslation();
  const connect = useApi(api.connectInfo);
  const profile = connect.data?.profile || "web";
  const zh = isZh();

  return (
    <div className="max-w-6xl" data-dev-page="examples">
      <PageHeader title={t("devdocs.examplesTitle")} desc={t("devdocs.examplesDesc")} />

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
        {DEV_EXAMPLES.map((example) => (
          <Card key={example.id} className="flex h-full flex-col p-5" data-dev-example={example.id}>
            <div className="flex flex-wrap items-start justify-between gap-2">
              <Link
                to={`/examples/${example.id}`}
                className="text-[15px] leading-6 font-semibold text-neutral-900 hover:text-brand-700 dark:hover:text-brand-300"
              >
                {zh ? example.title.zh : example.title.en}
              </Link>
              <Badge tone={difficultyTone[example.difficulty] ?? "slate"}>{t(`devdocs.difficultyLevels.${example.difficulty}`)}</Badge>
            </div>
            <p className="mt-1.5 line-clamp-3 text-sm leading-6 text-neutral-600">{zh ? example.summary.zh : example.summary.en}</p>
            <div className="mt-2.5 flex flex-wrap items-center gap-1.5">
              {example.surfaces.map((s) => (
                <Badge key={s} tone="slate">
                  {t(`devdocs.surfaceNames.${s}`)}
                </Badge>
              ))}
              <span className="text-xs text-neutral-500 tabular-nums">v{example.version}</span>
            </div>
            <div className="mt-auto pt-4">
              <CopyBox text={exampleInstallCommand(example, profile)} label={t("devdocs.installTitle")} wrap />
            </div>
            <div className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-neutral-100 pt-3 text-sm dark:border-white/10">
              <Link
                to={`/examples/${example.id}`}
                className="inline-flex items-center gap-1 font-medium text-brand-600 hover:underline dark:text-brand-300"
              >
                {t("devdocs.exampleDetail")}
                <ArrowUpRight size={14} />
              </Link>
              <a
                href={exampleSourceUrl(example)}
                target="_blank"
                rel="noreferrer"
                className="inline-flex items-center gap-1 text-neutral-500 hover:text-neutral-900 dark:hover:text-white"
              >
                <ExternalLink size={13} />
                {t("devdocs.source")}
              </a>
            </div>
          </Card>
        ))}
      </div>

      <Notice className="mt-6" tone="info">
        {t("devdocs.sourceDisclaimer")}
      </Notice>
    </div>
  );
}
