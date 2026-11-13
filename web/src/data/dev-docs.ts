/**
 * 「开发文档」页的静态内容源：精选的中文导读，每篇只写 dsh-ee 视角的摘要与外链，
 * 不复制上游正文与图片（政策见 docs/plugin-quality.md 的「映射不复制」）。
 *
 * 正文是 Markdown，用 react-markdown + remark-gfm + rehype-slug 渲染，
 * 支持 GFM 表格与 GitHub 同款标题锚点；新增一篇 = 加一个 .md 文件 + 在这里登记。
 */
import overview from "./dev-docs/overview.md?raw";
import harnessQuickstart from "./dev-docs/harness-quickstart.md?raw";
import pluginQuickstart from "./dev-docs/plugin-quickstart.md?raw";
import extensionQuickstart from "./dev-docs/extension-quickstart.md?raw";
import accessRules from "./dev-docs/access-rules.md?raw";
import publishPrecheck from "./dev-docs/publish-precheck.md?raw";
import qualityStandards from "./dev-docs/quality-standards.md?raw";

/** 文档分类：列表页按它展示标签，顺序即导航顺序。 */
export type DocCategory = "start" | "harness" | "plugin" | "extension" | "access" | "publish" | "quality";

export interface DevDoc {
  /** 稳定 slug：详情路由 /docs/:slug */
  slug: string;
  category: DocCategory;
  title: { zh: string; en: string };
  summary: { zh: string; en: string };
  /** 中文正文（Markdown）。只写导读，不复制上游正文。 */
  body: string;
  /** 内容复核日期，展示在列表与详情页 */
  updatedAt: string;
  tags: string[];
}

export const DOC_CATEGORIES: DocCategory[] = ["start", "harness", "plugin", "extension", "access", "publish", "quality"];

export const DEV_DOCS: DevDoc[] = [
  {
    slug: "overview",
    category: "start",
    title: { zh: "从哪开始：三条开发路径", en: "Where to start: three development paths" },
    summary: {
      zh: "插件作者、扩展作者、非 DSH 客户端三条路径的入口、交付物与本系统里的落点。",
      en: "Entry points, deliverables and where each one lands in dsh-ee: plugins, extensions, non-DSH clients.",
    },
    body: overview,
    updatedAt: "2026-11-12",
    tags: ["入口", "插件", "扩展", "客户端"],
  },
  {
    slug: "harness-quickstart",
    category: "harness",
    title: { zh: "接第三方 Harness / 自研客户端", en: "Connect a third-party Harness or your own client" },
    summary: {
      zh: "五分钟路径：一个 ee_at_ 令牌接 /v1 模型、/mcp 工具与服务端写确认，含发现端点和限制。",
      en: "Five-minute path: one ee_at_ token for /v1 models, /mcp tools and server-side write confirmation, plus discovery and limits.",
    },
    body: harnessQuickstart,
    updatedAt: "2026-11-13",
    tags: ["Harness", "令牌", "MCP", "模型网关"],
  },
  {
    slug: "plugin-quickstart",
    category: "plugin",
    title: { zh: "写 DSH 插件并发布到企业源", en: "Write a DSH plugin and publish it to the company registry" },
    summary: {
      zh: "从骨架、清单字段、本地自查到三个发布入口；只讲企业侧必看的部分。",
      en: "From scaffold and manifest fields to local checks and three publish entries — only what the company side needs.",
    },
    body: pluginQuickstart,
    updatedAt: "2026-11-12",
    tags: ["插件", "package.json", "发布"],
  },
  {
    slug: "extension-quickstart",
    category: "extension",
    title: { zh: "写 dsh-ee 扩展：清单、检查与调用", en: "Write a dsh-ee extension: manifest, checks and calls" },
    summary: {
      zh: "声明式、服务式、本地三种扩展的骨架、CLI 命令与交付前检查。",
      en: "Declarative, service and local extensions: scaffold, CLI commands and pre-delivery checks.",
    },
    body: extensionQuickstart,
    updatedAt: "2026-11-12",
    tags: ["扩展", "清单", "CLI"],
  },
  {
    slug: "access-rules",
    category: "access",
    title: { zh: "权限与访问规则速查", en: "Permissions and access rules: a quick reference" },
    summary: {
      zh: "权限点、字段访问规则、管理约束三层的判断顺序与配置入口。",
      en: "How permission codes, field access rules and admin constraints are evaluated, and where to configure them.",
    },
    body: accessRules,
    updatedAt: "2026-11-12",
    tags: ["权限", "字段", "规则"],
  },
  {
    slug: "publish-precheck",
    category: "publish",
    title: { zh: "发布入口与预检结果怎么读", en: "Publish entries and how to read the precheck" },
    summary: {
      zh: "三个发布入口、预检的扫描键、结果语义与提交前自查清单。",
      en: "The three publish entries, precheck scan keys, what results mean, and a pre-submit checklist.",
    },
    body: publishPrecheck,
    updatedAt: "2026-11-12",
    tags: ["发布", "预检", "版本"],
  },
  {
    slug: "quality-standards",
    category: "quality",
    title: { zh: "规范与检查：ENTRY / FLOW / BOOST", en: "Standards and checks: ENTRY / FLOW / BOOST" },
    summary: {
      zh: "上游规范三层与 dsh-ee 预检的边界：哪些是作者自证，哪些是企业准入。",
      en: "Where upstream standards end and the dsh-ee precheck begins: author evidence vs company admission.",
    },
    body: qualityStandards,
    updatedAt: "2026-11-12",
    tags: ["规范", "检查", "样例"],
  },
];

/** 按 slug 找文档；找不到返回 null（详情页据此显示空态）。 */
export function findDevDoc(slug: string): DevDoc | null {
  return DEV_DOCS.find((d) => d.slug === slug) ?? null;
}

/** 粗略的阅读时长：按正文里的中英文字符数估算，只用于列表页提示。 */
export function readMinutes(body: string): number {
  const chars = Array.from(body.replace(/```[\s\S]*?```/g, "")).filter((ch) => /[\u4e00-\u9fa5A-Za-z0-9]/.test(ch)).length;
  return Math.max(1, Math.round(chars / 350));
}
