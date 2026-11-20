import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useAuth } from "../lib/auth";
import { api } from "../lib/api";
import { Button, inputCls, PasswordInput, Spinner } from "../components/ui";
import { ErrorNote } from "../components/api-ui";
import { LangSwitch } from "../components/lang-switch";
import { ThemeToggle } from "../components/theme-toggle";

/** 全新部署的第一个页面：凭服务端日志里的初始化码创建第一个管理员，建好后直接登录进入配置向导。 */
export default function InitAdmin({ onDone }: { onDone: () => void }) {
  const { t } = useTranslation();
  const { refresh } = useAuth();
  const [code, setCode] = useState("");
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const mismatch = confirm !== "" && confirm !== password;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (mismatch) return;
    setBusy(true);
    setError(null);
    try {
      await api.initAdmin(code.trim(), username.trim(), password);
      await refresh();
      onDone();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="auth-bg relative flex min-h-screen flex-col items-center justify-center overflow-hidden px-4 py-10">
      <div aria-hidden className="auth-stripe pointer-events-none absolute inset-x-0 top-0 h-[52vh] min-h-80 dark:opacity-80" />
      <form onSubmit={submit} className="relative w-full max-w-[400px] rounded-xl bg-card p-7 shadow-modal sm:p-8">
        <h1 className="text-[22px] leading-7 font-bold tracking-[-0.02em] text-neutral-900">{t("initAdmin.title")}</h1>
        <p className="mt-1.5 text-sm leading-6 text-neutral-600">{t("initAdmin.subtitle")}</p>
        <div className="mt-6 space-y-4">
          <label className="block">
            <span className="mb-1.5 block text-[13px] font-medium text-neutral-700">{t("initAdmin.code")}</span>
            <input className={`${inputCls} font-mono uppercase`} autoComplete="one-time-code" autoFocus required value={code} onChange={(e) => setCode(e.target.value)} placeholder="XXXX-XXXX-XXXX" />
            <span className="mt-1 block text-xs leading-5 break-all text-neutral-500">{t("initAdmin.codeHint")}</span>
          </label>
          <label className="block">
            <span className="mb-1.5 block text-[13px] font-medium text-neutral-700">{t("initAdmin.username")}</span>
            <input className={inputCls} autoComplete="username" required value={username} onChange={(e) => setUsername(e.target.value)} />
          </label>
          <label className="block">
            <span className="mb-1.5 block text-[13px] font-medium text-neutral-700">{t("initAdmin.password")}</span>
            <PasswordInput label={t("initAdmin.password")} className={inputCls} autoComplete="new-password" required minLength={10} value={password} onChange={(e) => setPassword(e.target.value)} />
            <span className="mt-1 block text-xs text-neutral-500">{t("initAdmin.passwordHint")}</span>
          </label>
          <label className="block">
            <span className="mb-1.5 block text-[13px] font-medium text-neutral-700">{t("initAdmin.confirm")}</span>
            <PasswordInput label={t("initAdmin.confirm")} className={inputCls} autoComplete="new-password" required value={confirm} onChange={(e) => setConfirm(e.target.value)} />
          </label>
        </div>
        {(error ?? (mismatch ? t("initAdmin.mismatch") : null)) && (
          <div className="mt-4">
            <ErrorNote>{error ?? t("initAdmin.mismatch")}</ErrorNote>
          </div>
        )}
        <Button type="submit" size="lg" className="mt-6 w-full" disabled={busy || !code || !username || password.length < 10 || confirm !== password}>
          {busy && <Spinner />}
          {busy ? t("initAdmin.submitting") : t("initAdmin.submit")}
        </Button>
      </form>
      <div className="relative mt-6 flex items-center justify-center gap-2">
        <LangSwitch />
        <ThemeToggle />
      </div>
    </div>
  );
}
