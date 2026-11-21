import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/shadcn/dialog";
import { Button, Notice } from "../../components/ui";
import i18n from "../../i18n";
import { resolveApiErrorText } from "../../lib/api-error-i18n";

export function ImportReleaseDialog({ onClose, onSuccess }: { onClose: () => void; onSuccess: () => void }) {
  const { t } = useTranslation();
  const [target, setTarget] = useState("mac-arm64");
  const [file, setFile] = useState<File | null>(null);
  const [version, setVersion] = useState("");
  const [hash, setHash] = useState("");
  const [progress, setProgress] = useState<number | null>(null);
  const [error, setError] = useState("");
  const upload = () => {
    if (!file) return;
    setError(""); setProgress(0);
    const xhr = new XMLHttpRequest();
    const q = new URLSearchParams({ target, version, file: file.name, sha512: hash.trim() });
    xhr.open("POST", `/api/admin/dsh-releases/import?${q}`);
    xhr.upload.onprogress = (e) => { if (e.lengthComputable) setProgress(Math.round(e.loaded / e.total * 100)); };
    xhr.onerror = () => { setError(t("api.unreachable")); setProgress(null); };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) { onSuccess(); return; }
      let data: unknown;
      try { data = JSON.parse(xhr.responseText); } catch { data = null; }
      setError(resolveApiErrorText(data, xhr.status, {
        hasCode: (code) => i18n.exists(`apiErrors.${code}`),
        translateCode: (code, params) => i18n.t(`apiErrors.${code}` as never, params) as unknown as string,
        statusText: (status) => t("api.requestFailed", { status }),
      }));
      setProgress(null);
    };
    xhr.send(file);
  };
  const inputClass = "mt-1 block w-full rounded-md border border-neutral-300 bg-card px-3 py-2 text-sm";
  return <Dialog open onOpenChange={(open) => { if (!open && progress === null) onClose(); }}>
    <DialogContent>
      <DialogHeader><DialogTitle>{t("distributionAdmin.importOffline")}</DialogTitle><DialogDescription>{t("distributionAdmin.importHint")}</DialogDescription></DialogHeader>
      <form className="space-y-4" onSubmit={(e) => { e.preventDefault(); upload(); }}>
        <label className="block text-sm font-medium">{t("distributionAdmin.importTarget")}<select className={inputClass} value={target} onChange={(e) => setTarget(e.target.value)} disabled={progress !== null}>
          <option value="mac-arm64">{t("distributionAdmin.arm64")}</option><option value="mac-x64">{t("distributionAdmin.x64")}</option><option value="win-x64">{t("distributionAdmin.windows")}</option>
        </select></label>
        <label className="block text-sm font-medium">{t("distributionAdmin.importFile")}<input required type="file" accept=".zip,.exe,.msi,.dmg" className={inputClass} disabled={progress !== null} onChange={(e) => { const f = e.target.files?.[0] ?? null; setFile(f); const stem = f?.name.replace(/\.(zip|exe|msi|dmg)$/i, ""); setVersion(stem?.match(/\d+\.\d+\.\d+(?:-[A-Za-z0-9]+(?:\.[A-Za-z0-9]+)*)?/)?.[0] ?? ""); }} /></label>
        <label className="block text-sm font-medium">{t("distributionAdmin.importVersion")}<input required maxLength={64} className={inputClass} value={version} disabled={progress !== null} onChange={(e) => setVersion(e.target.value)} /></label>
        <label className="block text-sm font-medium">{t("distributionAdmin.importHash")}<input className={inputClass} value={hash} disabled={progress !== null} onChange={(e) => setHash(e.target.value)} /></label>
        {progress !== null && <div className="space-y-2 text-sm"><div>{t("distributionAdmin.importUploading")} · {progress}%</div><progress aria-label={t("distributionAdmin.importUploading")} className="h-2 w-full accent-emerald-600" max={100} value={progress} /></div>}
        {error && <Notice tone="warn">{error}</Notice>}
        <DialogFooter><Button type="button" variant="secondary" disabled={progress !== null} onClick={onClose}>{t("common.cancel")}</Button><Button type="submit" disabled={!file || !version.trim() || progress !== null}>{t("distributionAdmin.importSubmit")}</Button></DialogFooter>
      </form>
    </DialogContent>
  </Dialog>;
}
