import { useEffect, useState } from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { FileUp, FolderOpen, RefreshCw, ShieldCheck } from "lucide-react";
import {
  api,
  ApiError,
  formatTime,
  type DirectoryEntry,
  type DirectoryInstallPlan,
  type DirectoryInstallReq,
  type DirectoryListResp,
  type DirectoryPlan,
  type DirectoryPluginPlan,
  type ExtAdminParam,
  type ExtPlan,
} from "../../lib/api";
import { useApi, useAction } from "../../lib/use-api";
import { useAuth } from "../../lib/auth";
import { useStore } from "../../lib/store";
import { ErrorNote, LoadState } from "../../components/api-ui";
import { Badge, Button, Card, CardHeader, Empty, Field, Notice, PageHeader, PasswordInput, cx, inputCls } from "../../components/ui";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/shadcn/dialog";

const textareaCls =
  "w-full rounded-md border border-neutral-300 bg-card px-3 py-2 font-mono text-xs outline-none placeholder:text-neutral-500 focus:border-brand-500 focus:ring-2 focus:ring-brand-100 dark:border-white/15 dark:bg-transparent";

/** 目录条目安装 / 升级需要的权限点：扩展管连接，插件管插件（服务端也按类型再校验一次）。 */
function kindPerm(kind: DirectoryEntry["kind"]) {
  return kind === "dsh-plugin" ? "plugins.manage" : "connections.manage";
}

/** 失败响应里的安装计划（409 / 400 的 body 是 {error, plan}）。 */
function installPlanOf(e: unknown): DirectoryInstallPlan | null {
  if (!(e instanceof ApiError)) return null;
  const body = e.data as { plan?: DirectoryInstallPlan } | null;
  return body?.plan ?? null;
}

/** 登记失败响应里的目录计划（apply 的 409 带 plan）。 */
function directoryPlanOf(e: unknown): DirectoryPlan | null {
  if (!(e instanceof ApiError)) return null;
  const body = e.data as { plan?: DirectoryPlan } | null;
  return body?.plan ?? null;
}

/** 管理员设置值在输入框里怎么显示：字符串去掉引号，其余按 JSON。 */
function shown(v: unknown) {
  if (v === undefined || v === null) return "";
  return typeof v === "string" ? v : JSON.stringify(v);
}

/** 输入框里的文字转回清单要求的类型。 */
function parsedAdmin(p: ExtAdminParam, text: string): unknown {
  if (p.type === "number" || p.type === "integer") return text.trim() === "" ? undefined : Number(text);
  if (p.type === "boolean") return text === "true";
  if (p.type === "array" || p.type === "object") {
    if (text.trim() === "") return undefined;
    try {
      return JSON.parse(text);
    } catch {
      return text;
    }
  }
  return text;
}

/**
 * 企业插件目录（docs/plugin-signing.md §8）：管理员维护登记过的目录条目，
 * 对单条明确安装 / 升级。页面不显示私钥，也不自动安装 / 自动升级：
 * 点安装先取「只出计划」的预览（plan_only），确认后才真正执行。
 */
export default function AdminDirectory() {
  const { t } = useTranslation();
  const { can } = useAuth();
  const list = useApi(api.directory);
  const canImport = can("connections.manage", "settings.manage");
  const [action, setAction] = useState<{ entry: DirectoryEntry; kind: "install" | "upgrade" } | null>(null);

  return (
    <div data-testid="directory-page">
      <PageHeader
        title={t("directory.title")}
        desc={t("directory.desc")}
        actions={
          <Button variant="secondary" size="sm" disabled={list.loading} onClick={list.reload}>
            <RefreshCw size={14} /> {t("directory.reload")}
          </Button>
        }
      />
      <div className="space-y-6">
        <div className="space-y-2">
          <Notice tone="info">{t("directory.installHint")}</Notice>
          {list.data && (list.data.tarball_hosts ?? []).length > 0 && (
            <p className="text-xs leading-5 text-neutral-500">{t("directory.tarballHosts", { hosts: (list.data.tarball_hosts ?? []).join("、") })}</p>
          )}
          {!canImport && <Notice tone="warn">{t("directory.readOnly")}</Notice>}
        </div>
        {canImport && <ImportCard onApplied={list.reload} />}
        <Card data-testid="directory-list">
          <CardHeader
            title={t("directory.listTitle")}
            aside={<ListCounts data={list.data} />}
          />
          <LoadState loading={list.loading && !list.data} error={list.data ? null : list.error} onRetry={list.reload}>
            <div className="px-5 pb-5">
              <p className="mb-4 text-sm text-neutral-600">{t("directory.listDesc")}</p>
              {list.data && (list.data.entries ?? []).length === 0 ? (
                <Empty icon={FolderOpen} title={t("directory.empty")}>
                  {t("directory.emptyHint")}
                </Empty>
              ) : (
                list.data && (
                  <EntriesTable
                    data={{ ...list.data, entries: list.data.entries ?? [] }}
                    canExtensions={can("connections.manage")}
                    canPlugins={can("plugins.manage")}
                    onAction={(entry, kind) => setAction({ entry, kind })}
                  />
                )
              )}
            </div>
          </LoadState>
        </Card>
      </div>
      {action && (
        <DirectoryActionDialog
          entry={action.entry}
          actionKind={action.kind}
          onClose={() => setAction(null)}
          onFinished={list.reload}
        />
      )}
    </div>
  );
}

function ListCounts({ data }: { data: DirectoryListResp | null }) {
  const { t } = useTranslation();
  if (!data || data.count === 0) return null;
  const extensions = (data.entries ?? []).filter((e) => e.kind === "extension").length;
  const plugins = (data.count ?? 0) - extensions;
  return (
    <span className="flex flex-wrap items-center gap-2">
      {extensions > 0 && <Badge tone="brand">{t("directory.countExtensions", { count: extensions })}</Badge>}
      {plugins > 0 && <Badge tone="slate">{t("directory.countPlugins", { count: plugins })}</Badge>}
    </span>
  );
}

// ---- 导入 / 校验 / 登记 ----

/** 导入目录 JSON：校验 → 展示计划 / issues / 差异 → 有敏感变化时确认后登记。 */
function ImportCard({ onApplied }: { onApplied: () => void }) {
  const { t } = useTranslation();
  const { notify } = useStore();
  const { busy, run } = useAction();
  const [document, setDocument] = useState("");
  const [plan, setPlan] = useState<DirectoryPlan | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [sensitive, setSensitive] = useState<string[]>([]);
  const [applied, setApplied] = useState<string | null>(null);

  const reset = () => {
    setPlan(null);
    setError(null);
    setSensitive([]);
    setApplied(null);
  };

  const pickFile = async (files: FileList | null) => {
    const f = files?.[0];
    if (!f) return;
    reset();
    try {
      setDocument(await f.text());
    } catch {
      setError(t("directory.fileReadFailed"));
    }
  };

  const check = async () => {
    reset();
    const r = await run(() => api.checkDirectory(document));
    if (!r.ok) return setError(r.error);
    setPlan(r.value);
  };

  const apply = async () => {
    setError(null);
    setApplied(null);
    const confirm = sensitive.length > 0;
    const r = await run(() => api.applyDirectory(document, confirm));
    if (!r.ok) {
      const p = directoryPlanOf(r.error);
      if (p && p.sensitive?.length) {
        setPlan(p);
        setSensitive(p.sensitive);
        setError(r.error);
        return;
      }
      setError(r.error);
      return;
    }
    setSensitive([]);
    setPlan(null);
    setApplied(
      t("directory.applied", {
        created: r.value.result.created,
        updated: r.value.result.updated,
        removed: r.value.result.removed,
        unchanged: r.value.result.unchanged,
      }),
    );
    notify(t("directory.appliedToast"));
    onApplied();
  };

  return (
    <Card data-testid="directory-import">
      <CardHeader title={t("directory.importTitle")} aside={t("directory.importPrivacy")} />
      <div className="space-y-4 px-5 pb-5">
        <p className="text-sm text-neutral-600">{t("directory.importDesc")}</p>
        <div className="flex flex-wrap items-center gap-2">
          <label className={cx("inline-flex cursor-pointer items-center gap-1.5 rounded-md border border-neutral-300 bg-card px-3 py-1.5 text-sm font-medium text-neutral-800 hover:bg-neutral-50 dark:border-white/15 dark:text-neutral-200 dark:hover:bg-white/5")}>
            <FileUp size={14} /> {t("directory.importFile")}
            <input
              type="file"
              accept="application/json,.json"
              className="hidden"
              data-testid="directory-import-file"
              onChange={(e) => pickFile(e.target.files)}
            />
          </label>
          <span className="text-xs text-neutral-500">{t("directory.importFileHint")}</span>
        </div>
        <Field label={t("directory.importDocument")} hint={t("directory.importDocumentHint")}>
          <textarea
            className={cx(textareaCls, "h-40")}
            data-testid="directory-import-document"
            value={document}
            placeholder={t("directory.importPlaceholder")}
            onChange={(e) => {
              setDocument(e.target.value);
              setPlan(null);
              setSensitive([]);
              setApplied(null);
            }}
          />
        </Field>
        <ErrorNote>{error}</ErrorNote>
        {applied && <Notice tone="success">{applied}</Notice>}
        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" variant="secondary" disabled={busy || !document.trim()} onClick={check} data-testid="directory-check">
            {t("directory.check")}
          </Button>
          <Button
            size="sm"
            disabled={busy || !plan || !plan.ok}
            onClick={apply}
            data-testid="directory-apply"
            title={plan && !plan.ok ? t("directory.applyNeedsValidPlan") : undefined}
          >
            {sensitive.length > 0 ? t("directory.applyConfirm") : t("directory.apply")}
          </Button>
          {plan && !plan.ok && <span className="text-xs text-red-700 dark:text-red-300">{t("directory.applyBlocked")}</span>}
        </div>
        {plan && <PlanView plan={plan} sensitive={sensitive} />}
      </div>
    </Card>
  );
}

function SignatureBadge({ sig }: { sig: DirectoryPlan["document"]["signature"] }) {
  const { t } = useTranslation();
  if (!sig) return <Badge>{t("directory.sigMissing")}</Badge>;
  switch (sig.status) {
    case "valid":
      return <Badge tone="green">{t("directory.sigValid")}</Badge>;
    case "revoked":
      return <Badge tone="red">{t("directory.sigRevoked")}</Badge>;
    case "invalid":
      return <Badge tone="red">{t("directory.sigInvalid")}</Badge>;
    default:
      return <Badge tone="amber">{t("directory.sigUnsigned")}</Badge>;
  }
}

function severityTone(is: { severity: string }) {
  return is.severity === "error" ? "red" : "amber";
}

function PlanView({ plan, sensitive }: { plan: DirectoryPlan; sensitive: string[] }) {
  const { t } = useTranslation();
  const issues = plan.issues;
  return (
    <div className="space-y-4 rounded-md border border-neutral-200/80 p-4 dark:border-white/10" data-testid="directory-plan">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <Badge tone={plan.ok ? "green" : "red"}>{plan.ok ? t("directory.planOK") : t("directory.planFailed")}</Badge>
        <span className="text-neutral-700" data-testid="directory-plan-id">
          {plan.document.directory_id}
        </span>
        <span className="text-neutral-500">{t("directory.planEntries", { count: plan.document.entries_count })}</span>
        <SignatureBadge sig={plan.document.signature} />
        {plan.document.signature?.key_id && <span className="font-mono text-xs text-neutral-500">{plan.document.signature.key_id}</span>}
        {plan.errors > 0 && <Badge tone="red">{t("directory.planErrors", { count: plan.errors })}</Badge>}
        {plan.warnings > 0 && <Badge tone="amber">{t("directory.planWarnings", { count: plan.warnings })}</Badge>}
      </div>
      {plan.document.signature?.message && <p className="text-xs leading-5 text-neutral-500">{plan.document.signature.message}</p>}
      {plan.existing && (
        <p className="text-xs leading-5 text-neutral-500">
          {t("directory.planExisting", {
            count: plan.existing.entries_count,
            time: plan.existing.applied_at ? formatTime(plan.existing.applied_at) : "—",
          })}
        </p>
      )}
      {issues.length > 0 && (
        <div className="space-y-1" data-testid="directory-plan-issues">
          {issues.slice(0, 30).map((is, i) => (
            <div key={i} className="flex items-start gap-2 text-xs leading-5">
              <Badge tone={severityTone(is)}>{is.severity === "error" ? t("directory.error") : t("directory.warning")}</Badge>
              <span className="min-w-0 break-words text-neutral-700">
                <span className="font-mono text-neutral-500">{is.code}</span>
                {is.index >= 0 ? ` · #${is.index}` : ""} {is.message}
              </span>
            </div>
          ))}
        </div>
      )}
      {plan.diff && <DiffLine diff={plan.diff} />}
      {sensitive.length > 0 && (
        <Notice tone="warn" data-testid="directory-plan-sensitive">
          <div className="mb-1 font-medium">{t("directory.planSensitive", { count: sensitive.length })}</div>
          <ul className="list-disc space-y-1 pl-4 text-xs">
            {sensitive.map((s) => (
              <li key={s} className="break-words">
                {s}
              </li>
            ))}
          </ul>
          <div className="mt-1 text-xs">{t("directory.planSensitiveHint")}</div>
        </Notice>
      )}
    </div>
  );
}

function DiffLine({ diff }: { diff: NonNullable<DirectoryPlan["diff"]> }) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-wrap items-center gap-2 text-xs text-neutral-600" data-testid="directory-plan-diff">
      <Badge tone="green">{t("directory.diffCreated", { count: diff.created })}</Badge>
      <Badge tone="amber">{t("directory.diffUpdated", { count: diff.updated })}</Badge>
      <Badge>{t("directory.diffUnchanged", { count: diff.unchanged })}</Badge>
      {diff.removed > 0 && <Badge tone="red">{t("directory.diffRemoved", { count: diff.removed })}</Badge>}
    </div>
  );
}

// ---- 条目列表 ----

function entrySource(entry: DirectoryEntry) {
  if (entry.source_type === "npm") return entry.source_registry ? `npm · ${entry.source_registry}` : "npm";
  return entry.source_url ?? "tarball";
}

function publisherOf(entry: DirectoryEntry) {
  if (!entry.publisher_username && !entry.publisher_department) return "—";
  return [entry.publisher_username, entry.publisher_department].filter(Boolean).join(" / ");
}

type EntryState = { badge: string; tone: "slate" | "brand" | "green" | "amber" | "red" };

/** 一条目录条目在管理页上的状态：已安装 / 待审核 / 可升级 / 已驳回 / 未安装。 */
function entryState(entry: DirectoryEntry, t: TFunction): EntryState {
  if (entry.installed) return { badge: t("directory.stateInstalled"), tone: "green" };
  if (entry.kind === "dsh-plugin" && entry.installed_status === "pending" && entry.installed_version === entry.version) {
    return { badge: t("directory.statePendingReview"), tone: "amber" };
  }
  if (entry.kind === "dsh-plugin" && entry.installed_status === "rejected" && entry.installed_version === entry.version) {
    return { badge: t("directory.stateRejected"), tone: "red" };
  }
  if (entry.kind === "dsh-plugin" && entry.installed_status === "unpublished" && entry.installed_version === entry.version) {
    return { badge: t("directory.stateUnpublished"), tone: "slate" };
  }
  if (entry.upgrade_available) {
    return { badge: t("directory.stateUpgradable", { from: entry.installed_version ?? "—" }), tone: "brand" };
  }
  if (entry.installed_version) {
    return { badge: t("directory.stateOtherVersion", { version: entry.installed_version }), tone: "slate" };
  }
  return { badge: t("directory.stateNotInstalled"), tone: "slate" };
}

/** 这一条该显示「安装」还是「升级」；都不合适时不显示操作。 */
function actionOf(entry: DirectoryEntry): "install" | "upgrade" | null {
  if (entry.upgrade_available) return "upgrade";
  if (!entry.installed && !entry.installed_version) return "install";
  return null;
}

function EntriesTable({
  data,
  canExtensions,
  canPlugins,
  onAction,
}: {
  data: DirectoryListResp;
  canExtensions: boolean;
  canPlugins: boolean;
  onAction: (entry: DirectoryEntry, kind: "install" | "upgrade") => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="overflow-x-auto">
      <table className="tbl max-sm:min-w-[64rem]" data-testid="directory-entries">
        <thead>
          <tr>
            <th className="px-3 py-2">{t("directory.colID")}</th>
            <th className="px-3 py-2">{t("directory.colKind")}</th>
            <th className="px-3 py-2">{t("directory.colName")}</th>
            <th className="px-3 py-2">{t("directory.colSource")}</th>
            <th className="px-3 py-2">{t("directory.colPublisher")}</th>
            <th className="px-3 py-2">{t("directory.colSignature")}</th>
            <th className="px-3 py-2">{t("directory.colState")}</th>
            <th className="px-3 py-2 text-right">{t("directory.colActions")}</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-neutral-100">
          {data.entries.map((entry) => {
            const state = entryState(entry, t);
            const act = actionOf(entry);
            const allowed = entry.kind === "dsh-plugin" ? canPlugins : canExtensions;
            return (
              <tr key={entry.id} data-testid={`directory-entry-${entry.id}`}>
                <td className="px-3 py-3 font-mono text-xs text-neutral-500">{entry.id}</td>
                <td className="px-3 py-3">
                  <Badge tone={entry.kind === "extension" ? "brand" : "slate"}>
                    {entry.kind === "extension" ? t("directory.kindExtension") : t("directory.kindPlugin")}
                  </Badge>
                </td>
                <td className="px-3 py-3">
                  <div className="font-medium text-neutral-900">{entry.name}</div>
                  <div className="mt-0.5 flex flex-wrap items-center gap-1.5 text-xs text-neutral-500">
                    <span className="font-mono">{entry.version}</span>
                    {entry.channel && <Badge>{entry.channel}</Badge>}
                    {entry.notes && <span className="truncate">{entry.notes}</span>}
                  </div>
                </td>
                <td className="max-w-64 px-3 py-3">
                  <div className="truncate text-xs text-neutral-600" title={entrySource(entry)}>
                    {entrySource(entry)}
                  </div>
                  <div className="mt-0.5 font-mono text-[11px] text-neutral-400" title={entry.digest}>
                    {entry.digest.slice(0, 20)}…
                  </div>
                </td>
                <td className="px-3 py-3 text-xs text-neutral-600">{publisherOf(entry)}</td>
                <td className="px-3 py-3">
                  {entry.signature_key_id ? (
                    <span className="flex flex-col gap-1">
                      <Badge tone={entry.signature_key_state === "active" ? "green" : entry.signature_key_state === "revoked" ? "red" : "slate"}>
                        <ShieldCheck size={12} /> {entry.signature_key_state || t("directory.entrySigDeclared")}
                      </Badge>
                      <span className="font-mono text-[11px] text-neutral-500">{entry.signature_key_id}</span>
                    </span>
                  ) : (
                    <span className="text-xs text-neutral-500">{t("directory.entrySigNone")}</span>
                  )}
                </td>
                <td className="px-3 py-3">
                  <Badge tone={state.tone}>{state.badge}</Badge>
                </td>
                <td className="px-3 py-3 text-right">
                  {act ? (
                    allowed ? (
                      <Button
                        size="sm"
                        variant="secondary"
                        data-testid={`directory-${act}-${entry.id}`}
                        onClick={() => onAction(entry, act)}
                      >
                        {act === "install" ? t("directory.install") : t("directory.upgrade")}
                      </Button>
                    ) : (
                      <span className="text-xs text-neutral-500">{t("directory.needPerm", { perm: kindPerm(entry.kind) })}</span>
                    )
                  ) : (
                    <span className="text-xs text-neutral-400">—</span>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

// ---- 安装 / 升级对话框：先出计划，确认后执行 ----

function DirectoryActionDialog({
  entry,
  actionKind,
  onClose,
  onFinished,
}: {
  entry: DirectoryEntry;
  actionKind: "install" | "upgrade";
  onClose: () => void;
  onFinished: () => void;
}) {
  const { t } = useTranslation();
  const { notify } = useStore();
  const { busy, run } = useAction();
  const [plan, setPlan] = useState<DirectoryInstallPlan | null>(null);
  const [planError, setPlanError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<DirectoryInstallPlan | null>(null);
  const [slug, setSlug] = useState("");
  const [secret, setSecret] = useState("");
  const [oauthID, setOauthID] = useState("");
  const [oauthSecret, setOauthSecret] = useState("");
  const [admin, setAdmin] = useState<Record<string, string>>({});

  useEffect(() => {
    let live = true;
    setLoading(true);
    api
      .directoryEntryPlan(entry.id, actionKind)
      .then((p) => {
        if (!live) return;
        setPlan(p);
        initForm(p);
      })
      .catch((e: unknown) => {
        if (!live) return;
        const p = installPlanOf(e);
        setPlan(p);
        setPlanError(e instanceof Error ? e.message : String(e));
        if (p) initForm(p);
      })
      .finally(() => live && setLoading(false));
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [entry.id, actionKind]);

  const initForm = (p: DirectoryInstallPlan) => {
    const ext = p.extension;
    if (!ext) return;
    setSlug(ext.existing?.slug ?? ext.slug ?? "");
    setOauthID(ext.oauth_client_id ?? "");
    setOauthSecret("");
    setSecret("");
    const init: Record<string, string> = {};
    for (const ap of ext.admin_params ?? []) init[ap.key] = shown(ext.admin?.[ap.key] ?? ap.default);
    setAdmin(init);
  };

  const req = (): DirectoryInstallReq => {
    const out: DirectoryInstallReq = { confirm: true };
    if (entry.kind === "extension") {
      if (actionKind === "install" && slug.trim()) out.slug = slug.trim();
      if (secret) out.secret = secret;
      if (oauthID.trim()) out.oauth_client_id = oauthID.trim();
      if (oauthSecret) out.oauth_client_secret = oauthSecret;
      const params: Record<string, unknown> = {};
      for (const ap of plan?.extension?.admin_params ?? []) {
        const v = parsedAdmin(ap, admin[ap.key] ?? "");
        if (v !== undefined) params[ap.key] = v;
      }
      if (Object.keys(params).length > 0) out.admin = params;
    }
    return out;
  };

  const confirm = async () => {
    setError(null);
    const r = await run(() => api.directoryEntryInstall(entry.id, actionKind, req()));
    if (!r.ok) {
      setError(r.error);
      return;
    }
    setResult(r.value);
    onFinished();
    notify(t("directory.doneToast"));
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[88vh] overflow-y-auto sm:max-w-2xl" data-testid="directory-action-dialog">
        <DialogHeader>
          <DialogTitle>
            {entry.kind === "extension" ? t("directory.kindExtension") : t("directory.kindPlugin")} ·{" "}
            {actionKind === "install" ? t("directory.install") : t("directory.upgrade")}
          </DialogTitle>
          <DialogDescription>
            {entry.name}@{entry.version} · {entry.channel}
          </DialogDescription>
        </DialogHeader>
        {loading ? (
          <div className="py-8 text-sm text-neutral-500" role="status">
            {t("directory.planLoading")}
          </div>
        ) : result ? (
          <ResultView result={result} />
        ) : (
          <div className="space-y-4">
            <EntryFacts entry={entry} />
            <ErrorNote>{planError || error}</ErrorNote>
            {plan?.extension && <ExtensionPlanBody plan={plan.extension} />}
            {plan?.plugin && <PluginPlanBody plan={plan.plugin} />}
            {plan?.extension && (
              <div className="space-y-3">
                {!plan.extension.existing && (
                  <Field label={t("directory.formSlug")} hint={t("directory.formSlugHint")}>
                    <input className={inputCls} value={slug} onChange={(e) => setSlug(e.target.value)} data-testid="directory-form-slug" />
                  </Field>
                )}
                {(plan.extension.admin_params ?? []).map((ap) => (
                  <Field key={ap.key} label={ap.label} hint={ap.type ? t("directory.formAdminHint", { type: ap.type }) : undefined}>
                    <input
                      className={inputCls}
                      value={admin[ap.key] ?? ""}
                      onChange={(e) => setAdmin((m) => ({ ...m, [ap.key]: e.target.value }))}
                      data-testid={`directory-form-admin-${ap.key}`}
                    />
                  </Field>
                ))}
                <Field label={t("directory.formSecret")} hint={t("directory.formSecretHint")}>
                  <PasswordInput className={inputCls} value={secret} onChange={(e) => setSecret(e.target.value)} data-testid="directory-form-secret" />
                </Field>
                {plan.extension.oauth_client && (
                  <>
                    <Field label={t("directory.formOauthID")}>
                      <input className={inputCls} value={oauthID} onChange={(e) => setOauthID(e.target.value)} data-testid="directory-form-oauth-id" />
                    </Field>
                    <Field label={t("directory.formOauthSecret")} hint={t("directory.formOauthSecretHint")}>
                      <PasswordInput className={inputCls} value={oauthSecret} onChange={(e) => setOauthSecret(e.target.value)} data-testid="directory-form-oauth-secret" />
                    </Field>
                  </>
                )}
              </div>
            )}
          </div>
        )}
        <DialogFooter>
          {result ? (
            <Button onClick={onClose} data-testid="directory-action-close">
              {t("common.close")}
            </Button>
          ) : (
            <>
              <Button variant="secondary" onClick={onClose} disabled={busy}>
                {t("common.cancel")}
              </Button>
              <Button
                onClick={confirm}
                disabled={busy || loading || !!planError || !plan}
                data-testid="directory-action-confirm"
              >
                {actionKind === "install" ? t("directory.install") : t("directory.upgrade")}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function EntryFacts({ entry }: { entry: DirectoryEntry }) {
  const { t } = useTranslation();
  return (
    <dl className="grid grid-cols-[8rem_1fr] gap-x-3 gap-y-1.5 text-sm" data-testid="directory-action-entry">
      <dt className="text-neutral-500">{t("directory.colKind")}</dt>
      <dd>{entry.kind === "extension" ? t("directory.kindExtension") : t("directory.kindPlugin")}</dd>
      <dt className="text-neutral-500">{t("directory.colSource")}</dt>
      <dd className="break-all font-mono text-xs">{entrySource(entry)}</dd>
      <dt className="text-neutral-500">{t("directory.colPublisher")}</dt>
      <dd>{publisherOf(entry)}</dd>
      <dt className="text-neutral-500">{t("directory.digest")}</dt>
      <dd className="break-all font-mono text-xs">{entry.digest}</dd>
      {entry.installed_version && (
        <>
          <dt className="text-neutral-500">{t("directory.installedVersion")}</dt>
          <dd className="font-mono text-xs">
            {entry.installed_version}
            {entry.installed_connection ? ` · ${entry.installed_connection}` : ""}
          </dd>
        </>
      )}
    </dl>
  );
}

function ExtensionPlanBody({ plan }: { plan: ExtPlan }) {
  const { t } = useTranslation();
  const errors = plan.check.results?.filter((r) => r.severity === "error") ?? [];
  const warnings = plan.check.results?.filter((r) => r.severity === "warning") ?? [];
  const sensitive = plan.sensitive ?? [];
  return (
    <div className="space-y-3" data-testid="directory-action-plan">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <Badge tone={plan.check.ok ? "green" : "red"}>{plan.check.ok ? t("directory.planOK") : t("directory.planFailed")}</Badge>
        <span className="font-mono text-xs">{plan.check.identity}</span>
        {plan.signature && (
          <Badge tone={plan.signature.status === "valid" ? "green" : plan.signature.status === "unsigned" ? "amber" : "red"}>
            {t(`directory.extSig.${plan.signature.status}`, { defaultValue: plan.signature.status })}
          </Badge>
        )}
        {plan.existing && (
          <Badge tone="brand">{t("directory.upgradeFrom", { version: plan.existing.version })}</Badge>
        )}
      </div>
      {plan.check.servers && plan.check.servers.length > 0 && (
        <p className="text-xs leading-5 text-neutral-500">{t("directory.servers", { servers: plan.check.servers.join("、") })}</p>
      )}
      {errors.length > 0 && (
        <div className="space-y-1" data-testid="directory-action-errors">
          {errors.slice(0, 20).map((d, i) => (
            <div key={i} className="text-xs leading-5 text-red-700 dark:text-red-300">
              <span className="font-mono">{d.code}</span> {d.message}
            </div>
          ))}
        </div>
      )}
      {warnings.length > 0 && (
        <div className="space-y-1">
          {warnings.slice(0, 10).map((d, i) => (
            <div key={i} className="text-xs leading-5 text-amber-700 dark:text-amber-300">
              <span className="font-mono">{d.code}</span> {d.message}
            </div>
          ))}
        </div>
      )}
      {sensitive.length > 0 && (
        <Notice tone="warn" data-testid="directory-action-sensitive">
          <div className="font-medium">{t("directory.sensitiveChanges")}</div>
          <ul className="mt-1 list-disc space-y-1 pl-4 text-xs">
            {sensitive.map((s) => (
              <li key={s} className="break-words">
                {s}
              </li>
            ))}
          </ul>
        </Notice>
      )}
    </div>
  );
}

function PluginPlanBody({ plan }: { plan: DirectoryPluginPlan }) {
  const { t } = useTranslation();
  return (
    <div className="space-y-2" data-testid="directory-action-plan">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <Badge tone="green">{plan.name}@{plan.version}</Badge>
        {plan.review_required ? (
          <Badge tone="amber">{t("directory.pluginReviewRequired")}</Badge>
        ) : (
          <Badge tone="slate">{t("directory.pluginNoReview")}</Badge>
        )}
        {plan.existing_version && <span className="text-xs text-neutral-500">{t("directory.pluginExisting", { version: plan.existing_version })}</span>}
      </div>
      <p className="break-all text-xs leading-5 text-neutral-500">{t("directory.digest")}：{plan.digest}</p>
      {plan.message && <p className="text-sm text-neutral-700 dark:text-neutral-300">{plan.message}</p>}
      <Notice tone="info">{t("directory.pluginReviewHint")}</Notice>
    </div>
  );
}

function ResultView({ result }: { result: DirectoryInstallPlan }) {
  const { t } = useTranslation();
  const r = result.result ?? {};
  const plugin = result.plugin;
  return (
    <div className="space-y-3" data-testid="directory-action-result">
      <Notice tone="success">{t("directory.done")}</Notice>
      {result.kind === "extension" ? (
        <dl className="grid grid-cols-[8rem_1fr] gap-x-3 gap-y-1.5 text-sm">
          <dt className="text-neutral-500">{t("directory.resultIdentity")}</dt>
          <dd className="font-mono text-xs">{String(r.identity ?? "—")}</dd>
          <dt className="text-neutral-500">{t("directory.resultTools")}</dt>
          <dd>{typeof r.tools === "number" ? r.tools : "—"}</dd>
          <dt className="text-neutral-500">{t("directory.resultAsset")}</dt>
          <dd className="font-mono text-xs">{(r.asset as { slug?: string } | undefined)?.slug ?? "—"}</dd>
          {r.upgraded_from && (
            <>
              <dt className="text-neutral-500">{t("directory.resultUpgradedFrom")}</dt>
              <dd className="font-mono text-xs">{String(r.upgraded_from)}</dd>
            </>
          )}
          {r.sync_error && (
            <>
              <dt className="text-neutral-500">{t("directory.resultSyncError")}</dt>
              <dd className="text-xs text-red-700 dark:text-red-300">{String(r.sync_error)}</dd>
            </>
          )}
        </dl>
      ) : (
        <dl className="grid grid-cols-[8rem_1fr] gap-x-3 gap-y-1.5 text-sm">
          <dt className="text-neutral-500">{t("directory.resultStatus")}</dt>
          <dd>{String(r.status ?? "—")}</dd>
          {plugin?.review_required && (
            <>
              <dt className="text-neutral-500">{t("directory.resultReview")}</dt>
              <dd>{t("directory.pluginReviewRequired")}</dd>
            </>
          )}
          {r.job && (
            <>
              <dt className="text-neutral-500">{t("directory.resultJob")}</dt>
              <dd className="font-mono text-xs">#{r.job.id}</dd>
            </>
          )}
        </dl>
      )}
      {result.message && <p className="text-sm text-neutral-600">{result.message}</p>}
    </div>
  );
}
