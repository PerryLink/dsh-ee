import { useState } from "react";
import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import { api } from "../lib/api";
import { useStore } from "../lib/store";
import { useMenus, PageLink } from "../lib/menus";
import { useAction, useApi } from "../lib/use-api";
import { Button, Card } from "../components/ui";
import { AgentConnect } from "../components/agent-connect";
import { PublicUrlNotice } from "../components/public-url-notice";

/** 员工第一次登录看到的「开始使用」：把一段话贴进 DSH，自动装好企业插件并登录，之后 Agent 带着认识能用的能力。 */
export default function GetStarted() {
  const { t } = useTranslation();
  const nav = useNavigate();
  const { settings } = useStore();
  const { homePath } = useMenus();
  const { busy, run } = useAction();
  const [connected, setConnected] = useState(false);
  const home = homePath || "/";

  const connectInfo = useApi(api.connectInfo);
  const later = async () => {
    await run(() => api.putWelcome(true));
    nav(home, { replace: true });
  };

  return (
    <div className="min-h-screen bg-neutral-50">
      <div className="mx-auto max-w-3xl px-5 py-10">
        <div className="flex items-start justify-between gap-4">
          <div>
            <h1 className="text-2xl text-neutral-900 leading-8 font-bold tracking-[-0.02em]">{t("getStarted.title", { org: settings.orgName || "dsh-ee" })}</h1>
            <p className="mt-1 text-sm text-neutral-600">{t("getStarted.subtitle")}</p>
          </div>
          {!connected && (
            <Button variant="ghost" className="shrink-0" disabled={busy} onClick={() => void later()}>
              {t("getStarted.later")}
            </Button>
          )}
        </div>

        <PublicUrlNotice info={connectInfo.data} className="mt-6" />
        <Card className="mt-6 p-6">
          <AgentConnect admin={false} onConnected={() => setConnected(true)} />
        </Card>

        <div className="mt-4 flex flex-wrap items-center justify-between gap-3">
          <p className="text-xs text-neutral-500">
            {t("getStarted.other")}{" "}
            <PageLink page="setup" className="text-brand-700 hover:underline dark:text-brand-300">
              {t("getStarted.otherLink")}
            </PageLink>
          </p>
          {connected && <Button onClick={() => nav(home, { replace: true })}>{t("getStarted.enter")}</Button>}
        </div>
      </div>
    </div>
  );
}
