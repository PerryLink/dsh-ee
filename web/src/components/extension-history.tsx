import { useState } from "react";
import { useTranslation } from "react-i18next";
import { History, RotateCcw } from "lucide-react";
import { formatTime } from "../lib/api";
import { extApi, type ExtHistoryVersion, type ExtRollbackPlan } from "../lib/ext-api";
import { useAction, useApi } from "../lib/use-api";
import { useStore } from "../lib/store";
import { Badge, Button, Notice } from "./ui";
import { ErrorNote, LoadState } from "./api-ui";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/shadcn/dialog";

/** 签名状态的颜色：有效绿、无效/撤销红、没有签名琥珀、旧版本没存材料灰。 */
function signatureTone(status: string): "green" | "red" | "amber" | "slate" {
  switch (status) {
    case "valid":
      return "green";
    case "invalid":
    case "revoked":
      return "red";
    case "unsigned":
      return "amber";
    default:
      return "slate";
  }
}

function SignatureBadge({ status }: { status: string }) {
  const { t } = useTranslation();
  const label = status === "unknown" ? t("extAdmin.historySigUnknown") : t(`extensions.signatureStatus.${status}`, { defaultValue: status });
  return (
    <Badge tone={signatureTone(status)}>
      {label}
    </Badge>
  );
}

/**
 * 连接详情页上的「安装历史」入口（docs/plugins.md §9.4）：列出扩展装出来的连接的历史版本，
 * 选中一个看回滚计划（变化 / 敏感项），确认后调用回滚接口，并把工具清单刷出来。
 */
export function ExtensionHistoryButton({ slug, onChanged }: { slug: string; onChanged?: () => void }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button size="sm" variant="secondary" data-testid="extension-history-open" onClick={() => setOpen(true)}>
        <History size={14} /> {t("extAdmin.historyOpen")}
      </Button>
      {open && <ExtensionHistoryDialog slug={slug} onClose={() => setOpen(false)} onChanged={onChanged} />}
    </>
  );
}

function ExtensionHistoryDialog({ slug, onClose, onChanged }: { slug: string; onClose: () => void; onChanged?: () => void }) {
  const { t } = useTranslation();
  const { notify } = useStore();
  const history = useApi(() => extApi.extHistory(slug));
  const { busy, run } = useAction();
  const [plan, setPlan] = useState<ExtRollbackPlan | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [previewing, setPreviewing] = useState(false);

  const preview = async (v: ExtHistoryVersion) => {
    setError(null);
    setPreviewing(true);
    const r = await run(() => extApi.extRollbackPlan(slug, v.asset_version));
    setPreviewing(false);
    if (!r.ok) return setError(r.error);
    setPlan(r.value);
  };

  const confirm = async () => {
    const target = plan?.to.asset_version;
    if (!target) return;
    setError(null);
    const r = await run(() => extApi.extRollback(slug, target));
    if (!r.ok) return setError(r.error);
    notify(t("extAdmin.rollbackDone", { version: r.value.rolled_back_to.extension }));
    onChanged?.();
    onClose();
  };

  const versions = history.data?.versions ?? [];
  const current = versions.find((v) => v.current);
  const rollable = versions.filter((v) => !v.current);

  return (
    <Dialog
      open
      onOpenChange={(v) => {
        if (!v) onClose();
      }}
    >
      <DialogContent data-testid="extension-history-dialog" className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("extAdmin.historyTitle")}</DialogTitle>
          <DialogDescription>{t("extAdmin.historyDesc")}</DialogDescription>
        </DialogHeader>
        <LoadState loading={history.loading} error={history.error} onRetry={history.reload}>
          {plan ? (
            <RollbackPlanView plan={plan} busy={busy} error={error} onBack={() => { setPlan(null); setError(null); }} onConfirm={confirm} />
          ) : (
            <div className="space-y-3">
              {current && (
                <p className="text-sm text-neutral-600">
                  {t("extAdmin.historyCurrentHint", { extension: current.extension, version: current.asset_version })}
                </p>
              )}
              <div className="divide-y divide-neutral-100 rounded-md border border-neutral-200">
                {versions.map((v) => (
                  <div
                    key={v.asset_version}
                    data-testid={`ext-history-${v.asset_version}`}
                    className="flex flex-wrap items-center gap-x-3 gap-y-2 px-3 py-2.5 text-sm"
                  >
                    <span className="font-mono text-neutral-900">{t("extAdmin.historyVersion", { n: v.asset_version })}</span>
                    <span className="min-w-0 font-mono text-xs break-all text-neutral-600">{v.extension}</span>
                    {v.current && (
                      <span data-testid="ext-history-current">
                        <Badge tone="green">{t("extAdmin.historyCurrent")}</Badge>
                      </span>
                    )}
                    <span className="font-mono text-[11px] text-neutral-500">#{v.digest.slice(0, 12)}</span>
                    <SignatureBadge status={v.signature} />
                    <span className="text-xs text-neutral-500">
                      {t("extAdmin.historyInstalled", { name: v.installed_by || "—", time: formatTime(v.installed_at) })}
                    </span>
                    <span className="ml-auto">
                      {!v.current &&
                        (v.can_rollback ? (
                          <Button
                            size="sm"
                            variant="secondary"
                            disabled={busy || previewing}
                            data-testid={`ext-rollback-${v.asset_version}`}
                            onClick={() => void preview(v)}
                          >
                            <RotateCcw size={13} /> {t("extAdmin.historyRollback")}
                          </Button>
                        ) : (
                          <span className="text-xs text-neutral-500" data-testid={`ext-rollback-blocked-${v.asset_version}`}>
                            {t("extAdmin.historyBlocked")}
                          </span>
                        ))}
                    </span>
                  </div>
                ))}
              </div>
              {versions.length === 0 && (
                <p className="text-sm text-neutral-600" data-testid="ext-history-empty">
                  {t("extAdmin.historyEmpty")}
                </p>
              )}
              {versions.length > 0 && rollable.length === 0 && (
                <p className="text-sm text-neutral-600" data-testid="ext-history-empty">
                  {t("extAdmin.historyNoTarget")}
                </p>
              )}
              <ErrorNote>{error}</ErrorNote>
            </div>
          )}
        </LoadState>
      </DialogContent>
    </Dialog>
  );
}

function RollbackPlanView({
  plan,
  busy,
  error,
  onBack,
  onConfirm,
}: {
  plan: ExtRollbackPlan;
  busy: boolean;
  error: string | null;
  onBack: () => void;
  onConfirm: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="space-y-4 text-sm" data-testid="ext-rollback-plan">
      <dl className="grid grid-cols-[5rem_1fr] gap-x-3 gap-y-2">
        <dt className="text-neutral-500">{t("extAdmin.rollbackFrom")}</dt>
        <dd className="min-w-0 font-mono text-xs break-all">{plan.from.extension}</dd>
        <dt className="text-neutral-500">{t("extAdmin.rollbackTo")}</dt>
        <dd className="flex min-w-0 flex-wrap items-center gap-2 font-mono text-xs break-all">
          <span>{plan.to.extension}</span>
          <span className="text-neutral-500">#{plan.to.digest.slice(0, 12)}</span>
          <SignatureBadge status={plan.to.signature} />
        </dd>
      </dl>
      {plan.changes.length > 0 && (
        <div>
          <p className="mb-1 font-medium text-neutral-900">{t("extAdmin.rollbackChanges")}</p>
          <ul className="list-disc space-y-0.5 pl-5 text-neutral-700">
            {plan.changes.map((c) => (
              <li key={c}>{c}</li>
            ))}
          </ul>
        </div>
      )}
      {plan.sensitive.length > 0 && (
        <Notice tone="warn">
          <p className="font-medium">{t("extAdmin.rollbackSensitive")}</p>
          <ul className="mt-1 list-disc space-y-0.5 pl-5">
            {plan.sensitive.map((c) => (
              <li key={c}>{c}</li>
            ))}
          </ul>
        </Notice>
      )}
      {plan.warnings.length > 0 && (
        <Notice tone="info">
          <ul className="list-disc space-y-0.5 pl-5">
            {plan.warnings.map((c) => (
              <li key={c}>{c}</li>
            ))}
          </ul>
        </Notice>
      )}
      <p className="text-xs text-neutral-500">{t("extAdmin.rollbackToolNote")}</p>
      <ErrorNote>{error}</ErrorNote>
      <DialogFooter>
        <Button variant="secondary" disabled={busy} onClick={onBack}>
          {t("extAdmin.rollbackBack")}
        </Button>
        <Button variant="danger" disabled={busy} data-testid="ext-rollback-confirm" onClick={onConfirm}>
          <RotateCcw size={14} /> {t("extAdmin.rollbackConfirm")}
        </Button>
      </DialogFooter>
    </div>
  );
}
