import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/shadcn/dialog";
import { Button, CopyBox, Notice } from "../../components/ui";
import i18n from "../../i18n";
import { resolveApiErrorText } from "../../lib/api-error-i18n";
import type { DshRelease, DshTarget } from "../../lib/types";
import { installerDetails, installerMatchesTarget, officialInstallerReference, releaseFeedURL } from "../../lib/distribution-import";

export interface ImportedRelease { id: number; target: DshTarget; version: string }

export function ImportReleaseDialog({ releases, onClose, onSuccess }: { releases: DshRelease[]; onClose: () => void; onSuccess: (release: ImportedRelease) => void }) {
  const { t } = useTranslation();
  const [target, setTarget] = useState<DshTarget>("mac-arm64");
  const [file, setFile] = useState<File | null>(null);
  const [version, setVersion] = useState("");
  const [hash, setHash] = useState("");
  const [progress, setProgress] = useState<number | null>(null);
  const [error, setError] = useState("");
  const mismatch = file !== null && !installerMatchesTarget(file.name, target);
  const official = officialInstallerReference(releases, target, file?.name ?? "", version);
  const linkVersion = version.trim() || official?.version;
  const installerURL = official?.upstream_url || (!/\.dmg$/i.test(file?.name ?? "") && linkVersion && /^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$/.test(linkVersion) ? `https://download.deepseek.com/dsh-desk/bin/${target}/deepseek-harness-${linkVersion}-${target}.${target === "win-x64" ? "exe" : "zip"}` : "");
  const upload = () => {
    if (!file || mismatch || progress !== null) return;
    setError(""); setProgress(0);
    const xhr = new XMLHttpRequest();
    const q = new URLSearchParams({ target, version, file: file.name, sha512: hash.trim() });
    xhr.open("POST", `/api/admin/dsh-releases/import?${q}`);
    xhr.upload.onprogress = (e) => { if (e.lengthComputable) setProgress(Math.round(e.loaded / e.total * 100)); };
    xhr.onerror = () => { setError(t("api.unreachable")); setProgress(null); };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) { onSuccess(JSON.parse(xhr.responseText) as ImportedRelease); return; }
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
    <DialogContent className="flex flex-col overflow-hidden sm:max-w-2xl">
      <DialogHeader className="shrink-0"><DialogTitle>{t("distributionAdmin.importOffline")}</DialogTitle><DialogDescription>{t("distributionAdmin.importHint")}</DialogDescription></DialogHeader>
      <form className="flex min-h-0 flex-1 flex-col gap-4" onSubmit={(e) => { e.preventDefault(); upload(); }}>
        <div className="-mx-1 min-h-0 flex-1 space-y-4 overflow-y-auto px-1">
        <label className="block text-sm font-medium">{t("distributionAdmin.importTarget")}<select className={inputClass} value={target} onChange={(e) => setTarget(e.target.value as DshTarget)} disabled={progress !== null}>
          <option value="mac-arm64">{t("distributionAdmin.arm64")}</option><option value="mac-x64">{t("distributionAdmin.x64")}</option><option value="win-x64">{t("distributionAdmin.windows")}</option>
        </select></label>
        <details className="rounded-lg border border-neutral-200 bg-neutral-50 p-3 text-sm" open>
          <summary className="cursor-pointer font-medium">{t("distributionAdmin.importGuideTitle")}</summary>
          <p className="my-2 text-neutral-600">{t("distributionAdmin.importGuide")}</p>
          <div className="space-y-2">
            <CopyBox text={releaseFeedURL(target)} label={t("distributionAdmin.importFeed")} wrap />
            {installerURL && <CopyBox text={installerURL} label={t("distributionAdmin.importInstallerURL")} wrap />}
          </div>
          <p className="mt-2 text-xs text-neutral-500">{t("distributionAdmin.importURLHint")}</p>
        </details>
        <label className="block text-sm font-medium">{t("distributionAdmin.importFile")}<input required type="file" accept=".zip,.exe,.msi,.dmg" className={inputClass} disabled={progress !== null} onChange={(e) => { const f = e.target.files?.[0] ?? null; setFile(f); const details = installerDetails(f?.name ?? ""); setVersion(details.version); if (details.target) setTarget(details.target); setError(""); }} /></label>
        {mismatch && <div role="alert"><Notice tone="warn">{t("distributionAdmin.importMismatch")}</Notice></div>}
        <label className="block text-sm font-medium">{t("distributionAdmin.importVersion")}<input required maxLength={64} className={inputClass} value={version} disabled={progress !== null} onChange={(e) => setVersion(e.target.value)} /></label>
        <label className="block text-sm font-medium">{t("distributionAdmin.importHash")}<input className={inputClass} value={hash} disabled={progress !== null} onChange={(e) => setHash(e.target.value)} /></label>
        {progress !== null && <div className="space-y-2 text-sm"><div>{t("distributionAdmin.importUploading")} · {progress}%</div><progress aria-label={t("distributionAdmin.importUploading")} className="h-2 w-full accent-emerald-600" max={100} value={progress} /></div>}
        {error && <Notice tone="warn">{error}</Notice>}
        </div>
        <DialogFooter className="shrink-0"><Button type="button" variant="secondary" disabled={progress !== null} onClick={onClose}>{t("common.cancel")}</Button><Button type="submit" disabled={!file || mismatch || !version.trim() || progress !== null}>{t("distributionAdmin.importSubmit")}</Button></DialogFooter>
      </form>
    </DialogContent>
  </Dialog>;
}
