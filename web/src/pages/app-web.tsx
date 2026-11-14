import { useEffect, useState } from "react";
import { Link, useLocation, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import { ArrowLeft } from "lucide-react";
import { api, type WebApp } from "../lib/api";
import { useApi } from "../lib/use-api";
import { useAuth } from "../lib/auth";
import { LoadState } from "../components/api-ui";
import { AppDataPanel } from "../components/app-data";
import { Card, Notice, Empty, tabCls } from "../components/ui";
import { WhaleMark } from "../components/whale-mark";
import { DemoBar } from "../components/demo-site";
import { LivePane } from "../components/live-pane";
import { EmbedFailure, EmbedMobileNotice } from "../components/embed";
import { embedErrorForStatus, type EmbedOptions } from "../lib/embed";
import { useEmbedError, useEmbedMobile, useEmbedOptions } from "../lib/use-embed";
import type { WorkbenchPane } from "../lib/workbench-render";

/**
 * 网页版应用（M14）：和 DSH 右侧栏用同一份工作台配置、同一套渲染，没有 AI。
 * 表单提交到应用自带的表时直接保存；发给 AI 的按钮把那段话复制下来，到 DSH 里继续。
 * 版面是响应式的仪表盘：手机上一列，桌面上按组件的宽度并排（最宽 6xl）。
 *
 * ?embed=1 供宿主 iframe 嵌入：隐藏返回/管理/演示条/导出等会跳出宿主上下文的入口。
 * postMessage 握手（ready/connect/session/auth-required/error/theme/resize）在 EmbedProvider 里，
 * 协议见 docs/embedding.md；CSP 与来源校验见 server/internal/api/security_headers.go。
 *
 * fit=auto：页面改为内容驱动（去掉 min-h-screen，头部不再 sticky），useEmbedResize 上报内容高度，
 * 宿主按高度撑开 iframe；fill（默认）保持原来的整页布局。
 */
export default function AppWebPage() {
  const { slug = "" } = useParams();
  const options = useEmbedOptions();
  const mobile = useEmbedMobile(options);
  const app = useApi(() => api.webApp(slug));
  // 加载失败 / 应用不存在 / 不在可用范围：把可判断的失败码发给宿主（未登录由 RequireAuth 处理）。
  const loadError = app.error ? embedErrorForStatus(app.errorStatus, app.error) : null;
  useEmbedError(options, loadError);
  if (options.embed && loadError) {
    return <EmbedFailure error={loadError} onRetry={app.reload} />;
  }
  const autoFit = options.embed && options.fit === "auto";
  return (
    <div className={autoFit ? "bg-neutral-50" : "min-h-screen bg-neutral-50"}>
      <LoadState loading={app.loading && !app.data} error={app.error} onRetry={app.reload}>
        {app.data && <AppWeb app={app.data} options={options} mobile={mobile} autoFit={autoFit} />}
      </LoadState>
    </div>
  );
}

function AppWeb({ app, options, mobile, autoFit }: { app: WebApp; options: EmbedOptions; mobile: boolean; autoFit: boolean }) {
  const { t } = useTranslation();
  const { user } = useAuth();
  const { search } = useLocation();
  const embed = options.embed;
  const demo = useApi(api.authMethods).data?.demo ?? null;
  const panes = (app.workbench?.panes ?? []).filter((p) => p.type === "panel");
  const tabs = [...panes.map((p) => ({ id: p.id, title: p.title })), ...(app.tables ? [{ id: "@data", title: t("appWeb.dataTab") }] : [])];
  const tabParam = new URLSearchParams(search).get("tab");
  const initialTab = tabParam && tabs.some((x) => x.id === tabParam) ? tabParam : (tabs[0]?.id ?? "@data");
  const [tab, setTab] = useState(initialTab);
  const pane = panes.find((p) => p.id === tab);

  // 深链（?tab= / v2 navigate / deeplink）：URL 变化时切到对应页签，不整页刷新。
  useEffect(() => {
    if (tabParam && tabs.some((x) => x.id === tabParam)) setTab(tabParam);
    // tabs 由 app 决定，app 变化时重新计算；这里只依赖 tabParam 与可用页签集合。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tabParam, tabs.map((x) => x.id).join(",")]);

  useEffect(() => {
    document.title = `${app.title} · dsh-ee`;
  }, [app.title]);

  const first = tab === tabs[0]?.id;
  return (
    <>
      {mobile && <EmbedMobileNotice />}
      <header className={autoFit ? "border-b border-neutral-200/70 bg-card/90 backdrop-blur-md" : "sticky top-0 z-10 border-b border-neutral-200/70 bg-card/90 backdrop-blur-md"}>
        <div className="mx-auto flex max-w-6xl items-center gap-3 px-4 pt-3 pb-2 sm:px-6">
          {!embed && (
            <Link to="/apps" aria-label={t("appWeb.back")} className="rounded-md p-1 text-neutral-500 transition-colors hover:bg-neutral-100 hover:text-neutral-900">
              <ArrowLeft size={18} />
            </Link>
          )}
          <span className="flex size-9 shrink-0 items-center justify-center rounded-xl bg-gradient-to-br from-brand-50 to-brand-100 text-xl shadow-[inset_0_0_0_1px_rgba(77,107,254,0.15)] dark:from-brand-500/15 dark:to-brand-500/5">
            {app.icon || <WhaleMark size={20} className="text-brand-500" />}
          </span>
          <div className="min-w-0 flex-1">
            <h1 className="truncate text-[15px] font-semibold tracking-tight text-neutral-900">{app.title}</h1>
            <p className="truncate text-xs text-neutral-500">{user?.display_name || user?.username}</p>
          </div>
          {!embed && app.admin && <Link to={`/assets/${app.asset_id}`} className="rounded-md px-2.5 py-1 text-xs font-medium text-brand-600 transition-colors hover:bg-brand-50 dark:text-brand-300 dark:hover:bg-brand-500/15">{t("appWeb.manage")}</Link>}
        </div>
        {tabs.length > 1 && (
          <nav className="mx-auto flex max-w-6xl gap-5 overflow-x-auto px-4 sm:px-6">
            {tabs.map((x) => (
              <button
                key={x.id}
                type="button"
                onClick={() => setTab(x.id)}
                className={tabCls(tab === x.id)}
              >
                {x.title}
              </button>
            ))}
          </nav>
        )}
      </header>
      <main className="mx-auto max-w-6xl px-2 py-4 sm:px-4 sm:py-6">
        {!embed && demo && <DemoBar demo={demo} className="mx-2 mb-3 sm:mx-3.5" />}
        {!app.web && <Notice tone="warn" className="mx-2 mb-3 sm:mx-3.5">{t("appWeb.previewOnly")}</Notice>}
        {first && (app.description || (app.highlights?.length ?? 0) > 0) && (
          <section className="mx-2 mb-1 sm:mx-3.5">
            {app.description && <p className="max-w-3xl text-sm leading-relaxed text-neutral-600">{app.description}</p>}
            {(app.highlights?.length ?? 0) > 0 && (
              <ul className="mt-3 flex flex-wrap gap-2 text-xs text-neutral-700">
                {app.highlights!.map((h) => (
                  <li key={h} className="flex items-center gap-1.5 rounded-full border border-neutral-200 bg-card px-3 py-1 shadow-[0_1px_2px_rgba(10,37,64,0.04)]">
                    <span className="text-brand-500">✓</span>
                    {h}
                  </li>
                ))}
              </ul>
            )}
          </section>
        )}
        {/* 网页的类型里 source.connection 是可选的（表数据源没有），接口返回的就是插件渲染认的那份配置 */}
        {pane ? <WebPane key={pane.id} app={app} pane={pane as unknown as WorkbenchPane} embed={embed} /> : tab === "@data" ? <div className="px-2 sm:px-3.5"><AppDataPanel slug={app.slug} embed={embed} /></div> : <Card className="mx-2"><Empty>{t("appWeb.noPanel")}</Empty></Card>}
      </main>
    </>
  );
}

/** 一个业务面板：取数走 /api/me/apps/<标识>/source（服务端按配置执行，网页自己不能指定调哪个工具）。 */
function WebPane({ app, pane, embed }: { app: WebApp; pane: WorkbenchPane; embed: boolean }) {
  return (
    <LivePane
      slug={app.slug}
      pane={pane}
      mode="web"
      embed={embed}
      load={(paneId, index) => api.webAppSource(app.slug, paneId, index)}
      requestHref={(connection) => `/connections/${encodeURIComponent(connection)}?app=${encodeURIComponent(app.slug)}`}
    />
  );
}
