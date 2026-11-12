import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { extApi, type FieldObject, type ObjectFieldView } from "../lib/ext-api";
import { useAction, useApi } from "../lib/use-api";
import { useStore } from "../lib/store";
import { Badge, Button, Card, CardHeader, cx, inputCls } from "./ui";

/** 把接口给的值变成表单里的值：多选是数组，其余是字符串。 */
function toForm(f: ObjectFieldView): string | string[] {
  if (f.type === "tags") return Array.isArray(f.value) ? (f.value as unknown[]).map(String) : [];
  return f.value === null || f.value === undefined ? "" : String(f.value);
}

/** 表单里的值交回接口：空的清掉（null），数字转成数字。 */
function fromForm(f: ObjectFieldView, v: string | string[]): unknown {
  if (Array.isArray(v)) return v.length ? v : null;
  if (v.trim() === "") return null;
  return f.type === "number" ? Number(v) : v.trim();
}

/**
 * 一个对象（文档、知识库、用户）的扩展字段（docs/access-control.md §4.2）。
 * 用于访问控制的字段只有「管理字段和访问规则」的人能改，接口按人给出 editable。
 */
export function ObjectFields({ object, id, compact, showEmpty }: { object: FieldObject; id: number; compact?: boolean; showEmpty?: boolean }) {
  const { t } = useTranslation();
  const { notify } = useStore();
  const { busy, run } = useAction();
  const list = useApi(() => extApi.objectFields(object, id));
  const [form, setForm] = useState<Record<string, string | string[]>>({});
  useEffect(() => {
    const init: Record<string, string | string[]> = {};
    for (const f of list.data ?? []) init[f.key] = toForm(f);
    setForm(init);
  }, [list.data]);

  const fields = list.data ?? [];
  // 还没有字段：不占地方（管理员没用这个功能时页面保持原样）
  if (list.data && fields.length === 0 && showEmpty) return <p className="text-sm text-neutral-500">{t("objectFields.empty")}</p>;
  if (!list.data || fields.length === 0) return null;
  const dirty = fields.filter((f) => f.editable && JSON.stringify(form[f.key] ?? toForm(f)) !== JSON.stringify(toForm(f)));
  const save = async () => {
    const values: Record<string, unknown> = {};
    for (const f of dirty) values[f.key] = fromForm(f, form[f.key] ?? "");
    const r = await run(() => extApi.setObjectFields(object, id, values));
    notify(r.ok ? t("objectFields.saved") : r.error);
    if (r.ok) list.reload();
  };

  const body = (
    <div className={cx("grid gap-3", !compact && "sm:grid-cols-2")} data-object-fields={object}>
      {fields.map((f) => {
        const v = form[f.key] ?? toForm(f);
        const opts = f.options ?? [];
        return (
          <label key={f.key} className="block text-sm">
            <span className="mb-1 flex items-center gap-1.5 text-neutral-600">
              {f.label}
              {f.access && <Badge tone="amber">{t("objectFields.accessBadge")}</Badge>}
            </span>
            {f.type === "tags" ? (
              <div className="flex flex-wrap gap-x-4 gap-y-1">
                {opts.map((o) => {
                  const on = (v as string[]).includes(o);
                  return (
                    <span key={o} className="inline-flex items-center gap-1.5">
                      <input
                        type="checkbox"
                        className="size-4 accent-brand-600"
                        disabled={!f.editable}
                        checked={on}
                        onChange={() => setForm({ ...form, [f.key]: on ? (v as string[]).filter((x) => x !== o) : [...(v as string[]), o] })}
                      />
                      {o}
                    </span>
                  );
                })}
              </div>
            ) : f.type === "select" || f.type === "level" ? (
              <select className={inputCls} disabled={!f.editable} value={v as string} onChange={(e) => setForm({ ...form, [f.key]: e.target.value })}>
                <option value="">{t("objectFields.none")}</option>
                {opts.map((o) => (
                  <option key={o} value={o}>
                    {o}
                  </option>
                ))}
              </select>
            ) : (
              <input
                className={inputCls}
                disabled={!f.editable}
                type={f.type === "number" ? "number" : f.type === "date" ? "date" : "text"}
                value={v as string}
                onChange={(e) => setForm({ ...form, [f.key]: e.target.value })}
              />
            )}
            {!f.editable && <span className="mt-0.5 block text-xs text-neutral-500">{t("objectFields.readOnly")}</span>}
          </label>
        );
      })}
      {fields.some((f) => f.editable) && (
        <div className={cx("flex justify-end", !compact && "sm:col-span-2")}>
          <Button size="sm" disabled={busy || dirty.length === 0} onClick={() => void save()}>
            {t("objectFields.save")}
          </Button>
        </div>
      )}
    </div>
  );
  if (compact) return body;
  return (
    <Card>
      <CardHeader title={t("objectFields.title")} />
      <div className="px-5 pb-5">{body}</div>
    </Card>
  );
}
