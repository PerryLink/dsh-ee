import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Badge, Button, Field, inputCls } from "../../components/ui";
import { FullAccessDialog } from "../../components/full-access-dialog";
import { ApiError, formatTime, request } from "../../lib/api";
import { APPROVAL_MODES, type ApprovalMode } from "../../lib/bot-chat";
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
  /** 共享给的部门（员工资料里的部门名）；空串表示不共享。只能走单独的共享接口修改。旧后端没有这个字段。 */
  shared_department?: string;
  /** 写操作审批模式；旧后端没有这个字段，按只读处理。 */
  approval_mode?: ApprovalMode;
  /** 自动审批规则（工具全名的通配模式），只在 auto 下生效。 */
  approval_rules?: string[];
  /** 是否允许员工在对话页自己切换审批模式。 */
  approval_employee_editable?: boolean;
};

type Run = {
  id: number;
  bot_id?: number;
  status: "queued" | "running" | "succeeded" | "failed" | "canceled";
  summary: string;
  error: string;
  created_at: string;
  private_result?: boolean;
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
  next_before?: string;
};

const botsPath = "/api/admin/runtime/bots";
const tasksPath = "/api/runtime/tasks";
const computersPath = "/api/admin/runtime/computers";
const computerPagePath = (before?: string) => before ? `${computersPath}?before=${encodeURIComponent(before)}` : computersPath;
const mb = (n: number) => `${String(Math.round(n / 1048576))} MB`;
const blank: Bot = {
  id: 0, owner_id: 0, run_as_id: 0, name: "", prompt: "", schedule: "0 9 * * *", enabled: true, web_chat_enabled: false,
  approval_mode: "read_only", approval_rules: [], approval_employee_editable: false,
};
const modeOf = (bot: Bot): ApprovalMode => bot.approval_mode ?? "read_only";

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
  /** 自动审批规则的编辑框文字（每行一条）；保存时拆成数组。 */
  const [rulesText, setRulesText] = useState("");
  /** 正在等风险确认的保存：确认后带 confirm_full_access 继续。 */
  const [fullPending, setFullPending] = useState(false);
  const [computers, setComputers] = useState<Computers | null>(null);
  const [computerPages, setComputerPages] = useState([""]);
  const confirm = useConfirm();

  const load = async () => {
    const [nextBots, nextRuns, nextComputers] = await Promise.all([
      request<Bot[]>("GET", botsPath),
      request<Run[]>("GET", tasksPath),
      // 旧后端没有这个接口：不影响其余部分
      request<Computers>("GET", computerPagePath(computerPages.at(-1))).catch(() => null),
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

  const navigateComputers = (pages: string[]) => perform(async () => {
    setComputers(await request<Computers>("GET", computerPagePath(pages.at(-1))));
    setComputerPages(pages);
  });

  useEffect(() => { void refresh(); }, []);

  const edit = (bot: Bot) => {
    setDraft(bot);
    setShareDraft(bot.shared_department ?? "");
    setRulesText((bot.approval_rules ?? []).join("\n"));
  };
  const reset = () => {
    setDraft(blank);
    setShareDraft("");
    setRulesText("");
  };

  const submit = (confirmFullAccess: boolean) => {
    void perform(async () => {
      const mode = modeOf(draft);
      await request<Bot>(draft.id ? "PUT" : "POST", draft.id ? `${botsPath}/${draft.id}` : botsPath, {
        ...draft,
        web_chat_enabled: Boolean(draft.web_chat_enabled),
        approval_mode: mode,
        approval_rules: rulesText.split("\n").map((r) => r.trim()).filter(Boolean),
        approval_employee_editable: Boolean(draft.approval_employee_editable),
        confirm_full_access: confirmFullAccess,
        read_only: mode === "read_only",
        delivery_target: "feishu_owner",
      });
      reset();
      await load();
    });
  };

  const save = (event: FormEvent) => {
    event.preventDefault();
    // 从别的模式切到完全放行：先弹风险确认
    const previous = bots.find((bot) => bot.id === draft.id);
    if (modeOf(draft) === "full" && (!previous || modeOf(previous) !== "full")) {
      setFullPending(true);
      return;
    }
    submit(false);
  };

  if (disabled) return <p className="text-sm text-neutral-500">{t("runtime.unavailable")}</p>;
  return (
    <>
      {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
      <div className="space-y-2">
        {bots.map((bot) => (
          <div key={bot.id} className="flex flex-wrap items-center gap-2 rounded-lg border border-neutral-200 p-2">
            <strong className="mr-auto break-all text-sm">{bot.name} <span className="font-normal text-neutral-500">· {bot.schedule}{bot.web_chat_enabled ? ` · ${t("runtime.webChatBadge")}` : ""}{bot.shared_department ? ` · ${t("runtime.sharedBadge", { department: bot.shared_department })}` : ""}</span></strong>
            {modeOf(bot) === "full" ? (
              <span title={t("approval.fullBadgeTitle")}><Badge tone="red">{t("approval.fullBadge")}</Badge></span>
            ) : (
              <Badge tone={modeOf(bot) === "read_only" ? "slate" : "brand"}>{t(`approval.modes.${modeOf(bot)}`)}</Badge>
            )}
            <Button variant="secondary" size="sm" disabled={busy} onClick={() => edit(bot)}>{t("runtime.edit")}</Button>
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
        <fieldset className="space-y-2 rounded-lg border border-neutral-200 p-3 dark:border-white/10">
          <legend className="px-1 text-sm font-medium">{t("approval.admin.mode")}</legend>
          <select
            className={inputCls}
            aria-describedby="runtime-approval-hint"
            value={modeOf(draft)}
            onChange={(e) => setDraft({ ...draft, approval_mode: e.target.value as ApprovalMode })}
          >
            {APPROVAL_MODES.map((mode) => <option key={mode} value={mode}>{t(`approval.modes.${mode}`)}</option>)}
          </select>
          <p id="runtime-approval-hint" className={modeOf(draft) === "full" ? "text-xs leading-5 text-red-600 dark:text-red-400" : "text-xs leading-5 text-neutral-500"}>
            {t(`approval.modeHints.${modeOf(draft)}`)}
          </p>
          {modeOf(draft) === "auto" && (
            <Field label={t("approval.admin.rules")}>
              <textarea
                className={inputCls}
                rows={3}
                aria-describedby="runtime-approval-rules-hint"
                placeholder={"wiki__create_page\n*__comment"}
                value={rulesText}
                onChange={(e) => setRulesText(e.target.value)}
              />
              <p id="runtime-approval-rules-hint" className="mt-1 text-xs leading-5 text-neutral-500">{t("approval.admin.rulesHint")}</p>
            </Field>
          )}
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={Boolean(draft.approval_employee_editable)}
              aria-describedby="runtime-approval-editable-hint"
              onChange={(e) => setDraft({ ...draft, approval_employee_editable: e.target.checked })}
            />
            {t("approval.admin.employeeEditable")}
          </label>
          <p id="runtime-approval-editable-hint" className="pl-6 text-xs leading-5 text-neutral-500">{t("approval.admin.employeeEditableHint")}</p>
        </fieldset>
        <div className="flex gap-2">
          <Button type="submit" disabled={busy}>{t("runtime.save")}</Button>
          {draft.id > 0 && <Button type="button" variant="secondary" onClick={reset}>{t("runtime.new")}</Button>}
        </div>
      </form>
      <FullAccessDialog
        open={fullPending}
        botName={draft.name}
        onResult={(confirmed) => {
          setFullPending(false);
          if (confirmed) submit(true);
        }}
      />
      {computers && (
        <div className="space-y-2" data-runtime-computers="">
          <h3 className="text-sm font-semibold">{t("runtime.computers.title")}</h3>
          <p className="text-xs leading-5 text-neutral-500">
            {computers.persistent
              ? t("runtime.computers.policy", {
                idle: Math.round(computers.idle_timeout_seconds / 60),
                disk: mb(computers.disk_limit_bytes),
                retention: !computers.retention_seconds
                  ? t("runtime.computers.retentionOff")
                  : computers.retention_seconds % 86400 === 0
                    ? t("runtime.computers.retentionDays", { days: computers.retention_seconds / 86400 })
                    : t("runtime.computers.retentionHours", { hours: Math.round(computers.retention_seconds / 36) / 100 }),
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
          {(computerPages.length > 1 || computers.next_before) && (
            <div className="flex gap-2">
              <Button variant="secondary" size="sm" disabled={busy || computerPages.length <= 1}
                onClick={() => void navigateComputers(computerPages.slice(0, -1))}>{t("table.prev")}</Button>
              <Button variant="secondary" size="sm" disabled={busy || !computers.next_before} onClick={() => {
                if (computers.next_before) void navigateComputers([...computerPages, computers.next_before]);
              }}>{t("table.next")}</Button>
            </div>
          )}
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
            {run.private_result && <p className="text-neutral-500">{t("runtime.privateResult")}</p>}
            {run.status === "succeeded" && !run.private_result && !files[run.id] && <Button size="sm" variant="secondary" disabled={busy} onClick={() => void perform(async () => {
              const next = await request<Artifact[]>("GET", `${tasksPath}/${run.id}/artifacts`);
              setFiles((current) => ({ ...current, [run.id]: next }));
            })}>{t("runtime.files")}</Button>}
            {!run.private_result && files[run.id]?.length === 0 && <p className="text-neutral-500">{t("runtime.noFiles")}</p>}
            {!run.private_result && files[run.id]?.map((file) => (
              <a key={file.id} className="block break-all text-brand-600 underline" href={`${tasksPath}/${run.id}/artifacts/${file.id}`}>{file.name}</a>
            ))}
          </article>
        ))}
      </div>
    </>
  );
}
