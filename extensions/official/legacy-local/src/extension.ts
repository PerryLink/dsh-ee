/**
 * 端侧扩展模板（docs/plugins.md §8、§10 的 official/legacy-local）：一个没有接口、只能用员工自己登录态的老 OA。
 *
 * 做法：员工第一次用时弹出 OA 自己的登录页（webSignIn），登录后 cookie 存在他自己的电脑上；
 * 之后每次调用带着这个 cookie 发 HTTP 请求，从页面或内部接口里取数。写操作（提交请假单）执行前由 SDK 先经 dsh-ee 让员工确认。
 *
 * 复制它：改 id、package、OA_URL，把 listForms / submitLeave 换成对你们系统的请求。
 */
import { CookieStore, ExtError, cookieHeader, defineExtension, webSignIn } from "@dsh-ee/extension-local";

const ID = "acme/legacy-oa";
/** 老 OA 的地址：只有员工的电脑（内网 / VPN）访问得到 */
const OA_URL = (process.env.LEGACY_OA_URL ?? "http://oa.acme.internal").replace(/\/+$/, "");
/** 登录成功后 OA 会种下的 cookie */
const SESSION_COOKIE = process.env.LEGACY_OA_COOKIE ?? "JSESSIONID";

const store = new CookieStore(ID);

/** 带着员工的 cookie 请求 OA；没登录或登录过期时让员工登录一次再重试。 */
async function oa(path: string, init: RequestInit = {}, signal?: AbortSignal): Promise<Response> {
  for (let attempt = 0; attempt < 2; attempt++) {
    if (!store.has([SESSION_COOKIE])) {
      await webSignIn({ url: `${OA_URL}/login`, cookiesOf: [SESSION_COOKIE], store });
    }
    const url = `${OA_URL}${path}`;
    const r = await fetch(url, { ...init, signal, redirect: "manual", headers: { ...(init.headers ?? {}), cookie: cookieHeader(store.load(), url) } });
    // 老系统登录过期一般是 302 到登录页，或者 401
    if (r.status === 401 || (r.status >= 300 && r.status < 400 && (r.headers.get("location") ?? "").includes("login"))) {
      store.clear();
      continue;
    }
    if (r.status === 403) throw new ExtError("forbidden", "OA 说你没有这个权限");
    if (!r.ok) throw new ExtError("unavailable", `OA 返回 HTTP ${r.status}`);
    return r;
  }
  throw new ExtError("authorize", "还没有登录老 OA：请在弹出的窗口里登录后再试");
}

export default defineExtension({
  id: ID,
  version: "0.1.0",
  name: { zh: "老 OA（本机）", en: "Legacy OA (local)" },
  description: { zh: "用你自己的 OA 登录态查待办、提交请假单。在你自己的电脑上执行。", en: "Query and submit forms in the legacy OA with your own login, on your own computer." },
  package: "@acme/legacy-oa-local",
  minVersion: "0.3.0",
  tools: [
    {
      name: "list_forms",
      description: { zh: "列出我在老 OA 里的审批单", en: "List my forms in the legacy OA" },
      readOnly: true,
      params: [{ name: "status", type: "string", enum: ["pending", "done"], description: "pending：待办；done：已办" }],
      handler: async (args, { signal }) => {
        const status = args.status === "done" ? "done" : "pending";
        const r = await oa(`/api/forms?status=${status}`, {}, signal);
        // 只把模型需要的字段交出去
        const items = (await r.json()) as { id: string; title: string; status: string }[];
        return items.map((f) => ({ id: f.id, title: f.title, status: f.status }));
      },
    },
    {
      // 没标 readOnly：执行前 SDK 会先经 dsh-ee 让员工在网页上确认
      name: "submit_leave",
      description: { zh: "提交一张请假单", en: "Submit a leave request" },
      params: [
        { name: "days", type: "number", required: true, description: "请几天" },
        { name: "reason", type: "string", description: "事由" },
      ],
      handler: async (args, { signal }) => {
        const days = Number(args.days);
        if (!Number.isFinite(days) || days <= 0 || days > 30) throw new ExtError("invalid", "days 要是 1～30 之间的数字");
        const r = await oa(
          "/api/forms",
          { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ type: "leave", days, reason: String(args.reason ?? "") }) },
          signal,
        );
        const f = (await r.json()) as { id: string };
        return `已提交请假单 ${f.id}`;
      },
    },
  ],
});
