import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Button, Field, inputCls } from "../../components/ui";
import { ApiError, formatTime, request } from "../../lib/api";
import { useConfirm } from "../../lib/confirm";

type Bot = {
  id: number;
  owner_id: number;
  run_as_id: number;
  name: string;
  prompt: string;
  schedule: string;
  enabled: boolean;
  /** 是否开放网页对话（只对运行身份那位员工）；与定时运行互不影响。旧后端没有这个字段。 */
  web_chat_enabled?: boolean;
};

type Run = {
  id: number;
  bot_id?: number;
  status: "queued" | "running" | "succeeded" | "failed" | "canceled";
  summary: string;
  error: string;
  created_at: string;
};

type Artifact = { id: number; name: string };

/** 常驻云电脑（GET /api/admin/runtime/computers）：员工电脑（owner_kind=user）或团队电脑（team）。 */
type Computer = {
  id: number;
  owner_kind?: "user" | "team";
  owner_id?: number;
  owner_name?: string;
  department?: string;
  /** 正在用（或最近一次用）这台电脑的机器人。 */
  bot_id?: number;
  bot_name?: string;
  state: "none" | "waking" | "running" | "hibernating" | "hibernated";
  busy: boolean;
  disk_bytes: number;
  disk_limit_bytes: number;
  last_used_at?: string | null;
  hibernated_at?: string | null;
};

type Computers = {
  persistent: boolean;
  idle_timeout_seconds: number;
  disk_limit_bytes: number;
  retention_seconds: number;
  computers: Computer[] | null;
};

const botsPath = "/api/admin/runtime/bots";
const tasksPath = "/api/runtime/tasks";
const computersPath = "/api/admin/runtime/computers";
const mb = (n: number) => `${String(Math.round(n / 1048576))} MB`;
const blank: Bot = { id: 0, owner_id: 0, run_as_id: 0, name: "", prompt: "", schedule: "0 9 * * *", enabled: true, web_chat_enabled: false };

export default function RuntimeSettings() {
  const { t } = useTranslation();
  const [bots, setBots] = useState<Bot[]>([]);
  const [runs, setRuns] = useState<Run[]>([]);
  const [draft, setDraft] = useState<Bot>(blank);
  const [files, setFiles] = useState<Record<number, Artifact[]>>({});
  const [error, setError] = useState("");
  const [disabled, setDisabled] = useState(false);
  const [busy, setBusy] = useState(true);
  const [computers, setComputers] = useState<Computers | null>(null);
  const confirm = useConfirm();

  const load = async () => {
    const [nextBots, nextRuns, nextComputers] = await Promise.all([
      request<Bot[]>("GET", botsPath),
      request<Run[]>("GET", tasksPath),
      // 旧后端没有这个接口：不影响其余部分
      request<Computers>("GET", computersPath).catch(() => null),
    ]);
    setBots(nextBots);
    setRuns(nextRuns);
    setComputers(nextComputers);
    setDisabled(false);
  };

  const perform = async (action: () => Promise<void>) => {
    setBusy(true);
    setError("");
    try {
      await action();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const refresh = () => perform(async () => {
    try {
      await load();
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) setDisabled(true);
      else throw err;
    }
  });

  useEffect(() => { void refresh(); }, []);

  const save = (event: FormEvent) => {
    event.preventDefault();
    void perform(async () => {
      await request<Bot>(draft.id ? "PUT" : "POST", draft.id ? `${botsPath}/${draft.id}` : botsPath, {
        ...draft, web_chat_enabled: Boolean(draft.web_chat_enabled), read_only: true, delivery_target: "feishu_owner",
      });
      setDraft(blank);
      await load();
    });
  };

  if (disabled) return <p className="text-sm text-neutral-500">{t("runtime.unavailable")}</p>;
  return (
    <>
      {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
      <div className="space-y-2">
        {bots.map((bot) => (
          <div key={bot.id} className="flex flex-wrap items-center gap-2 rounded-lg border border-neutral-200 p-2">
            <strong className="mr-auto break-all text-sm">{bot.name} <span className="font-normal text-neutral-500">· {bot.schedule}{bot.web_chat_enabled ? ` · ${t("runtime.webChatBadge")}` : ""}</span></strong>
            <Button variant="secondary" size="sm" disabled={busy} onClick={() => setDraft(bot)}>{t("runtime.edit")}</Button>
            <Button variant="secondary" size="sm" disabled={busy} onClick={() => void perform(async () => {
              await request("POST", `${botsPath}/${bot.id}/run`);
              await load();
            })}>{t("runtime.run")}</Button>
          </div>
        ))}
      </div>
      <form className="space-y-3" onSubmit={save}>
        <div className="grid gap-3 sm:grid-cols-3">
          <Field label={t("runtime.name")}>
            <input className={inputCls} required maxLength={128} value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} />
          </Field>
          <Field label={t("runtime.employee")}>
            <input className={inputCls} required type="number" min="1" disabled={Boolean(draft.id)} value={draft.run_as_id || ""} onChange={(e) => setDraft({ ...draft, run_as_id: +e.target.value, owner_id: +e.target.value })} />
          </Field>
          <Field label={t("runtime.schedule")}>
            <input className={inputCls} required value={draft.schedule} onChange={(e) => setDraft({ ...draft, schedule: e.target.value })} />
          </Field>
        </div>
        <Field label={t("runtime.prompt")}>
          <textarea className={inputCls} required rows={4} maxLength={8000} value={draft.prompt} onChange={(e) => setDraft({ ...draft, prompt: e.target.value })} />
        </Field>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={draft.enabled} onChange={(e) => setDraft({ ...draft, enabled: e.target.checked })} />
          {t("runtime.enabled")}
        </label>
        <div>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={Boolean(draft.web_chat_enabled)}
              aria-describedby="runtime-web-chat-hint"
              onChange={(e) => setDraft({ ...draft, web_chat_enabled: e.target.checked })}
            />
            {t("runtime.webChat")}
          </label>
          <p id="runtime-web-chat-hint" className="mt-1 pl-6 text-xs leading-5 text-neutral-500">{t("runtime.webChatHint")}</p>
        </div>
        <div className="flex gap-2">
          <Button type="submit" disabled={busy}>{t("runtime.save")}</Button>
          {draft.id > 0 && <Button type="button" variant="secondary" onClick={() => setDraft(blank)}>{t("runtime.new")}</Button>}
        </div>
      </form>
      {computers && (
        <div className="space-y-2" data-runtime-computers="">
          <h3 className="text-sm font-semibold">{t("runtime.computers.title")}</h3>
          <p className="text-xs leading-5 text-neutral-500">
            {computers.persistent
              ? t("runtime.computers.policy", {
                idle: Math.round(computers.idle_timeout_seconds / 60),
                disk: mb(computers.disk_limit_bytes),
                retention: computers.retention_seconds ? t("runtime.computers.retentionDays", { days: Math.round(computers.retention_seconds / 86400) }) : t("runtime.computers.retentionOff"),
              })
              : t("runtime.computers.ephemeral")}
          </p>
          <p className="text-xs leading-5 text-neutral-500">{t("runtime.computers.hint")}</p>
          {!computers.computers?.length && <p className="text-sm text-neutral-500">{t("runtime.computers.empty")}</p>}
          {computers.computers?.map((c) => {
            const owner = c.owner_kind === "team"
              ? t("runtime.computers.teamOwner", { department: c.department ?? "" })
              : t("runtime.computers.userOwner", { name: c.owner_name || `#${String(c.owner_id ?? "")}` });
            return (
              <div key={c.id} className="flex flex-wrap items-center gap-2 rounded-lg border border-neutral-200 p-2 text-sm" data-computer-owner={c.owner_kind ?? "user"}>
                <span className="mr-auto break-all">
                  <strong>{owner}</strong>
                  <span className="text-neutral-500"> · {t(`runtime.computers.state.${c.state}`)}{c.busy ? ` · ${t("runtime.computers.busy")}` : ""} · {mb(c.disk_bytes)} / {mb(c.disk_limit_bytes)}{c.last_used_at ? ` · ${formatTime(c.last_used_at)}` : ""}{c.bot_name ? ` · ${t("runtime.computers.lastBot", { bot: c.bot_name })}` : ""}</span>
                </span>
                <Button variant="secondary" size="sm" disabled={busy || c.busy || c.state !== "running"} onClick={() => void perform(async () => {
                  await request("POST", `${computersPath}/${String(c.id)}/hibernate`);
                  await load();
                })}>{t("runtime.computers.hibernate")}</Button>
                <Button variant="secondary" size="sm" disabled={busy || c.busy} onClick={() => void perform(async () => {
                  if (!(await confirm({ kind: "delete", description: t("runtime.computers.reclaimConfirm", { owner }) }))) return;
                  await request("DELETE", `${computersPath}/${String(c.id)}`);
                  await load();
                })}>{t("runtime.computers.reclaim")}</Button>
              </div>
            );
          })}
        </div>
      )}
      <div className="space-y-2">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold">{t("runtime.runs")}</h3>
          <Button variant="secondary" size="sm" disabled={busy} onClick={() => void refresh()}>{t("runtime.refresh")}</Button>
        </div>
        {!runs.length && <p className="text-sm text-neutral-500">{t("runtime.empty")}</p>}
        {runs.map((run) => (
          <article key={run.id} className="space-y-1 rounded-lg border border-neutral-200 p-2 text-sm">
            <p>#{run.id} · {bots.find((bot) => bot.id === run.bot_id)?.name ?? ""} · {t(`runtime.${run.status}`)} · {formatTime(run.created_at)}</p>
            {run.summary && <p className="whitespace-pre-wrap break-words">{run.summary}</p>}
            {run.error && <p className="break-words text-neutral-500">{run.error}</p>}
            {run.status === "succeeded" && !files[run.id] && <Button size="sm" variant="secondary" disabled={busy} onClick={() => void perform(async () => {
              const next = await request<Artifact[]>("GET", `${tasksPath}/${run.id}/artifacts`);
              setFiles((current) => ({ ...current, [run.id]: next }));
            })}>{t("runtime.files")}</Button>}
            {files[run.id]?.length === 0 && <p className="text-neutral-500">{t("runtime.noFiles")}</p>}
            {files[run.id]?.map((file) => (
              <a key={file.id} className="block break-all text-brand-600 underline" href={`${tasksPath}/${run.id}/artifacts/${file.id}`}>{file.name}</a>
            ))}
          </article>
        ))}
      </div>
    </>
  );
}
