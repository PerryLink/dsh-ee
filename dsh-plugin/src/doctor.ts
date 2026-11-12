import { npmrcPath } from "./config.js";
import { npmrcState } from "./token.js";
import type { McpBridge } from "./bridge.js";
import type { DshContext, PluginConfig } from "./dsh.js";
import type { HttpClient } from "./http.js";
import type { TokenManager } from "./token.js";
import type { AppBindings } from "./app-bindings.js";

export interface Check {
  id: string;
  status: "ok" | "warn" | "fail";
  title: string;
  detail?: string;
  fix?: string;
}

/** 比较两个 x.y.z[-pre] 版本，a 比 b 新返回正数。 */
export function compareVersions(a: string, b: string): number {
  const parse = (v: string) => {
    const [core, pre] = v.replace(/^v/, "").split("-", 2);
    return { nums: core.split(".").map((n) => Number.parseInt(n, 10) || 0), pre };
  };
  const x = parse(a);
  const y = parse(b);
  for (let i = 0; i < Math.max(x.nums.length, y.nums.length); i++) {
    const d = (x.nums[i] ?? 0) - (y.nums[i] ?? 0);
    if (d) return d;
  }
  if (x.pre === y.pre) return 0;
  if (!x.pre) return 1;
  if (!y.pre) return -1;
  return x.pre < y.pre ? -1 : 1;
}

export async function runLocalDoctor(ctx: DshContext, config: PluginConfig, tokens: TokenManager, http: HttpClient, bridge: McpBridge, bindings?: AppBindings): Promise<{ summary: string; checks: Check[] }> {
  const checks: Check[] = [];
  const add = (check: Check) => checks.push(check);
  if (bindings?.warning()) add({ id: "app_bindings", status: "warn", title: bindings.warning(), fix: "重新打开微应用会建立新绑定；原损坏文件无需手动删除" });

  const current = await tokens.resolve();
  if (!current?.value) {
    add({ id: "credential", status: "fail", title: "本机没有企业登录凭据", fix: "在对话框输入 /ee-login 登录" });
    return { summary: summarize(checks), checks };
  }
  add({ id: "credential", status: "ok", title: "本机有企业登录凭据", detail: `来源：${current.source}` });

  // 令牌是否还有效
  let reachable = true;
  try {
    const response = await http.fetch("/api/dsh/whoami");
    if (response.ok) {
      const me = await response.json() as { username: string; department: string; expires_at?: string };
      const days = me.expires_at ? (new Date(me.expires_at).getTime() - Date.now()) / 86_400_000 : Infinity;
      add(days < 7
        ? { id: "token", status: "warn", title: `登录将在 ${Math.max(0, Math.ceil(days))} 天内到期`, detail: me.username, fix: "到期前输入 /ee-login 重新登录" }
        : { id: "token", status: "ok", title: `已登录 ${me.username}（${me.department}）` });
    } else {
      const body = await response.json().catch(() => null) as { code?: string } | null;
      if (response.status === 401) {
        add({ id: "token", status: "fail", title: "企业登录已失效", detail: body?.code ?? "HTTP 401", fix: "在对话框输入 /ee-login 重新登录" });
      } else {
        add({ id: "token", status: "fail", title: `读取企业账号失败（HTTP ${response.status}）`, detail: "dsh-ee 可能正在重启或暂时不可用；不是登录失效，稍后再试", fix: "稍等片刻重新运行诊断；持续失败请检查服务端状态" });
      }
    }
  } catch {
    reachable = false;
    add({ id: "token", status: "fail", title: `连不上 dsh-ee（${config.base}）`, fix: "检查网络或 VPN；地址不对的话在 DSH 的「设置 → dsh-ee」里改成浏览器能打开的 dsh-ee 地址并测试" });
  }

  // npm 源令牌
  const npmrc = npmrcState(ctx, config, current.value);
  add(npmrc === "ok"
    ? { id: "npmrc", status: "ok", title: "DSH 的 .npmrc 里企业源配置与当前登录一致", detail: npmrcPath(ctx) }
    : {
      id: "npmrc",
      status: "warn",
      title: npmrc === "missing" ? "DSH 的 .npmrc 里缺少企业 npm 源配置" : "DSH 的 .npmrc 里企业源令牌与当前登录不一致",
      detail: `${npmrcPath(ctx)}；装企业插件会因 401 失败`,
      fix: "重启 DSH 或重新登录会自动同步",
    });

  if (!reachable) return { summary: summarize(checks), checks };

  // 模型
  let models: string[] = [];
  try {
    const response = await http.fetch("/v1/models");
    if (response.ok) models = ((await response.json()) as { data: { id: string }[] }).data.map((m) => m.id);
    add(models.length
      ? { id: "models", status: "ok", title: `可用模型 ${models.join("、")}` }
      : { id: "models", status: "fail", title: response.ok ? "没有可用的模型" : `读取模型列表失败（HTTP ${response.status}）`, fix: "联系管理员开通模型权限" });
  } catch {
    add({ id: "models", status: "fail", title: "读取模型列表失败" });
  }

  // 服务端检查（账号、额度、模型、目录），以及版本与必装插件的比对依据
  let server: any;
  try {
    const result = await bridge.callTool("ee_doctor", { action: "check" });
    if (!result?.isError) server = JSON.parse(result.content[0].text);
  } catch {
    // Agent 关闭或令牌没有 agent 范围：服务端检查不可用，不算失败
  }
  if (server) {
    for (const c of server.checks ?? []) add({ ...c, id: `server.${c.id}` });
    if (server.default_model && models.length && !models.includes(server.default_model)) {
      add({ id: "default_model", status: "warn", title: `企业默认模型 ${server.default_model} 不在你的可用列表里`, fix: "在 DSH 里改选可用的模型" });
    }
    if (server.connect_version && config.version !== "dev") {
      add(compareVersions(server.connect_version, config.version) > 0
        ? { id: "plugin_version", status: "warn", title: `接入插件有新版本 ${server.connect_version}（当前 ${config.version}）`, fix: "在 DSH 的「设置 → dsh-ee」点「更新到最新版」，装好后重启 DSH" }
        : { id: "plugin_version", status: "ok", title: `接入插件 ${config.version} 已是最新` });
    }
    const manager = ctx.get("pluginManager");
    if (manager && (server.required_plugins ?? []).length) {
      try {
        const have = new Set((await manager.listBundles()).filter((b: any) => b.installed || b.enabled).map((b: any) => b.name));
        const missing = server.required_plugins.map((p: any) => p.name).filter((n: string) => !have.has(n));
        add(missing.length
          ? { id: "required_plugins", status: "warn", title: `缺少必装插件 ${missing.join("、")}`, fix: "在 DSH 插件页安装，或重启 DSH 让接入插件自动安装" }
          : { id: "required_plugins", status: "ok", title: "必装插件都已安装" });
      } catch {
        // 读不到插件列表就不下结论
      }
    }
  }

  // 桥
  add(bridge.status.error
    ? { id: "bridge", status: "fail", title: "企业工具同步失败", detail: bridge.status.error, fix: "检查网络；输入 /ee-status 看登录状态" }
    : { id: "bridge", status: "ok", title: `企业工具已同步（${bridge.status.tools} 个）` });

  return { summary: summarize(checks), checks };
}

function summarize(checks: Check[]): string {
  const fails = checks.filter((c) => c.status === "fail").length;
  const warns = checks.filter((c) => c.status === "warn").length;
  return fails ? `${fails} 项失败，${warns} 项需要注意` : warns ? `${warns} 项需要注意` : "全部正常";
}

/** 注册本机只读工具 ee_local_doctor。修复交给模型用 DSH 自带的文件 / shell 工具做（有 DSH 自己的审批），这里不改任何文件。 */
export function registerDoctor(ctx: DshContext, config: PluginConfig, tokens: TokenManager, http: HttpClient, bridge: McpBridge, bindings?: AppBindings): void {
  const dispose = ctx.tools.register({
    name: "ee_local_doctor",
    description: "检查这台电脑上 DSH 与企业工作台 dsh-ee 的接入：登录凭据、令牌是否有效、.npmrc、接入插件版本、必装插件、模型、企业工具同步，并附带服务端检查。只读，不修改任何东西。",
    parameters: { type: "object", properties: {} },
    output: { schema: {}, render: (_args: unknown, value: unknown) => [{ type: "text", text: JSON.stringify(value) }] },
    execute: async () => runLocalDoctor(ctx, config, tokens, http, bridge, bindings),
  });
  ctx.effect(() => dispose);
}
