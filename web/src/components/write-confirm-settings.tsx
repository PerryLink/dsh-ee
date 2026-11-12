import { useTranslation } from "react-i18next";
import { extApi, type WriteConfirmMode } from "../lib/ext-api";
import { useAction, useApi } from "../lib/use-api";
import { useStore } from "../lib/store";
import { useConfirm } from "../lib/confirm";
import { Segmented, Spinner } from "./ui";

/** 系统设置里「写操作的服务端确认」：off / non_dsh（默认）/ always（docs/plugins.md §7.5）。 */
export function WriteConfirmSettings() {
  const { t } = useTranslation();
  const { notify } = useStore();
  const confirm = useConfirm();
  const mode = useApi(extApi.writeConfirmMode);
  const { busy, run } = useAction();
  if (!mode.data) return mode.error ? <p className="text-sm text-red-700">{mode.error}</p> : <Spinner className="text-neutral-400" />;
  const set = async (v: WriteConfirmMode) => {
    if (v === "off" && !(await confirm({ kind: "disable", description: t("writeConfirm.offConfirm") }))) return;
    const r = await run(() => extApi.setWriteConfirmMode(v));
    notify(r.ok ? t("writeConfirm.saved") : r.error);
    mode.reload();
  };
  return (
    <div className="space-y-2" data-write-confirm={mode.data}>
      <Segmented
        value={mode.data}
        onChange={(v) => !busy && void set(v)}
        options={[
          { value: "non_dsh", label: t("writeConfirm.nonDsh") },
          { value: "always", label: t("writeConfirm.always") },
          { value: "off", label: t("writeConfirm.off") },
        ]}
      />
      <p className="text-sm text-neutral-600">{t(mode.data === "off" ? "writeConfirm.offDesc" : mode.data === "always" ? "writeConfirm.alwaysDesc" : "writeConfirm.nonDshDesc")}</p>
    </div>
  );
}
