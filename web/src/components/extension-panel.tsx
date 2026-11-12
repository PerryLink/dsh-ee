import { useTranslation } from "react-i18next";
import { Activity } from "lucide-react";
import { formatTime } from "../lib/api";
import { extApi } from "../lib/ext-api";
import { useAction, useApi } from "../lib/use-api";
import { useStore } from "../lib/store";
import { Badge, Button, Card, CardHeader, CopyBox, Notice } from "./ui";

/** 值是 {"$enc": …} 的是 secret：不显示内容。 */
function configValue(v: unknown, hidden: string) {
  // 服务端把 secret 类型的值显示成 {"set": true}（存的是 {"$enc": …}）：不显示内容
  if (v && typeof v === "object" && ("$enc" in (v as object) || "set" in (v as object))) return (v as { set?: boolean }).set === false ? "—" : hidden;
  if (v === null || v === undefined || v === "") return "—";
  return typeof v === "string" ? v : JSON.stringify(v);
}

/**
 * 连接详情页上给管理员看的「扩展」（docs/plugins.md §4.2、§6.3、§7.3、§9.4）：
 * 健康检查、清单是否变了、回调地址、OAuth 回调地址、管理员填的配置。
 */
export function ExtensionPanel({ connectionId }: { connectionId: number }) {
  const { t } = useTranslation();
  const { notify } = useStore();
  const { busy, run } = useAction();
  const info = useApi(() => extApi.extInfo(connectionId));
  const e = info.data;
  if (!e) return null;
  const h = e.health;
  return (
    <Card data-extension-panel>
      <CardHeader
        title={t("extAdmin.title")}
        aside={
          <span className="flex items-center gap-2">
            <Badge tone="brand">{t(`extAdmin.kind.${e.kind}`)}</Badge>
            <span className="font-mono text-xs text-neutral-500">
              {e.id}@{e.version}
            </span>
          </span>
        }
      />
      <div className="space-y-4 px-5 pb-5 text-sm">
        {e.kind === "service" && (
          <div className="flex flex-wrap items-center gap-3">
            <span className="text-neutral-500">{t("extAdmin.health")}</span>
            {h ? (
              <>
                {h.ok ? <Badge tone="green">{t("extAdmin.healthy")}</Badge> : <Badge tone="red">{t("extAdmin.unhealthy", { error: h.error ?? "" })}</Badge>}
                <span className="text-xs text-neutral-500">{t("extAdmin.checkedAt", { time: formatTime(h.checked_at) })}</span>
              </>
            ) : (
              <span className="text-neutral-500">{t("extAdmin.never")}</span>
            )}
            <Button
              size="sm"
              variant="secondary"
              disabled={busy}
              onClick={async () => {
                const r = await run(() => extApi.extHealth(connectionId));
                if (!r.ok) notify(r.error);
                info.reload();
              }}
            >
              <Activity size={14} /> {t("extAdmin.check")}
            </Button>
          </div>
        )}
        {h?.changed && <Notice tone="warn">{t("extAdmin.changed", { version: h.new_version ?? "" })}</Notice>}
        {e.service_url && (
          <div>
            <div className="mb-1 text-xs text-neutral-500">{t("extAdmin.serviceUrl")}</div>
            <span className="font-mono text-xs break-all">{e.service_url}</span>
          </div>
        )}
        {e.endpoint_url && (
          <div>
            <div className="mb-1 text-xs text-neutral-500">{t("extAdmin.endpoint")}</div>
            <CopyBox text={e.endpoint_url} wrap />
            <p className="mt-1 text-xs text-neutral-500">{t("extAdmin.endpointHint")}</p>
          </div>
        )}
        {e.oauth_redirect_uri && (
          <div>
            <div className="mb-1 text-xs text-neutral-500">
              {t("extAdmin.oauthRedirect")}
              {e.oauth_client_id && <span className="ml-2 font-mono">client_id = {e.oauth_client_id}</span>}
            </div>
            <CopyBox text={e.oauth_redirect_uri} wrap />
            <p className="mt-1 text-xs text-neutral-500">{t("extAdmin.oauthRedirectHint")}</p>
          </div>
        )}
        {e.config && Object.keys(e.config).length > 0 && (
          <div>
            <div className="mb-1 text-xs text-neutral-500">{t("extAdmin.config")}</div>
            <dl className="grid grid-cols-[10rem_1fr] gap-x-3 gap-y-1">
              {Object.entries(e.config).map(([k, v]) => (
                <div key={k} className="contents">
                  <dt className="font-mono text-xs text-neutral-600">{k}</dt>
                  <dd className="font-mono text-xs break-all">{configValue(v, t("extAdmin.secretHidden"))}</dd>
                </div>
              ))}
            </dl>
          </div>
        )}
      </div>
    </Card>
  );
}
