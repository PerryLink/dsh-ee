import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { Link, useNavigate } from "react-router";
import { api } from "../lib/api";
import { useAuth } from "../lib/auth";
import { useAction, useApi } from "../lib/use-api";
import { ErrorNote } from "../components/api-ui";
import { Button, Card, Field, inputCls, PageHeader, PasswordInput } from "../components/ui";
import { LangSwitch } from "../components/lang-switch";
import { ThemeToggle } from "../components/theme-toggle";
import { useTranslation } from "react-i18next";
import { useLabels } from "../lib/labels";
import { PageLink, useMenus } from "../lib/menus";
import { useStore } from "../lib/store";
import type { PageKey } from "../lib/page-meta";
import { LANGUAGE_OPTIONS, isLanguageCode, setUiLanguage, type LanguageCode } from "../lib/languages";

/** 不在侧边栏里的接入页与开发页：员工侧栏只放每天用的页面，这些从账号页打开。 */
function Shortcuts() {
  const { t } = useTranslation();
  const { hasPage } = useMenus();
  const { settings } = useStore();
  const linkCls = "rounded-md border border-neutral-200 px-2.5 py-1 text-sm text-neutral-700 transition-colors hover:border-neutral-300 hover:bg-neutral-50 hover:text-neutral-900 dark:border-white/10";
  const pages = (keys: PageKey[]) => keys.filter((k) => hasPage(k) && (k !== "dsh-download" || settings.distribution.enabled));
  const connect = pages(["setup", "dsh-download", "my-keys"]);
  const develop = pages(["dev-docs", "dev-examples"]);
  const row = (label: string, keys: PageKey[], first?: ReactNode) => (
    <div className="flex flex-wrap items-center gap-2">
      <span className="w-14 shrink-0 text-sm text-neutral-600">{label}</span>
      {first}
      {keys.map((k) => (
        <PageLink key={k} page={k} className={linkCls}>
          {t(`pages.${k}`)}
        </PageLink>
      ))}
    </div>
  );
  return (
    <Card className="mt-4 p-5" data-account-shortcuts="">
      <h2 className="text-neutral-900 text-[15px] leading-6 font-semibold">{t("account.shortcuts.title")}</h2>
      <p className="mt-1 text-sm text-neutral-600">{t("account.shortcuts.hint")}</p>
      <div className="mt-4 space-y-3">
        {row(t("account.shortcuts.connect"), connect, <Link to="/get-started" className={linkCls}>{t("account.shortcuts.getStarted")}</Link>)}
        {develop.length > 0 && row(t("account.shortcuts.develop"), develop)}
      </div>
    </Card>
  );
}

function DingTalkConnection() {
  const { t } = useTranslation();
  const { data } = useApi(api.dingTalkChatStatus);
  if (!data?.available) return null;
  return (
    <Card className="mt-4 p-5" data-dingtalk-connection="">
      <h2 className="text-[15px] font-semibold leading-6 text-neutral-900">{t("oidcAdmin.dingTalkConnection")}</h2>
      <p className="mt-1 text-sm text-neutral-600">
        {t(data.bound ? "oidcAdmin.dingTalkBound" : "oidcAdmin.dingTalkUnbound")}
      </p>
      <p className="mt-1 text-sm text-neutral-600">
        {t(!data.enabled ? "oidcAdmin.dingTalkDisabled" : data.online ? "oidcAdmin.dingTalkOnline" : "oidcAdmin.dingTalkOffline")}
      </p>
    </Card>
  );
}

export default function Account() {
  const { t } = useTranslation();
  const L = useLabels();
  const { user, logout, clear } = useAuth();
  const navigate = useNavigate();
  const { busy, run } = useAction();
  const [oldPw, setOldPw] = useState("");
  const [newPw, setNewPw] = useState("");
  const [confirmPw, setConfirmPw] = useState("");
  const [error, setError] = useState<string | null>(null);
  // 空 = 跟随组织默认语言；有值 = 个人的规范语言短码。
  const [languagePref, setLanguagePref] = useState<"follow" | LanguageCode>(
    user && isLanguageCode(user.language) ? user.language : "follow",
  );
  const [orgLanguage, setOrgLanguage] = useState<LanguageCode>("zh");
  useEffect(() => {
    let alive = true;
    api
      .settings()
      .then((s) => {
        if (alive && isLanguageCode(s.defaultLanguage)) setOrgLanguage(s.defaultLanguage);
      })
      .catch(() => {
        // 组织默认语言读不到不影响个人选择：切换语言仍会尽量保存。
      });
    return () => {
      alive = false;
    };
  }, []);
  useEffect(() => {
    setLanguagePref(user && isLanguageCode(user.language) ? user.language : "follow");
  }, [user?.language]);

  if (!user) return null;

  // 界面立即切换；保存服务端是尽力而为，失败不阻塞切换（下次登录可能回到旧偏好）。
  const changeLanguage = async (value: string) => {
    if (value === "follow") {
      setLanguagePref("follow");
      setUiLanguage(orgLanguage);
    } else if (isLanguageCode(value)) {
      setLanguagePref(value);
      setUiLanguage(value);
    } else {
      return;
    }
    try {
      await api.updatePreferences({ language: value === "follow" ? "" : value });
    } catch {
      // 忽略：语言偏好属于界面设置，不应因为网络失败打断用户。
    }
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    if (newPw !== confirmPw) return setError(t("account.mismatch"));
    const r = await run(() => api.changePassword(oldPw, newPw));
    if (!r.ok) return setError(r.error);
    // 服务端已让所有会话失效，回登录页用新密码登录
    clear();
    navigate("/login?next=%2Faccount", { replace: true });
  };

  return (
    <div className="max-w-2xl">
      <PageHeader
        title={t("account.title")}
        actions={
          <Button
            variant="secondary"
            size="sm"
            onClick={() => void logout()}
          >
            {t("account.logout")}
          </Button>
        }
      />
      <Card className="mt-4 p-5">
        <h2 className="text-neutral-900 text-[15px] leading-6 font-semibold">{t("account.language")}</h2>
        <p className="mt-1 text-sm text-neutral-600">{t("account.languageHint")}</p>
        <div className="mt-4">
          <Field label={t("account.language")}>
            <select className={inputCls} value={languagePref} onChange={(e) => void changeLanguage(e.target.value)}>
              <option value="follow">{t("account.followOrg")}</option>
              {LANGUAGE_OPTIONS.map((l) => (
                <option key={l.code} value={l.code}>
                  {l.label}
                </option>
              ))}
            </select>
          </Field>
        </div>
      </Card>

      <Card className="p-5">
        <dl className="grid grid-cols-[6rem_1fr] gap-y-2 text-sm">
          <dt className="text-neutral-600">{t("account.username")}</dt>
          <dd className="text-neutral-900">{user.username}</dd>
          <dt className="text-neutral-600">{t("account.displayName")}</dt>
          <dd className="text-neutral-900">{user.display_name || "—"}</dd>
          <dt className="text-neutral-600">{t("account.department")}</dt>
          <dd className="text-neutral-900">{user.department || "—"}</dd>
          {user.email && (
            <>
              <dt className="text-neutral-600">{t("account.email")}</dt>
              <dd className="text-neutral-900">{user.email}</dd>
            </>
          )}
          <dt className="text-neutral-600">{t("account.source")}</dt>
          <dd className="text-neutral-900">
            {user.auth_source === "ldap" ? t("account.sourceLdap") : user.auth_source === "oidc" ? t("ssoLogin.sourceOidc") : t("account.sourceLocal")}
          </dd>
          <dt className="text-neutral-600">{t("account.roles")}</dt>
          <dd className="text-neutral-900">{(user.roles ?? []).map((r) => L.role(r)).join(", ") || t("shell.noRoles")}</dd>
        </dl>
      </Card>

      <Card className="mt-4 p-5">
        <h2 className="text-neutral-900 text-[15px] leading-6 font-semibold">{t("account.preferences")}</h2>
        <p className="mt-2 text-sm text-neutral-600">{t("account.preferencesHint")}</p>
        <div className="mt-4 flex flex-wrap items-center gap-x-8 gap-y-3">
          <div className="flex items-center gap-2">
            <span className="text-sm text-neutral-600">{t("account.language")}</span>
            <LangSwitch />
          </div>
          <div className="flex items-center gap-2">
            <span className="text-sm text-neutral-600">{t("account.theme")}</span>
            <ThemeToggle />
          </div>
        </div>
      </Card>

      <DingTalkConnection />
      <Shortcuts />

      {user.auth_source !== "local" ? (
        <Card className="mt-4 p-5">
          <h2 className="text-neutral-900 text-[15px] leading-6 font-semibold">{t("account.changePassword")}</h2>
          <p className="mt-2 text-sm text-neutral-600">{user.auth_source === "oidc" ? t("ssoLogin.oidcManaged") : t("account.ldapManaged")}</p>
        </Card>
      ) : (
        <Card className="mt-4 p-5">
          <form onSubmit={submit} className="space-y-4">
            <h2 className="text-neutral-900 text-[15px] leading-6 font-semibold">{t("account.changePassword")}</h2>
            <Field label={t("account.currentPassword")}>
              <PasswordInput label={t("account.currentPassword")} className={inputCls} autoComplete="current-password" required value={oldPw} onChange={(e) => setOldPw(e.target.value)} />
            </Field>
            <Field label={t("account.newPassword")} hint={t("account.newPasswordHint")}>
              <PasswordInput label={t("account.newPassword")} className={inputCls} autoComplete="new-password" required value={newPw} onChange={(e) => setNewPw(e.target.value)} />
            </Field>
            <Field label={t("account.confirmPassword")}>
              <PasswordInput label={t("account.confirmPassword")} className={inputCls} autoComplete="new-password" required value={confirmPw} onChange={(e) => setConfirmPw(e.target.value)} />
            </Field>
            <ErrorNote>{error}</ErrorNote>
            <Button type="submit" disabled={busy || !oldPw || !newPw}>
              {busy ? t("common.submitting") : t("account.changePassword")}
            </Button>
          </form>
        </Card>
      )}
    </div>
  );
}
