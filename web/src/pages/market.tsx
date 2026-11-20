import { useMemo, useState } from "react";
import { useNavigate, useSearchParams } from "react-router";
import { ChevronDown, Download, Search, Terminal, X } from "lucide-react";
import { categoryIds, categoryLabel, kindOf } from "../lib/catalog";
import type { PluginKind } from "../lib/types";
import { useScoring, useStore } from "../lib/store";
import { useAuth } from "../lib/auth";
import { api, recommendedDshRelease, type AssetKind, type HubAsset } from "../lib/api";
import { useAction, useApi } from "../lib/use-api";
import { ASSET_KINDS, KIND_ICONS } from "../lib/hub";
import { PluginCard } from "../components/plugin-card";
import { AssetCard } from "../components/asset-card";
import { LoadState } from "../components/api-ui";
import { Button, Card, cx, inputBase, PageHeader, Segmented, Empty, Spinner, buttonCls, tabListCls, tabCls } from "../components/ui";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/shadcn/dropdown-menu";
import { PageLink, useMenus } from "../lib/menus";
import { Trans, useTranslation } from "react-i18next";

const kindOrder: PluginKind[] = ["skill", "connector", "knowledge", "plugin"];
type Tab = "all" | "plugin" | AssetKind;
const TABS: Tab[] = ["all", "plugin", "skill", "prompt", "connection", "app"];

type Sort = "installs" | "score" | "rating" | "recent" | "stars";
type Origin = "all" | "internal" | "upstream";
type AssetFilter = "all" | "enabled" | "mine";

const BANNER_KEY = "dsh-ee:hub-connect-banner";

/** 插件列表：沿用原能力市场的筛选与排序。 */
export function PluginsTab({ limit }: { limit?: number }) {
  const { t } = useTranslation();
  const { plugins, pluginsLoading } = useStore();
  const scoring = useScoring();
  const [q, setQ] = useState("");
  const [cat, setCat] = useState("");
  const [origin, setOrigin] = useState<Origin>("all");
  const [kind, setKind] = useState<PluginKind | "all">("all");
  const [sortPref, setSort] = useState<Sort>("installs");

  // 评分开关被关掉时，之前选中的评分排序要自动退回默认，否则列表会按一个看不见的字段排
  const sort: Sort =
    (sortPref === "score" && !scoring.upstream) || (sortPref === "rating" && !scoring.rating) ? "installs" : sortPref;

  const list = useMemo(() => {
    const kw = q.trim().toLowerCase();
    const xs = plugins.filter(
      (p) =>
        (origin === "all" || p.origin === origin) &&
        (kind === "all" || kindOf(p) === kind) &&
        (!cat || p.category === cat) &&
        (!kw || `${p.name} ${p.title} ${p.description} ${p.author}`.toLowerCase().includes(kw)),
    );
    const key: Record<Sort, (p: (typeof xs)[number]) => number> = {
      installs: (p) => p.installs,
      score: (p) => p.score ?? -1,
      rating: (p) => p.rating ?? -1,
      recent: (p) => p.publishedAt,
      stars: (p) => p.stars,
    };
    return [...xs].sort((a, b) => key[sort](b) - key[sort](a));
  }, [plugins, q, cat, origin, kind, sort]);

  const sortOptions: { value: Sort; label: string }[] = [
    { value: "installs", label: t("market.sortInstalls") },
    ...(scoring.upstream ? [{ value: "score" as const, label: t("market.sortScore") }] : []),
    ...(scoring.rating ? [{ value: "rating" as const, label: t("market.sortRating") }] : []),
    { value: "recent", label: t("market.sortRecent") },
    { value: "stars", label: "GitHub Star" },
  ];

  if (limit) {
    const top = [...plugins].sort((a, b) => b.installs - a.installs).slice(0, limit);
    if (top.length === 0) return null;
    return (
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
        {top.map((p) => (
          <PluginCard key={p.name} p={p} />
        ))}
      </div>
    );
  }

  return (
    <>
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <div className="relative w-full sm:w-72">
          <Search size={16} className="absolute top-1/2 left-3 -translate-y-1/2 text-neutral-500" />
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder={t("market.searchPlaceholder")}
            aria-label={t("market.searchLabel")}
            className={cx(inputBase, "w-full pl-9")}
          />
        </div>
        <Segmented
          value={kind}
          onChange={setKind}
          options={[
            { value: "all", label: t("market.all") },
            ...kindOrder.map((k) => ({ value: k, label: t(`kinds.${k}`) })),
          ]}
        />
        <div className="flex gap-2 sm:ml-auto">
          <select aria-label={t("market.source")} value={origin} onChange={(e) => setOrigin(e.target.value as Origin)} className={cx(inputBase, "w-32")}>
            <option value="all">{t("market.allSources")}</option>
            <option value="internal">{t("market.internal")}</option>
            <option value="upstream">{t("market.community")}</option>
          </select>
          <select aria-label={t("market.category")} value={cat} onChange={(e) => setCat(e.target.value)} className={cx(inputBase, "w-32")}>
            <option value="">{t("market.allCategories")}</option>
            {categoryIds.map((id) => (
              <option key={id} value={id}>
                {categoryLabel(id)}
              </option>
            ))}
          </select>
          <select aria-label={t("market.sort")} value={sort} onChange={(e) => setSort(e.target.value as Sort)} className={cx(inputBase, "w-36")}>
            {sortOptions.map((o) => (
              <option key={o.value} value={o.value}>
                {t("market.sortBy", { label: o.label })}
              </option>
            ))}
          </select>
        </div>
      </div>

      <div className="mb-3 text-sm text-neutral-600">{t("market.count", { count: list.length })}</div>
      {pluginsLoading && plugins.length === 0 ? (
        <Card className="flex justify-center"><div role="status" className="flex items-center gap-2 px-5 py-12 text-sm text-neutral-500"><Spinner className="text-neutral-400" />{t("common.loading")}</div></Card>
      ) : list.length === 0 ? (
        <Card className="p-12 text-center text-sm text-neutral-600">
          <Trans i18nKey="market.empty" components={{ link: <PageLink page="publish" className="font-medium text-brand-600 hover:text-brand-700 dark:text-brand-300 dark:hover:text-brand-200" /> }} />
        </Card>
      ) : (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
          {list.map((p) => (
            <PluginCard key={p.name} p={p} />
          ))}
        </div>
      )}
    </>
  );
}

/** 技能、提示词、连接、微应用（kind 为空时是全部）。 */
export function AssetsGrid({
  assets,
  kind,
  onChanged,
}: {
  assets: HubAsset[];
  kind?: AssetKind;
  onChanged: () => void;
}) {
  const { t } = useTranslation();
  const { user } = useAuth();
  const { notify } = useStore();
  const { busy, run } = useAction();
  const [q, setQ] = useState("");
  const [filter, setFilter] = useState<AssetFilter>("all");
  const list = useMemo(() => {
    const kw = q.trim().toLowerCase();
    return assets
      .filter(
        (a) =>
          (!kind || a.kind === kind) &&
          (filter === "all" || (filter === "enabled" ? a.enabled : a.owner_id === user?.id)) &&
          (!kw || `${a.slug} ${a.spec.title} ${a.spec.description} ${a.owner} ${a.owner_name} ${a.department}`.toLowerCase().includes(kw)),
      )
      .sort((a, b) => Number(b.enabled) - Number(a.enabled) || b.uses - a.uses || a.slug.localeCompare(b.slug));
  }, [assets, kind, filter, q, user?.id]);

  const toggle = async (a: HubAsset) => {
    const r = await run(() => (a.subscribed ? api.unsubscribe(a.id) : api.subscribe(a.id)));
    notify(r.ok ? t(a.subscribed ? "hub.disabledMsg" : "hub.enabledMsg", { title: a.spec.title }) : r.error);
    if (r.ok) onChanged();
  };

  return (
    <>
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <div className="relative w-full sm:w-72">
          <Search size={16} className="absolute top-1/2 left-3 -translate-y-1/2 text-neutral-500" />
          <input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("hub.search")} aria-label={t("hub.search")} className={cx(inputBase, "w-full pl-9")} />
        </div>
        <Segmented
          value={filter}
          onChange={setFilter}
          options={[
            { value: "all", label: t("hub.filterAll") },
            { value: "enabled", label: t("hub.filterEnabled") },
            { value: "mine", label: t("hub.filterMine") },
          ]}
        />
        <span className="text-sm text-neutral-600 sm:ml-auto">{t("hub.count", { count: list.length })}</span>
      </div>
      {list.length === 0 ? (
        <Card><Empty>{assets.length === 0 ? t("hub.empty") : t("hub.emptyFiltered")}</Empty></Card>
      ) : (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
          {list.map((a) => (
            <AssetCard key={a.id} a={a} busy={busy} onToggle={toggle} />
          ))}
        </div>
      )}
    </>
  );
}

export default function Hub() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { plugins, settings } = useStore();
  const { can } = useAuth();
  const { pathOf } = useMenus();
  const [params, setParams] = useSearchParams();
  const tab = (TABS.includes(params.get("tab") as Tab) ? params.get("tab") : "all") as Tab;
  const hub = useApi(api.hub);
  const releaseData = useApi(api.dshReleases);
  const assets = hub.data?.assets ?? [];
  const [banner, setBanner] = useState(() => localStorage.getItem(BANNER_KEY) !== "off");
  const recommendedCore = recommendedDshRelease(releaseData.data?.releases);
  const count = (k: Tab) => (k === "all" ? assets.length + plugins.length : k === "plugin" ? plugins.length : assets.filter((a) => a.kind === k).length);
  const setTab = (k: Tab) => setParams(k === "all" ? {} : { tab: k }, { replace: true });

  return (
    <div>
      <PageHeader
        title={t("hub.title")}
        desc={t("hub.desc")}
        actions={
          <>
            <PageLink page="setup" className={buttonCls("secondary")}>
              <Terminal size={15} /> {t("market.setupGuide")}
            </PageLink>
            {settings.distribution.enabled && recommendedCore && (
              <PageLink page="dsh-download" className={buttonCls("secondary")}>
                <Download size={15} /> {t("market.downloadDsh", { version: recommendedCore.version })}
              </PageLink>
            )}
            {(can("registry.publish") || can("connections.manage")) && (
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button>
                    {t("hub.share")} <ChevronDown size={14} />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  {can("registry.publish") && (
                    <>
                      {(["skill", "prompt", "app"] as const).map((k) => {
                        const Icon = KIND_ICONS[k];
                        return (
                          <DropdownMenuItem key={k} onSelect={() => navigate(`/assets/new?kind=${k}`)}>
                            <Icon /> {t(k === "skill" ? "hub.shareSkill" : k === "prompt" ? "hub.sharePrompt" : "hub.shareApp")}
                          </DropdownMenuItem>
                        );
                      })}
                      <DropdownMenuItem onSelect={() => navigate(pathOf("publish"))}>
                        <KIND_ICONS.plugin /> {t("hub.sharePlugin")}
                      </DropdownMenuItem>
                    </>
                  )}
                  {can("connections.manage") && (
                    <>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem onSelect={() => navigate("/assets/new?kind=connection")}>
                        <KIND_ICONS.connection /> {t("hub.addConnection")}
                      </DropdownMenuItem>
                    </>
                  )}
                </DropdownMenuContent>
              </DropdownMenu>
            )}
          </>
        }
      />

      {banner && (
        <Card className="mb-4 flex flex-wrap items-center gap-3 border-brand-200/80 bg-brand-50/70 px-4 py-3 text-sm shadow-none dark:border-brand-400/25 dark:bg-brand-500/10">
          <span className="font-medium text-neutral-900">{t("hub.connectBanner")}</span>
          <span className="text-neutral-700">{t("hub.connectBannerHint")}</span>
          <PageLink page="setup" className="font-medium text-brand-700 hover:underline dark:text-brand-300">
            {t("hub.connectBannerLink")}
          </PageLink>
          <button
            type="button"
            className="ml-auto text-neutral-500 hover:text-neutral-900"
            aria-label={t("common.close")}
            onClick={() => {
              localStorage.setItem(BANNER_KEY, "off");
              setBanner(false);
            }}
          >
            <X size={16} />
          </button>
        </Card>
      )}

      <div role="tablist" className={cx(tabListCls, "mb-3")}>
        {TABS.map((k) => {
          const Icon = k === "all" ? null : KIND_ICONS[k];
          return (
            <button
              key={k}
              role="tab"
              type="button"
              aria-selected={tab === k}
              onClick={() => setTab(k)}
              className={tabCls(tab === k)}
            >
              {Icon && <Icon size={15} strokeWidth={1.6} />}
              {t(`hub.tabs.${k}`)}
              <span className="text-xs text-neutral-500 tabular-nums">{count(k)}</span>
            </button>
          );
        })}
      </div>
      <p className="mb-4 min-h-5 text-sm text-neutral-600">{t(`hub.tabHints.${tab}`)}</p>

      {tab === "plugin" ? (
        <PluginsTab />
      ) : (
        <LoadState loading={hub.loading && !hub.data} error={hub.data ? null : hub.error} onRetry={hub.reload}>
          <AssetsGrid assets={assets.filter((a) => ASSET_KINDS.includes(a.kind))} kind={tab === "all" ? undefined : tab} onChanged={hub.reload} />
          {tab === "all" && plugins.length > 0 && (
            <div className="mt-8">
              <div className="mb-3 flex items-baseline justify-between">
                <h2 className="text-neutral-900 text-[15px] leading-6 font-semibold">{t("hub.tabs.plugin")}</h2>
                <button type="button" onClick={() => setTab("plugin")} className="text-sm font-medium text-brand-600 hover:text-brand-700 dark:text-brand-300 dark:hover:text-brand-200">
                  {t("hub.viewAllPlugins", { count: plugins.length })}
                </button>
              </div>
              <PluginsTab limit={6} />
            </div>
          )}
        </LoadState>
      )}
    </div>
  );
}
