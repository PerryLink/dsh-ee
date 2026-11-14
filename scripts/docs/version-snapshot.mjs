#!/usr/bin/env node
/**
 * docs 版本快照：从 git tag 生成静态的文档快照目录与索引。
 *
 * 发布时把 tag 上「站内 /docs 发布白名单」（scripts/docs/manifest.json）里的 Markdown
 * 原样复制到 `docs/versions/<tag>/`，并生成每版索引与本版清单副本，再更新
 * `docs/versions/index.json`。快照是静态文件，不依赖站内 /docs 生成管线，也不改变
 * /docs 仍以 main 的 docs/ 为单一事实源的现状；用于归档、离线查看和后续版本选择。
 *
 * 三个模式：
 *   node scripts/docs/version-snapshot.mjs --tag v0.1.0             生成/更新快照（写文件）
 *   node scripts/docs/version-snapshot.mjs --tag v0.1.0 --dry-run   只打印计划，不写文件
 *   node scripts/docs/version-snapshot.mjs --check                  只读校验已提交的快照索引
 *
 * 只依赖 Node 内置模块与 git；不创建/移动 tag，不修改 docs/ 里除 versions/ 之外的文件。
 * 快照内容来自 `git show <tag>:<path>`，因此工作区的未提交改动不会混进去。
 *
 * 可指定（测试或特殊环境用）：
 *   --root <dir>       仓库根，默认脚本所在仓库
 *   --docs-dir <dir>   文档目录（相对 root），默认 docs
 *   --manifest <file>  发布白名单（相对 root），默认 scripts/docs/manifest.json
 *   --out <dir>        快照目录（相对 root），默认 docs/versions
 *   --force            覆盖已有版本目录/索引里的冲突（例如 tag 被移动后重新生成）
 *   --check-git        校验模式额外对照本地 tag（需要 fetch 过该 tag）
 *   --json             输出 JSON 结论
 *
 * 退出码：0 成功/校验通过；1 失败；2 用法错误。
 */
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import { dirname, join, posix, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(HERE, "..", "..");

const SCHEMA = 1;
const DEFAULT_REPOSITORY = "https://github.com/hikariming/dsh-ee";
const INDEX_NOTE =
  "版本快照由 scripts/docs/version-snapshot.mjs 从 git tag 生成：每个版本目录是 tag 上发布白名单文档的静态副本" +
  "（docs/ + manifest.json + index.json）。站内 /docs 仍以 main 的 docs/ 为单一事实源；这里用于归档与后续版本选择。";

/** 与 scripts/docs/build.mjs 保持一致的 SemVer 校验（tag 去掉开头的 v）。 */
const SEMVER_RE =
  /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/;

class SnapshotError extends Error {}
class UsageError extends Error {}

function usage() {
  return [
    "docs 版本快照：从 git tag 生成静态快照目录/索引，或只读校验已提交的快照。",
    "",
    "  node scripts/docs/version-snapshot.mjs --tag v0.1.0             生成/更新（写文件）",
    "  node scripts/docs/version-snapshot.mjs --tag v0.1.0 --dry-run   只打印计划",
    "  node scripts/docs/version-snapshot.mjs --check                  只读校验（CI 用）",
    "",
    "  --tag <tag>        发布 tag（生成模式必填），形如 v1.2.3",
    "  --root <dir>       仓库根，默认脚本所在仓库",
    "  --docs-dir <dir>   文档目录，默认 docs",
    "  --manifest <file>  发布白名单，默认 scripts/docs/manifest.json",
    "  --out <dir>        快照目录，默认 docs/versions",
    "  --force            覆盖已有版本目录/索引里的冲突",
    "  --check-git        校验时额外对照本地 tag（需要 fetch 过 tag）",
    "  --json             输出 JSON 结论",
    "  --help, -h         显示本帮助",
  ].join("\n");
}

function parseArgs(argv) {
  const opts = {
    tag: "",
    root: REPO_ROOT,
    docsDir: "docs",
    manifest: "scripts/docs/manifest.json",
    out: "docs/versions",
    dryRun: false,
    check: false,
    force: false,
    checkGit: false,
    json: false,
    help: false,
  };
  const args = [...argv];
  while (args.length) {
    const arg = args.shift();
    if (arg === "--help" || arg === "-h") {
      opts.help = true;
      continue;
    }
    if (arg === "--dry-run") {
      opts.dryRun = true;
      continue;
    }
    if (arg === "--check") {
      opts.check = true;
      continue;
    }
    if (arg === "--force") {
      opts.force = true;
      continue;
    }
    if (arg === "--check-git") {
      opts.checkGit = true;
      continue;
    }
    if (arg === "--json") {
      opts.json = true;
      continue;
    }
    const match = /^--(tag|root|docs-dir|manifest|out)(?:=(.*))?$/.exec(arg);
    if (match) {
      const value = match[2] ?? args.shift();
      if (!value) throw new UsageError(`--${match[1]} 需要一个值`);
      opts[{ tag: "tag", root: "root", "docs-dir": "docsDir", manifest: "manifest", out: "out" }[match[1]]] = value;
      continue;
    }
    throw new UsageError(`未知参数：${arg}`);
  }
  if (opts.help) return opts;
  if (opts.check && opts.dryRun) throw new UsageError("--check 与 --dry-run 不能同时使用");
  if (opts.check && opts.tag) throw new UsageError("--check 不接受 --tag（校验已提交的快照，不生成）");
  if (!opts.check && !opts.tag) throw new UsageError("缺少 --tag（生成模式，例如 --tag v0.1.0）");
  opts.root = resolve(opts.root);
  return opts;
}

// ---------- 通用小工具 ----------

function json(value) {
  return JSON.stringify(value);
}

function sha256(buffer) {
  return createHash("sha256").update(buffer).digest("hex");
}

/** 相对仓库根显示路径；仓库外的路径（测试临时目录/构建产物）直接显示绝对路径。 */
function displayPath(root, file) {
  const rel = relative(root, file);
  return rel && !rel.startsWith("..") && !rel.includes(`..${sep}`) ? rel.split(sep).join("/") : file;
}

/** 把绝对路径换成仓库相对的 POSIX 路径；不在仓库根内时返回空字符串。 */
function repoPathOf(root, file) {
  const rel = relative(root, file);
  if (!rel || rel.startsWith("..") || rel.includes(`..${sep}`)) return "";
  return rel.split(sep).join("/");
}

function isInside(root, file) {
  const rel = relative(root, file);
  return rel === "" || (!rel.startsWith("..") && !rel.includes(`..${sep}`));
}

function isSafeRelative(value) {
  if (typeof value !== "string" || !value) return false;
  if (value.startsWith("/") || value.includes("\\")) return false;
  const parts = value.split("/");
  return parts.every((part) => part && part !== "." && part !== "..");
}

function parseReleaseTag(tag) {
  if (typeof tag !== "string" || !tag.startsWith("v")) return null;
  const match = SEMVER_RE.exec(tag.slice(1));
  if (!match) return null;
  return { version: tag.slice(1), prerelease: match[4] ?? "", build: match[5] ?? "" };
}

function parseSemver(version) {
  const match = SEMVER_RE.exec(version);
  if (!match) return null;
  return {
    core: [Number(match[1]), Number(match[2]), Number(match[3])],
    prerelease: match[4] ? match[4].split(".") : [],
  };
}

/** SemVer 2.0.0 §11 的优先级比较：返回负数 / 0 / 正数。 */
function compareSemver(a, b) {
  const left = parseSemver(a);
  const right = parseSemver(b);
  if (!left || !right) throw new SnapshotError(`无法比较版本号：${a} / ${b}`);
  for (let index = 0; index < 3; index += 1) {
    if (left.core[index] !== right.core[index]) return left.core[index] - right.core[index];
  }
  const leftPre = left.prerelease;
  const rightPre = right.prerelease;
  if (!leftPre.length && !rightPre.length) return 0;
  if (!leftPre.length) return 1;
  if (!rightPre.length) return -1;
  const total = Math.max(leftPre.length, rightPre.length);
  for (let index = 0; index < total; index += 1) {
    if (index >= leftPre.length) return -1;
    if (index >= rightPre.length) return 1;
    const leftPart = leftPre[index];
    const rightPart = rightPre[index];
    const leftNumeric = /^\d+$/.test(leftPart);
    const rightNumeric = /^\d+$/.test(rightPart);
    if (leftNumeric && rightNumeric) {
      const diff = Number(leftPart) - Number(rightPart);
      if (diff !== 0) return diff;
    } else if (leftNumeric) {
      return -1;
    } else if (rightNumeric) {
      return 1;
    } else if (leftPart !== rightPart) {
      return leftPart < rightPart ? -1 : 1;
    }
  }
  return 0;
}

// ---------- git 只读访问 ----------

function gitText(root, args) {
  const result = spawnSync("git", ["-C", root, ...args], { encoding: "utf8" });
  if (result.status !== 0) {
    throw new SnapshotError(`git ${args.join(" ")} 失败：${(result.stderr || result.stdout || "").trim()}`);
  }
  return result.stdout.trim();
}

function gitBuffer(root, args) {
  const result = spawnSync("git", ["-C", root, ...args]);
  if (result.status !== 0) {
    const stderr = result.stderr ? result.stderr.toString("utf8").trim() : "";
    throw new SnapshotError(`git ${args.join(" ")} 失败：${stderr}`);
  }
  return result.stdout;
}

function checkRepo(root) {
  if (!existsSync(root)) throw new SnapshotError(`--root 不存在：${root}`);
  const result = spawnSync("git", ["-C", root, "rev-parse", "--show-toplevel"], { encoding: "utf8" });
  if (result.status !== 0) throw new SnapshotError(`--root 不是 git 仓库：${root}`);
}

function tryResolveCommit(root, ref) {
  const result = spawnSync("git", ["-C", root, "rev-parse", "-q", "--verify", `${ref}^{commit}`], {
    encoding: "utf8",
  });
  return result.status === 0 ? result.stdout.trim() : "";
}

/** 读 tag 上某个文件；tag 自己先校验过 SemVer，路径也校验过是仓库安全相对路径。 */
function readTagFile(root, tag, repoPath) {
  return gitBuffer(root, ["show", `${tag}:${repoPath}`]);
}

// ---------- 文档解析（与 build.mjs 同口径的轻量版本） ----------

function stripInlineMarkdown(text) {
  return text
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/`([^`]*)`/g, "$1")
    .replace(/\*\*([^*]+)\*\*/g, "$1")
    .replace(/\*([^*]+)\*/g, "$1")
    .trim();
}

function firstHeading(body) {
  for (const line of body.split("\n")) {
    const match = /^#{1,3}\s+(.+?)\s*#*\s*$/.exec(line);
    if (match) return stripInlineMarkdown(match[1]);
  }
  return "";
}

function unquote(value) {
  const text = value.trim();
  if (text.length >= 2) {
    const first = text[0];
    const last = text[text.length - 1];
    if ((first === '"' || first === "'") && last === first) return text.slice(1, -1);
  }
  return text;
}

/**
 * 解析 front matter 里快照索引需要的字段（title/title_en/slug/category/updated_at），
 * 返回 {data, body}。只认识这几个键，其它键忽略，和 scripts/docs/build.mjs 的白名单一致。
 */
function parseFrontMatter(rawText) {
  const text = rawText.replace(/^\uFEFF/, "").replace(/\r\n?/g, "\n");
  const keys = new Set(["title", "title_en", "slug", "category", "updated_at"]);
  const lines = text.split("\n");
  if ((lines[0] ?? "").trim() !== "---") return { data: {}, body: text };
  const data = {};
  for (let index = 1; index < lines.length; index += 1) {
    const line = lines[index];
    if (line.trim() === "---") return { data, body: lines.slice(index + 1).join("\n") };
    if (!line.trim() || line.trim().startsWith("#")) continue;
    const match = /^([A-Za-z][A-Za-z0-9_]*)\s*:\s*(.*)$/.exec(line);
    if (!match) continue;
    if (!keys.has(match[1])) continue;
    data[match[1]] = unquote(match[2]);
  }
  return { data: {}, body: text };
}

function docMeta(entry, buffer, defaultCategory) {
  const text = buffer.toString("utf8");
  const { data, body } = parseFrontMatter(text);
  const title = (data.title ?? firstHeading(body)).trim();
  if (!title) throw new SnapshotError(`docs/${entry}: 缺少标题（front matter 的 title 或正文标题），无法写快照索引`);
  const titleEn = (data.title_en ?? title).trim() || title;
  const slug = (data.slug ?? posix.basename(entry, ".md")).trim();
  if (!/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(slug)) {
    throw new SnapshotError(`docs/${entry}: slug「${slug}」不合法（只允许小写字母、数字与连字符）`);
  }
  const category = (data.category ?? defaultCategory ?? "").trim();
  const updatedAt = (data.updated_at ?? "").trim();
  if (updatedAt && !/^\d{4}-\d{2}-\d{2}$/.test(updatedAt)) {
    throw new SnapshotError(`docs/${entry}: updated_at「${updatedAt}」不是合法的 YYYY-MM-DD`);
  }
  return {
    entry,
    slug,
    category,
    title,
    title_en: titleEn,
    updated_at: updatedAt,
    sha256: sha256(buffer),
    size: buffer.length,
    snapshot_path: `docs/${entry}`,
  };
}

// ---------- Markdown 可移植性检查 ----------

const LINK_RE = /!?\[[^\]]*\]\(\s*([^()\s]+)(?:\s+["'][^"']*["'])?\s*\)/g;

/** 把代码围栏与行内代码替换成等长空白，保留行号；语义与 scripts/check-docs.mjs 一致。 */
function stripCode(text) {
  const lines = text.split("\n");
  let inFence = false;
  return lines.map((line) => {
    if (/^\s*(```|~~~)/.test(line)) {
      inFence = !inFence;
      return "";
    }
    if (inFence) return "";
    return line.replace(/`[^`]*`/g, (match) => " ".repeat(match.length));
  });
}

/**
 * 快照会原样复制到 docs/versions/<tag>/docs/ 下；相对链接必须能落在同一批
 * 白名单文档里，否则复制后就是断链（CI 的 scripts/check-docs.mjs 也会走到这个目录）。
 * 只检查可移植性，不修改正文：需要跨出发布集时请用仓库绝对链接。
 */
function assertPortableLinks(text, entry, entries, label) {
  const stripped = stripCode(text);
  for (let index = 0; index < stripped.length; index += 1) {
    const line = stripped[index];
    let match;
    LINK_RE.lastIndex = 0;
    while ((match = LINK_RE.exec(line)) !== null) {
      const dest = match[1];
      if (/^[a-z][a-z0-9+.-]*:/i.test(dest) || dest.startsWith("//") || dest.startsWith("/")) continue;
      const hashIndex = dest.indexOf("#");
      const pathPart = (hashIndex === -1 ? dest : dest.slice(0, hashIndex)).split("?")[0];
      if (!pathPart) continue;
      let decoded;
      try {
        decoded = decodeURIComponent(pathPart);
      } catch {
        decoded = pathPart;
      }
      const target = posix.normalize(posix.join(posix.dirname(entry), decoded));
      if (!entries.includes(target)) {
        throw new SnapshotError(
          `${label}:${index + 1}: 相对链接「${dest}」指向发布白名单之外（解析为 docs/${target}）；` +
            "复制到版本快照后会断链。发布前请改用仓库绝对链接，或把目标加进发布白名单",
        );
      }
    }
  }
}

// ---------- manifest ----------

function parseManifestAtTag(root, tag, manifestPath) {
  const label = `${tag}:${manifestPath}`;
  let buffer;
  try {
    buffer = readTagFile(root, tag, manifestPath);
  } catch {
    throw new SnapshotError(
      `tag「${tag}」上没有发布白名单 ${manifestPath}；不生成快照（确认这个 tag 包含站内文档管线）`,
    );
  }
  let data;
  try {
    data = JSON.parse(buffer.toString("utf8"));
  } catch (error) {
    throw new SnapshotError(`${label}: JSON 解析失败：${error.message}`);
  }
  if (!data || typeof data !== "object" || Array.isArray(data) || !Array.isArray(data.docs)) {
    throw new SnapshotError(`${label}: 顶层必须是对象且 docs 是数组`);
  }
  const seen = new Set();
  const entries = data.docs.map((raw, index) => {
    if (typeof raw !== "string" || !raw.trim()) throw new SnapshotError(`${label}: docs[${index}] 必须是非空字符串`);
    const entry = raw.trim().replace(/\\/g, "/").replace(/^\.\//, "");
    if (entry.startsWith("/") || entry.split("/").includes("..") || !entry.endsWith(".md")) {
      throw new SnapshotError(`${label}: docs[${index}]「${raw}」必须是 docs/ 内的 .md 相对路径`);
    }
    if (seen.has(entry)) throw new SnapshotError(`${label}: 白名单重复列出 ${entry}`);
    seen.add(entry);
    return entry;
  });
  const defaultCategory = typeof data.defaultCategory === "string" ? data.defaultCategory : "";
  const repository =
    typeof data.repository === "string" && /^https?:\/\/[^\s]+$/.test(data.repository)
      ? data.repository.replace(/\/+$/, "")
      : DEFAULT_REPOSITORY;
  return { entries, defaultCategory, repository, buffer };
}

// ---------- 生成 ----------

function loadSnapshotFromTag(opts, root) {
  const tag = opts.tag;
  const parsed = parseReleaseTag(tag);
  if (!parsed) {
    throw new SnapshotError(`tag「${tag}」不是 vMAJOR.MINOR.PATCH[-pre] 形式（例如 v1.2.3、v1.2.3-rc.1）`);
  }
  const tagRef = `refs/tags/${tag}`;
  const tagType = spawnSync("git", ["-C", root, "cat-file", "-t", tagRef], { encoding: "utf8" });
  if (tagType.status !== 0) throw new SnapshotError(`本地没有 tag「${tag}」；先 fetch tag 再生成快照`);
  const commit = tryResolveCommit(root, tagRef);
  if (!commit) throw new SnapshotError(`tag「${tag}」没有解析到 commit`);
  const commitDate = gitText(root, ["show", "-s", "--format=%cI", commit]);

  const manifestRepoPath = repoPathOf(root, resolve(root, opts.manifest));
  if (!manifestRepoPath) throw new SnapshotError(`--manifest 必须在仓库根内：${opts.manifest}`);
  const docsDirAbs = resolve(root, opts.docsDir);
  if (!isInside(root, docsDirAbs)) throw new SnapshotError(`--docs-dir 必须在仓库根内：${opts.docsDir}`);
  const docsDirRepoPath = repoPathOf(root, docsDirAbs);
  if (!docsDirRepoPath) throw new SnapshotError(`--docs-dir 必须在仓库根内：${opts.docsDir}`);

  const manifest = parseManifestAtTag(root, tag, manifestRepoPath);
  const entries = manifest.entries;
  const docs = entries.map((entry) => {
    const buffer = readTagFile(root, tag, posix.join(docsDirRepoPath, entry));
    if (!buffer.length) throw new SnapshotError(`docs/${entry}: tag「${tag}」上的文件为空`);
    const meta = docMeta(entry, buffer, manifest.defaultCategory);
    assertPortableLinks(buffer.toString("utf8"), entry, entries, `docs/${entry}`);
    return { meta, buffer };
  });
  const slugs = new Map();
  for (const doc of docs) {
    const existing = slugs.get(doc.meta.slug);
    if (existing) throw new SnapshotError(`slug「${doc.meta.slug}」重复：docs/${existing} 与 docs/${doc.meta.entry}`);
    slugs.set(doc.meta.slug, doc.meta.entry);
  }
  const index = {
    schema: SCHEMA,
    tag,
    version: parsed.version,
    prerelease: parsed.prerelease || null,
    commit,
    commit_date: commitDate,
    source: {
      repository: manifest.repository,
      docs_dir: docsDirRepoPath,
      manifest: manifestRepoPath,
      manifest_sha256: sha256(manifest.buffer),
    },
    docs: docs.map((doc) => doc.meta),
  };
  return {
    tag,
    version: parsed.version,
    prerelease: parsed.prerelease || null,
    commit,
    commit_date: commitDate,
    repository: manifest.repository,
    docs,
    manifestBuffer: manifest.buffer,
    index,
    manifestPath: manifestRepoPath,
    docsRepoPath: docsDirRepoPath,
  };
}

function normalizeRootEntry(raw, label) {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) throw new SnapshotError(`${label}: 版本项必须是对象`);
  const parsed = parseReleaseTag(raw.tag);
  if (!parsed) throw new SnapshotError(`${label}: tag「${raw.tag}」不是合法发布 tag`);
  if (raw.version !== parsed.version) throw new SnapshotError(`${label}: version「${raw.version}」与 tag「${raw.tag}」不一致`);
  if (raw.snapshot !== raw.tag) throw new SnapshotError(`${label}: snapshot「${raw.snapshot}」必须等于 tag「${raw.tag}」`);
  if (!/^[0-9a-f]{40}$/.test(String(raw.commit))) throw new SnapshotError(`${label}: commit 必须是 40 位小写十六进制`);
  if (typeof raw.commit_date !== "string" || Number.isNaN(Date.parse(raw.commit_date))) {
    throw new SnapshotError(`${label}: commit_date 不是合法时间`);
  }
  if (!Number.isInteger(raw.docs) || raw.docs < 0) throw new SnapshotError(`${label}: docs 必须是非负整数`);
  return {
    tag: raw.tag,
    version: parsed.version,
    // 以 tag 名重新推导预发布标记，避免手改索引里的 prerelease 字段造成不一致。
    prerelease: parsed.prerelease || null,
    commit: raw.commit,
    commit_date: raw.commit_date,
    docs: raw.docs,
  };
}

/** 由版本项生成根索引：按 SemVer 从新到旧排序，标记 latest / stable_latest / prerelease_latest。 */
function buildRootIndex(versionEntries) {
  const sorted = [...versionEntries].sort((a, b) => compareSemver(b.version, a.version));
  const versions = sorted.map((entry, position) => ({
    tag: entry.tag,
    version: entry.version,
    prerelease: entry.prerelease ?? (parseReleaseTag(entry.tag)?.prerelease || null),
    commit: entry.commit,
    commit_date: entry.commit_date,
    docs: entry.docs,
    snapshot: entry.tag,
    stable: !(parseReleaseTag(entry.tag)?.prerelease || ""),
    latest: position === 0,
  }));
  return {
    schema: SCHEMA,
    note: INDEX_NOTE,
    latest: versions[0]?.tag ?? null,
    stable_latest: versions.find((version) => version.stable)?.tag ?? null,
    prerelease_latest: versions.find((version) => version.prerelease)?.tag ?? null,
    versions,
  };
}

function readExistingRootIndex(outDir, outLabel) {
  const file = join(outDir, "index.json");
  if (!existsSync(file)) return null;
  let data;
  try {
    data = JSON.parse(readFileSync(file, "utf8"));
  } catch (error) {
    throw new SnapshotError(`${outLabel}/index.json: JSON 解析失败：${error.message}；先修复或删除它再生成`);
  }
  if (!data || data.schema !== SCHEMA || !Array.isArray(data.versions)) {
    throw new SnapshotError(`${outLabel}/index.json: 格式无法识别（schema 应为 ${SCHEMA} 且 versions 是数组）`);
  }
  const entries = data.versions.map((raw, index) =>
    normalizeRootEntry(raw, `${outLabel}/index.json versions[${index}]`),
  );
  const tags = new Set();
  for (const entry of entries) {
    if (tags.has(entry.tag)) throw new SnapshotError(`${outLabel}/index.json: tag「${entry.tag}」重复`);
    tags.add(entry.tag);
  }
  return buildRootIndex(entries);
}

function jsonFile(value) {
  return `${JSON.stringify(value, null, 2)}\n`;
}

function writeSnapshot(opts, root, snapshot) {
  const outAbs = resolve(root, opts.out);
  const outLabel = displayPath(root, outAbs);
  const versionDir = join(outAbs, snapshot.tag);
  const versionDirLabel = `${outLabel}/${snapshot.tag}`;
  const existing = readExistingRootIndex(outAbs, outLabel);
  const existingEntry = existing?.versions.find((version) => version.tag === snapshot.tag) ?? null;
  if (existingEntry && existingEntry.commit !== snapshot.commit && !opts.force) {
    throw new SnapshotError(
      `已存在「${snapshot.tag}」的快照但 commit 不同（${existingEntry.commit.slice(0, 12)} ≠ ${snapshot.commit.slice(0, 12)}）；` +
        "tag 被移动过？确认后用 --force 覆盖，或改用新版本号",
    );
  }
  if (existsSync(versionDir) && !existingEntry && !opts.force) {
    throw new SnapshotError(
      `目录 ${versionDirLabel} 已存在但索引没有登记；确认无用后删除它，或加 --force 重新生成`,
    );
  }
  const others = (existing?.versions ?? []).filter((version) => version.tag !== snapshot.tag);
  const rootIndex = buildRootIndex([
    ...others,
    normalizeRootEntry(
      {
        tag: snapshot.tag,
        version: snapshot.version,
        prerelease: snapshot.prerelease,
        commit: snapshot.commit,
        commit_date: snapshot.commit_date,
        docs: snapshot.docs.length,
        snapshot: snapshot.tag,
      },
      snapshot.tag,
    ),
  ]);
  const files = [
    ...snapshot.docs.map((doc) => ({
      path: join(versionDir, "docs", doc.meta.entry),
      label: `${versionDirLabel}/docs/${doc.meta.entry}`,
      content: doc.buffer,
    })),
    {
      path: join(versionDir, "manifest.json"),
      label: `${versionDirLabel}/manifest.json`,
      content: snapshot.manifestBuffer,
    },
    { path: join(versionDir, "index.json"), label: `${versionDirLabel}/index.json`, content: jsonFile(snapshot.index) },
    { path: join(outAbs, "index.json"), label: `${outLabel}/index.json`, content: jsonFile(rootIndex) },
  ];
  if (opts.dryRun) {
    if (!opts.json) {
      console.log(`docs: dry-run：tag ${snapshot.tag} → 将写入 ${files.length} 个文件`);
      for (const file of files) console.log(`  ${file.label}`);
    }
    return {
      mode: "dry-run",
      tag: snapshot.tag,
      version: snapshot.version,
      commit: snapshot.commit,
      docs: snapshot.docs.length,
      files: files.map((file) => file.label),
    };
  }
  // 重新生成时先清掉旧版本目录，避免 tag 移动/白名单变化后残留过期副本。
  if (existsSync(versionDir)) rmSync(versionDir, { recursive: true, force: true });
  for (const file of files) {
    mkdirSync(dirname(file.path), { recursive: true });
    writeFileSync(file.path, file.content);
  }
  if (!opts.json) {
    console.log(
      `docs: 版本快照 ${snapshot.tag}（${snapshot.docs.length} 篇）→ ${versionDirLabel}；索引 ${outLabel}/index.json`,
    );
  }
  return {
    mode: "write",
    tag: snapshot.tag,
    version: snapshot.version,
    commit: snapshot.commit,
    docs: snapshot.docs.length,
    files: files.map((file) => file.label),
  };
}

// ---------- 只读校验 ----------

function readJsonFile(file, label) {
  if (!existsSync(file)) throw new SnapshotError(`缺少 ${label}（${file}）；生成快照后一起提交`);
  let data;
  try {
    data = JSON.parse(readFileSync(file, "utf8"));
  } catch (error) {
    throw new SnapshotError(`${label}: JSON 解析失败：${error.message}`);
  }
  return data;
}

function walkFiles(dir, base = dir, out = []) {
  if (!existsSync(dir)) return out.sort();
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) walkFiles(full, base, out);
    else out.push(relative(base, full).split(sep).join("/"));
  }
  return out.sort();
}

function validateVersionIndex(versionDir, versionLabel, rootEntry) {
  const index = readJsonFile(join(versionDir, "index.json"), `${versionLabel}/index.json`);
  if (!index || index.schema !== SCHEMA) throw new SnapshotError(`${versionLabel}/index.json: schema 应为 ${SCHEMA}`);
  if (index.tag !== rootEntry.tag) throw new SnapshotError(`${versionLabel}/index.json: tag「${index.tag}」与根索引不一致`);
  const parsed = parseReleaseTag(index.tag);
  if (!parsed || index.version !== parsed.version) {
    throw new SnapshotError(`${versionLabel}/index.json: version「${index.version}」与 tag 不一致`);
  }
  if (index.commit !== rootEntry.commit) throw new SnapshotError(`${versionLabel}/index.json: commit 与根索引不一致`);
  if (index.commit_date !== rootEntry.commit_date) {
    throw new SnapshotError(`${versionLabel}/index.json: commit_date 与根索引不一致`);
  }
  const source = index.source;
  if (
    !source ||
    typeof source.repository !== "string" ||
    !/^https?:\/\//.test(source.repository) ||
    !isSafeRelative(source.docs_dir) ||
    !isSafeRelative(source.manifest) ||
    !/^[0-9a-f]{64}$/.test(String(source.manifest_sha256))
  ) {
    throw new SnapshotError(`${versionLabel}/index.json: source 字段不完整（repository/docs_dir/manifest/manifest_sha256）`);
  }
  const manifestFile = join(versionDir, "manifest.json");
  if (!existsSync(manifestFile)) throw new SnapshotError(`缺少 ${versionLabel}/manifest.json`);
  const manifestBuffer = readFileSync(manifestFile);
  if (sha256(manifestBuffer) !== source.manifest_sha256) {
    throw new SnapshotError(`${versionLabel}/manifest.json: 内容与索引里的 manifest_sha256 不一致`);
  }
  let manifest;
  try {
    manifest = JSON.parse(manifestBuffer.toString("utf8"));
  } catch (error) {
    throw new SnapshotError(`${versionLabel}/manifest.json: JSON 解析失败：${error.message}`);
  }
  if (!manifest || !Array.isArray(manifest.docs)) {
    throw new SnapshotError(`${versionLabel}/manifest.json: docs 必须是数组`);
  }
  const entries = manifest.docs.map((raw, position) => {
    if (typeof raw !== "string" || !isSafeRelative(raw) || !raw.endsWith(".md")) {
      throw new SnapshotError(`${versionLabel}/manifest.json: docs[${position}]「${raw}」不是安全的 .md 相对路径`);
    }
    return raw.trim().replace(/^\.\//, "");
  });
  if (!Array.isArray(index.docs)) throw new SnapshotError(`${versionLabel}/index.json: docs 必须是数组`);
  if (index.docs.length !== entries.length) {
    throw new SnapshotError(`${versionLabel}/index.json: docs 数量（${index.docs.length}）与 manifest（${entries.length}）不一致`);
  }
  if (index.docs.length !== rootEntry.docs) {
    throw new SnapshotError(`${versionLabel}/index.json: docs 数量与根索引（${rootEntry.docs}）不一致`);
  }
  const expectedFiles = [];
  index.docs.forEach((doc, position) => {
    const prefix = `${versionLabel} docs[${position}]`;
    if (!doc || typeof doc !== "object" || Array.isArray(doc)) throw new SnapshotError(`${prefix}: 必须是对象`);
    if (doc.entry !== entries[position]) throw new SnapshotError(`${prefix}: entry「${doc.entry}」与 manifest 顺序不一致`);
    if (!isSafeRelative(String(doc.snapshot_path)) || doc.snapshot_path !== `docs/${doc.entry}`) {
      throw new SnapshotError(`${prefix}: snapshot_path「${doc.snapshot_path}」应为 docs/${doc.entry}`);
    }
    if (typeof doc.title !== "string" || !doc.title.trim()) throw new SnapshotError(`${prefix}: title 不能为空`);
    if (typeof doc.slug !== "string" || !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(doc.slug)) {
      throw new SnapshotError(`${prefix}: slug「${doc.slug}」不合法`);
    }
    if (!/^[0-9a-f]{64}$/.test(String(doc.sha256))) throw new SnapshotError(`${prefix}: sha256 不合法`);
    if (!Number.isInteger(doc.size) || doc.size < 0) throw new SnapshotError(`${prefix}: size 必须是非负整数`);
    const file = join(versionDir, doc.snapshot_path);
    if (!existsSync(file)) throw new SnapshotError(`缺少快照文件 ${versionLabel}/${doc.snapshot_path}`);
    const buffer = readFileSync(file);
    if (buffer.length !== doc.size) {
      throw new SnapshotError(`${versionLabel}/${doc.snapshot_path}: 大小与索引不一致（${buffer.length} ≠ ${doc.size}）`);
    }
    if (sha256(buffer) !== doc.sha256) {
      throw new SnapshotError(`${versionLabel}/${doc.snapshot_path}: sha256 与索引不一致，快照被改动过？`);
    }
    assertPortableLinks(buffer.toString("utf8"), doc.entry, entries, `${versionLabel}/${doc.snapshot_path}`);
    expectedFiles.push(doc.entry);
  });
  const actualFiles = walkFiles(join(versionDir, "docs"));
  const expectedSorted = [...expectedFiles].sort();
  if (actualFiles.length !== expectedSorted.length || actualFiles.some((file, index) => file !== expectedSorted[index])) {
    throw new SnapshotError(
      `${versionLabel}/docs: 文件列表与索引不一致（索引 ${expectedFiles.length} 个，实际 ${actualFiles.length} 个）；` +
        "删掉多余文件或重新生成快照",
    );
  }
  return { tag: index.tag, version: index.version, docs: index.docs.length, commit: index.commit };
}

function checkAgainstGit(root, versionDir, versionLabel, rootEntry) {
  const tagRef = `refs/tags/${rootEntry.tag}`;
  const commit = tryResolveCommit(root, tagRef);
  if (!commit) throw new SnapshotError(`${versionLabel}: 本地没有 tag「${rootEntry.tag}」，无法 --check-git；先 fetch 全部 tag`);
  if (commit !== rootEntry.commit) {
    throw new SnapshotError(`${versionLabel}: tag 指向 ${commit.slice(0, 12)}，快照索引记录 ${rootEntry.commit.slice(0, 12)}`);
  }
  const index = readJsonFile(join(versionDir, "index.json"), `${versionLabel}/index.json`);
  const manifestRepoPath = index.source.manifest;
  const docsDirRepoPath = index.source.docs_dir;
  const manifestBuffer = readTagFile(root, rootEntry.tag, manifestRepoPath);
  if (sha256(manifestBuffer) !== index.source.manifest_sha256) {
    throw new SnapshotError(`${versionLabel}: manifest.json 与 tag「${rootEntry.tag}」上的 ${manifestRepoPath} 不一致`);
  }
  for (const doc of index.docs) {
    const sourceBuffer = readTagFile(root, rootEntry.tag, posix.join(docsDirRepoPath, doc.entry));
    if (sha256(sourceBuffer) !== doc.sha256) {
      throw new SnapshotError(`${versionLabel}/${doc.snapshot_path}: 与 tag「${rootEntry.tag}」上的 docs/${doc.entry} 不一致`);
    }
  }
}

function checkSnapshots(opts, root) {
  const outAbs = resolve(root, opts.out);
  const outLabel = displayPath(root, outAbs);
  const rootIndex = readJsonFile(join(outAbs, "index.json"), `${outLabel}/index.json`);
  if (!rootIndex || rootIndex.schema !== SCHEMA) throw new SnapshotError(`${outLabel}/index.json: schema 应为 ${SCHEMA}`);
  if (!Array.isArray(rootIndex.versions)) throw new SnapshotError(`${outLabel}/index.json: versions 必须是数组`);
  const entries = rootIndex.versions.map((raw, index) =>
    normalizeRootEntry(raw, `${outLabel}/index.json versions[${index}]`),
  );
  const tags = new Set();
  const versionsSeen = new Set();
  for (const entry of entries) {
    if (tags.has(entry.tag)) throw new SnapshotError(`${outLabel}/index.json: tag「${entry.tag}」重复`);
    if (versionsSeen.has(entry.version)) throw new SnapshotError(`${outLabel}/index.json: version「${entry.version}」重复`);
    tags.add(entry.tag);
    versionsSeen.add(entry.version);
  }
  const expected = buildRootIndex(entries);
  const actualTags = rootIndex.versions.map((raw) => raw?.tag);
  const expectedTags = expected.versions.map((version) => version.tag);
  if (actualTags.join(",") !== expectedTags.join(",")) {
    throw new SnapshotError(
      `${outLabel}/index.json: 版本顺序应与 SemVer 从新到旧一致（期望 ${expectedTags.join(" > ") || "空"}）；` +
        "用 scripts/docs/version-snapshot.mjs 重新生成",
    );
  }
  if (rootIndex.latest !== expected.latest) {
    throw new SnapshotError(`${outLabel}/index.json: latest 应为 ${expected.latest ?? "null"}`);
  }
  if (rootIndex.stable_latest !== expected.stable_latest) {
    throw new SnapshotError(`${outLabel}/index.json: stable_latest 应为 ${expected.stable_latest ?? "null"}`);
  }
  if (rootIndex.prerelease_latest !== expected.prerelease_latest) {
    throw new SnapshotError(`${outLabel}/index.json: prerelease_latest 应为 ${expected.prerelease_latest ?? "null"}`);
  }
  const expectedDirs = new Set([...tags]);
  for (const name of readdirSync(outAbs)) {
    if (name === "index.json") continue;
    const full = join(outAbs, name);
    const isDir = statSync(full).isDirectory();
    if (!isDir || !expectedDirs.has(name)) {
      throw new SnapshotError(`${outLabel}: 未登记的条目 ${name}（快照目录只应有 index.json 与索引里的版本目录）`);
    }
  }
  let docCount = 0;
  for (const entry of entries) {
    const versionDir = join(outAbs, entry.tag);
    const versionLabel = `${outLabel}/${entry.tag}`;
    if (!existsSync(versionDir) || !statSync(versionDir).isDirectory()) {
      throw new SnapshotError(`缺少版本快照目录 ${versionLabel}（索引里有登记）`);
    }
    const summary = validateVersionIndex(versionDir, versionLabel, entry);
    if (opts.checkGit) checkAgainstGit(root, versionDir, versionLabel, entry);
    docCount += summary.docs;
  }
  if (!opts.json) {
    console.log(`docs: 版本快照校验通过（${entries.length} 个版本，${docCount} 篇文档）`);
  }
  return { mode: "check", versions: entries.length, docs: docCount, tags: entries.map((entry) => entry.tag) };
}

// ---------- 主流程 ----------

function main(argv) {
  let opts;
  try {
    opts = parseArgs(argv);
  } catch (error) {
    if (error instanceof UsageError) {
      console.error(`docs: ${error.message}\n\n${usage()}`);
      return 2;
    }
    throw error;
  }
  if (opts.help) {
    console.log(usage());
    return 0;
  }
  try {
    checkRepo(opts.root);
    const result = opts.check
      ? checkSnapshots(opts, opts.root)
      : writeSnapshot(opts, opts.root, loadSnapshotFromTag(opts, opts.root));
    if (opts.json) console.log(JSON.stringify(result, null, 2));
    return 0;
  } catch (error) {
    if (error instanceof SnapshotError) {
      console.error(`docs: ${error.message}`);
      return 1;
    }
    throw error;
  }
}

const exitCode = main(process.argv.slice(2));
if (exitCode !== 0) process.exitCode = exitCode;
