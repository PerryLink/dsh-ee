// .github/workflows/release.yml 的结构测试 + 一个只覆盖本仓库用到的 YAML 子集的解析器。
//
// 仓库没有 JS 依赖，所以不引入 yaml 库：测试里实现「映射 / 序列 / 块标量 / 引号标量 / 注释」
// 这个够用的子集解析器，既验证 release.yml 能被解析成预期结构，也顺带测了解析器自己。
// 关键断言：tag 触发、PR 只读校验、构建/校验和/notes/docs 快照/GitHub Release 的步骤，
// 以及「不向任何 registry 发布」。
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";

const HERE = dirname(fileURLToPath(import.meta.url));
const WORKFLOW_FILE = join(HERE, "..", "..", ".github", "workflows", "release.yml");

// ---------- YAML 子集解析器 ----------

const KEY_RE = /^([A-Za-z0-9_.$/-]+):(?:[ ](.*))?$/;

function parseYaml(text, label = "YAML") {
  const lines = text.replace(/\r\n?/g, "\n").split("\n");
  if (lines.some((line) => line.includes("\t"))) throw new Error(`${label}: 含制表符（YAML 缩进只能用空格）`);
  let i = 0;

  const indentOf = (line) => line.length - line.trimStart().length;
  const isSkippable = (line) => line.trim() === "" || line.trimStart().startsWith("#");
  const where = () => `${label}:${i + 1}`;
  function skipSkippable() {
    while (i < lines.length && isSkippable(lines[i])) i += 1;
  }

  function parseScalar(raw) {
    const value = raw.trim();
    if (!value) return null;
    if (value.startsWith('"')) {
      if (!value.endsWith('"') || value.length < 2) throw new Error(`${where()}: 双引号没有闭合`);
      return JSON.parse(value);
    }
    if (value.startsWith("'")) {
      if (!value.endsWith("'") || value.length < 2) throw new Error(`${where()}: 单引号没有闭合`);
      return value.slice(1, -1).replace(/''/g, "'");
    }
    if (value === "true") return true;
    if (value === "false") return false;
    if (value === "null" || value === "~") return null;
    if (/^-?\d+$/.test(value)) return Number(value);
    if (/^[[{&*!|>%@`]/.test(value)) throw new Error(`${where()}: 不支持/未预期的标量语法「${value.slice(0, 40)}」`);
    return value;
  }

  function parseBlockScalar(parentIndent) {
    const block = [];
    while (i < lines.length) {
      const line = lines[i];
      if (line.trim() === "") {
        block.push("");
        i += 1;
        continue;
      }
      if (indentOf(line) <= parentIndent) break;
      block.push(line);
      i += 1;
    }
    while (block.length && block[block.length - 1] === "") block.pop();
    if (!block.length) return "";
    const minIndent = Math.min(...block.filter((line) => line.trim() !== "").map(indentOf));
    return `${block.map((line) => (line === "" ? "" : line.slice(minIndent))).join("\n")}\n`;
  }

  function setEntry(map, key, rest, entryIndent) {
    if (Object.hasOwn(map, key)) throw new Error(`${where()}: 重复的键「${key}」`);
    if (rest === undefined || rest.trim() === "") {
      skipSkippable();
      if (i < lines.length && indentOf(lines[i]) > entryIndent) {
        map[key] = parseNode(indentOf(lines[i]));
      } else {
        map[key] = null;
      }
      return;
    }
    const trimmed = rest.trim();
    if (/^[|>][+-]?$/.test(trimmed)) {
      map[key] = parseBlockScalar(entryIndent);
      return;
    }
    map[key] = parseScalar(rest);
  }

  function parseMap(indent) {
    const map = {};
    while (true) {
      skipSkippable();
      if (i >= lines.length) break;
      const actual = indentOf(lines[i]);
      if (actual < indent) break;
      if (actual > indent) throw new Error(`${where()}: 缩进不一致（期望 ${indent}，实际 ${actual}）`);
      const content = lines[i].slice(indent);
      if (content === "-" || content.startsWith("- ")) {
        throw new Error(`${where()}: 映射层里出现序列项`);
      }
      const match = KEY_RE.exec(content);
      if (!match) throw new Error(`${where()}: 无法解析的映射行「${content.slice(0, 60)}」`);
      i += 1;
      setEntry(map, match[1], match[2], indent);
    }
    return map;
  }

  function parseSeq(indent) {
    const items = [];
    while (true) {
      skipSkippable();
      if (i >= lines.length) break;
      if (indentOf(lines[i]) !== indent) break;
      const content = lines[i].slice(indent);
      if (!(content === "-" || content.startsWith("- "))) break;
      const rest = content === "-" ? "" : content.slice(2).trim();
      i += 1;
      if (rest === "") {
        skipSkippable();
        if (i < lines.length && indentOf(lines[i]) > indent) items.push(parseNode(indentOf(lines[i])));
        else items.push(null);
        continue;
      }
      const inlineKey = KEY_RE.exec(rest);
      if (!inlineKey) {
        items.push(parseScalar(rest));
        continue;
      }
      const itemIndent = indent + 2;
      const item = {};
      setEntry(item, inlineKey[1], inlineKey[2], itemIndent);
      while (true) {
        skipSkippable();
        if (i >= lines.length) break;
        const actual = indentOf(lines[i]);
        if (actual <= indent) break;
        if (actual !== itemIndent) throw new Error(`${where()}: 序列项的缩进不一致（期望 ${itemIndent}，实际 ${actual}）`);
        const inner = lines[i].slice(itemIndent);
        if (inner === "-" || inner.startsWith("- ")) break;
        const match = KEY_RE.exec(inner);
        if (!match) throw new Error(`${where()}: 无法解析的序列项映射行「${inner.slice(0, 60)}」`);
        i += 1;
        setEntry(item, match[1], match[2], itemIndent);
      }
      items.push(item);
    }
    return items;
  }

  function parseNode(indent) {
    skipSkippable();
    if (i >= lines.length) return null;
    const actual = indentOf(lines[i]);
    if (actual !== indent) throw new Error(`${where()}: 缩进不一致（期望 ${indent}，实际 ${actual}）`);
    const content = lines[i].slice(indent);
    if (content === "-" || content.startsWith("- ")) return parseSeq(indent);
    return parseMap(indent);
  }

  skipSkippable();
  const root = parseNode(0);
  skipSkippable();
  if (i < lines.length) throw new Error(`${where()}: 还有无法解析的内容「${lines[i].trim().slice(0, 60)}」`);
  if (!root || typeof root !== "object" || Array.isArray(root)) throw new Error(`${label}: 顶层必须是映射`);
  return root;
}

// ---------- workflow 断言 ----------

const workflowText = readFileSync(WORKFLOW_FILE, "utf8");
const workflow = parseYaml(workflowText, "release.yml");

function stepsOf(job) {
  return Array.isArray(job?.steps) ? job.steps : [];
}

function runScript(steps) {
  return steps
    .filter((step) => typeof step.run === "string")
    .map((step) => step.run)
    .join("\n");
}

test("YAML 子集解析器：映射/序列/块标量/引号标量可用，坏语法会报错", () => {
  assert.deepEqual(parseYaml("a: 1\nb:\n  - x\n  - \"y\"\nc: |\n  line1\n  line2\n"), {
    a: 1,
    b: ["x", "y"],
    c: "line1\nline2\n",
  });
  assert.deepEqual(parseYaml("list:\n  - name: x\n    run: hi\n  - plain\n"), {
    list: [{ name: "x", run: "hi" }, "plain"],
  });
  assert.throws(() => parseYaml("a:\n\tb: 1"), /制表符/);
  assert.throws(() => parseYaml("a: 1\na: 2"), /重复的键/);
  assert.throws(() => parseYaml("a: [1, 2]"), /不支持/);
});

test("触发：push v* tag；PR 改动这些路径时跑只读校验", () => {
  assert.equal(workflow.name, "release");
  assert.equal(workflow.permissions.contents, "read");
  assert.equal(workflow.concurrency["cancel-in-progress"], false);
  assert.deepEqual(workflow.on.push.tags, ["v*"]);
  const paths = workflow.on.pull_request.paths;
  for (const expected of [
    ".github/workflows/release.yml",
    "scripts/release/**",
    "scripts/docs/version-snapshot.mjs",
    "scripts/docs/version-snapshot.test.mjs",
    "docs/versions/**",
    "docs/RELEASING.md",
    "docs/VERSIONING.md",
  ]) {
    assert.ok(paths.includes(expected), `PR paths 缺少 ${expected}`);
  }
  assert.deepEqual(Object.keys(workflow.jobs).sort(), ["release", "validate"]);
});

test("validate job 只读：脚本自测 + 已提交快照校验 + 文档检查，不构建不发布", () => {
  const job = workflow.jobs.validate;
  assert.equal(job.if, "github.event_name == 'pull_request'");
  assert.equal(job["runs-on"], "ubuntu-latest");
  const node = stepsOf(job).find((step) => step.uses === "actions/setup-node@v5");
  assert.equal(node.with["node-version"], 22);
  const script = runScript(stepsOf(job));
  assert.match(script, /node --test/);
  for (const testFile of [
    "scripts/release/verify-tag.test.mjs",
    "scripts/release/release-workflow.test.mjs",
    "scripts/docs/version-snapshot.test.mjs",
  ]) {
    assert.ok(script.includes(testFile), `validate 没有跑 ${testFile}`);
  }
  assert.ok(script.includes("node scripts/docs/version-snapshot.mjs --check"), "validate 缺少快照只读校验");
  assert.ok(script.includes("node scripts/check-docs.mjs"), "validate 缺少文档链接检查");
  assert.doesNotMatch(script, /make release/);
  assert.doesNotMatch(script, /gh release/);
});

test("release job：tag 触发、写权限、检出完整历史与 tag", () => {
  const job = workflow.jobs.release;
  assert.equal(job.if, "github.event_name == 'push'");
  assert.equal(job["runs-on"], "ubuntu-latest");
  assert.equal(job.permissions.contents, "write");
  const checkout = stepsOf(job).find((step) => step.uses === "actions/checkout@v5");
  assert.ok(checkout, "release job 缺少 actions/checkout");
  assert.equal(checkout.with["fetch-depth"], 0, "打 tag 的 checkout 必须 fetch-depth: 0（tag 校验与 release notes 需要）");
  assert.ok(
    stepsOf(job).some((step) => step.uses === "actions/setup-go@v6"),
    "release job 缺少 setup-go",
  );
});

test("release 流程顺序：校验 tag → 构建 → 二进制版本 → 快照 → SHA256SUMS → notes → Release", () => {
  const job = workflow.jobs.release;
  const steps = stepsOf(job);
  const names = steps.map((step) => step.name ?? "").filter(Boolean);
  const position = (pattern) => {
    const index = names.findIndex((name) => pattern.test(name));
    assert.notEqual(index, -1, `release job 缺少步骤：${pattern}`);
    return index;
  };
  const verifyAt = position(/校验 tag/);
  const buildAt = position(/构建 Linux 静态二进制/);
  const binaryAt = position(/二进制内嵌版本/);
  const snapshotAt = position(/生成 docs 版本快照/);
  const checkSnapshotAt = position(/只读校验刚生成的 docs 版本快照/);
  const checksumAt = position(/发布产物|SHA256SUMS/);
  const notesAt = position(/^生成 release notes$/);
  const releaseAt = position(/创建 GitHub Release/);
  assert.ok(verifyAt < buildAt, "先校验 tag 再构建");
  assert.ok(buildAt < binaryAt, "构建后校验二进制版本");
  assert.ok(binaryAt < snapshotAt, "二进制版本校验在快照之前");
  assert.ok(snapshotAt < checkSnapshotAt, "先生成再只读校验快照");
  assert.ok(checkSnapshotAt < checksumAt, "快照校验后再打包产物");
  assert.ok(checksumAt < notesAt, "release notes 在产物之后");
  assert.ok(notesAt < releaseAt, "先 notes 再创建 Release");

  const script = runScript(steps);
  assert.ok(script.includes("node scripts/release/verify-tag.mjs"), "缺少 verify-tag 调用");
  assert.ok(script.includes('--sha "$GITHUB_SHA"'), "verify-tag 应带上 GITHUB_SHA");
  assert.ok(script.includes("--ref origin/main"), "verify-tag 应要求 tag 在 main 上");
  assert.ok(script.includes('make release VERSION="$GITHUB_REF_NAME"'), "缺少 make release 构建");
  assert.ok(
    script.includes('got="$(./dist/dsh-ee-linux-amd64 version)"') || script.includes("./dist/dsh-ee-linux-amd64 version"),
    "缺少二进制内嵌版本校验",
  );
  assert.ok(script.includes('test "${got}" = "$GITHUB_REF_NAME"'), "二进制版本必须等于 tag");
  assert.ok(
    script.includes('node scripts/docs/version-snapshot.mjs --tag "$GITHUB_REF_NAME" --out dist/docs-versions'),
    "缺少 docs 快照生成",
  );
  assert.ok(
    script.includes("node scripts/docs/version-snapshot.mjs --check --out dist/docs-versions --check-git"),
    "缺少 docs 快照只读校验",
  );
  assert.match(script, /sha256sum dsh-ee-/, "缺少 SHA256SUMS");
  assert.match(script, /git describe --tags/, "release notes 应找上一个 tag");
  assert.match(script, /git log --no-merges/, "release notes 应列变更");
  assert.ok(script.includes('gh release create "$GITHUB_REF_NAME"'), "缺少 GitHub Release 创建");
  assert.match(script, /--notes-file dist\/release-notes\.md/, "Release 使用生成的 notes");
  assert.match(script, /--verify-tag/, "gh release 应 --verify-tag");
  assert.match(script, /--prerelease/, "预发布 tag 应创建 GitHub prerelease");
  assert.match(script, /--max-count=200/, "释放 notes 的提交列表应设上限");
  assert.match(script, /docs-versions-\$\{GITHUB_REF_NAME\}\.tar\.gz/, "应把 docs 快照作为 asset 上传");
  assert.match(script, /dsh-ee-\$\{GITHUB_REF_NAME\}-linux-amd64/, "应上传版本化二进制");
  const releaseStep = steps.find((step) => /创建 GitHub Release/.test(step.name ?? ""));
  assert.equal(releaseStep.env.GH_TOKEN, "${{ github.token }}");
});

test("release job 上传 artifact，且不向任何 registry 发布", () => {
  const job = workflow.jobs.release;
  const upload = stepsOf(job).find((step) => step.uses === "actions/upload-artifact@v4");
  assert.ok(upload, "缺少 upload-artifact");
  assert.equal(upload.with.name, "release-${{ github.ref_name }}");
  assert.equal(upload.with.path, "dist/");
  for (const banned of [
    "docker push",
    "docker login",
    "npm publish",
    "pnpm publish",
    "yarn publish",
    "cargo publish",
    "twine upload",
    "ghcr.io",
    "registry-url",
    "NPM_TOKEN",
  ]) {
    assert.equal(workflowText.includes(banned), false, `workflow 不应包含「${banned}」（不发布到 registry）`);
  }
});

test("所有第三方 action 固定到主版本，步骤都有 name 或 uses", () => {
  const allSteps = Object.values(workflow.jobs).flatMap((job) => stepsOf(job));
  for (const step of allSteps) {
    assert.ok(step.name || step.uses, "每个 step 都要有 name 或 uses");
    if (step.uses) assert.match(step.uses, /^[a-z0-9-]+\/[a-z0-9-]+@v\d+$/, `action 版本应固定为 @vN：${step.uses}`);
    if (step.run) assert.match(step.run, /\n?$/, "run 只能是字符串");
  }
});
