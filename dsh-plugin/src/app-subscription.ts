import type { HttpClient } from "./http.js";
import { t } from "./i18n/index.js";

const nameRe = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

/**
 * 在 DSH 里启用或停用一个「自选」微应用（与网页上的启用是同一件事）。
 * 只给接入插件的界面用，不注册成工具：模型不能替员工改这项配置。
 * 成功后由调用方立刻刷新目录，让 /名字 马上可用或消失。
 */
export async function setAppSubscription(http: HttpClient, name: string, enabled: boolean): Promise<{ ok: true } | { ok: false; error: string }> {
  if (typeof name !== "string" || !nameRe.test(name) || name.length > 48) return { ok: false, error: t("appSubscription.invalidAppName") };
  const response = await http.fetch(`/api/dsh/apps/${encodeURIComponent(name)}/subscription`, { method: enabled ? "POST" : "DELETE" });
  if (response.ok) return { ok: true };
  let error = `HTTP ${response.status}`;
  try {
    const body = await response.json() as { error?: string };
    if (body?.error) error = body.error;
  } catch {
    // 不是 JSON 就用状态码
  }
  return { ok: false, error };
}
