import { useTranslation } from "react-i18next";
import type { ConnectInfo } from "../lib/api";
import { useAuth } from "../lib/auth";
import { PageLink } from "../lib/menus";
import { Notice } from "./ui";

/** 「访问地址」没配时只提醒管理员；员工照常只看到一个地址。 */
export function PublicUrlNotice({ info, className }: { info: ConnectInfo | null | undefined; className?: string }) {
  const { t } = useTranslation();
  const { can } = useAuth();
  if (!info || info.base_url_set || !info.base_url || !can("settings.manage")) return null;
  return (
    <Notice tone="warn" className={className}>
      <span data-public-url-unset="">{t("shell.publicUrl.unset", { url: info.base_url })}</span>{" "}
      <PageLink page="admin-settings" className="font-medium underline">
        {t("shell.publicUrl.action")}
      </PageLink>
    </Notice>
  );
}
