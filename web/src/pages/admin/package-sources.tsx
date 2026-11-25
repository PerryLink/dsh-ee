import { useState } from "react";
import { useTranslation } from "react-i18next";
import { LoadState } from "../../components/api-ui";
import { Button, Card, CardHeader, CopyBox, Empty, inputBase } from "../../components/ui";
import { api, formatBytes, formatTime, type PackageSource } from "../../lib/api";
import { useStore } from "../../lib/store";
import { useAction, useApi } from "../../lib/use-api";

const sourceKey = (source: PackageSource) => `${source.protocol}/${source.name}`;

export default function PackageSourcesCard() {
  const { t } = useTranslation();
  const { baseUrl, notify } = useStore();
  const sources = useApi(api.packageSources);
  const { busy, run } = useAction();
  const [selected, setSelected] = useState<PackageSource>();
  const [creating, setCreating] = useState(false);
  const [protocol, setProtocol] = useState<PackageSource["protocol"]>("conan");
  const [mode, setMode] = useState<PackageSource["mode"]>("hosted");
  const [name, setName] = useState("");
  const [upstream, setUpstream] = useState("");
  const showUpstream = protocol === "raw" && mode === "proxy";
  const active = selected ?? sources.data?.[0];

  return (
    <Card className="mb-4">
      <CardHeader title={t("packageSources.title")} aside={
        <Button variant="secondary" size="sm" onClick={() => setCreating(!creating)}>
          {t(creating ? "packageSources.cancel" : "packageSources.add")}
        </Button>
      } />
      <div className="space-y-4 px-5 pb-5">
        <p className="text-sm text-neutral-600">{t("packageSources.description")}</p>
        {creating && (
          <form className="grid gap-3 rounded-lg border border-neutral-200 p-4 sm:grid-cols-3" onSubmit={async (e) => {
            e.preventDefault();
            const result = await run(() => api.createPackageSource({ name: name.trim(), protocol, mode, upstream_url: showUpstream ? upstream.trim() : undefined }));
            if (!result.ok) return notify(result.error);
            setSelected(result.value);
            setName("");
            setUpstream("");
            setCreating(false);
            sources.reload();
          }}>
            <label className="space-y-1 text-sm">
              <span>{t("packageSources.protocol")}</span>
              <select className={inputBase} value={protocol} onChange={(e) => setProtocol(e.target.value as PackageSource["protocol"])}>
                <option value="conan">Conan (C/C++)</option>
                <option value="raw">Raw</option>
              </select>
            </label>
            <label className="space-y-1 text-sm">
              <span>{t("packageSources.mode")}</span>
              <select className={inputBase} value={mode} onChange={(e) => setMode(e.target.value as PackageSource["mode"])}>
                <option value="hosted">{t("packageSources.hosted")}</option>
                <option value="proxy">{t("packageSources.proxy")}</option>
              </select>
            </label>
            <label className="space-y-1 text-sm">
              <span>{t("packageSources.name")}</span>
              <input className={inputBase} value={name} onChange={(e) => setName(e.target.value)} placeholder="team-packages" required maxLength={64} pattern="[a-z0-9][a-z0-9_\x2d]{0,63}" />
            </label>
            {showUpstream && (
              <label className="space-y-1 text-sm sm:col-span-3">
                <span>{t("packageSources.upstream")}</span>
                <input className={inputBase} type="url" value={upstream} onChange={(e) => setUpstream(e.target.value)} placeholder="https://example.com/artifacts/" required />
              </label>
            )}
            {protocol === "conan" && mode === "proxy" && <p className="sm:col-span-3 text-sm text-neutral-600">{t("packageSources.conanCenter")}</p>}
            <div><Button type="submit" size="sm" disabled={busy}>{t("packageSources.create")}</Button></div>
          </form>
        )}
        <LoadState loading={sources.loading && !sources.data} error={sources.error} onRetry={sources.reload}>
          {!active ? <Empty>{t("packageSources.empty")}</Empty> : (
            <>
              <label className="block max-w-sm space-y-1 text-sm">
                <span>{t("packageSources.select")}</span>
                <select className={inputBase} value={sourceKey(active)} onChange={(e) => setSelected(sources.data?.find((source) => sourceKey(source) === e.target.value))}>
                  {sources.data?.map((source) => <option key={sourceKey(source)} value={sourceKey(source)}>
                    {source.protocol === "conan" ? "Conan" : "Raw"} / {source.name} · {t(source.mode === "hosted" ? "packageSources.hosted" : "packageSources.proxy")}
                  </option>)}
                </select>
              </label>
              <SourceDetails key={sourceKey(active)} source={active} url={`${baseUrl}/${sourceKey(active)}`} onUpdate={(source) => {
                setSelected(source);
                sources.reload();
              }} />
            </>
          )}
        </LoadState>
      </div>
    </Card>
  );
}

function SourceDetails({ source, url, onUpdate }: { source: PackageSource; url: string; onUpdate: (source: PackageSource) => void }) {
  const { t } = useTranslation();
  const { notify } = useStore();
  const { busy, run } = useAction();
  const [upstream, setUpstream] = useState(source.upstream_url ?? "");
  const [cursors, setCursors] = useState([""]);
  const hosted = source.mode === "hosted";
  const alias = `dsh-${source.name}`;
  const commands = source.protocol === "conan"
    ? [
        `conan remote add ${alias} "${url}"`,
        `CONAN_PASSWORD="$DSH_EE_TOKEN" conan remote login ${alias} dsh-ee`,
        ...(hosted ? [`conan upload "my-package/1.0" -r ${alias} --confirm`] : []),
        `conan install --requires=${hosted ? "my-package/1.0" : "zlib/1.3.1"} -r ${alias}`,
      ]
    : [
        ...(hosted ? [`curl -f -H "Authorization: Bearer $DSH_EE_TOKEN" -T ./artifact.bin "${url}/path/artifact.bin"`] : []),
        `curl -f -H "Authorization: Bearer $DSH_EE_TOKEN" -O "${url}/path/artifact.bin"`,
      ];

  return (
    <div className="space-y-4">
      {source.protocol === "raw" && !hosted && (
        <form className="flex items-end gap-2" onSubmit={async (e) => {
          e.preventDefault();
          const result = await run(() => api.updatePackageSource(source, upstream.trim()));
          if (!result.ok) return notify(result.error);
          onUpdate(result.value);
          notify(t("packageSources.saved"));
        }}>
          <label className="grow space-y-1 text-sm">
            <span>{t("packageSources.upstream")}</span>
            <input className={inputBase} type="url" value={upstream} onChange={(e) => setUpstream(e.target.value)} required />
          </label>
          <Button type="submit" size="sm" disabled={busy || upstream.trim() === source.upstream_url}>{t("packageSources.save")}</Button>
        </form>
      )}
      {source.protocol === "conan" && !hosted && <p className="text-sm text-neutral-600">{t("packageSources.conanCenter")}</p>}
      <CopyBox text={url} label={t("packageSources.endpoint")} wrap />
      <p className="text-sm text-neutral-600">{t("packageSources.tokenHint")}</p>
      <CopyBox text={commands.join("\n")} label={t("packageSources.instructions")} wrap />
      {hosted ? <Artifacts key={cursors.at(-1)} source={source} after={cursors.at(-1) ?? ""} previous={cursors.length > 1 ? () => setCursors(cursors.slice(0, -1)) : undefined} next={(cursor) => setCursors([...cursors, cursor])} />
        : <p className="text-sm text-neutral-600">{t("packageSources.proxyArtifacts")}</p>}
    </div>
  );
}

function Artifacts({ source, after, previous, next }: { source: PackageSource; after: string; previous?: () => void; next: (cursor: string) => void }) {
  const { t } = useTranslation();
  const page = useApi(() => api.packageArtifacts(source, after));
  return (
    <section aria-label={t("packageSources.artifacts")} className="space-y-2">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold">{t("packageSources.artifacts")}</h3>
        <Button variant="secondary" size="sm" onClick={page.reload} disabled={page.loading}>{t("packageSources.refresh")}</Button>
      </div>
      <LoadState loading={page.loading && !page.data} error={page.error} onRetry={page.reload}>
        {page.data?.items.length ? (
          <div className="overflow-x-auto">
            <table className="tbl">
              <thead><tr><th>{t("packageSources.path")}</th><th>{t("packageSources.size")}</th><th>{t("packageSources.updated")}</th></tr></thead>
              <tbody>{page.data.items.map((file) => (
                <tr key={file.path}><td className="break-all font-mono">{file.path}</td><td className="whitespace-nowrap">{formatBytes(file.size)}</td><td className="whitespace-nowrap">{formatTime(file.updated_at)}</td></tr>
              ))}</tbody>
            </table>
          </div>
        ) : <Empty>{t("packageSources.noFiles")}</Empty>}
      </LoadState>
      {(previous || page.data?.next) && <div className="flex gap-2">
        {previous && <Button size="sm" variant="secondary" onClick={previous}>{t("packageSources.previous")}</Button>}
        {page.data?.next && <Button size="sm" variant="secondary" onClick={() => next(page.data!.next)}>{t("packageSources.next")}</Button>}
      </div>}
    </section>
  );
}
