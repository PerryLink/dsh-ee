import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ChevronRight, Plus, RefreshCw, Trash2 } from "lucide-react";
import { formatTime } from "../lib/api";
import { extApi, type KBBrowseNode, type KBSourceExtension, type KBSourceNode } from "../lib/ext-api";
import { useAction, useApi } from "../lib/use-api";
import { useStore } from "../lib/store";
import { useConfirm } from "../lib/confirm";
import { LoadState } from "./api-ui";
import { Badge, Button, cx, Field, inputCls, Notice, Segmented, Spinner } from "./ui";

/**
 * 知识库页面上的「知识源」（docs/plugins.md §6.2）：把服务扩展（或内置的飞书文档）挂到这个库上。
 * 同步方式勾选范围、定时拉进来；联邦检索不复制。只有这个库的维护者能管。
 */
export function KBSources({ kbId, onChanged }: { kbId: number; onChanged: () => void }) {
  const { t } = useTranslation();
  const { notify } = useStore();
  const confirm = useConfirm();
  const { busy, run } = useAction();
  const data = useApi(() => extApi.kbSources(kbId));
  const [adding, setAdding] = useState(false);
  const sources = data.data?.sources ?? [];
  const exts = data.data?.extensions ?? [];

  return (
    <div className="space-y-4 p-5" data-kb-sources>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <p className="max-w-2xl text-sm text-neutral-600">{t("kbSources.desc")}</p>
        <Button size="sm" disabled={!exts.length} onClick={() => setAdding((x) => !x)}>
          <Plus size={14} /> {t("kbSources.add")}
        </Button>
      </div>
      <LoadState loading={data.loading && !data.data} error={data.error} onRetry={data.reload}>
        {exts.length === 0 && <Notice>{t("kbSources.noExtensions")}</Notice>}
        {adding && exts.length > 0 && (
          <AddSource
            kbId={kbId}
            exts={exts}
            onCancel={() => setAdding(false)}
            onDone={() => {
              setAdding(false);
              data.reload();
              onChanged();
            }}
          />
        )}
        {sources.length === 0 ? (
          <p className="text-sm text-neutral-500">{t("kbSources.empty")}</p>
        ) : (
          <ul className="divide-y divide-neutral-100 rounded-md border border-neutral-200">
            {sources.map((s) => (
              <li key={s.id} className="px-4 py-3 text-sm" data-kb-source={s.id}>
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium text-neutral-900">{s.title}</span>
                  <Badge tone={s.mode === "sync" ? "brand" : "green"}>{t(`kbSources.mode.${s.mode}`)}</Badge>
                  {s.asset_id < 0 && <Badge>{t("kbSources.builtin")}</Badge>}
                  <span className="ml-auto flex gap-1">
                    {s.mode === "sync" && (
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={busy}
                        onClick={async () => {
                          const r = await run(() => extApi.kbSyncSource(kbId, s.id));
                          if (!r.ok) notify(r.error);
                          else notify(r.value.error ? r.value.error : t("kbSources.syncResult", { ...r.value }));
                          data.reload();
                          onChanged();
                        }}
                      >
                        <RefreshCw size={14} /> {t("kbSources.sync")}
                      </Button>
                    )}
                    <RemoveButton
                      busy={busy}
                      onRemove={async (withDocs) => {
                        const r = await run(() => extApi.kbDeleteSource(kbId, s.id, withDocs));
                        notify(r.ok ? t("kbSources.removed") : r.error);
                        data.reload();
                        onChanged();
                      }}
                      title={s.title}
                      confirm={confirm}
                    />
                  </span>
                </div>
                <div className="mt-1 flex flex-wrap gap-x-4 text-xs text-neutral-500">
                  {s.mode === "sync" && <span>{s.last_sync_at ? t("kbSources.lastSync", { time: formatTime(s.last_sync_at) }) : t("kbSources.neverSynced")}</span>}
                  {s.mode === "sync" && <span>{t("kbSources.docs", { count: s.doc_count })}</span>}
                  {s.folder && <span>/{s.folder}</span>}
                  {s.scope.length > 0 && <span>{s.scope.map((n) => n.title || n.id).join("、")}</span>}
                </div>
                {s.last_error && <p className="mt-1 text-xs text-red-700">{s.last_error}</p>}
              </li>
            ))}
          </ul>
        )}
      </LoadState>
    </div>
  );
}

function RemoveButton({ busy, title, onRemove, confirm }: { busy: boolean; title: string; onRemove: (withDocs: boolean) => void; confirm: ReturnType<typeof useConfirm> }) {
  const { t } = useTranslation();
  const [withDocs, setWithDocs] = useState(false);
  return (
    <span className="inline-flex items-center gap-2">
      <label className="inline-flex items-center gap-1 text-xs text-neutral-500">
        <input type="checkbox" className="size-3.5 accent-red-600" checked={withDocs} onChange={(e) => setWithDocs(e.target.checked)} />
        {t("kbSources.removeDocs")}
      </label>
      <Button
        variant="ghost"
        size="sm"
        disabled={busy}
        aria-label={t("kbSources.remove")}
        onClick={async () => {
          if (!(await confirm({ kind: "delete", description: t("kbSources.removeConfirm", { title }) }))) return;
          onRemove(withDocs);
        }}
      >
        <Trash2 size={14} />
      </Button>
    </span>
  );
}

function AddSource({ kbId, exts, onCancel, onDone }: { kbId: number; exts: KBSourceExtension[]; onCancel: () => void; onDone: () => void }) {
  const { t } = useTranslation();
  const { notify } = useStore();
  const { busy, run } = useAction();
  const [assetId, setAssetId] = useState(exts[0].asset_id);
  const ext = exts.find((e) => e.asset_id === assetId) ?? exts[0];
  const [mode, setMode] = useState(ext.modes[0]);
  const [scope, setScope] = useState<KBSourceNode[]>([]);
  const [folder, setFolder] = useState("");
  const [title, setTitle] = useState("");
  const pickExt = (id: number) => {
    const e = exts.find((x) => x.asset_id === id)!;
    setAssetId(id);
    setMode(e.modes[0]);
    setScope([]);
  };
  const create = async () => {
    const r = await run(() => extApi.kbAddSource(kbId, { asset_id: assetId, mode, scope, folder: folder.trim(), title: title.trim() }));
    if (!r.ok) return notify(r.error);
    const sync = r.value.sync;
    notify(sync ? (sync.error ? sync.error : t("kbSources.syncResult", { ...sync })) : t("kbSources.created"));
    onDone();
  };
  return (
    <div className="space-y-4 rounded-lg border border-neutral-200 bg-neutral-50/60 p-4" data-kb-source-form>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label={t("kbSources.extension")}>
          <select className={inputCls} value={assetId} onChange={(e) => pickExt(Number(e.target.value))}>
            {exts.map((e) => (
              <option key={e.asset_id} value={e.asset_id}>
                {e.title}
                {e.builtin ? ` · ${t("kbSources.builtin")}` : ""}
              </option>
            ))}
          </select>
        </Field>
        <Field label={t("kbSources.titleLabel")}>
          <input className={inputCls} value={title} placeholder={ext.title} onChange={(e) => setTitle(e.target.value)} />
        </Field>
      </div>
      {ext.modes.length > 1 && <Segmented value={mode} onChange={setMode} options={ext.modes.map((m) => ({ value: m, label: t(`kbSources.mode.${m}`) }))} />}
      <p className="text-[13px] text-neutral-600">{t(`kbSources.modeDesc.${mode}`)}</p>
      {mode === "sync" && (
        <>
          <Field label={t("kbSources.scope")} hint={t("kbSources.scopeHint")}>
            <div className="max-h-72 overflow-auto rounded-md border border-neutral-200 bg-card p-2">
              <BrowseLevel kbId={kbId} assetId={assetId} parent="" scope={scope} setScope={setScope} depth={0} />
            </div>
          </Field>
          <Field label={t("kbSources.folder")} hint={t("kbSources.folderHint")}>
            <input className={cx(inputCls, "font-mono")} value={folder} onChange={(e) => setFolder(e.target.value)} />
          </Field>
        </>
      )}
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onCancel}>
          {t("common.cancel")}
        </Button>
        <Button disabled={busy} onClick={() => void create()}>
          {busy && <Spinner size={14} />} {t("kbSources.create")}
        </Button>
      </div>
    </div>
  );
}

/** 知识源的一层目录：勾选节点，有子节点的可以展开。 */
function BrowseLevel({ kbId, assetId, parent, scope, setScope, depth }: { kbId: number; assetId: number; parent: string; scope: KBSourceNode[]; setScope: (s: KBSourceNode[]) => void; depth: number }) {
  const { t } = useTranslation();
  const list = useApi(() => extApi.kbBrowse(kbId, assetId, parent));
  if (list.loading && !list.data) return <Spinner size={14} className="m-1 text-neutral-400" />;
  if (list.error) return <p className="px-1 text-xs text-red-700">{list.error}</p>;
  if (list.data?.unsupported) return <p className="px-1 text-xs text-neutral-500">{t("kbSources.unsupported")}</p>;
  const items = list.data?.items ?? [];
  if (!items.length) return <p className="px-1 text-xs text-neutral-500">{t("kbSources.browseEmpty")}</p>;
  return (
    <ul className={cx(depth > 0 && "ml-5 border-l border-neutral-100 pl-2")}>
      {items.map((n) => (
        <BrowseNode key={n.id} n={n} kbId={kbId} assetId={assetId} scope={scope} setScope={setScope} depth={depth} />
      ))}
    </ul>
  );
}

function BrowseNode({ n, kbId, assetId, scope, setScope, depth }: { n: KBBrowseNode; kbId: number; assetId: number; scope: KBSourceNode[]; setScope: (s: KBSourceNode[]) => void; depth: number }) {
  const [open, setOpen] = useState(false);
  const checked = scope.some((s) => s.id === n.id);
  const expandable = n.kind === "folder" || n.hasChildren;
  return (
    <li className="py-0.5 text-sm">
      <div className="flex items-center gap-1.5">
        <button type="button" className={cx("inline-flex size-5 items-center justify-center text-neutral-400", !expandable && "invisible")} onClick={() => setOpen((x) => !x)} aria-label="expand">
          <ChevronRight size={14} className={cx("transition-transform", open && "rotate-90")} />
        </button>
        <label className="inline-flex items-center gap-1.5">
          <input
            type="checkbox"
            className="size-4 accent-brand-600"
            checked={checked}
            onChange={() => setScope(checked ? scope.filter((s) => s.id !== n.id) : [...scope, { id: n.id, title: n.title }])}
          />
          {n.title || n.id}
        </label>
      </div>
      {open && expandable && <BrowseLevel kbId={kbId} assetId={assetId} parent={n.id} scope={scope} setScope={setScope} depth={depth + 1} />}
    </li>
  );
}
