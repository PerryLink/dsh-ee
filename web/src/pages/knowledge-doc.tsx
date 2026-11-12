import { useEffect, useRef, useState } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import { ArrowLeft, Download, MoreHorizontal } from "lucide-react";
import { api, formatTime, type KDocConvert, type KDocDetail } from "../lib/api";
import { ObjectFields } from "../components/object-fields";
import { useAction, useApi } from "../lib/use-api";
import { useStore } from "../lib/store";
import { useConfirm } from "../lib/confirm";
import { LoadState, ErrorNote } from "../components/api-ui";
import { Badge, Button, Card, CardHeader, cx, Field, inputCls, Segmented, Notice, backLinkCls } from "../components/ui";
import { KBReader } from "../components/kb-reader";
import { ConvertNotes, convertLines } from "../components/kb-convert";
import { KB_ACCEPT } from "../components/kb-upload";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/shadcn/dropdown-menu";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/shadcn/dialog";
import { kbPath } from "./knowledge";

/** 地址里的 #L5-8：高亮这几行（从检索结果点进来时带上）。 */
function hashRange(hash: string): { from: number; to: number } | null {
  const m = /^#L(\d+)(?:-(\d+))?$/.exec(hash);
  if (!m) return null;
  const from = Number(m[1]);
  return { from, to: m[2] ? Math.max(from, Number(m[2])) : from };
}

function Lines({ markdown, highlight }: { markdown: string; highlight: { from: number; to: number } | null }) {
  const lines = markdown.replace(/\n$/, "").split("\n");
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!highlight) return;
    ref.current?.querySelector(`[data-line="${highlight.from}"]`)?.scrollIntoView({ block: "center" });
  }, [highlight?.from, highlight?.to, markdown]);
  return (
    <div ref={ref} className="overflow-x-auto font-mono text-[13px] leading-6">
      {lines.map((line, i) => {
        const n = i + 1;
        const hit = !!highlight && n >= highlight.from && n <= highlight.to;
        const heading = /^#{1,6}\s/.test(line);
        return (
          <div key={n} data-line={n} id={`L${n}`} className={cx("grid grid-cols-[3.5rem_1fr] gap-3", hit && "bg-amber-100 dark:bg-amber-500/20")}>
            <a href={`#L${n}`} className="pr-1 text-right text-neutral-400 select-none hover:text-neutral-700">
              {n}
            </a>
            <span className={cx("break-words whitespace-pre-wrap", heading ? "font-semibold text-neutral-900" : "text-neutral-800")}>{line || " "}</span>
          </div>
        );
      })}
    </div>
  );
}

function RenameDialog({ d, open, onOpenChange, onSaved }: { d: KDocDetail; open: boolean; onOpenChange: (v: boolean) => void; onSaved: () => void }) {
  const { t } = useTranslation();
  const [title, setTitle] = useState(d.doc.title);
  const [folder, setFolder] = useState(d.doc.folder);
  const [error, setError] = useState<string | null>(null);
  const { busy, run } = useAction();
  useEffect(() => {
    if (open) {
      setTitle(d.doc.title);
      setFolder(d.doc.folder);
      setError(null);
    }
  }, [open, d]);
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("kdoc.renameTitle")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <Field label={t("kbase.docTitle")}>
            <input className={inputCls} value={title} onChange={(e) => setTitle(e.target.value)} />
          </Field>
          <Field label={t("kbase.uploadFolder")} hint={t("kbase.uploadFolderHint")}>
            <input className={inputCls} value={folder} onChange={(e) => setFolder(e.target.value)} />
          </Field>
        </div>
        <ErrorNote>{error}</ErrorNote>
        <DialogFooter>
          <Button variant="secondary" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button
            disabled={busy || !title.trim()}
            onClick={async () => {
              const r = await run(() => api.patchKBDoc(d.doc.id, { title: title.trim(), folder }));
              if (r.ok) {
                onOpenChange(false);
                onSaved();
              } else setError(r.error);
            }}
          >
            {t("kbase.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ReplaceDialog({ d, open, onOpenChange, onSaved }: { d: KDocDetail; open: boolean; onOpenChange: (v: boolean) => void; onSaved: (version: number) => void }) {
  const { t } = useTranslation();
  const [file, setFile] = useState<File | null>(null);
  const [error, setError] = useState<string | null>(null);
  const { busy, run } = useAction();
  useEffect(() => {
    if (open) {
      setFile(null);
      setError(null);
    }
  }, [open]);
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("kdoc.replace")}</DialogTitle>
          <DialogDescription>{t("kdoc.replaceHint")}</DialogDescription>
        </DialogHeader>
        <input type="file" accept={KB_ACCEPT} className="block w-full text-sm" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
        <ErrorNote>{error}</ErrorNote>
        <DialogFooter>
          <Button variant="secondary" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button
            disabled={busy || !file}
            onClick={async () => {
              const r = await run(() => api.replaceKBDoc(d.doc.id, file!));
              if (r.ok) {
                onOpenChange(false);
                onSaved(r.value.version);
              } else setError(r.error);
            }}
          >
            {t("kbase.uploadSubmit")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** 转换情况：收录了什么、哪几页没收录；Excel / CSV 可以改表头设置后重新转换。 */
function ConvertCard({ d, canEdit, onDone }: { d: KDocDetail; canEdit: boolean; onDone: (version: number) => void }) {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const { notify } = useStore();
  const { busy, run } = useAction();
  const info: KDocConvert | undefined = d.doc.convert_info;
  const [header, setHeader] = useState<"" | "first" | "none">(info?.header_option ?? "");
  useEffect(() => setHeader(info?.header_option ?? ""), [info?.header_option]);
  const sheet = info?.format === "xlsx" || info?.format === "csv";
  // 没什么可说的（例如 Markdown）就不显示
  if (!info || (convertLines(t, info).length === 0 && !(sheet && canEdit))) return null;
  return (
    <Card>
      <CardHeader title={t("kdoc.convertTitle")} />
      <div className="space-y-3 px-5 pb-4">
        <ConvertNotes info={info} />
        {sheet && canEdit && d.doc.has_original && (
          <div className="space-y-2 border-t border-neutral-100 pt-3">
            <div className="text-xs font-medium text-neutral-600">{t("kdoc.headerSetting")}</div>
            <div role="radiogroup" aria-label={t("kdoc.headerSetting")} className="space-y-1">
              {([
                ["", t("kdoc.headerAuto")],
                ["first", t("kdoc.headerFirst")],
                ["none", t("kdoc.headerNone")],
              ] as const).map(([v, label]) => (
                <label key={v} className="flex cursor-pointer items-center gap-2 text-sm text-neutral-700">
                  <input type="radio" name="kb-header" className="accent-brand-600" checked={header === v} onChange={() => setHeader(v)} />
                  {label}
                </label>
              ))}
            </div>
            {header !== (info.header_option ?? "") && (
              <Button
                size="sm"
                disabled={busy}
                onClick={async () => {
                  if (info.edited && !(await confirm({ kind: "restore", description: t("kdoc.reconvertEdited") }))) return;
                  const r = await run(() => api.reconvertKBDoc(d.doc.id, header));
                  if (!r.ok) return notify(r.error);
                  notify(r.value.version !== d.doc.version ? t("kdoc.reconverted", { version: r.value.version }) : t("kdoc.reconvertSame"));
                  onDone(r.value.version);
                }}
              >
                {t("kdoc.reconvert")}
              </Button>
            )}
          </div>
        )}
      </div>
    </Card>
  );
}

/** 改了多少行：按行比较（同样的行配对掉，剩下的算增删），够用来提醒，不是精确的 diff。 */
function lineChanges(before: string, after: string): { added: number; removed: number } {
  const lines = (s: string) => s.replace(/\r\n?/g, "\n").replace(/\n+$/, "").split("\n");
  const count = new Map<string, number>();
  for (const l of lines(before)) count.set(l, (count.get(l) ?? 0) + 1);
  let added = 0;
  for (const l of lines(after)) {
    const n = count.get(l) ?? 0;
    if (n > 0) count.set(l, n - 1);
    else added++;
  }
  let removed = 0;
  for (const n of count.values()) removed += n;
  return { added, removed };
}

/** 保存前的提醒：改了多少行；这篇最近被 AI 读过的话，引用旧版本的答案会显示「原文已更新」。 */
function EditImpact({ before, after, reads }: { before: string; after: string; reads?: number }) {
  const { t } = useTranslation();
  const { added, removed } = lineChanges(before, after);
  if (added === 0 && removed === 0) return <p className="text-sm text-neutral-500">{t("kdoc.noChanges")}</p>;
  return (
    <Notice tone="warn">
      <span className="font-medium">{t("kdoc.changes", { added, removed })}</span>
      {reads ? ` · ${t("kdoc.impact", { count: reads })}` : ` · ${t("kdoc.impactNone")}`}
    </Notice>
  );
}

/** 大纲：滚动时高亮当前所在的标题；点标题滚过去（不改地址里的高亮）。 */
function Outline({ outline, onJump }: { outline: { level: number; title: string; line: number }[]; onJump: (line: number) => void }) {
  const { t } = useTranslation();
  const [active, setActive] = useState<number | null>(null);
  useEffect(() => {
    let frame = 0;
    const update = () => {
      frame = 0;
      let cur: number | null = null;
      for (const h of outline) {
        const el = document.querySelector(`[data-line="${h.line}"]`);
        if (!el) continue;
        if (el.getBoundingClientRect().top <= 140) cur = h.line;
        else break;
      }
      setActive(cur);
    };
    const onScroll = () => {
      if (!frame) frame = requestAnimationFrame(update);
    };
    update();
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => {
      window.removeEventListener("scroll", onScroll);
      if (frame) cancelAnimationFrame(frame);
    };
  }, [outline]);
  return (
    <ul className="max-h-[60vh] space-y-0.5 overflow-auto px-3 pb-4 text-sm">
      {outline.map((h) => (
        <li key={h.line} style={{ paddingLeft: (h.level - 1) * 12 }}>
          <button
            onClick={() => onJump(h.line)}
            aria-current={active === h.line ? "location" : undefined}
            title={active === h.line ? t("kdoc.outlineHere") : undefined}
            className={cx(
              "w-full truncate rounded px-2 py-0.5 text-left transition",
              active === h.line ? "bg-brand-50 font-medium text-brand-700 dark:bg-brand-500/15 dark:text-brand-200" : "text-neutral-700 hover:bg-neutral-100 hover:text-neutral-900",
            )}
          >
            {h.title}
          </button>
        </li>
      ))}
    </ul>
  );
}

export default function KnowledgeDoc() {
  const { t } = useTranslation();
  const { id } = useParams();
  const location = useLocation();
  const navigate = useNavigate();
  const confirm = useConfirm();
  const { notify } = useStore();
  // ?v=3：看引用时的那一版（DSH 里的出处链接带着），行号按那一版算
  const [version, setVersion] = useState<number | undefined>(() => {
    const v = Number(new URLSearchParams(location.search).get("v"));
    return Number.isInteger(v) && v > 0 ? v : undefined;
  });
  const detail = useApi(() => api.kbDoc(Number(id), version));
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [note, setNote] = useState("");
  const [dialog, setDialog] = useState<"rename" | "replace" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const { busy, run } = useAction();
  const range = hashRange(location.hash);
  // ?q=：引用的原文摘句（DSH 里的出处链接带着），阅读视图里精确标出来
  const quote = new URLSearchParams(location.search).get("q") ?? undefined;
  const highlight = range ? { ...range, quote } : null;
  const [view, setView] = useState<"read" | "raw">("read");
  const [jump, setJump] = useState<number | undefined>(undefined);
  const d = detail.data;
  const canEdit = !!d?.kb.can_edit;
  const viewingOld = !!d && d.version !== d.doc.version;

  // 切换查看的版本时重新取（首次加载由 useApi 自己做）
  const first = useRef(true);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    detail.reload();
  }, [version]);

  const saveEdit = async () => {
    if (!d) return;
    if (draft === d.markdown) {
      notify(t("kdoc.unchanged"));
      setEditing(false);
      return;
    }
    const r = await run(() => api.patchKBDoc(d.doc.id, { markdown: draft, note }));
    if (r.ok) {
      notify(t("kdoc.savedVersion", { version: r.value.version }));
      setEditing(false);
      setError(null);
      detail.reload();
    } else setError(r.error);
  };

  const restore = async (v: number) => {
    if (!d) return;
    if (!(await confirm({ kind: "restore", description: t("kdoc.restoreConfirm", { version: v }) }))) return;
    const r = await run(async () => {
      const old = await api.kbDoc(d.doc.id, v);
      return api.patchKBDoc(d.doc.id, { markdown: old.markdown, note: t("kdoc.restoreNote", { version: v }) });
    });
    if (r.ok) {
      notify(t("kdoc.restored", { version: r.value.version }));
      setVersion(undefined);
      detail.reload();
    } else notify(r.error);
  };

  return (
    <div>
      <LoadState loading={detail.loading && !detail.data} error={detail.data ? null : detail.error} onRetry={detail.reload}>
        {d && (
          <>
            <Link to={kbPath(d.kb.id)} className={backLinkCls}>
              <ArrowLeft size={14} /> {t("kdoc.back", { title: d.kb.title })}
            </Link>
            <div className="mb-5 flex flex-wrap items-start justify-between gap-4">
              <div>
                <h1 className="text-2xl text-neutral-900 leading-8 font-bold tracking-[-0.02em]">{d.doc.title}</h1>
                <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-neutral-600">
                  {d.doc.folder && <Badge>{d.doc.folder}</Badge>}
                  <span>{t("kdoc.meta", { version: d.doc.version, lines: d.doc.lines })}</span>
                  <span>{formatTime(d.doc.updated_at)}</span>
                  {d.doc.source_name && <span>{t("kdoc.source", { name: d.doc.source_name })}</span>}
                  {d.doc.has_original && (
                    <a href={api.kbOriginalURL(d.doc.id)} className="inline-flex items-center gap-1 text-neutral-800 hover:underline">
                      <Download size={13} /> {t("kdoc.download")}
                    </a>
                  )}
                </div>
              </div>
              {canEdit && !editing && (
                <div className="flex gap-2">
                  <Button
                    onClick={() => {
                      setVersion(undefined);
                      setDraft(d.version === d.doc.version ? d.markdown : "");
                      setNote("");
                      setEditing(true);
                      if (d.version !== d.doc.version) void api.kbDoc(d.doc.id).then((cur) => setDraft(cur.markdown));
                    }}
                  >
                    {t("kdoc.edit")}
                  </Button>
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button variant="secondary" aria-label="⋯">
                        <MoreHorizontal size={16} />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                      <DropdownMenuItem onSelect={() => setDialog("replace")}>{t("kdoc.replace")}</DropdownMenuItem>
                      <DropdownMenuItem onSelect={() => setDialog("rename")}>{t("kdoc.rename")}</DropdownMenuItem>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem
                        className="text-red-600"
                        onSelect={async () => {
                          if (!(await confirm({ kind: "delete", description: t("kdoc.deleteConfirm", { title: d.doc.title }) }))) return;
                          try {
                            await api.deleteKBDoc(d.doc.id);
                            notify(t("kdoc.deleted"));
                            navigate(kbPath(d.kb.id));
                          } catch (e) {
                            notify(e instanceof Error ? e.message : String(e));
                          }
                        }}
                      >
                        {t("kdoc.delete")}
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </div>
              )}
            </div>

            {viewingOld && !editing && (
              <Notice tone="warn" className="mb-4">
                <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
                  {t("kdoc.viewing", { version: d.version, current: d.doc.version })}
                  <Button size="sm" variant="secondary" onClick={() => setVersion(undefined)}>
                    {t("kdoc.backToCurrent")}
                  </Button>
                </div>
              </Notice>
            )}

            <div className="grid gap-6 lg:grid-cols-[1fr_280px]">
              <Card className="min-w-0 p-4 sm:p-6">
                {editing ? (
                  <div className="space-y-3">
                    <div className="grid gap-4 xl:grid-cols-2">
                      <div>
                        <div className="mb-1 text-xs font-medium text-neutral-500">{t("kdoc.editSource")}</div>
                        <textarea className={cx(inputCls, "h-[64vh] py-2 font-mono text-xs")} value={draft} onChange={(e) => setDraft(e.target.value)} />
                      </div>
                      <div className="min-w-0">
                        <div className="mb-1 text-xs font-medium text-neutral-500">{t("kdoc.editPreview")}</div>
                        <div className="h-[64vh] overflow-auto rounded-md border border-neutral-200 p-4">
                          <KBReader markdown={draft} format={d.format} highlight={null} />
                        </div>
                      </div>
                    </div>
                    <EditImpact before={d.markdown} after={draft} reads={d.reads_30d} />
                    <Field label={t("kdoc.note")}>
                      <input className={inputCls} value={note} placeholder={t("kdoc.notePlaceholder")} onChange={(e) => setNote(e.target.value)} />
                    </Field>
                    <ErrorNote>{error}</ErrorNote>
                    <div className="flex gap-2">
                      <Button disabled={busy || !draft.trim()} onClick={() => void saveEdit()}>
                        {t("kdoc.saveEdit")}
                      </Button>
                      <Button variant="secondary" onClick={() => setEditing(false)}>
                        {t("kdoc.cancel")}
                      </Button>
                    </div>
                  </div>
                ) : (
                  <>
                    <div className="mb-4 flex justify-end">
                      <Segmented
                        value={view}
                        onChange={setView}
                        options={[
                          { value: "read", label: t("kdoc.viewRead") },
                          { value: "raw", label: t("kdoc.viewRaw") },
                        ]}
                      />
                    </div>
                    {view === "read" ? (
                      <div className="px-1 sm:px-3">
                        <KBReader markdown={d.markdown} format={d.format} highlight={highlight} jump={jump} />
                      </div>
                    ) : (
                      <Lines markdown={d.markdown} highlight={range} />
                    )}
                  </>
                )}
              </Card>
              <div className="space-y-6 lg:sticky lg:top-20 lg:self-start">
                <ConvertCard
                  d={d}
                  canEdit={canEdit}
                  onDone={() => {
                    setVersion(undefined);
                    detail.reload();
                  }}
                />
                <ObjectFields object="document" id={d.doc.id} />
                {(d.outline?.length ?? 0) >= 3 && (
                  <Card>
                    <CardHeader title={t("kdoc.outline")} />
                    <Outline
                      outline={d.outline!}
                      onJump={(line) => {
                        setJump(undefined);
                        requestAnimationFrame(() => setJump(line));
                        if (view === "raw") document.querySelector(`[data-line="${line}"]`)?.scrollIntoView({ block: "start" });
                      }}
                    />
                  </Card>
                )}
                <Card>
                  <CardHeader title={t("kdoc.versions")} />
                  <ul className="max-h-96 overflow-auto px-5 pb-4 text-sm">
                    {d.versions.map((v) => (
                      <li key={v.version} className="border-t border-neutral-100 py-2 first:border-t-0">
                        <div className="flex items-center justify-between gap-2">
                          <span className="font-medium text-neutral-900">
                            v{v.version} {v.version === d.doc.version && <Badge tone="green">{t("kdoc.current")}</Badge>}
                          </span>
                          <span className="flex gap-1">
                            {v.version !== d.version && (
                              <Button size="sm" variant="ghost" onClick={() => setVersion(v.version === d.doc.version ? undefined : v.version)}>
                                {t("kdoc.view")}
                              </Button>
                            )}
                            {canEdit && v.version !== d.doc.version && (
                              <Button size="sm" variant="ghost" disabled={busy} onClick={() => void restore(v.version)}>
                                {t("kdoc.restore")}
                              </Button>
                            )}
                          </span>
                        </div>
                        <div className="text-xs text-neutral-500">
                          {formatTime(v.created_at)} {v.created_by_name && `· ${v.created_by_name}`}
                          {v.note && ` · ${v.note}`}
                        </div>
                      </li>
                    ))}
                  </ul>
                </Card>
              </div>
            </div>
            <RenameDialog d={d} open={dialog === "rename"} onOpenChange={(v) => setDialog(v ? "rename" : null)} onSaved={detail.reload} />
            <ReplaceDialog
              d={d}
              open={dialog === "replace"}
              onOpenChange={(v) => setDialog(v ? "replace" : null)}
              onSaved={(v) => {
                notify(t("kdoc.replaced", { version: v }));
                detail.reload();
              }}
            />
          </>
        )}
      </LoadState>
    </div>
  );
}
