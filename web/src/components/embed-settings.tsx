import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { api } from "../lib/api";
import { useStore } from "../lib/store";
import { useApi } from "../lib/use-api";
import { Badge, Button, Field, Notice, ToggleRow, cx, inputCls } from "./ui";

/** 一行一个：去掉空行与首尾空白。 */
const lines = (text: string) => text.split("\n").map((x) => x.trim()).filter(Boolean);

const LOOPBACK = new Set(["localhost", "127.0.0.1", "[::1]"]);

/** 与服务端同一套规则的轻量前端校验：完整 https origin，http 只允许回环地址。 */
function validOrigin(raw: string): boolean {
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return false;
  }
  if (u.protocol !== "https:" && !(u.protocol === "http:" && LOOPBACK.has(u.hostname))) return false;
  return !u.username && !u.password && (u.pathname === "" || u.pathname === "/") && !u.search && !u.hash;
}

/**
 * 系统设置里的「跨站嵌入」分区：
 * - 部署环境变量（DSH_EE_EMBED_ORIGINS / DSH_EE_EMBED_CROSS_SITE / DSH_EE_EMBED_SESSION_TTL）是只读基线；
 * - 这里的值保存到 settings.embed，显式值覆盖环境变量、来源追加到环境变量之后；
 * - 保存走整份设置（StoreProvider 合并保存），服务端校验后立即生效（CSP 与 mint 同一份有效配置）。
 */
export function EmbedSettings() {
  const { t } = useTranslation();
  const { settings, updateSettings, notify } = useStore();
  const info = useApi(api.adminEmbedSettings);
  const env = info.data?.env;
  const effective = info.data?.effective;
  const embed = settings.embed ?? { crossSite: null, allowedOrigins: [], apps: [], sessionMinutes: 0 };

  const [originsText, setOriginsText] = useState("");
  const [appsText, setAppsText] = useState("");
  const [minutesText, setMinutesText] = useState("0");

  // 服务端值变化（首次加载、保存成功返回）时同步输入框。
  useEffect(() => {
    setOriginsText((embed.allowedOrigins ?? []).join("\n"));
    setAppsText((embed.apps ?? []).join("\n"));
    setMinutesText(String(embed.sessionMinutes ?? 0));
  }, [embed.allowedOrigins, embed.apps, embed.sessionMinutes]);

  const setEmbed = (patch: Partial<NonNullable<typeof settings.embed>>) =>
    updateSettings((cur) => ({ ...cur, embed: { ...(cur.embed ?? { allowedOrigins: [], apps: [], sessionMinutes: 0 }), ...patch } }));

  const refreshEffective = () => window.setTimeout(() => info.reload(), 900);

  const commitOrigins = () => {
    const values = lines(originsText);
    for (const origin of values) {
      if (!validOrigin(origin)) {
        notify(t("settingsAdmin.embed.originInvalid", { origin }));
        setOriginsText((embed.allowedOrigins ?? []).join("\n"));
        return;
      }
    }
    if (values.join("\n") !== (embed.allowedOrigins ?? []).join("\n")) {
      setEmbed({ allowedOrigins: values });
      refreshEffective();
    }
  };

  const commitApps = () => {
    const values = lines(appsText);
    for (const app of values) {
      if (!/^[a-z0-9-]{1,48}$/.test(app)) {
        notify(t("settingsAdmin.embed.appInvalid", { app }));
        setAppsText((embed.apps ?? []).join("\n"));
        return;
      }
    }
    if (values.join("\n") !== (embed.apps ?? []).join("\n")) {
      setEmbed({ apps: values });
      refreshEffective();
    }
  };

  const commitMinutes = () => {
    const n = Number(minutesText);
    if (!Number.isInteger(n) || n < 0 || n > 480) {
      notify(t("settingsAdmin.embed.minutesInvalid"));
      setMinutesText(String(embed.sessionMinutes ?? 0));
      return;
    }
    if (n !== (embed.sessionMinutes ?? 0)) {
      setEmbed({ sessionMinutes: n });
      refreshEffective();
    }
  };

  const envMinutes = env ? Math.round(env.sessionSeconds / 60) : null;
  const followEnv = embed.crossSite === null || embed.crossSite === undefined;

  return (
    <div className="space-y-5">
      <Notice>
        <p className="text-sm leading-6">{t("settingsAdmin.embed.desc")}</p>
        <p className="mt-1 text-xs leading-5 text-neutral-500">{t("settingsAdmin.embed.saveHint")}</p>
      </Notice>

      <ToggleRow
        label={t("settingsAdmin.embed.crossSite")}
        desc={t("settingsAdmin.embed.crossSiteDesc")}
        checked={embed.crossSite ?? env?.crossSite ?? false}
        onChange={(v) => {
          setEmbed({ crossSite: v });
          refreshEffective();
        }}
      />
      <div className="flex flex-wrap items-center gap-2 text-xs text-neutral-500">
        {followEnv ? (
          <Badge>{t("settingsAdmin.embed.followEnv")}</Badge>
        ) : (
          <Badge tone="amber">{t("settingsAdmin.embed.dbOverride")}</Badge>
        )}
        <Button
          variant="secondary"
          size="sm"
          disabled={followEnv}
          onClick={() => {
            setEmbed({ crossSite: null });
            refreshEffective();
          }}
        >
          {t("settingsAdmin.embed.resetToEnv")}
        </Button>
      </div>

      <Field label={t("settingsAdmin.embed.origins")} hint={t("settingsAdmin.embed.originsDesc")}>
        <div className="space-y-2">
          {env && env.allowedOrigins.length > 0 && (
            <div className="flex flex-wrap items-center gap-1.5 text-xs">
              <span className="text-neutral-500">{t("settingsAdmin.embed.envOrigins")}</span>
              {env.allowedOrigins.map((origin) => (
                <span key={origin} className="rounded border border-neutral-200 bg-neutral-50 px-1.5 py-0.5 font-mono text-neutral-600 dark:border-white/10 dark:bg-white/5 dark:text-neutral-300">
                  {origin}
                </span>
              ))}
              <span className="text-neutral-400">· {t("settingsAdmin.embed.readonly")}</span>
            </div>
          )}
          <textarea
            className={cx(inputCls, "min-h-24 font-mono text-xs")}
            value={originsText}
            placeholder="https://harness.acme.internal"
            onChange={(e) => setOriginsText(e.target.value)}
            onBlur={commitOrigins}
          />
        </div>
      </Field>

      <Field label={t("settingsAdmin.embed.apps")} hint={t("settingsAdmin.embed.appsDesc")}>
        <textarea
          className={cx(inputCls, "min-h-20 font-mono text-xs")}
          value={appsText}
          placeholder={t("settingsAdmin.embed.appsPlaceholder")}
          onChange={(e) => setAppsText(e.target.value)}
          onBlur={commitApps}
        />
      </Field>

      <Field label={t("settingsAdmin.embed.sessionMinutes")} hint={t("settingsAdmin.embed.sessionMinutesDesc")}>
        <div className="flex flex-wrap items-center gap-2">
          <input
            className={cx(inputCls, "w-24 tabular-nums")}
            type="number"
            min={0}
            max={480}
            value={minutesText}
            onChange={(e) => setMinutesText(e.target.value)}
            onBlur={commitMinutes}
          />
          <span className="text-xs text-neutral-500">
            {envMinutes !== null ? t("settingsAdmin.embed.envSession", { minutes: envMinutes }) : null}
            {embed.sessionMinutes === 0 ? ` · ${t("settingsAdmin.embed.followEnv")}` : ""}
          </span>
        </div>
      </Field>

      {info.error && <Notice tone="warn">{info.error}</Notice>}
      {effective && (
        <div className="rounded-md border border-neutral-200/70 px-3 py-2 text-xs leading-5 text-neutral-500 dark:border-white/10">
          <span className="font-medium text-neutral-700 dark:text-neutral-200">{t("settingsAdmin.embed.effective")}</span>
          {" · "}
          {effective.crossSite ? t("settingsAdmin.embed.on") : t("settingsAdmin.embed.off")}
          {" · "}
          {t("settingsAdmin.embed.effectiveTTL", { minutes: Math.round(effective.sessionSeconds / 60) })}
          {" · "}
          {effective.apps.length === 0 ? t("settingsAdmin.embed.allApps") : effective.apps.join("、")}
          {" · "}
          <span className="font-mono">{effective.allowedOrigins.join(" ")}</span>
        </div>
      )}
    </div>
  );
}
