import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { api, formatTime, formatTokens, type TokenClientKind, type TokenScope, type TokenView } from "../../lib/api";
import { useAction, useApi } from "../../lib/use-api";
import { useStore } from "../../lib/store";
import { useConfirm } from "@/lib/confirm";
import { Combobox } from "@/components/pickers";
import { ErrorNote, LoadState } from "../../components/api-ui";
import { Badge, Button, Card, CardHeader, CopyBox, Field, inputBase, inputCls, PageHeader, cx, Empty } from "../../components/ui";

const scopes: TokenScope[] = ["models", "registry:read", "registry:publish", "hub", "connections", "agent"];
type BulkMode = "department" | "user" | "device";

export default function AdminTokens() {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const { notify } = useStore();
  const { data, error, loading, reload } = useApi(api.adminTokens);
  const users = useApi(api.users);
  const { busy, run } = useAction();
  const [query, setQuery] = useState("");
  const [source, setSource] = useState("all");
  const [status, setStatus] = useState("active");
  const [departmentFilter, setDepartmentFilter] = useState("");
  const [issuing, setIssuing] = useState(false);
  const [issued, setIssued] = useState<string | null>(null);
  const [userId, setUserId] = useState("");
  const [name, setName] = useState("");
  const [days, setDays] = useState("");
  // 管理员签发的默认也是第三方 Harness（写操作始终人工确认）；给 DSH 手工令牌时显式选 dsh
  const [clientKind, setClientKind] = useState<TokenClientKind>("third_party");
  const [formError, setFormError] = useState<string | null>(null);
  const [bulkMode, setBulkMode] = useState<BulkMode>("department");
  const [bulkTarget, setBulkTarget] = useState("");

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setFormError(null);
    const r = await run(() => api.issueToken({ user_id: Number(userId), name: name.trim(), scopes, client_kind: clientKind, ...(days ? { expires_in_days: Number(days) } : {}) }));
    if (!r.ok) return setFormError(r.error);
    setIssued(r.value.token);
    setIssuing(false);
    setName("");
    setClientKind("third_party");
    reload();
  };

  const bulkMatches = (data ?? []).filter((token) => {
    if (token.revoked_at || !bulkTarget.trim()) return false;
    if (bulkMode === "department") return token.department === bulkTarget.trim();
    if (bulkMode === "user") return token.user_id === Number(bulkTarget);
    return token.client_name === bulkTarget.trim();
  });
  const bulkLabel = bulkMode === "user" ? users.data?.find((user) => String(user.id) === bulkTarget)?.username ?? bulkTarget : bulkTarget.trim();
  const revokeBulk = async () => {
    if (!(await confirm({ kind: "revoke", description: t("tokens.bulkConfirm", { target: bulkLabel, count: bulkMatches.length }) }))) return;
    const input = bulkMode === "department" ? { department: bulkTarget.trim() } : bulkMode === "user" ? { user_id: Number(bulkTarget) } : { client_name: bulkTarget.trim() };
    const r = await run(() => api.revokeTokens(input));
    notify(r.ok ? t("tokens.bulkDone", { count: r.value.revoked }) : r.error);
    if (r.ok) {
      setBulkTarget("");
      reload();
    }
  };

  const list = (data ?? []).filter((token) => {
    const text = `${token.name} ${token.username ?? ""} ${token.prefix} ${token.client_name}`.toLowerCase();
    if (!text.includes(query.toLowerCase())) return false;
    if (source !== "all" && token.source !== source) return false;
    if (status === "active" && token.revoked_at) return false;
    if (status === "revoked" && !token.revoked_at) return false;
    return !departmentFilter.trim() || token.department === departmentFilter.trim();
  });

  return (
    <div>
      <PageHeader title={t("pages.admin-keys")} desc={t("tokens.adminDesc")} actions={<Button size="sm" onClick={() => setIssuing((v) => !v)}>{issuing ? t("common.close") : t("tokens.issue")}</Button>} />
      {issued && <Card className="mb-4 p-5"><h2 className="mb-2 text-[15px] leading-6 font-semibold text-neutral-900">{t("tokens.issuedTitle")}</h2><p className="mb-3 text-sm text-neutral-600">{t("tokens.issuedHint")}</p><CopyBox text={issued} /></Card>}
      {issuing && (
        <Card className="mb-4"><CardHeader title={t("tokens.issue")} />
          <form onSubmit={submit} className="space-y-4 px-5 pb-5">
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
              <Field label={t("tokens.owner")}><Combobox value={userId} onChange={setUserId} placeholder={t("tokens.chooseOwner")} options={(users.data ?? []).filter((u) => !u.disabled).map((u) => ({ value: String(u.id), label: u.display_name ? `${u.display_name} (${u.username})` : u.username, hint: u.account_type === "bot" ? `bot · ${u.department}` : u.department }))} /></Field>
              <Field label={t("tokens.name")}><input className={inputCls} required value={name} onChange={(e) => setName(e.target.value)} /></Field>
              <Field label={t("tokens.clientKind")}>
                <select aria-label={t("tokens.clientKind")} className={inputCls} value={clientKind} onChange={(e) => setClientKind(e.target.value as TokenClientKind)}>
                  {(["dsh", "third_party"] as const).map((kind) => <option key={kind} value={kind}>{t(`tokens.clientKinds.${kind}`)}</option>)}
                </select>
              </Field>
              <Field label={t("tokens.days")}><input className={inputCls} type="number" min={0} max={3650} value={days} onChange={(e) => setDays(e.target.value)} placeholder={t("tokens.noExpiry")} /></Field>
            </div>
            <p className="-mt-2 text-xs leading-5 text-neutral-600">{t(`tokens.clientKindHints.${clientKind}`)}</p>
            <ErrorNote>{formError}</ErrorNote>
            <Button type="submit" disabled={busy || !userId || !name.trim()}>{t("tokens.issue")}</Button>
          </form>
        </Card>
      )}
      <Card className="mb-4 p-5">
        <div className="flex flex-wrap items-end gap-3">
          <Field label={t("tokens.bulkMode")}><select aria-label={t("tokens.bulkMode")} className={cx(inputBase, "w-44")} value={bulkMode} onChange={(e) => { setBulkMode(e.target.value as BulkMode); setBulkTarget(""); }}><option value="department">{t("tokens.bulkModes.department")}</option><option value="user">{t("tokens.bulkModes.user")}</option><option value="device">{t("tokens.bulkModes.device")}</option></select></Field>
          {bulkMode === "user" ? (
            <Field label={t("tokens.owner")}><Combobox value={bulkTarget} onChange={setBulkTarget} placeholder={t("tokens.chooseOwner")} options={(users.data ?? []).map((user) => ({ value: String(user.id), label: user.display_name ? `${user.display_name} (${user.username})` : user.username, hint: user.department }))} /></Field>
          ) : (
            <Field label={t(`tokens.bulkTargets.${bulkMode}`)}><input className={cx(inputBase, "w-56")} value={bulkTarget} onChange={(e) => setBulkTarget(e.target.value)} placeholder={t(`tokens.bulkPlaceholders.${bulkMode}`)} /></Field>
          )}
          <Button variant="secondary" size="sm" disabled={busy || !bulkTarget.trim() || bulkMatches.length === 0} onClick={revokeBulk}>{t(`tokens.bulkActions.${bulkMode}`)}</Button>
          <span className="pb-2 text-sm text-neutral-600">{t("tokens.bulkPreview", { count: bulkMatches.length })}</span>
        </div>
      </Card>
      <Card>
        <CardHeader title={t("tokens.list")} aside={<input className={cx(inputBase, "w-64")} value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t("tokens.search")} />} />
        <div className="flex flex-wrap gap-3 border-t border-neutral-100 px-5 py-3">
          <select aria-label={t("tokens.source")} className={inputBase} value={source} onChange={(e) => setSource(e.target.value)}><option value="all">{t("tokens.allSources")}</option>{(["manual", "device", "issued", "legacy"] as const).map((value) => <option key={value} value={value}>{t(`tokens.sources.${value}`)}</option>)}</select>
          <select aria-label={t("tokens.status")} className={inputBase} value={status} onChange={(e) => setStatus(e.target.value)}><option value="all">{t("tokens.allStates")}</option><option value="active">{t("tokens.states.active")}</option><option value="revoked">{t("tokens.states.revoked")}</option></select>
          <input aria-label={t("tokens.departmentFilter")} className={inputBase} value={departmentFilter} onChange={(e) => setDepartmentFilter(e.target.value)} placeholder={t("tokens.departmentFilter")} />
        </div>
        <LoadState loading={loading && !data} error={error} onRetry={reload}>
          <div className="overflow-x-auto"><table className="tbl max-sm:min-w-[44rem]">
            <thead><tr><th className="px-5 py-2">{t("tokens.name")}</th><th className="px-5 py-2">{t("tokens.owner")}</th><th className="px-5 py-2">{t("tokens.source")}</th><th className="px-5 py-2">{t("tokens.clientKind")}</th><th className="px-5 py-2">{t("tokens.scopesTitle")}</th><th className="px-5 py-2 text-right">{t("tokens.usage")}</th><th className="px-5 py-2 text-right">{t("tokens.lastUsed")}</th><th className="px-5 py-2">{t("tokens.status")}</th></tr></thead>
            <tbody>{list.map((token) => {
              const revoked = !!token.revoked_at;
              return <tr key={token.id} className="border-t border-neutral-100 align-top">
                <td className="px-5 py-3"><div className="text-neutral-900">{token.name}</div><div className="font-mono text-xs text-neutral-600">{token.prefix}…</div>{token.client_name && <div className="text-xs text-neutral-600">{token.client_name}</div>}</td>
                <td className="px-5 py-3 text-neutral-600"><div className="flex items-center gap-1.5">{token.username || "—"}{token.account_type === "bot" && <Badge>bot</Badge>}</div><div className="text-xs">{token.department}</div></td>
                <td className="px-5 py-3 text-neutral-600">{t(`tokens.sources.${token.source}`)}</td>
                <td className="px-5 py-3"><Badge tone={token.client_kind === "dsh" ? "brand" : "slate"}>{t(`tokens.clientKinds.${token.client_kind ?? "third_party"}`)}</Badge></td>
                <td className="max-w-64 px-5 py-3 text-xs text-neutral-600">{token.scopes.map((scope) => t(`tokens.scopes.${scope}`)).join(" · ")}</td>
                <td className="px-5 py-3 text-right tabular-nums">{formatTokens(token.month_used)}{token.monthly_token_limit !== null && ` / ${formatTokens(token.monthly_token_limit)}`}</td>
                <td className="px-5 py-3 text-right tabular-nums text-neutral-600">{formatTime(token.last_used_at)}</td>
                <td className="px-5 py-3"><Badge tone={revoked ? "slate" : "green"}>{t(revoked ? "tokens.states.revoked" : "tokens.states.active")}</Badge></td>
              </tr>;
            })}</tbody>
          </table></div>
          {list.length === 0 && <Empty compact>{t("tokens.empty")}</Empty>}
        </LoadState>
      </Card>
    </div>
  );
}
