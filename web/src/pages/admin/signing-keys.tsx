import { useState } from "react";
import { KeyRound, RefreshCw, ShieldCheck } from "lucide-react";
import { useTranslation } from "react-i18next";
import { api, formatTime, type SigningKey, type SigningKeysResp } from "../../lib/api";
import { useAction, useApi } from "../../lib/use-api";
import { useConfirm } from "../../lib/confirm";
import { useStore } from "../../lib/store";
import { ErrorNote, LoadState } from "../../components/api-ui";
import { Badge, Button, Card, CardHeader, cx, Empty, Field, inputCls, Notice, PageHeader } from "../../components/ui";

/** 与安装扩展页的 PEM 输入保持一致：等宽、可换行。 */
const textareaCls =
  "w-full rounded-md border border-neutral-300 bg-card px-3 py-2 font-mono text-xs outline-none placeholder:text-neutral-500 focus:border-brand-500 focus:ring-2 focus:ring-brand-100 dark:border-white/15 dark:bg-transparent";

/**
 * 扩展签名的信任根（docs/plugin-signing.md §7、docs/plugins.md §9.3）：
 * 管理员在这里看 / 加 / 换下 / 撤销受信任公钥。私钥不进 dsh-ee，页面只收公钥 PEM；
 * 环境变量 DSH_EE_EXTENSION_TRUSTED_KEYS 是只读兼容来源，只展示、不给操作。
 */
export default function AdminSigningKeys() {
  const { t } = useTranslation();
  const resp = useApi(api.signingKeys);
  return (
    <div data-testid="signing-keys-page">
      <PageHeader
        title={t("signingKeys.title")}
        desc={t("signingKeys.desc")}
        actions={
          <Button variant="secondary" size="sm" disabled={resp.loading} onClick={resp.reload}>
            <RefreshCw size={14} /> {t("signingKeys.reload")}
          </Button>
        }
      />
      <div className="space-y-6">
        <Policy resp={resp.data} />
        <AddKey onAdded={resp.reload} />
        <Card data-testid="signing-keys-list">
          <CardHeader title={t("signingKeys.listTitle")} aside={<StateCounts resp={resp.data} />} />
          <LoadState loading={resp.loading && !resp.data} error={resp.data ? null : resp.error} onRetry={resp.reload}>
            <div className="px-5 pb-5">
              <p className="mb-4 text-sm text-neutral-600">{t("signingKeys.listDesc")}</p>
              {resp.data && <KeysTable list={resp.data} onChanged={resp.reload} />}
            </div>
          </LoadState>
        </Card>
        <Notice tone="info">{t("signingKeys.security")}</Notice>
      </div>
    </div>
  );
}

/** 验签策略是服务端环境变量，页面只读展示；开了强制验签又没有公钥时要显眼提醒。 */
function Policy({ resp }: { resp: SigningKeysResp | null }) {
  const { t } = useTranslation();
  if (!resp) return null;
  const noKeys = resp.count === 0 && resp.env_keys.length === 0;
  return (
    <div className="space-y-2">
      {resp.require_signature ? (
        <Notice tone={noKeys ? "danger" : "warn"}>{t(noKeys ? "signingKeys.policyNoKeys" : "signingKeys.policyOn")}</Notice>
      ) : (
        <Notice>{t("signingKeys.policyOff")}</Notice>
      )}
      <p className="text-xs leading-5 text-neutral-500">{t("signingKeys.policyHint")}</p>
    </div>
  );
}

/** 列表标题旁的各状态条数，只显示非零的。 */
function StateCounts({ resp }: { resp: SigningKeysResp | null }) {
  const { t } = useTranslation();
  if (!resp || resp.count === 0) return null;
  const states = [
    { key: "active", label: t("signingKeys.stateActive"), tone: "green" },
    { key: "pending", label: t("signingKeys.statePendingShort"), tone: "amber" },
    { key: "retired", label: t("signingKeys.stateRetired"), tone: "slate" },
    { key: "revoked", label: t("signingKeys.stateRevoked"), tone: "red" },
  ] as const;
  return (
    <span className="flex flex-wrap items-center gap-2">
      {states
        .filter((s) => (resp.states[s.key] ?? 0) > 0)
        .map((s) => (
          <Badge key={s.key} tone={s.tone}>
            {s.label} {resp.states[s.key]}
          </Badge>
        ))}
    </span>
  );
}

/** 库里的信任根（含换下 / 撤销的历史）+ 只读的环境变量来源。数据来自 GET /api/admin/signing-keys。 */
function KeysTable({ list, onChanged }: { list: SigningKeysResp; onChanged: () => void }) {
  const { t } = useTranslation();
  const dbIDs = new Set(list.keys.map((k) => k.key_id));
  return (
    <>
      {list.keys.length === 0 ? (
        <Empty icon={KeyRound} title={t("signingKeys.empty")}>
          {t("signingKeys.emptyHint")}
        </Empty>
      ) : (
        <div className="overflow-x-auto">
          <table className="tbl max-sm:min-w-[46rem]" data-testid="signing-keys-table">
            <thead>
              <tr>
                <th className="px-3 py-2">{t("signingKeys.colState")}</th>
                <th className="px-3 py-2">{t("signingKeys.colKeyID")}</th>
                <th className="px-3 py-2">{t("signingKeys.colSource")}</th>
                <th className="px-3 py-2">{t("signingKeys.colPurpose")}</th>
                <th className="px-3 py-2">{t("signingKeys.colValidFrom")}</th>
                <th className="px-3 py-2">{t("signingKeys.colUpdated")}</th>
                <th className="px-3 py-2">{t("signingKeys.colNote")}</th>
                <th className="px-3 py-2 text-right">{t("signingKeys.colActions")}</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-neutral-100">
              {list.keys.map((k) => (
                <KeyRow key={k.key_id} k={k} onChanged={onChanged} />
              ))}
            </tbody>
          </table>
        </div>
      )}
      {list.env_keys.length > 0 && (
        <div className="mt-5 rounded-md border border-neutral-200 bg-neutral-50/60 p-4 dark:border-white/10 dark:bg-white/[0.03]" data-testid="signing-key-env-keys">
          <div className="flex flex-wrap items-center gap-2">
            <Badge>{t("signingKeys.sourceEnv")}</Badge>
            <span className="font-medium text-neutral-800">{t("signingKeys.envTitle")}</span>
          </div>
          <p className="mt-1 text-xs leading-5 text-neutral-500">{t("signingKeys.envDesc")}</p>
          <ul className="mt-3 space-y-1.5">
            {list.env_keys.map((k) => (
              <li key={k.key_id} className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
                <span className="font-mono text-xs text-neutral-800">{k.key_id}</span>
                <span className="text-xs text-neutral-500">{t("signingKeys.purposeExtension")}</span>
                {dbIDs.has(k.key_id) && <Badge tone="amber">{t("signingKeys.envOverride")}</Badge>}
              </li>
            ))}
          </ul>
        </div>
      )}
    </>
  );
}

/** 一条信任根：状态、key id、来源、时间；没撤销的可以换下 / 撤销（都保留历史记录）。 */
function KeyRow({ k, onChanged }: { k: SigningKey; onChanged: () => void }) {
  const { t } = useTranslation();
  const { notify } = useStore();
  const { busy, run } = useAction();
  const confirm = useConfirm();
  const act = async (kind: "retire" | "revoke") => {
    const description = t(kind === "retire" ? "signingKeys.retireConfirm" : "signingKeys.revokeConfirm");
    // 信任根的撤销用专门的确认文案（吊销 / 撤销在别的页面语义不同）
    if (!(await confirm({ kind: kind === "retire" ? "retire" : "revokeKey", description }))) return;
    const r = await run(() => (kind === "retire" ? api.retireSigningKey(k.key_id) : api.revokeSigningKey(k.key_id)));
    if (!r.ok) return notify(r.error);
    notify(t(kind === "retire" ? "signingKeys.retiredDone" : "signingKeys.revokedDone"));
    onChanged();
  };
  return (
    <tr data-testid={`signing-key-${k.key_id}`}>
      <td className="px-3 py-3 whitespace-nowrap">
        <StateBadge k={k} />
      </td>
      <td className="px-3 py-3 font-mono text-xs text-neutral-800">{k.key_id}</td>
      <td className="px-3 py-3 whitespace-nowrap text-neutral-500">{t("signingKeys.sourceDb")}</td>
      <td className="px-3 py-3 whitespace-nowrap text-neutral-500">{t("signingKeys.purposeExtension")}</td>
      <td className="px-3 py-3 whitespace-nowrap text-neutral-500">{formatTime(k.valid_from)}</td>
      <td className="px-3 py-3 whitespace-nowrap text-neutral-500">
        {k.revoked_at ? (
          <div>
            {t("signingKeys.revoke")} {formatTime(k.revoked_at)}
          </div>
        ) : k.retired_at ? (
          <div>
            {t("signingKeys.retire")} {formatTime(k.retired_at)}
          </div>
        ) : (
          "—"
        )}
      </td>
      <td className="max-w-56 truncate px-3 py-3 text-neutral-600" title={k.note || undefined}>
        {k.note || "—"}
      </td>
      <td className="space-x-2 px-3 py-3 text-right whitespace-nowrap">
        {!k.retired_at && !k.revoked_at && (
          <Button variant="secondary" size="sm" disabled={busy} onClick={() => act("retire")}>
            {t("signingKeys.retire")}
          </Button>
        )}
        {!k.revoked_at && (
          <Button variant="danger" size="sm" disabled={busy} onClick={() => act("revoke")}>
            {t("signingKeys.revoke")}
          </Button>
        )}
        {k.revoked_at && <span className="text-xs text-neutral-500">—</span>}
      </td>
    </tr>
  );
}

function StateBadge({ k }: { k: SigningKey }) {
  const { t } = useTranslation();
  switch (k.state) {
    case "revoked":
      return <Badge tone="red">{t("signingKeys.stateRevoked")}</Badge>;
    case "retired":
      return <Badge>{t("signingKeys.stateRetired")}</Badge>;
    case "pending":
      return <Badge tone="amber">{t("signingKeys.statePending", { time: formatTime(k.valid_from) })}</Badge>;
    default:
      return <Badge tone="green">{t("signingKeys.stateActive")}</Badge>;
  }
}

/** 粘贴 PEM 添加信任根；客户端先挡明显的私钥/空内容，最终仍以服务端校验为准。 */
function AddKey({ onAdded }: { onAdded: () => void }) {
  const { t } = useTranslation();
  const { notify } = useStore();
  const { busy, run } = useAction();
  const [pem, setPem] = useState("");
  const [note, setNote] = useState("");
  const [error, setError] = useState<string | null>(null);
  const submit = async () => {
    const text = pem.trim();
    if (!text) return setError(t("signingKeys.pemRequired"));
    if (text.includes("PRIVATE KEY-----")) return setError(t("signingKeys.pemPrivate"));
    setError(null);
    const r = await run(() => api.addSigningKey({ public_key: text, note: note.trim(), purpose: "extension" }));
    if (!r.ok) return setError(r.error);
    notify(t("signingKeys.addDone"));
    setPem("");
    setNote("");
    onAdded();
  };
  return (
    <Card data-testid="signing-key-add-form">
      <CardHeader title={t("signingKeys.addTitle")} />
      <div className="space-y-4 px-5 pb-5">
        <p className="text-sm text-neutral-600">{t("signingKeys.addDesc")}</p>
        <Field label={t("signingKeys.pem")} hint={t("signingKeys.pemHint")}>
          <textarea
            data-testid="signing-key-pem"
            className={cx(textareaCls, "h-36")}
            value={pem}
            spellCheck={false}
            placeholder={"-----BEGIN PUBLIC KEY-----\n…\n-----END PUBLIC KEY-----"}
            onChange={(e) => {
              setPem(e.target.value);
              setError(null);
            }}
          />
        </Field>
        <Field label={t("signingKeys.note")} hint={t("signingKeys.noteHint")}>
          <input
            data-testid="signing-key-note"
            className={cx(inputCls, "max-w-md")}
            value={note}
            maxLength={255}
            placeholder={t("signingKeys.notePlaceholder")}
            onChange={(e) => setNote(e.target.value)}
          />
        </Field>
        {error && <ErrorNote>{error}</ErrorNote>}
        <div className="flex justify-end">
          <Button data-testid="signing-key-add" disabled={busy || !pem.trim()} onClick={submit}>
            <ShieldCheck size={15} /> {busy ? t("signingKeys.adding") : t("signingKeys.add")}
          </Button>
        </div>
      </div>
    </Card>
  );
}
