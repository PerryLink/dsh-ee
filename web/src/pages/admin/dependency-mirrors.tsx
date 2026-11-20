import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Card, Field, inputCls } from "../../components/ui";
import { useStore } from "../../lib/store";
import { useAction } from "../../lib/use-api";
import type { Settings } from "../../lib/types";
import { useAuth } from "../../lib/auth";

const endpoints = {
  pypi: "https://nexus.corp/repository/pypi-group/simple",
  maven: "https://nexus.corp/repository/maven-public/",
};

export default function DependencyMirrorsCard() {
  const { t } = useTranslation();
  const { settings, saveMirrors, notify } = useStore();
  const { can } = useAuth();
  const editable = can("settings.manage", "registry.manage");
  const [draft, setDraft] = useState(settings.mirrors);
  const [dirty, setDirty] = useState(false);
  const [secrets, setSecrets] = useState({ pypi: "", maven: "" });
  const { busy, run } = useAction();
  useEffect(() => { if (!dirty) setDraft(settings.mirrors); }, [settings.mirrors, dirty]);
  const patch = (kind: keyof typeof endpoints, value: Partial<Settings["mirrors"]["pypi"]>) => {
    setDirty(true);
    setDraft((current) => ({ ...current, [kind]: { ...current[kind], ...value } }));
  };
  const invalid = (Object.keys(endpoints) as (keyof typeof endpoints)[]).some((kind) =>
    draft[kind].url.trim() && (secrets[kind] || draft[kind].hasSecret) && !draft[kind].username.trim());
  return (
    <Card className="mt-4 p-5">
      <h2 className="text-[15px] font-semibold text-neutral-900">{t("dependencyMirrors.title")}</h2>
      <p className="mt-1 text-sm text-neutral-600">{t("dependencyMirrors.description")}</p>
      <p className="mt-2 text-sm text-neutral-600">{t("dependencyMirrors.warning")}</p>
      <form className="mt-4" onSubmit={async (event) => {
        event.preventDefault();
        if (!editable) return;
        const result = await run(() => saveMirrors({
          pypi: { ...draft.pypi, secret: secrets.pypi },
          maven: { ...draft.maven, secret: secrets.maven },
        }));
        if (!result.ok) return notify(result.error);
        setSecrets({ pypi: "", maven: "" });
        setDirty(false);
        notify(t("dependencyMirrors.saved"));
      }}>
        <fieldset disabled={busy || !editable} className="space-y-4 disabled:opacity-60">
          <div className="grid gap-5 lg:grid-cols-2">
            {(Object.keys(endpoints) as (keyof typeof endpoints)[]).map((kind) => (
              <section key={kind} className="space-y-3">
                <h3 className="text-sm font-medium text-neutral-900">{t(`dependencyMirrors.${kind}`)}</h3>
                <Field label={t("dependencyMirrors.url")} hint={t("dependencyMirrors.clearHint")}>
                  <input aria-label={t(`dependencyMirrors.${kind}`) + " " + t("dependencyMirrors.url")} type="url" className={inputCls} value={draft[kind].url} placeholder={endpoints[kind]} onChange={(e) => patch(kind, { url: e.target.value })} />
                </Field>
                <Field label={t("dependencyMirrors.username")}>
                  <input aria-label={t(`dependencyMirrors.${kind}`) + " " + t("dependencyMirrors.username")} autoComplete="off" className={inputCls} value={draft[kind].username} onChange={(e) => patch(kind, { username: e.target.value })} />
                </Field>
                <Field label={t("dependencyMirrors.secret")}>
                  <input aria-label={t(`dependencyMirrors.${kind}`) + " " + t("dependencyMirrors.secret")} type="password" autoComplete="new-password" className={inputCls} value={secrets[kind]} placeholder={draft[kind].hasSecret ? t("dependencyMirrors.secretSaved") : t("dependencyMirrors.optional")} onChange={(e) => { setDirty(true); setSecrets((current) => ({ ...current, [kind]: e.target.value })); }} />
                </Field>
              </section>
            ))}
          </div>
          <Button type="submit" disabled={!dirty || invalid}>{busy ? t("dependencyMirrors.saving") : t("dependencyMirrors.save")}</Button>
        </fieldset>
      </form>
    </Card>
  );
}
