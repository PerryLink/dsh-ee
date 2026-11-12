import { useMemo } from "react";
import { Link, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import { Check, ShieldAlert, X } from "lucide-react";
import { formatTime } from "../lib/api";
import { extApi, type WriteConfirmation } from "../lib/ext-api";
import { useAction, useApi } from "../lib/use-api";
import { useStore } from "../lib/store";
import { LoadState } from "../components/api-ui";
import { Badge, Button, Card, CardHeader, Notice, PageHeader, type Tone } from "../components/ui";

const statusTone: Record<WriteConfirmation["status"], Tone> = { pending: "amber", approved: "green", rejected: "red", used: "slate" };

/** 参数按 JSON 排版；解析不了就原样显示。 */
function prettyArgs(text: string) {
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}

/** 工具名去掉端侧扩展的前缀（local:作者/名字/工具 → 工具）。 */
function toolName(c: WriteConfirmation) {
  if (c.tool.startsWith("local:")) return c.tool.slice(c.tool.lastIndexOf("/") + 1);
  return c.tool;
}

/**
 * /confirm/:id：写操作的服务端确认（docs/plugins.md §7.5、§8）。
 * 不是 DSH 的 Agent（或端侧扩展）要改企业系统里的数据时，dsh-ee 先返回这个链接；员工本人在这里核对参数、同意之后，
 * Agent 用完全相同的参数再调一次才会执行。
 */
export default function ConfirmPage() {
  const { id = "" } = useParams();
  const num = Number(id);
  const { t } = useTranslation();
  const { notify } = useStore();
  const { busy, run } = useAction();
  const item = useApi(() => extApi.confirmation(num));
  const all = useApi(extApi.myConfirmations);
  const others = useMemo(() => (all.data ?? []).filter((c) => c.id !== num && c.status === "pending" && !c.expired), [all.data, num]);

  const decide = async (approve: boolean) => {
    const r = await run(() => extApi.decideConfirmation(num, approve));
    if (!r.ok) return notify(r.error);
    notify(approve ? t("confirmPage.approvedToast") : t("confirmPage.rejectedToast"));
    item.reload();
    all.reload();
  };

  const c = item.data;
  const local = c?.tool.startsWith("local:");
  return (
    <div className="max-w-3xl">
      <PageHeader title={t("confirmPage.title")} desc={t("confirmPage.desc")} />
      {item.error && !c ? (
        <Notice tone="danger">{t("confirmPage.notFound")}</Notice>
      ) : (
        <LoadState loading={item.loading && !c} error={null} onRetry={item.reload}>
          {c && (
            <Card data-confirmation={c.id}>
              <CardHeader
                title={
                  <span className="flex items-center gap-2">
                    <ShieldAlert size={16} className="text-amber-600" aria-hidden />
                    {c.system || c.slug || "—"}
                  </span>
                }
                aside={<Badge tone={c.expired && c.status === "pending" ? "slate" : statusTone[c.status]}>{t(`confirmPage.status.${c.status}`)}</Badge>}
              />
              <div className="space-y-4 px-5 pb-5 text-sm">
                <dl className="grid grid-cols-[6.5rem_1fr] gap-x-3 gap-y-2">
                  <dt className="text-neutral-500">{t("confirmPage.tool")}</dt>
                  <dd>
                    <span className="font-mono text-neutral-900">{toolName(c)}</span>
                    {c.description && <span className="mt-0.5 block text-neutral-600">{c.description}</span>}
                  </dd>
                  {c.client && (
                    <>
                      <dt className="text-neutral-500">{t("confirmPage.client")}</dt>
                      <dd className="break-all text-neutral-700">{c.client}</dd>
                    </>
                  )}
                  <dt className="text-neutral-500">{t("confirmPage.created")}</dt>
                  <dd>{formatTime(c.created_at)}</dd>
                  <dt className="text-neutral-500">{t("confirmPage.expires")}</dt>
                  <dd>{formatTime(c.expires_at)}</dd>
                </dl>
                <div>
                  <div className="mb-1 text-neutral-500">{t("confirmPage.args")}</div>
                  <pre className="max-h-96 overflow-auto rounded-md bg-neutral-50 p-3 font-mono text-xs whitespace-pre-wrap break-all text-neutral-800 ring-1 ring-neutral-200 ring-inset">
                    {prettyArgs(c.args)}
                  </pre>
                </div>
                {local && <Notice>{t("confirmPage.local")}</Notice>}
                {c.status === "pending" && c.expired && <Notice tone="warn">{t("confirmPage.expired")}</Notice>}
                {c.status === "approved" && <Notice tone="success">{c.expired ? t("confirmPage.expired") : t("confirmPage.approvedNote")}</Notice>}
                {c.status === "rejected" && <Notice tone="warn">{t("confirmPage.rejectedNote")}</Notice>}
                {c.status === "used" && <Notice>{t("confirmPage.usedNote")}</Notice>}
                {c.status === "pending" && !c.expired && (
                  <>
                    <p className="text-xs text-neutral-500">{t("confirmPage.readOnlyNote")}</p>
                    <div className="flex flex-wrap gap-2">
                      <Button disabled={busy} onClick={() => void decide(true)}>
                        <Check size={15} /> {t("confirmPage.approve")}
                      </Button>
                      <Button variant="secondary" disabled={busy} onClick={() => void decide(false)}>
                        <X size={15} /> {t("confirmPage.reject")}
                      </Button>
                    </div>
                  </>
                )}
              </div>
            </Card>
          )}
        </LoadState>
      )}

      <Card className="mt-6">
        <CardHeader title={t("confirmPage.others")} />
        <div className="px-5 pb-5 text-sm">
          {others.length === 0 ? (
            <p className="text-neutral-500">{t("confirmPage.none")}</p>
          ) : (
            <ul className="divide-y divide-neutral-100">
              {others.map((o) => (
                <li key={o.id} className="flex flex-wrap items-center gap-2 py-2">
                  <Link to={`/confirm/${o.id}`} className="font-medium text-brand-700 hover:underline">
                    {o.system || o.slug}
                  </Link>
                  <span className="font-mono text-xs text-neutral-500">{toolName(o)}</span>
                  <span className="ml-auto text-xs text-neutral-500">{formatTime(o.created_at)}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      </Card>
    </div>
  );
}
