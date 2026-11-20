import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { api, type OidcView } from "../../lib/api";
import { useApi } from "../../lib/use-api";
import { PageHeader } from "../../components/ui";
import LdapSettings from "./ldap-settings";
import OidcSettings from "./oidc-settings";
import { Section } from "./settings";

/** 登录方式：LDAP / AD 与企业统一登录（OIDC），从系统设置拆出来。 */
export default function AdminAuth() {
  const { t } = useTranslation();
  // 第二登录入口只在主登录方式是飞书或钉钉并已开启时可配，而且只能配另一个
  const { data } = useApi(() => api.oidc());
  const [primary, setPrimary] = useState<OidcView | null>(null);
  useEffect(() => {
    if (data) setPrimary(data);
  }, [data]);
  const other = !primary?.enabled ? null : primary.preset === "feishu" ? "dingtalk" : primary.preset === "dingtalk" ? "feishu" : null;
  return (
    <div className="max-w-3xl">
      <PageHeader title={t("pages.admin-auth")} desc={t("pageDesc.admin-auth")} />
      <div className="space-y-6">
        <Section id="auth-ldap" title={t("settingsAdmin.auth")} desc={t("settingsAdmin.authNote")}>
          <LdapSettings />
        </Section>
        <Section id="auth-oidc" title={t("oidcAdmin.title")}>
          <OidcSettings onSaved={setPrimary} />
        </Section>
        <Section id="auth-oidc-secondary" title={t("oidcAdmin.secondary.title")} desc={t("oidcAdmin.secondary.desc")}>
          {other ? <OidcSettings key={other} slot="secondary" only={other} /> : <p className="text-sm text-neutral-600">{t("oidcAdmin.secondary.needPrimary")}</p>}
        </Section>
      </div>
    </div>
  );
}
