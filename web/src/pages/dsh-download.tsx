import { useEffect, useRef, useState } from "react";
import { useLocation } from "react-router";
import { Apple, CheckCircle2, Copy, Download, Monitor, RefreshCw, ShieldCheck, Terminal, PackageOpen } from "lucide-react";
import { useTranslation } from "react-i18next";
import { api, formatBytes, formatTime } from "../lib/api";
import { useApi } from "../lib/use-api";
import { useStore } from "../lib/store";
import type { DshClient, DshRelease, DshTarget } from "../lib/types";
import { Badge, Card, CopyBox, PageHeader, Empty, buttonCls } from "../components/ui";
import { LoadState } from "../components/api-ui";
import { PageLink } from "../lib/menus";
import { PublicUrlNotice } from "../components/public-url-notice";
import { copyText } from "../lib/clipboard";

type Architecture = "arm" | "x64" | "unknown";

function useArchitecture() {
  const [arch, setArch] = useState<Architecture>("unknown");
  useEffect(() => {
    const ua = navigator as Navigator & { userAgentData?: { getHighEntropyValues: (hints: string[]) => Promise<{ architecture?: string }> } };
    if (!ua.userAgentData) return;
    ua.userAgentData.getHighEntropyValues(["architecture"]).then((v) => {
      if (v.architecture === "arm") setArch("arm");
      else if (v.architecture) setArch("x64");
    }).catch(() => {});
  }, []);
  return arch;
}

function DownloadRow({ release, title }: { release: DshRelease; title: string }) {
  const { t } = useTranslation();
  const { notify } = useStore();
  return (
    <div className="flex flex-wrap items-center gap-4 border-t border-neutral-100 px-5 py-4">
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2 font-medium text-neutral-800">
          {title} <span className="font-mono">{release.version}</span> · {release.kind}
          {release.recommended && <Badge tone="brand">{t("download.recommended")}</Badge>}
        </div>
        <div className="mt-1 text-xs text-neutral-500">
          {formatTime(release.released_at)} · {formatBytes(release.size)} · sha512 {release.sha512.slice(0, 16)}…
        </div>
        <button type="button" className="mt-1 inline-flex items-center gap-1 text-xs text-neutral-600 hover:text-neutral-900" onClick={async () => {
          notify(await copyText(release.sha512) ? t("common.copied") : t("common.copyFailed"));
        }}><Copy size={12} /> {t("download.copySha512")}</button>
        <div className="mt-1 text-xs text-neutral-500">{release.verified ? t(release.source === "import" ? "download.importVerified" : "download.officialVerified") : t(release.source === "import" ? "download.importUnverified" : "download.unverifiedDmg")}</div>
      </div>
      <a href={release.download_url} className={buttonCls("primary")}>
        <Download size={14} /> {t("download.download")}
      </a>
    </div>
  );
}

function useConnection() {
  const startedAt = useRef(Date.now());
  const [clients, setClients] = useState<DshClient[]>([]);
  const [timedOut, setTimedOut] = useState(false);
  useEffect(() => {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const poll = async () => {
      const items = await api.dshClients().catch(() => []);
      if (stopped) return;
      setClients(items);
      const online = items.some((c) => c.last_host.startsWith("desktop/") && Date.parse(c.last_used_at) > startedAt.current);
      if (online) return;
      if (Date.now() - startedAt.current >= 5 * 60 * 1000) {
        setTimedOut(true);
        return;
      }
      timer = setTimeout(poll, 3000);
    };
    void poll();
    return () => {
      stopped = true;
      if (timer) clearTimeout(timer);
    };
  }, []);
  const connected = clients.find((c) => c.last_host.startsWith("desktop/") && Date.parse(c.last_used_at) > startedAt.current);
  return { connected, timedOut };
}

export default function DshDownload() {
  const { t } = useTranslation();
  const { baseUrl: localBase } = useStore();
  const ci = useApi(api.connectInfo).data;
  const baseUrl = ci?.base_url || localBase;
  const { data, loading, error, reload } = useApi(api.dshReleases);
  const location = useLocation();
  const releaseLocation = useRef(location.key);
  useEffect(() => {
    if (releaseLocation.current === location.key) return;
    releaseLocation.current = location.key;
    reload();
  }, [location.key, reload]);
  useEffect(() => {
    window.addEventListener("focus", reload);
    return () => window.removeEventListener("focus", reload);
  }, [reload]);
  const { connected, timedOut } = useConnection();
  const arch = useArchitecture();
  const isMac = /Mac/i.test(navigator.userAgent);
  const isWindows = /Win/i.test(navigator.userAgent);
  const macTargets: DshTarget[] = arch === "arm" ? ["mac-arm64"] : arch === "x64" ? ["mac-x64"] : ["mac-arm64", "mac-x64"];
  // 只有 macOS 分芯片架构；Windows 只有一个 x64 安装包。
  const targets: DshTarget[] = isMac ? macTargets : isWindows ? ["win-x64"] : [];
  const releases = data?.releases ?? [];
  const rows = releases.filter((r) => targets.includes(r.target));

  if (data && !data.enabled) return <Card><Empty>{t("download.disabled")}</Empty></Card>;

  return (
    <div>
      <PageHeader title={t("download.title")} desc={t("download.desc")} />
      <PublicUrlNotice info={ci} className="mb-6" />
      <LoadState loading={loading && !data} error={error} onRetry={reload}>
        <div className="space-y-6">
          {!isMac && !isWindows ? (
            <Card>
              <Empty icon={Monitor}>{t("download.otherPlatform")}</Empty>
            </Card>
          ) : (
            <>
              <Card className="overflow-hidden">
                <div className="flex items-start gap-3 px-5 py-5">
                  {isMac ? <Apple className="mt-0.5 text-neutral-700" size={22} /> : <Monitor className="mt-0.5 text-neutral-700" size={22} />}
                  <div>
                    <h2 className="text-neutral-900 text-[15px] leading-6 font-semibold">{t("download.stepDownload")}</h2>
                    <p className="mt-1 text-sm text-neutral-500">{isMac ? t("download.unzip") : t("download.winInstall")}</p>
                    {isMac && arch === "unknown" && <p className="mt-1 text-sm text-neutral-500">{t("download.chooseChip")}</p>}
                  </div>
                </div>
                {rows.map((r) => <DownloadRow key={r.id} release={r} title={t(`download.target.${r.target}`)} />)}
                {!rows.length && <div className="border-t border-neutral-100"><Empty compact icon={PackageOpen}>{t("download.empty")}</Empty></div>}
                <div className="flex items-center gap-2 border-t border-neutral-100 bg-neutral-50 px-5 py-3 text-xs text-neutral-500">
                  <ShieldCheck size={14} /> {t("download.hashNote")}
                </div>
              </Card>

              <Card className="flex items-start gap-3 p-5">
                <CheckCircle2 className="mt-0.5 text-neutral-700" size={22} />
                <div>
                  <h2 className="text-neutral-900 text-[15px] leading-6 font-semibold">{t("download.stepOpen")}</h2>
                  <p className="mt-1 text-sm text-neutral-500">{isMac ? t("download.openDesc") : t("download.winQuitDesc")}</p>
                </div>
              </Card>

              <Card className="p-5">
                <div className="mb-3 flex items-start gap-3">
                  <Terminal className="mt-0.5 text-neutral-700" size={22} />
                  <div>
                    <h2 className="text-neutral-900 text-[15px] leading-6 font-semibold">{t("download.stepCommand")}</h2>
                    <p className="mt-1 text-sm text-neutral-500">{t("download.commandDesc")}</p>
                  </div>
                </div>
                <CopyBox text={isMac ? `curl -fsSL ${baseUrl}/setup.sh | sh` : `irm ${baseUrl}/setup.ps1 | iex`} />
              </Card>

              <Card className="p-5">
                <div className="flex items-start gap-3">
                  <RefreshCw className={`mt-0.5 ${connected ? "text-emerald-600" : "text-neutral-700"}`} size={22} />
                  <div>
                    <h2 className="text-neutral-900 text-[15px] leading-6 font-semibold">{t("download.stepReconnect")}</h2>
                    {connected ? (
                      <p className="mt-1 text-sm font-medium text-emerald-700">
                        {t("download.connected", { device: connected.client_name || t("download.defaultDevice"), version: connected.last_client.replace("dsh-connect/", "") })}
                      </p>
                    ) : timedOut ? (
                      <p className="mt-1 text-sm text-neutral-500">
                        {t("download.notDetected")} <PageLink page="setup" className="text-brand-600 dark:text-brand-300 hover:underline">{t("download.connectGuide")}</PageLink>
                      </p>
                    ) : (
                      <p className="mt-1 text-sm text-neutral-500">{t("download.waiting")}</p>
                    )}
                  </div>
                </div>
              </Card>
            </>
          )}
        </div>
      </LoadState>
    </div>
  );
}