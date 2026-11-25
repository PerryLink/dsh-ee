/**
 * 端侧（本机）扩展的 MCP 注册（docs/plugins.md §8）。
 *
 * dsh-ee 的端侧扩展是一个 npm 包，包里的 bin 起一个 stdio MCP 服务（extensions/sdk-local）。
 * DSH 0.2.2 没有 `mcpServers` 配置文件，也没有让插件改 profile 配置的接口；官方给插件用的是
 * `ctx.mcpManager.register(name, entry)`（@deepseek-ai/dsh-mcp-manager，Web/Desktop 组合默认挂载）：
 * 注册只在这个插件存活期间有效，插件每次启动重新注册。这里把「员工确认过的本机扩展」记在
 * `$DSH_HOME/dsh-ee/local-mcp.json`，启动时按记录重新注册；mcpManager 冷启动时可能晚于插件就绪，
 * 启动时取不到就用 cordis 的运行时 `ctx.inject` 等它出现再补注册（不写模块级 inject：旧 DSH 会因此
 * 卡在 pending，见 #231），没有 mcpManager 时退回可复制的 `@deepseek-ai/dsh-mcp-client` 配置片段。
 *
 * 约束（和接入插件的其他本机操作一致）：
 *   - 只动这台电脑：不调 dsh-ee 的写接口、不改服务端、不写 DSH 的 profile 配置文件；
 *   - 用户先看到计划、再带 --yes 执行（/ee-local add 两步）；
 *   - 已有同名 MCP 服务（员工自己配的、别的插件注册的）一律不覆盖，报错让员工换 --id。
 */
import fs from "node:fs";
import path from "node:path";
import { dshHome, npmrcPath } from "./config.js";
import type { DshContext, PluginConfig } from "./dsh.js";
import { t } from "./i18n/index.js";

/** 注册给 dsh-mcp-manager 的一项本机 MCP 服务（stdio）。 */
export interface LocalMcpEntry {
  transport: "stdio";
  command: string;
  args: string[];
  env: Record<string, string>;
  startupTimeoutMs?: number;
}

/** mcpManager 的最小接口（@deepseek-ai/dsh-mcp-manager）。 */
export interface LocalMcpManager {
  list: () => Array<{ name: string; status?: { state?: string } }> | Promise<Array<{ name: string; status?: { state?: string } }>>;
  register: (name: string, entry: LocalMcpEntry) => () => void;
}

/** 员工确认过、要常驻的本机扩展。 */
export interface LocalExtension {
  /** dsh-ee 扩展 id：作者/名字，例如 acme/legacy-oa */
  id: string;
  /** 企业 npm 源里的包名，例如 @acme/legacy-oa-local */
  package: string;
  /** 固定版本；空串表示 npx -y 取最新 */
  version: string;
  /** DSH 里的 MCP 服务名，工具名是 mcp__<serverName>__<tool> */
  serverName: string;
  addedAt: string;
}

export interface LocalMcpResult {
  kind: "success" | "error";
  text: string;
}

const PACKAGE_RE = /^(?:@[a-z0-9][a-z0-9._-]*\/)?[a-z0-9][a-z0-9._-]*$/;
const VERSION_RE = /^[0-9A-Za-z][0-9A-Za-z.+_-]*$/;
const EXTENSION_ID_RE = /^[a-z0-9][a-z0-9._-]{0,40}\/[a-z0-9][a-z0-9._-]{0,60}$/;
const SERVER_NAME_RE = /^[A-Za-z0-9_-]{1,32}$/;

/** 员工确认记录放在哪。 */
export function localMcpStorePath(): string {
  return path.join(dshHome(), "dsh-ee", "local-mcp.json");
}

/** 把「包名[@版本]」拆开；不是 npm 包名（URL、file:、git: 等）时报错。 */
export function parsePackageSpec(raw: string): { package: string; version: string } | { error: string } {
  const text = String(raw ?? "").trim();
  if (!text) return { error: t("localMcp.missingPackage") };
  if (/[\s:]/.test(text)) return { error: t("localMcp.notNpmPackage", { text }) };
  const at = text.lastIndexOf("@");
  // 作用域包以 @ 开头（@scope/name），所以版本分隔符只可能是最后一个 @（下标 > 0）
  const hasVersion = at > 0;
  const pkg = hasVersion ? text.slice(0, at) : text;
  const version = hasVersion ? text.slice(at + 1) : "";
  if (!PACKAGE_RE.test(pkg)) return { error: t("localMcp.invalidPackage", { pkg }) };
  if (hasVersion && !VERSION_RE.test(version)) return { error: t("localMcp.invalidVersion", { version }) };
  return { package: pkg, version };
}

/** 校验 dsh-ee 扩展 id（作者/名字）。 */
export function parseExtensionId(raw: unknown): string | undefined {
  const text = String(raw ?? "").trim();
  return EXTENSION_ID_RE.test(text) ? text : undefined;
}

/**
 * 给一个扩展生成 MCP 服务名：只保留 [A-Za-z0-9_-]，最长 32。
 * 优先用扩展 id（acme/legacy-oa → acme-legacy-oa），没有 id 时用包名（@acme/legacy-oa-local → acme-legacy-oa-local）。
 */
export function localServerName(extensionId: string | undefined, packageName: string): string {
  const base = (extensionId ?? packageName.replace(/^@/, ""))
    .replace(/[^A-Za-z0-9_-]+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "");
  const clipped = base.slice(0, 32).replace(/-$/, "");
  const name = clipped || "ee-local";
  return SERVER_NAME_RE.test(name) ? name : "ee-local";
}

/** 没有 --id 时，按包名推一个「作者/名字」。 */
export function deriveExtensionId(packageName: string): string {
  const scoped = /^@([a-z0-9][a-z0-9._-]*)\/([a-z0-9][a-z0-9._-]*)$/.exec(packageName);
  if (scoped) return `${scoped[1]}/${scoped[2]}`.slice(0, 102);
  return `local/${packageName}`.slice(0, 102);
}

/** npx 在 Windows 上是 npx.cmd；mcp-client 直接 spawn，不经 shell。 */
function npxCommand(platform: NodeJS.Platform): string {
  return platform === "win32" ? "npx.cmd" : "npx";
}

/** 构造注册项：npx -y <包名[@版本]>，env 里带 dsh-ee 地址、员工的令牌（走凭据模板）与 npm 源配置。 */
export function localMcpEntry(
  config: PluginConfig,
  extension: Pick<LocalExtension, "package" | "version">,
  options: { npmrc: string; platform?: NodeJS.Platform } = { npmrc: "" },
): LocalMcpEntry {
  const spec = extension.version ? `${extension.package}@${extension.version}` : extension.package;
  const env: Record<string, string> = {
    DSH_EE_URL: config.base,
    // 令牌留在 DSH 凭据库里，由 mcp-client 的 ${credential:…} 模板在连接时解析；注册项里不落明文。
    DSH_EE_TOKEN: `\${credential:${config.tokenRef}}`,
    DSH_EE_HARNESS: "dsh",
  };
  // npx 子进程读不到 profile 的 .npmrc（DSH 跑 pnpm 时才用那个目录），显式指过去，企业源与令牌才对得上。
  if (options.npmrc) env.NPM_CONFIG_USERCONFIG = options.npmrc;
  return {
    transport: "stdio",
    command: npxCommand(options.platform ?? process.platform),
    args: ["-y", spec],
    env,
    startupTimeoutMs: 60_000,
  };
}

/** 可复制的配置片段：DSH 没有 mcpServers 配置文件，这是 profile 里 cordis.patch.yml 的一行。 */
export function localMcpSnippet(config: PluginConfig, extension: Pick<LocalExtension, "id" | "package" | "version" | "serverName">, npmrc = ""): string {
  const spec = extension.version ? `${extension.package}@${extension.version}` : extension.package;
  const q = (value: string) => `'${value.replace(/'/g, "''")}'`;
  const env = [
    `      DSH_EE_URL: ${q(config.base)}`,
    `      DSH_EE_TOKEN: ${q(`\${credential:${config.tokenRef}}`)}`,
    "      DSH_EE_HARNESS: dsh",
  ];
  if (npmrc) env.push(`      NPM_CONFIG_USERCONFIG: ${q(npmrc)}`);
  return [
    `- id: ${q(`dsh-ee-local-${extension.serverName}`)}`,
    `  name: '@deepseek-ai/dsh-mcp-client'`,
    `  config:`,
    `    serverName: ${q(extension.serverName)}`,
    `    transport: stdio`,
    `    command: ${q(npxCommand(process.platform))}`,
    `    args: [${q("-y")}, ${q(spec)}]`,
    `    env:`,
    ...env,
    `    expandTemplates: true`,
    `    startupTimeoutMs: 60000`,
  ].join("\n");
}

function validExtension(value: unknown): value is LocalExtension {
  if (!value || typeof value !== "object") return false;
  const item = value as Record<string, unknown>;
  return typeof item.id === "string" && EXTENSION_ID_RE.test(item.id)
    && typeof item.package === "string" && PACKAGE_RE.test(item.package)
    && typeof item.version === "string" && (item.version === "" || VERSION_RE.test(item.version))
    && typeof item.serverName === "string" && SERVER_NAME_RE.test(item.serverName)
    && typeof item.addedAt === "string";
}

/** 读员工确认过的扩展记录；文件不存在或内容坏了都按空列表处理，不影响插件启动。 */
export function readLocalExtensions(file = localMcpStorePath()): LocalExtension[] {
  try {
    const data = JSON.parse(fs.readFileSync(file, "utf8")) as { extensions?: unknown };
    return Array.isArray(data.extensions) ? data.extensions.filter(validExtension) : [];
  } catch {
    return [];
  }
}

function writeLocalExtensions(extensions: LocalExtension[], file = localMcpStorePath()): void {
  fs.mkdirSync(path.dirname(file), { recursive: true, mode: 0o700 });
  const tmp = `${file}.tmp`;
  fs.writeFileSync(tmp, `${JSON.stringify({ version: 1, extensions }, null, 2)}\n`, { mode: 0o600 });
  fs.renameSync(tmp, file);
}

/** 命令输入：`/ee-local add <包名>[@版本] [--id 作者/名字] [--yes]` 之类。 */
export interface LocalCommandArgs {
  action: "list" | "add" | "remove" | "snippet" | "help";
  target: string;
  id?: string;
  yes: boolean;
  error?: string;
}

export function parseLocalCommand(rawInput: string | undefined): LocalCommandArgs {
  const tokens = String(rawInput ?? "").trim().split(/\s+/).filter(Boolean);
  const [action, ...rest] = tokens;
  const args: LocalCommandArgs = { action: "list", target: "", yes: false };
  if (!action) return args;
  if (action === "help") return { ...args, action: "help" };
  if (!["list", "add", "remove", "snippet"].includes(action)) {
    return { ...args, error: t("localMcp.unknownSubcommand", { action }) };
  }
  args.action = action as LocalCommandArgs["action"];
  const positional: string[] = [];
  for (let i = 0; i < rest.length; i++) {
    const token = rest[i];
    if (token === "--yes" || token === "-y") args.yes = true;
    else if (token === "--id") {
      const value = rest[++i];
      if (value === undefined) return { ...args, error: t("localMcp.idHint") };
      const id = parseExtensionId(value);
      if (!id) return { ...args, error: t("localMcp.invalidId", { value }) };
      args.id = id;
    } else if (token.startsWith("-")) return { ...args, error: t("localMcp.unknownFlag", { token }) };
    else positional.push(token);
  }
  if ((args.action === "add" || args.action === "remove" || args.action === "snippet") && positional.length === 0) {
    return { ...args, error: t("localMcp.missingTarget", { action: args.action }) };
  }
  if (positional.length > 1) return { ...args, error: t("localMcp.extraArgs", { args: positional.slice(1).join(" ") }) };
  args.target = positional[0] ?? "";
  return args;
}

/** 一次补注册的结果：本次新注册的扩展 id，与被跳过的记录（同名服务已存在 / 管理器报错）。 */
export interface LocalMcpRegistration {
  registered: string[];
  skipped: Array<{ name: string; reason: string }>;
}

export interface LocalMcpHandle {
  /** 这台 DSH 有没有 MCP 管理接口（dsh-mcp-manager）。 */
  readonly available: boolean;
  /** 按记录注册（插件启动时调用一次）；没有 mcpManager 时返回空，随后它出现会自动补。 */
  start: () => Promise<LocalMcpRegistration>;
  /** 幂等补注册：只补还没注册的确认记录（管理器晚到位时由运行时注入触发，没有注入的宿主由 list/add 兜底）。 */
  ensure: () => Promise<LocalMcpRegistration>;
  /** 员工改了 dsh-ee 地址之后，按新的地址重新注册一遍（env 里的 DSH_EE_URL 要跟着变）。 */
  refresh: () => Promise<LocalMcpRegistration>;
  /** 插件卸载时注销本次注册，并停掉之后的自动补注册。 */
  stop: () => void;
  /** 先补齐缺失的自动注册，再列出记录与状态。 */
  list: () => Promise<LocalMcpResult>;
  add: (parsed: LocalCommandArgs) => Promise<LocalMcpResult>;
  remove: (parsed: LocalCommandArgs) => Promise<LocalMcpResult>;
  snippet: (parsed: LocalCommandArgs) => LocalMcpResult;
}

/**
 * 本机 MCP 注册的运行时句柄。注册项的生命周期跟接入插件一致：插件每次启动按记录重新注册，
 * 员工不用重装；记录只在员工带 --yes 确认过之后才写。
 */
export function createLocalMcp(ctx: DshContext, config: PluginConfig, options: { store?: string; npmrc?: string; platform?: NodeJS.Platform } = {}): LocalMcpHandle {
  const store = options.store ?? localMcpStorePath();
  const npmrc = options.npmrc ?? npmrcPath(ctx);
  const platform = options.platform ?? process.platform;
  /** 一条注册的注销句柄；记住挂在哪个管理器上，管理器换实现时按身份丢弃。 */
  interface Registration {
    dispose: () => void;
    manager: LocalMcpManager;
  }
  const disposers = new Map<string, Registration>();
  const manager = (): LocalMcpManager | undefined => {
    // mcpManager 不在 inject 里（旧版 DSH 会因此卡住整个插件），这里按需取；取不到或宿主抛错都当没有
    let service: any;
    try { service = ctx.get("mcpManager"); } catch { return undefined; }
    return service && typeof service.register === "function" && typeof service.list === "function" ? service as LocalMcpManager : undefined;
  };
  const names = async (service: LocalMcpManager): Promise<Set<string>> => {
    try {
      const list = await service.list();
      return new Set(list.map((item) => item.name));
    } catch {
      return new Set();
    }
  };
  const register = (extension: LocalExtension, service: LocalMcpManager): void => {
    if (disposers.has(extension.id)) return;
    const dispose = service.register(extension.serverName, localMcpEntry(config, extension, { npmrc, platform }));
    disposers.set(extension.id, {
      manager: service,
      dispose: () => {
        try {
          dispose();
        } catch {
          // 管理器先卸载时再调用 disposer 会抛错；插件卸载过程不该被打断
        }
      },
    });
  };
  const unregister = (id: string): void => {
    const registration = disposers.get(id);
    if (!registration) return;
    disposers.delete(id);
    registration.dispose();
  };
  /** 注销本句柄记下的全部注册（stop、管理器换实现、插件卸载时用）。 */
  const dropRegistrations = (): void => {
    for (const registration of disposers.values()) registration.dispose();
    disposers.clear();
  };
  const describe = (extension: LocalExtension, state: string): string =>
    `  ${extension.id}  ${extension.package}${extension.version ? `@${extension.version}` : ""}  serverName=${extension.serverName}  ${state}`;

  // mcpManager 可能晚于插件就绪（DSH 冷启动）：启动时取不到不能就此放弃，等它出现后补注册。
  let stopped = false;
  let ensuring: Promise<LocalMcpRegistration> | undefined;
  let managerHook = false;

  const registerMissing = async (): Promise<LocalMcpRegistration> => {
    const empty = (): LocalMcpRegistration => ({ registered: [], skipped: [] });
    if (stopped) return empty();
    const service = manager();
    if (!service) return empty();
    // 管理器换了实现（例如它自己被重载）：旧注册项已随旧管理器失效，丢掉引用后重新补
    for (const [id, registration] of [...disposers]) {
      if (registration.manager === service) continue;
      disposers.delete(id);
      registration.dispose();
    }
    const extensions = readLocalExtensions(store);
    const missing = extensions.filter((extension) => !disposers.has(extension.id));
    if (!missing.length) return empty();
    // 只注册员工确认过的记录；已有同名服务（员工自己配的、别的插件注册的）一律不覆盖
    const taken = await names(service);
    if (stopped) return empty();
    const registered: string[] = [];
    const skipped: Array<{ name: string; reason: string }> = [];
    for (const extension of missing) {
      if (taken.has(extension.serverName)) {
        skipped.push({ name: extension.id, reason: t("localMcp.duplicateService", { name: extension.serverName }) });
        continue;
      }
      try {
        register(extension, service);
        taken.add(extension.serverName);
        registered.push(extension.id);
      } catch (error) {
        skipped.push({ name: extension.id, reason: error instanceof Error ? error.message : String(error) });
      }
    }
    return { registered, skipped };
  };

  /** 幂等补注册：同一时刻只跑一次；管理器不可用 / 宿主报错都只当这次没补。 */
  const ensure = (): Promise<LocalMcpRegistration> => {
    if (ensuring) return ensuring;
    const current = registerMissing().finally(() => {
      if (ensuring === current) ensuring = undefined;
    });
    ensuring = current;
    return current;
  };

  /**
   * 等 mcpManager 出现后补注册。cordis 的运行时 ctx.inject 只在依赖就绪后才执行回调，
   * 不会像模块级 inject 那样把整个插件卡在 pending（#231 的教训），所以旧版 DSH 上也能挂。
   * 宿主没有运行时注入（很老的 DSH）时不挂 hook：由 list/add/refresh 惰性补。
   */
  const hookManager = (): void => {
    if (managerHook || stopped) return;
    const inject = (ctx as any).inject;
    if (typeof inject !== "function") return;
    try {
      inject.call(ctx, ["mcpManager"], (scope: DshContext) => {
        // 管理器卸载或换实现：本句柄在旧管理器上的注册随之失效，清掉引用；新管理器出现后会重新补
        try {
          scope.effect(() => () => dropRegistrations());
        } catch {
          // 拿不到 effect 就不挂清理；下次补注册会按管理器身份丢弃旧引用
        }
        void ensure().catch(() => undefined);
      });
      managerHook = true;
    } catch {
      // 宿主拒绝运行时注入：退回首次使用（list/add）前补注册
    }
  };

  return {
    get available() {
      return manager() !== undefined;
    },

    ensure,

    async start() {
      stopped = false;
      const result = await ensure();
      // 管理器这时还没就绪的话，挂上运行时注入等它出现（不阻塞插件加载）
      hookManager();
      return result;
    },

    stop() {
      stopped = true;
      dropRegistrations();
    },

    async refresh() {
      stopped = false;
      dropRegistrations();
      const result = await ensure();
      hookManager();
      return result;
    },

    async list() {
      // 补注册是幂等的；管理器晚到位而宿主又没有运行时注入时，至少在首次使用前补齐
      await ensure().catch(() => undefined);
      const extensions = readLocalExtensions(store);
      const hint = this.available ? "" : t("localMcp.noManagerHint");
      if (!extensions.length) {
        return { kind: "success", text: t("localMcp.noExtensions", { hint }) };
      }
      const state = new Map<string, string>();
      const service = manager();
      if (service) {
        try {
          const rows = service.list();
          if (Array.isArray(rows)) for (const row of rows) state.set(row.name, row.status?.state ?? t("localMcp.registered"));
        } catch {
          // 读不到状态就只列记录
        }
      }
      const lines = extensions.map((extension) => describe(extension, state.get(extension.serverName) ?? (this.available ? t("localMcp.unregistered") : t("localMcp.unregisteredNoManager"))));
      return { kind: "success", text: t("localMcp.listHeader", { count: extensions.length, lines: lines.join("\n"), hint }) };
    },

    async add(parsed) {
      // 先把记录里缺的注册补齐，避免“记录已存在但本会话没注册”被 alreadyAdded 挡住
      await ensure().catch(() => undefined);
      const spec = parsePackageSpec(parsed.target);
      if ("error" in spec) return { kind: "error", text: spec.error };
      const id = parsed.id ?? deriveExtensionId(spec.package);
      const extension: LocalExtension = {
        id,
        package: spec.package,
        version: spec.version,
        serverName: localServerName(parsed.id ? id : undefined, spec.package),
        addedAt: new Date().toISOString(),
      };
      const stored = readLocalExtensions(store);
      const sameId = stored.find((item) => item.id === extension.id);
      const samePackage = stored.find((item) => item.package === extension.package);
      if (sameId ? sameId.package === extension.package && sameId.version === extension.version : samePackage && samePackage.version === extension.version) {
        return { kind: "success", text: t("localMcp.alreadyAdded", { spec: `${extension.package}${extension.version ? `@${extension.version}` : ""}` }) };
      }
      if (sameId && sameId.package !== extension.package) {
        return { kind: "error", text: t("localMcp.idTaken", { id: extension.id, pkg: sameId.package }) };
      }
      if (!sameId && samePackage) {
        return { kind: "error", text: t("localMcp.packageBound", { id: samePackage!.id }) };
      }
      const service = manager();
      if (!service) {
        return {
          kind: "error",
          text: t("localMcp.noManagerAdd", { snippet: localMcpSnippet(config, extension, npmrc) }),
        };
      }
      const taken = await names(service);
      // 更新同一扩展的版本时，服务名是我们自己注册的，不算冲突
      const ours = sameId !== undefined && disposers.has(sameId.id);
      if (!ours && taken.has(extension.serverName)) {
        return { kind: "error", text: t("localMcp.serviceConflict", { name: extension.serverName, spec: `${extension.package}${extension.version ? `@${extension.version}` : ""}`, scope: id.split("/")[0] }) };
      }
      if (!parsed.yes) {
        return {
          kind: "success",
          text: t("localMcp.confirmPlan", { update: sameId ? t("localMcp.updateSuffix") : "", id: extension.id, pkg: extension.package, latest: extension.version ? `@${extension.version}` : t("localMcp.latestVersion"), server: extension.serverName, spec: `${extension.package}${extension.version ? `@${extension.version}` : ""}`, idFlag: parsed.id ? ` --id ${parsed.id}` : "" }),
        };
      }
      try {
        unregister(extension.id);
        register(extension, service);
      } catch (error) {
        return { kind: "error", text: t("localMcp.registerFailed", { error: error instanceof Error ? error.message : String(error) }) };
      }
      writeLocalExtensions([...stored.filter((item) => item.id !== extension.id), extension], store);
      return {
        kind: "success",
        text: t("localMcp.registeredText", { id: extension.id, spec: `${extension.package}${extension.version ? `@${extension.version}` : ""}`, server: extension.serverName }),
      };
    },

    async remove(parsed) {
      const stored = readLocalExtensions(store);
      const target = parsed.target;
      const hit = stored.find((item) => item.package === target || item.id === target || item.serverName === target || `${item.package}@${item.version}` === target);
      if (!hit) return { kind: "error", text: t("localMcp.notFound", { target }) };
      unregister(hit.id);
      writeLocalExtensions(stored.filter((item) => item !== hit), store);
      return { kind: "success", text: t("localMcp.removed", { id: hit.id, pkg: hit.package, server: hit.serverName }) };
    },

    snippet(parsed) {
      const spec = parsePackageSpec(parsed.target);
      if ("error" in spec) return { kind: "error", text: spec.error };
      const id = parsed.id ?? deriveExtensionId(spec.package);
      const extension = {
        id,
        package: spec.package,
        version: spec.version,
        serverName: localServerName(parsed.id ? id : undefined, spec.package),
      };
      return {
        kind: "success",
        text: t("localMcp.snippetIntro", { snippet: localMcpSnippet(config, extension, npmrc), hint: this.available ? t("localMcp.snippetHint") : "" }),
      };
    },
  };
}

/** /ee-local：在 DSH 里注册 / 查看 / 移除本机（端侧）扩展的 MCP 服务。 */
export function registerLocalMcpCommand(ctx: DshContext, local: LocalMcpHandle): void {
  const commands = ctx.get("commands");
  if (!commands) return;
  try {
    const dispose = commands.register({
      name: "ee-local",
      description: t("localMcp.description"),
      input: { hint: t("localMcp.inputHint") },
      handler: async (invocation: any) => {
        let parsed = parseLocalCommand(invocation?.rawInput);
        if (parsed.error) return { kind: "error", text: parsed.error };
        if (parsed.action === "help") parsed = { ...parsed, action: "list" };
        switch (parsed.action) {
          case "add":
            return local.add(parsed);
          case "remove":
            return local.remove(parsed);
          case "snippet":
            return local.snippet(parsed);
          default:
            return local.list();
        }
      },
    });
    ctx.effect(() => dispose);
  } catch {
    // 和宿主已有的命令撞名时不拖死接入插件
  }
}
