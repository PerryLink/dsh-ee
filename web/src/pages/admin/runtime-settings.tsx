import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Button, Field, inputCls } from "../../components/ui";
import { ApiError, formatTime, request } from "../../lib/api";

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
  /** 共享给的部门（员工资料里的部门名）；空串表示不共享。只能走单独的共享接口修改。旧后端没有这个字段。 */
  shared_department?: string;
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

const botsPath = "/api/admin/runtime/bots";
const tasksPath = "/api/runtime/tasks";
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
  const [shareDraft, setShareDraft] = useState("");
  const editBot = (bot: Bot) => {
    setDraft(bot);
    setShareDraft(bot.shared_department ?? "");
  };

  const load = async () => {
    const [nextBots, nextRuns] = await Promise.all([
      request<Bot[]>("GET", botsPath),
      request<Run[]>("GET", tasksPath),
    ]);
    setBots(nextBots);
    setRuns(nextRuns);
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
            <strong className="mr-auto break-all text-sm">{bot.name} <span className="font-normal text-neutral-500">· {bot.schedule}{bot.web_chat_enabled ? ` · ${t("runtime.webChatBadge")}` : ""}{bot.shared_department ? ` · ${t("runtime.sharedBadge", { department: bot.shared_department })}` : ""}</span></strong>
            <Button variant="secondary" size="sm" disabled={busy} onClick={() => editBot(bot)}>{t("runtime.edit")}</Button>
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
        {draft.id > 0 && (
          <div>
            <Field label={t("runtime.share")}>
              <div className="flex flex-wrap gap-2">
                <input
                  className={`${inputCls} min-w-0 flex-1`}
                  maxLength={128}
                  value={shareDraft}
                  placeholder={t("runtime.sharePlaceholder")}
                  aria-describedby="runtime-share-hint"
                  onChange={(e) => setShareDraft(e.target.value)}
                />
                <Button type="button" variant="secondary" disabled={busy} onClick={() => void perform(async () => {
                  const saved = await request<Bot>("PUT", `${botsPath}/${draft.id}/share`, { department: shareDraft.trim() });
                  setDraft({ ...draft, shared_department: saved.shared_department });
                  setShareDraft(saved.shared_department ?? "");
                  await load();
                })}>{t("runtime.shareSave")}</Button>
              </div>
            </Field>
            <p id="runtime-share-hint" className="mt-1 text-xs leading-5 text-neutral-500">{t("runtime.shareHint")}</p>
          </div>
        )}
        <div className="flex gap-2">
          <Button type="submit" disabled={busy}>{t("runtime.save")}</Button>
          {draft.id > 0 && <Button type="button" variant="secondary" onClick={() => editBot(blank)}>{t("runtime.new")}</Button>}
        </div>
      </form>
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
