/**
 * 旧的站内静态副本，只作兜底：站内 /docs 的单一事实源已是仓库 `docs/`
 * （构建期由 scripts/docs/build.mjs 生成 web/src/generated/dev-docs.ts）。
 *
 * 只有生成物为空时（例如构建环境没带 docs/）页面才读这里；内容冻结，不再新增。
 * 后续 PR 会在确认部署流程都带 docs/ 后删除本文件与 ./dev-docs/ 下的正文。
 *
 * 正文是 Markdown，用 react-markdown + remark-gfm + rehype-slug 渲染，
 * 支持 GFM 表格与 GitHub 同款标题锚点。
 */
import overview from "./dev-docs/overview.md?raw";
import harnessQuickstart from "./dev-docs/harness-quickstart.md?raw";
import pluginQuickstart from "./dev-docs/plugin-quickstart.md?raw";
import extensionQuickstart from "./dev-docs/extension-quickstart.md?raw";
import accessRules from "./dev-docs/access-rules.md?raw";
import publishPrecheck from "./dev-docs/publish-precheck.md?raw";
import qualityStandards from "./dev-docs/quality-standards.md?raw";
import type { DevDoc } from "./dev-docs-types";

export const FALLBACK_DEV_DOCS: DevDoc[] = [
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
    sourcePath: "dev/overview.md",
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
    sourcePath: "dev/harness-quickstart.md",
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
    sourcePath: "dev/plugin-quickstart.md",
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
    sourcePath: "dev/extension-quickstart.md",
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
    sourcePath: "dev/access-rules.md",
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
    sourcePath: "dev/publish-precheck.md",
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
    sourcePath: "dev/quality-standards.md",
    tags: ["规范", "检查", "样例"],
  },
];
