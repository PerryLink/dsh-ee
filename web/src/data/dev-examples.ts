/**
 * 「开发样例」页的静态内容源：8 个 DSH 插件样例的中文导读。
 *
 * 只写自己的导读与事实（固定版本、来源、许可、安装命令），不复制上游 README、截图或媒体；
 * 也不放检查结论 —— dsh-ee 不执行 checker，样例只说明「能学到哪一类能力」，不做排序或评级。
 * 安装命令由 pluginAddCommand 统一生成，保证和「接入指南」里的一致。
 */
import { pluginAddCommand } from "../lib/install";

/** 难度只描述读这个样例需要的先验知识，不构成等级或排序。 */
export type ExampleDifficulty = "starter" | "intermediate" | "advanced";

/** 样例涉及的 Cordis 面。 */
export type ExampleSurface = "tool" | "command" | "service" | "client" | "skill" | "mcp" | "policy" | "renderer" | "storage";

export interface DevExample {
  /** 稳定 id：详情路由 /examples/:id */
  id: string;
  title: { zh: string; en: string };
  /** 一句话摘要 */
  summary: { zh: string; en: string };
  difficulty: ExampleDifficulty;
  surfaces: ExampleSurface[];
  /** 从这个样例能看到什么写法 */
  teach: { zh: string; en: string };
  /** 安装参数（包名，最后一项带钉死的版本） */
  install: string[];
  /** 页面上展示的固定版本 */
  version: string;
  /** 来源仓库 */
  repo: string;
  /** 来源 tag（与 install 的版本不一致时看 versionNote） */
  tag: string;
  license: { spdx: string };
  /** 版权人 / 归属说明 */
  attribution: string;
  requires: { zh: string; en: string };
  /** #126《DSH 插件样例走读》里对应小节的 GitHub 锚点（文档合并后可用） */
  walkthroughAnchor: string;
  updatedAt: string;
  versionNote?: { zh: string; en: string };
}

export const DIFFICULTIES: ExampleDifficulty[] = ["starter", "intermediate", "advanced"];

/** 按「入门 → 进阶 → 高级」排列；同难度内按样例由小到大，不做质量排序。 */
export const DEV_EXAMPLES: DevExample[] = [
  {
    id: "dsh-claim-check",
    walkthroughAnchor: "1-dsh-claim-check证据逐字核对",
    title: { zh: "证据逐字核对", en: "Verbatim claim check" },
    summary: {
      zh: "最小工具样例：在原文里查找逐字证据，输出确定性的核对结论。",
      en: "Smallest tool sample: find verbatim evidence in source text and return a deterministic check result.",
    },
    difficulty: "starter",
    surfaces: ["tool"],
    teach: {
      zh: "ctx.effect 与 ctx.tools.register 的最小组合；零模型、零网络也能复算。",
      en: "A minimal ctx.effect + ctx.tools.register combo that runs without models or network.",
    },
    install: ["@perrylink/dsh-research-core@0.1.1", "@perrylink/dsh-claim-check@0.1.2"],
    version: "0.1.2",
    repo: "https://github.com/PerryLink/dsh-claim-check",
    tag: "v0.1.2",
    license: { spdx: "Apache-2.0" },
    attribution: "PerryLink contributors",
    requires: {
      zh: "Node 22.19+ 或 24；准备一段原文与一条断言；不需要模型与网络。",
      en: "Node 22.19+ or 24; one source passage and one claim; no model or network needed.",
    },
    updatedAt: "2026-11-12",
  },
  {
    id: "dsh-translate",
    walkthroughAnchor: "2-dsh-translate全页翻译客户端与服务",
    title: { zh: "全页翻译：客户端与服务", en: "Full-page translation: client and service" },
    summary: {
      zh: "手写客户端插件样例：只翻译显示层，不把会话写进日志。",
      en: "Hand-written client plugin sample: translate the display layer only, never write sessions to logs.",
    },
    difficulty: "starter",
    surfaces: ["client", "service"],
    teach: {
      zh: "手写 client bundle、服务描述符与占位符遮罩的两端契约。",
      en: "A hand-written client bundle, a service descriptor, and placeholder masking across both ends.",
    },
    install: ["dsh-translate@0.3.5"],
    version: "0.3.5",
    repo: "https://github.com/PerryLink/dsh-translate",
    tag: "v0.3.5",
    license: { spdx: "Apache-2.0" },
    attribution: "PerryLink contributors",
    requires: {
      zh: "Node 22.19+ 或 24；DSH 0.2.1+；profile 里要有一个可用的模型 provider。",
      en: "Node 22.19+ or 24; DSH 0.2.1+; an available model provider in the profile.",
    },
    updatedAt: "2026-11-12",
  },
  {
    id: "dsh-output-styles",
    walkthroughAnchor: "3-dsh-output-styles输出风格与渲染注册表",
    title: { zh: "输出风格与渲染注册表", en: "Output styles and renderer registry" },
    summary: {
      zh: "命令与渲染样例：运行时切换风格、向 system prompt 注入段落、注册 output.render.*。",
      en: "Command and renderer sample: switch styles at runtime, inject system-prompt sections, register output.render.*.",
    },
    difficulty: "intermediate",
    surfaces: ["command", "renderer"],
    teach: {
      zh: "命令注册、system prompt 段落注入与 output.render.* 注册表。",
      en: "Command registration, system-prompt section injection and the output.render.* registry.",
    },
    install: ["dsh-output-styles@0.8.3"],
    version: "0.8.3",
    repo: "https://github.com/PerryLink/dsh-output-styles",
    tag: "v0.8.3",
    license: { spdx: "Apache-2.0" },
    attribution: "PerryLink contributors",
    requires: {
      zh: "DSH 0.2.1 线；需要在 web profile 里看渲染效果。",
      en: "DSH 0.2.1 line; use the web profile to see the rendering.",
    },
    updatedAt: "2026-11-12",
  },
  {
    id: "dsh-library",
    walkthroughAnchor: "4-dsh-library本地知识库服务与存储",
    title: { zh: "本地知识库：服务与存储", en: "Local knowledge base: service and storage" },
    summary: {
      zh: "服务加存储样例：本地切片、哈希嵌入、重排与引用校验。",
      en: "Service and storage sample: local chunking, hashed embeddings, reranking and citation checks.",
    },
    difficulty: "intermediate",
    surfaces: ["service", "storage"],
    teach: {
      zh: "ctx.service 与 storage-domain 的用法、混合检索与清除后的验证。",
      en: "ctx.service with storage-domain, hybrid retrieval and verifying that deletion really happened.",
    },
    install: ["dsh-library@0.3.0"],
    version: "0.3.0",
    repo: "https://github.com/PerryLink/dsh-library",
    tag: "v0.3.0",
    license: { spdx: "Apache-2.0" },
    attribution: "PerryLink contributors；算法移植自多个 Apache-2.0 项目，归属见上游 THIRD_PARTY_NOTICES.md",
    requires: {
      zh: "DSH 0.2.1+；storage-domain；默认零模型零网络。",
      en: "DSH 0.2.1+; storage-domain; zero model and zero network by default.",
    },
    updatedAt: "2026-11-12",
  },
  {
    id: "dsh-industry-research",
    walkthroughAnchor: "5-dsh-industry-research领域技能包技能与多工具",
    title: { zh: "领域技能包：技能与多工具", en: "Domain skill pack: skills and multiple tools" },
    summary: {
      zh: "技能加工具样例：两个 SKILL.md、四个工具，带合成 fixtures 与 SHA-256 台账。",
      en: "Skill and tool sample: two SKILL.md files and four tools, with synthetic fixtures and a SHA-256 ledger.",
    },
    difficulty: "intermediate",
    surfaces: ["skill", "tool"],
    teach: {
      zh: "随包分发技能、领域包结构与可追溯的合成数据。",
      en: "Skills shipped inside a package, a domain pack layout, and traceable synthetic data.",
    },
    install: ["dsh-industry-research@0.3.25"],
    version: "0.3.25",
    repo: "https://github.com/PerryLink/dsh-industry-research",
    tag: "v0.3.25",
    license: { spdx: "Apache-2.0" },
    attribution: "PerryLink contributors",
    requires: {
      zh: "DSH 0.2.1+；ctx.web 可选，没有时会走内置回退。",
      en: "DSH 0.2.1+; ctx.web is optional, a built-in fallback is used when absent.",
    },
    updatedAt: "2026-11-12",
  },
  {
    id: "dsh-test-drive",
    walkthroughAnchor: "6-dsh-test-drive安装验证装启断言卸",
    title: { zh: "安装验证：装、启、断言、卸", en: "Install verification: add, start, assert, remove" },
    summary: {
      zh: "验证样例：在临时 DSH_HOME 里安装、启动、断言能力，再卸载并产出验证记录。",
      en: "Verification sample: install, start and assert capabilities in a temporary DSH_HOME, then uninstall and emit a record.",
    },
    difficulty: "intermediate",
    surfaces: ["tool", "command"],
    teach: {
      zh: "用临时 DSH_HOME 做安装冒烟，以及 ctx.jobs 后台任务的用法。",
      en: "Install smoke tests with a temporary DSH_HOME, plus ctx.jobs background tasks.",
    },
    install: ["dsh-test-drive@0.3.25"],
    version: "0.3.25",
    repo: "https://github.com/PerryLink/dsh-test-drive",
    tag: "v0.3.25",
    license: { spdx: "Apache-2.0" },
    attribution: "PerryLink contributors",
    requires: {
      zh: "dsh CLI 与 pnpm；桌面版不能当测试宿主；安装包需要网络。",
      en: "dsh CLI and pnpm; the desktop app cannot host the tests; installing packages needs network.",
    },
    updatedAt: "2026-11-12",
  },
  {
    id: "dsh-permission-rules",
    walkthroughAnchor: "7-dsh-permission-rules工具策略规则与网络",
    title: { zh: "工具策略：规则与网络", en: "Tool policy: rules and network" },
    summary: {
      zh: "策略中间件样例：allow / deny / ask 规则、网络策略与 tools/pre-execute 瀑布。",
      en: "Policy middleware sample: allow / deny / ask rules, network policy and the tools/pre-execute waterfall.",
    },
    difficulty: "advanced",
    surfaces: ["policy", "client"],
    teach: {
      zh: "tools/pre-execute 瀑布、规则测试向量与热重载设置页。",
      en: "A tools/pre-execute waterfall, rule test vectors and a hot-reloading settings page.",
    },
    install: ["dsh-permission-rules@0.7.21"],
    version: "0.7.21",
    repo: "https://github.com/PerryLink/dsh-permission-rules",
    tag: "v0.7.21",
    license: { spdx: "Apache-2.0" },
    attribution: "PerryLink contributors",
    requires: {
      zh: "DSH 0.2.1 线；需要 web 设置页；WSL 下走轮询。",
      en: "DSH 0.2.1 line; a web settings page; polling under WSL.",
    },
    updatedAt: "2026-11-12",
  },
  {
    id: "dsh-memento",
    walkthroughAnchor: "8-dsh-memento记忆服务审批与审计",
    title: { zh: "记忆服务：审批与审计", en: "Memory service: approvals and audit" },
    summary: {
      zh: "高级样例：ctx.memory 服务、可重建的审计表与不可绕过的审批门，附带 MCP server。",
      en: "Advanced sample: a ctx.memory service, a rebuildable audit table, a non-bypassable approval gate, and an MCP server.",
    },
    difficulty: "advanced",
    surfaces: ["service", "client", "mcp"],
    teach: {
      zh: "service seam、审批不可绕过、SQLite provider 与 MCP 入口。",
      en: "Service seams, non-bypassable approvals, a SQLite provider and an MCP entry point.",
    },
    install: ["dsh-memento@0.5.30"],
    version: "0.5.30",
    repo: "https://github.com/PerryLink/dsh-memento",
    tag: "v0.5.30",
    license: { spdx: "Apache-2.0" },
    attribution: "PerryLink contributors 及个人贡献者（完整名单见上游 package.json contributors）",
    requires: {
      zh: "Node 22.19+（node:sqlite）；DSH 0.2.x；$DSH_HOME 可写。",
      en: "Node 22.19+ (node:sqlite); DSH 0.2.x; a writable $DSH_HOME.",
    },
    updatedAt: "2026-11-12",
  },
];

/** 按 id 找样例；找不到返回 null（详情页据此显示空态）。 */
export function findDevExample(id: string): DevExample | null {
  return DEV_EXAMPLES.find((e) => e.id === id) ?? null;
}

/** 样例的安装命令：复用「接入指南」同一套 dsh plugin add 生成逻辑。 */
export function exampleInstallCommand(example: DevExample, profile?: string): string {
  return pluginAddCommand(example.install.join(" "), profile);
}

/** #126 样例走读文档（合并后可用）与对应锚点。 */
export const WALKTHROUGH_DOC_URL = "https://github.com/hikariming/dsh-ee/blob/main/docs/examples-walkthrough.md";

export function exampleWalkthroughUrl(example: DevExample): string {
  return `${WALKTHROUGH_DOC_URL}#${example.walkthroughAnchor}`;
}

/** 来源链接：仓库 + 固定 tag（repo@tag）。 */
export function exampleSourceUrl(example: DevExample): string {
  return `${example.repo}/tree/${example.tag}`;
}

/** 许可文件链接（Apache-2.0 等全文在上游仓库里）。 */
export function exampleLicenseUrl(example: DevExample): string {
  return `${example.repo}/blob/${example.tag}/LICENSE`;
}
