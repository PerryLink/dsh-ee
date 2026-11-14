#!/usr/bin/env node
/**
 * 发布 tag 校验（只读）。
 *
 * 发布 workflow 在构建前先跑它：确认 tag 名是合法的 `v` + SemVer、tag 指向的 commit
 * 与当前 checkout（以及可选的 mainline ref）一致，避免「tag 和版本/构建产物对不上」。
 * 不创建、不移动、不删除 tag，也不改仓库里的任何文件；CI 下只把结论写进
 * `$GITHUB_OUTPUT`（可用 --no-github-output 关掉）。
 *
 * 用法：
 *   node scripts/release/verify-tag.mjs --tag v0.1.0
 *   node scripts/release/verify-tag.mjs --tag v0.1.0 --sha "$GITHUB_SHA" --ref origin/main
 *   node scripts/release/verify-tag.mjs --tag v0.1.0 --require-annotated --json
 *   node scripts/release/verify-tag.mjs --help
 *
 * 选项：
 *   --tag <tag>           必填，形如 v1.2.3 / v1.2.3-rc.1
 *   --sha <sha>           tag 事件的 GITHUB_SHA；必须等于 tag 指向的 commit（注解 tag 也接受 tag 对象本身）
 *   --ref <ref>           要求 tag 的 commit 可从该 ref 到达，如 origin/main（发布 tag 必须落在主干上）
 *   --root <dir>          仓库根，默认是本脚本所在仓库（测试用）
 *   --require-annotated   要求注解 tag（git tag -a），轻量 tag 直接失败
 *   --no-require-head     不要求当前 HEAD 正好是 tag 指向的 commit（默认要求）
 *   --no-github-output    即使设置了 $GITHUB_OUTPUT 也不写
 *   --json                向 stdout 打印 JSON 结论
 *
 * 退出码：0 通过；1 校验失败；2 用法错误。
 * 只依赖 Node 内置模块与 git。
 */
import { spawnSync } from "node:child_process";
import { appendFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(HERE, "..", "..");

/** SemVer 2.0.0 官方建议的正则（tag 去掉开头的 v 之后）。 */
const SEMVER_RE =
  /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/;

class UsageError extends Error {}
class VerifyError extends Error {}

function usage() {
  return [
    "发布 tag 校验（只读）：tag 名、tag 指向的 commit、与当前 checkout / mainline ref 是否一致。",
    "",
    "  node scripts/release/verify-tag.mjs --tag v0.1.0 [选项]",
    "",
    "  --tag <tag>           必填，形如 v1.2.3（可带 -rc.1 / +build 后缀）",
    "  --sha <sha>           tag 事件的提交（GITHUB_SHA），必须与 tag 指向一致",
    "  --ref <ref>           要求 tag 的 commit 可从该 ref 到达（例如 origin/main）",
    "  --root <dir>          仓库根，默认脚本所在仓库",
    "  --require-annotated   要求注解 tag，轻量 tag 失败",
    "  --no-require-head     不要求 HEAD 是 tag 指向的 commit",
    "  --no-github-output    不写 $GITHUB_OUTPUT",
    "  --json                打印 JSON 结论",
    "  --help, -h            显示本帮助",
  ].join("\n");
}

function parseArgs(argv) {
  const opts = {
    tag: "",
    sha: "",
    ref: "",
    root: REPO_ROOT,
    requireAnnotated: false,
    requireHead: true,
    githubOutput: !argv.includes("--no-github-output"),
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
    if (arg === "--json") {
      opts.json = true;
      continue;
    }
    if (arg === "--require-annotated") {
      opts.requireAnnotated = true;
      continue;
    }
    if (arg === "--no-require-head") {
      opts.requireHead = false;
      continue;
    }
    if (arg === "--no-github-output") {
      opts.githubOutput = false;
      continue;
    }
    const match = /^--(tag|sha|ref|root)(?:=(.*))?$/.exec(arg);
    if (match) {
      const value = match[2] ?? args.shift();
      if (!value) throw new UsageError(`--${match[1]} 需要一个值`);
      opts[{ tag: "tag", sha: "sha", ref: "ref", root: "root" }[match[1]]] = value;
      continue;
    }
    throw new UsageError(`未知参数：${arg}`);
  }
  if (!opts.help) {
    if (!opts.tag) throw new UsageError("缺少 --tag（例如 --tag v0.1.0）");
    opts.root = resolve(opts.root);
  }
  return opts;
}

function git(root, args) {
  const result = spawnSync("git", ["-C", root, ...args], { encoding: "utf8" });
  return {
    ok: result.status === 0,
    status: result.status,
    stdout: (result.stdout ?? "").trim(),
    stderr: (result.stderr ?? "").trim(),
  };
}

function resolveCommit(root, ref) {
  const result = git(root, ["rev-parse", "-q", "--verify", `${ref}^{commit}`]);
  return result.ok ? result.stdout : "";
}

function checkRepo(root) {
  const top = git(root, ["rev-parse", "--show-toplevel"]);
  if (!top.ok) throw new VerifyError(`--root 不是 git 仓库：${root}`);
  return top.stdout;
}

/** 只做名字/结构校验：返回 {version, prerelease, build, core}；不合法返回 null。 */
function parseReleaseTag(tag) {
  if (!tag.startsWith("v")) return null;
  const match = SEMVER_RE.exec(tag.slice(1));
  if (!match) return null;
  return {
    version: tag.slice(1),
    prerelease: match[4] ?? "",
    build: match[5] ?? "",
    core: [Number(match[1]), Number(match[2]), Number(match[3])],
  };
}

function verify(opts) {
  const root = checkRepo(opts.root);
  const parsed = parseReleaseTag(opts.tag);
  if (!parsed) {
    throw new VerifyError(
      `tag「${opts.tag}」不是 vMAJOR.MINOR.PATCH[-pre][+build] 形式（例如 v1.2.3、v1.2.3-rc.1）；` +
        "发布 tag 只能由 v + SemVer 组成，且不能有前导零",
    );
  }
  const tagRef = `refs/tags/${opts.tag}`;
  const tagObjectType = git(root, ["cat-file", "-t", tagRef]);
  if (!tagObjectType.ok) {
    throw new VerifyError(
      `本地没有 tag「${opts.tag}」；发布前先 push tag，或在 clone 里 fetch 全部 tag（actions/checkout 需要 fetch-depth: 0）`,
    );
  }
  const annotated = tagObjectType.stdout === "tag";
  if (opts.requireAnnotated && !annotated) {
    throw new VerifyError(`tag「${opts.tag}」是轻量 tag；发布 tag 要求注解 tag（git tag -a）`);
  }
  const tagObject = git(root, ["rev-parse", "-q", "--verify", tagRef]).stdout;
  const commit = resolveCommit(root, tagRef);
  if (!commit) throw new VerifyError(`tag「${opts.tag}」没有解析到 commit`);
  const head = resolveCommit(root, "HEAD");
  if (opts.requireHead && head !== commit) {
    throw new VerifyError(
      `当前 checkout 的 HEAD（${short(head)}）不是 tag「${opts.tag}」指向的 commit（${short(commit)}）；` +
        "发布 workflow 必须检出 tag（ref: ${{ github.ref }}），本地校验请 git checkout 到该 tag",
    );
  }
  if (opts.sha) {
    const sha = opts.sha.toLowerCase();
    const accepted = [commit, tagObject].filter(Boolean).map((value) => value.toLowerCase());
    if (!accepted.includes(sha)) {
      throw new VerifyError(
        `--sha ${short(opts.sha)} 与 tag「${opts.tag}」不对应（tag commit ${short(commit)}` +
          `${annotated ? `，tag 对象 ${short(tagObject)}` : ""}）`,
      );
    }
  }
  let refCommit = "";
  let ref = opts.ref;
  if (ref) {
    refCommit = resolveCommit(root, ref);
    if (!refCommit) throw new VerifyError(`--ref「${ref}」解析不到 commit`);
    const ancestor = spawnSync("git", ["-C", root, "merge-base", "--is-ancestor", commit, refCommit]);
    if (ancestor.status !== 0) {
      throw new VerifyError(
        `tag「${opts.tag}」的 commit ${short(commit)} 不在 ${ref} 上（${short(refCommit)}）；` +
          "发布 tag 必须先合并到主干，再从主干打 tag",
      );
    }
  }
  return {
    ok: true,
    tag: opts.tag,
    version: parsed.version,
    prerelease: parsed.prerelease || null,
    build: parsed.build || null,
    commit,
    annotated,
    head,
    ref: ref || null,
    refCommit: refCommit || null,
    root: root === REPO_ROOT ? "." : root,
  };
}

function short(sha) {
  return (sha ?? "").slice(0, 12);
}

function writeGithubOutput(result) {
  const file = process.env.GITHUB_OUTPUT;
  if (!file) return;
  appendFileSync(
    file,
    [`tag=${result.tag}`, `version=${result.version}`, `commit=${result.commit}`, `prerelease=${result.prerelease ?? ""}`].join(
      "\n",
    ) + "\n",
  );
}

function main(argv) {
  let opts;
  try {
    opts = parseArgs(argv);
  } catch (error) {
    if (error instanceof UsageError) {
      console.error(`release: ${error.message}\n\n${usage()}`);
      return 2;
    }
    throw error;
  }
  if (opts.help) {
    console.log(usage());
    return 0;
  }
  try {
    const result = verify(opts);
    if (opts.githubOutput) writeGithubOutput(result);
    if (opts.json) {
      console.log(JSON.stringify(result, null, 2));
    } else {
      console.log(
        `release: tag ok：${result.tag} → 版本 ${result.version}` +
          `${result.prerelease ? `（预发布 ${result.prerelease}）` : ""} ` +
          `@ ${short(result.commit)}${result.annotated ? "（注解 tag）" : "（轻量 tag）"}` +
          `${result.ref ? `，在 ${result.ref} 上` : ""}`,
      );
    }
    return 0;
  } catch (error) {
    if (error instanceof VerifyError) {
      console.error(`release: ${error.message}`);
      return 1;
    }
    throw error;
  }
}

const exitCode = main(process.argv.slice(2));
if (exitCode !== 0) process.exitCode = exitCode;
