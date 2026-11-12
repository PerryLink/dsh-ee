/**
 * /examples/:id：开发样例详情（固定路由）。
 *
 * 详情页只放导读、前置条件、固定版本的安装命令，以及来源 repo@tag / 许可 / 归属。
 * 不复制上游 README、截图与媒体，也不展示任何检查结论。
 */
import { Link, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import { ArrowLeft, ExternalLink } from "lucide-react";
import { findDevExample, exampleInstallCommand, exampleLicenseUrl, exampleSourceUrl, exampleWalkthroughUrl } from "../data/dev-examples";
import { isZh } from "../i18n";
import { api } from "../lib/api";
import { useApi } from "../lib/use-api";
import { Badge, Card, CopyBox, Empty, Notice, backLinkCls, type Tone } from "../components/ui";

const difficultyTone: Record<string, Tone> = { starter: "green", intermediate: "brand", advanced: "slate" };

export default function DevExampleDetail() {
  const { t } = useTranslation();
  const { id = "" } = useParams();
  const example = findDevExample(id);
  const connect = useApi(api.connectInfo);
  const profile = connect.data?.profile || "web";
  const zh = isZh();

  if (!example) {
    return (
      <div className="max-w-4xl" data-dev-page="examples">
        <Empty
          title={t("devdocs.exampleNotFoundTitle")}
          action={
            <Link to="/examples" className="text-sm font-medium text-brand-600 hover:underline dark:text-brand-300">
              {t("devdocs.backToExamples")}
            </Link>
          }
        >
          {t("devdocs.exampleNotFoundHint")}
        </Empty>
      </div>
    );
  }

  return (
    <div className="max-w-4xl" data-dev-page="examples" data-dev-example={example.id}>
      <Link to="/examples" className={backLinkCls}>
        <ArrowLeft size={14} />
        {t("devdocs.backToExamples")}
      </Link>

      <div className="mb-6">
        <div className="flex flex-wrap items-center gap-2">
          <Badge tone={difficultyTone[example.difficulty] ?? "slate"}>{t(`devdocs.difficultyLevels.${example.difficulty}`)}</Badge>
          <span className="text-xs text-neutral-500 tabular-nums">v{example.version}</span>
          <span className="text-xs text-neutral-500 tabular-nums">{t("devdocs.updated", { date: example.updatedAt })}</span>
        </div>
        <h1 className="mt-2 text-2xl leading-8 font-bold tracking-[-0.02em] text-neutral-900 sm:text-[28px] sm:leading-9">
          {zh ? example.title.zh : example.title.en}
        </h1>
        <p className="mt-2 max-w-3xl text-sm leading-6 text-neutral-600">{zh ? example.summary.zh : example.summary.en}</p>
      </div>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <Card className="p-5">
          <div className="text-xs font-semibold tracking-wide text-neutral-500 uppercase">{t("devdocs.teach")}</div>
          <p className="mt-1.5 text-sm leading-6 text-neutral-700">{zh ? example.teach.zh : example.teach.en}</p>
        </Card>
        <Card className="p-5">
          <div className="text-xs font-semibold tracking-wide text-neutral-500 uppercase">{t("devdocs.requires")}</div>
          <p className="mt-1.5 text-sm leading-6 text-neutral-700">{zh ? example.requires.zh : example.requires.en}</p>
        </Card>
      </div>

      <section className="mt-6">
        <h2 className="mb-2 text-[15px] leading-6 font-semibold text-neutral-900">{t("devdocs.installTitle")}</h2>
        <CopyBox text={exampleInstallCommand(example, profile)} />
        <p className="mt-2 text-xs leading-5 text-neutral-500">
          {t("devdocs.installProfile", { profile })} {t("devdocs.installNote")}
        </p>
      </section>

      <section className="mt-6" data-dev-walkthrough>
        <h2 className="mb-2 text-[15px] leading-6 font-semibold text-neutral-900">{t("devdocs.walkthroughTitle")}</h2>
        <p className="text-sm leading-6 text-neutral-600">{t("devdocs.walkthroughDesc")}</p>
        <a
          href={exampleWalkthroughUrl(example)}
          target="_blank"
          rel="noreferrer"
          className="mt-2 inline-flex items-center gap-1 text-sm font-medium text-brand-600 hover:underline dark:text-brand-300"
        >
          {t("devdocs.fullWalkthrough")}
          <ExternalLink size={13} />
        </a>
        <p className="mt-1.5 text-xs leading-5 text-neutral-500">{t("devdocs.walkthroughPending")}</p>
      </section>

      <section className="mt-6">
        <h2 className="mb-2 text-[15px] leading-6 font-semibold text-neutral-900">{t("devdocs.surfaces")}</h2>
        <div className="flex flex-wrap items-center gap-1.5">
          {example.surfaces.map((s) => (
            <Badge key={s} tone="slate">
              {t(`devdocs.surfaceNames.${s}`)}
            </Badge>
          ))}
        </div>
      </section>

      {example.versionNote && (
        <Notice className="mt-6" tone="warn">
          {zh ? example.versionNote.zh : example.versionNote.en}
        </Notice>
      )}

      <Card className="mt-6 p-5">
        <h2 className="text-[15px] leading-6 font-semibold text-neutral-900">{t("devdocs.sourceAndLicense")}</h2>
        <dl className="mt-3 grid grid-cols-1 gap-x-8 gap-y-2 text-sm sm:grid-cols-[7rem_minmax(0,1fr)]">
          <dt className="text-neutral-500">{t("devdocs.source")}</dt>
          <dd>
            <a
              href={exampleSourceUrl(example)}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1 break-all text-brand-600 hover:underline dark:text-brand-300"
            >
              {example.repo.replace(/^https:\/\/github\.com\//, "")}@{example.tag}
              <ExternalLink size={13} className="shrink-0" />
            </a>
          </dd>
          <dt className="text-neutral-500">{t("devdocs.version")}</dt>
          <dd className="tabular-nums">{example.version}</dd>
          <dt className="text-neutral-500">{t("devdocs.license")}</dt>
          <dd>
            <a href={exampleLicenseUrl(example)} target="_blank" rel="noreferrer" className="text-brand-600 hover:underline dark:text-brand-300">
              {example.license.spdx}
            </a>
          </dd>
          <dt className="text-neutral-500">{t("devdocs.attribution")}</dt>
          <dd className="text-neutral-700">{example.attribution}</dd>
        </dl>
        <p className="mt-4 border-t border-neutral-100 pt-3 text-xs leading-5 text-neutral-500 dark:border-white/10">{t("devdocs.sourceDisclaimer")}</p>
      </Card>
    </div>
  );
}
