// scripts/docs/version-snapshot.mjs 的测试：
// - 生成（写文件）与 dry-run（不写文件）确实都从 git tag 读内容，而不是工作区；
// - 目录/索引结构、哈希、排序与 latest / stable_latest / prerelease_latest；
// - --check 能发现被改动/缺失/未登记的快照，--check-git 能对照本地 tag；
// - 非法的 tag / manifest / 相对链接 / 索引会明确失败。
// 全部在临时 git 仓库里打本地 tag，不碰真实仓库、不推送。
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";

const HERE = dirname(fileURLToPath(import.meta.url));
const SNAPSHOT = join(HERE, "version-snapshot.mjs");

const MANIFEST = {
  defaultCategory: "start",
  repository: "https://github.com/hikariming/dsh-ee",
  docs: ["dev/a.md", "dev/b.md"],
};
const DOC_A = [
  "---",
  "slug: a-doc",
  "category: plugin",
  'title: "A 标题"',
  'title_en: "A title"',
  "updated_at: 2026-01-02",
  "---",
  "",
  "# A 标题",
  "",
  "相对链接 [B](./b.md)；行内代码 `[c](./c.md)` 不该被当成链接。",
  "",
  "```js",
  'const text = "[c](./c.md)";',
  "```",
  "",
].join("\n");
const DOC_B = "# B 标题\n\nB 的正文。\n";
const UNPUBLISHED = "# C\n\n不在发布白名单里。\n";

function git(root, args) {
  const result = spawnSync("git", ["-C", root, ...args], { encoding: "utf8" });
  assert.equal(result.status, 0, `git ${args.join(" ")} 失败：${result.stderr}`);
  return result.stdout.trim();
}

function writeFiles(root, files) {
  for (const [rel, content] of Object.entries(files)) {
    const file = join(root, rel);
    mkdirSync(dirname(file), { recursive: true });
    writeFileSync(file, content);
  }
}

function initRepo(extraFiles = {}) {
  const root = mkdtempSync(join(tmpdir(), "dsh-version-snapshot-"));
  git(root, ["init", "-q", "-b", "main"]);
  git(root, ["config", "user.email", "test@example.com"]);
  git(root, ["config", "user.name", "test"]);
  writeFiles(root, {
    "scripts/docs/manifest.json": `${JSON.stringify(MANIFEST, null, 2)}\n`,
    "docs/dev/a.md": DOC_A,
    "docs/dev/b.md": DOC_B,
    "docs/other/c.md": UNPUBLISHED,
    ...extraFiles,
  });
  return root;
}

function commitAll(root, message = "fixture") {
  git(root, ["add", "-A"]);
  git(root, ["commit", "-q", "-m", message]);
}

function tag(root, name) {
  git(root, ["tag", "-a", name, "-m", `release ${name}`]);
}

function run(root, args) {
  const result = spawnSync(process.execPath, [SNAPSHOT, "--root", root, ...args], { encoding: "utf8" });
  return { status: result.status, stdout: result.stdout ?? "", stderr: result.stderr ?? "" };
}

function snapshotFile(root, rel) {
  return join(root, "docs", "versions", rel);
}

function readJson(file) {
  return JSON.parse(readFileSync(file, "utf8"));
}

function listTree(dir, out = []) {
  for (const name of readdirSync(dir).sort()) {
    const full = join(dir, name);
    if (statSync(full).isDirectory()) listTree(full, out);
    else out.push(full);
  }
  return out;
}

function withRepo(setup, fn) {
  const root = initRepo();
  try {
    setup(root);
    fn(root);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}

/** 标准 fixture：一个 commit + v0.1.0 注解 tag。 */
function taggedRepo(fn) {
  withRepo(
    (root) => {
      commitAll(root);
      tag(root, "v0.1.0");
    },
    fn,
  );
}

// ---------- dry-run 与生成 ----------

test("dry-run 只打印计划，不写任何文件", () => {
  taggedRepo((root) => {
    const result = run(root, ["--tag", "v0.1.0", "--dry-run"]);
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stdout, /dry-run：tag v0\.1\.0/);
    assert.match(result.stdout, /docs\/versions\/v0\.1\.0\/index\.json/);
    assert.equal(existsSync(join(root, "docs", "versions")), false, "dry-run 不应创建 docs/versions");

    const json = run(root, ["--tag", "v0.1.0", "--dry-run", "--json"]);
    assert.equal(json.status, 0, json.stderr);
    const parsed = JSON.parse(json.stdout);
    assert.equal(parsed.mode, "dry-run");
    assert.equal(parsed.tag, "v0.1.0");
    assert.equal(parsed.docs, 2);
    assert.ok(parsed.files.includes("docs/versions/index.json"));
    assert.equal(existsSync(join(root, "docs", "versions")), false);
  });
});

test("生成快照：内容来自 tag、目录结构与索引正确、可重复生成", () => {
  taggedRepo((root) => {
    const commit = git(root, ["rev-parse", "HEAD"]);
    const result = run(root, ["--tag", "v0.1.0"]);
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stdout, /版本快照 v0\.1\.0（2 篇）/);

    const versionDir = join(root, "docs", "versions", "v0.1.0");
    for (const rel of ["index.json", "manifest.json", "docs/dev/a.md", "docs/dev/b.md"]) {
      assert.equal(existsSync(join(versionDir, rel)), true, `缺少 ${rel}`);
    }
    assert.equal(existsSync(join(versionDir, "docs/dev/c.md")), false, "白名单外的文档不应复制");
    // 原样复制：与 tag 上的 blob 逐字节一致（含代码块里的“链接”）
    assert.equal(readFileSync(join(versionDir, "docs/dev/a.md"), "utf8"), DOC_A);
    assert.equal(git(root, ["show", "v0.1.0:docs/dev/b.md"]), DOC_B.trimEnd());
    assert.equal(
      readFileSync(join(versionDir, "manifest.json"), "utf8"),
      `${git(root, ["show", "v0.1.0:scripts/docs/manifest.json"])}\n`,
    );

    const version = readJson(join(versionDir, "index.json"));
    assert.equal(version.schema, 1);
    assert.equal(version.tag, "v0.1.0");
    assert.equal(version.version, "0.1.0");
    assert.equal(version.prerelease, null);
    assert.equal(version.commit, commit);
    assert.match(version.commit_date, /^\d{4}-\d{2}-\d{2}T/);
    assert.equal(version.source.docs_dir, "docs");
    assert.equal(version.source.manifest, "scripts/docs/manifest.json");
    assert.match(version.source.manifest_sha256, /^[0-9a-f]{64}$/);
    assert.deepEqual(
      version.docs.map((doc) => doc.entry),
      ["dev/a.md", "dev/b.md"],
    );
    assert.equal(version.docs[0].slug, "a-doc");
    assert.equal(version.docs[0].category, "plugin");
    assert.equal(version.docs[0].title, "A 标题");
    assert.equal(version.docs[0].title_en, "A title");
    assert.equal(version.docs[0].updated_at, "2026-01-02");
    assert.equal(version.docs[0].snapshot_path, "docs/dev/a.md");
    assert.equal(version.docs[1].slug, "b");
    assert.equal(version.docs[1].category, "start");
    assert.match(version.docs[0].sha256, /^[0-9a-f]{64}$/);
    assert.equal(version.docs[0].size, Buffer.byteLength(DOC_A));

    const rootIndex = readJson(join(root, "docs", "versions", "index.json"));
    assert.equal(rootIndex.schema, 1);
    assert.equal(rootIndex.latest, "v0.1.0");
    assert.equal(rootIndex.stable_latest, "v0.1.0");
    assert.equal(rootIndex.prerelease_latest, null);
    assert.deepEqual(rootIndex.versions.map((entry) => entry.tag), ["v0.1.0"]);
    assert.equal(rootIndex.versions[0].docs, 2);
    assert.equal(rootIndex.versions[0].latest, true);
    assert.equal(rootIndex.versions[0].stable, true);

    const before = listTree(join(root, "docs", "versions")).map((file) => [file, readFileSync(file)]);
    const again = run(root, ["--tag", "v0.1.0"]);
    assert.equal(again.status, 0, again.stderr);
    const after = listTree(join(root, "docs", "versions")).map((file) => [file, readFileSync(file)]);
    assert.deepEqual(after, before, "同一 tag 重复生成必须字节一致");
  });
});

test("生成内容来自 tag 而不是工作区（未提交改动不混进快照）", () => {
  taggedRepo((root) => {
    // 工作区改动：改文档、改 manifest、加一篇新文档；都不提交。
    writeFiles(root, {
      "docs/dev/a.md": "# 工作区版本\n\n后来改的。\n",
      "docs/dev/new.md": "# 新增\n\n不在 tag 里。\n",
      "scripts/docs/manifest.json": `${JSON.stringify({ ...MANIFEST, docs: ["dev/a.md", "dev/b.md", "dev/new.md"] }, null, 2)}\n`,
    });
    const result = run(root, ["--tag", "v0.1.0"]);
    assert.equal(result.status, 0, result.stderr);
    const versionDir = join(root, "docs", "versions", "v0.1.0");
    assert.equal(readFileSync(join(versionDir, "docs/dev/a.md"), "utf8"), DOC_A);
    assert.equal(existsSync(join(versionDir, "docs/dev/new.md")), false);
  });
});

// ---------- --check ----------

test("--check 校验已生成的快照，--check-git 对照本地 tag", () => {
  taggedRepo((root) => {
    assert.equal(run(root, ["--tag", "v0.1.0"]).status, 0);
    const check = run(root, ["--check"]);
    assert.equal(check.status, 0, check.stderr);
    assert.match(check.stdout, /校验通过（1 个版本，2 篇文档）/);
    const withGit = run(root, ["--check", "--check-git"]);
    assert.equal(withGit.status, 0, withGit.stderr);
    const json = JSON.parse(run(root, ["--check", "--json"]).stdout);
    assert.equal(json.mode, "check");
    assert.equal(json.versions, 1);
    assert.equal(json.docs, 2);
  });
});

test("--check 发现快照文件被改动 / 版本目录缺失 / 未登记条目", () => {
  taggedRepo((root) => {
    assert.equal(run(root, ["--tag", "v0.1.0"]).status, 0);
    const docFile = snapshotFile(root, join("v0.1.0", "docs", "dev", "a.md"));
    const original = readFileSync(docFile);
    writeFileSync(docFile, Buffer.concat([original, Buffer.from("tampered\n")]));
    const tampered = run(root, ["--check"]);
    assert.equal(tampered.status, 1);
    assert.match(tampered.stderr, /大小与索引不一致|sha256 与索引不一致/);

    assert.equal(run(root, ["--tag", "v0.1.0"]).status, 0, "重新生成应修复被改动的副本");
    assert.equal(run(root, ["--check"]).status, 0);

    rmSync(snapshotFile(root, "v0.1.0"), { recursive: true, force: true });
    const missing = run(root, ["--check"]);
    assert.equal(missing.status, 1);
    assert.match(missing.stderr, /缺少版本快照目录/);

    assert.equal(run(root, ["--tag", "v0.1.0"]).status, 0);
    mkdirSync(snapshotFile(root, "v9.9.9"), { recursive: true });
    const stray = run(root, ["--check"]);
    assert.equal(stray.status, 1);
    assert.match(stray.stderr, /未登记的条目 v9\.9\.9/);
  });
});

test("--check 发现索引标记/顺序/哈希不一致", () => {
  taggedRepo((root) => {
    assert.equal(run(root, ["--tag", "v0.1.0"]).status, 0);
    const rootIndexFile = snapshotFile(root, "index.json");
    const rootIndex = readJson(rootIndexFile);
    rootIndex.latest = null;
    writeFileSync(rootIndexFile, `${JSON.stringify(rootIndex, null, 2)}\n`);
    const badLatest = run(root, ["--check"]);
    assert.equal(badLatest.status, 1);
    assert.match(badLatest.stderr, /latest 应为 v0\.1\.0/);

    assert.equal(run(root, ["--tag", "v0.1.0"]).status, 0);
    const versionIndexFile = snapshotFile(root, join("v0.1.0", "index.json"));
    const versionIndex = readJson(versionIndexFile);
    versionIndex.docs[0].sha256 = "0".repeat(64);
    writeFileSync(versionIndexFile, `${JSON.stringify(versionIndex, null, 2)}\n`);
    const badHash = run(root, ["--check"]);
    assert.equal(badHash.status, 1);
    assert.match(badHash.stderr, /sha256 与索引不一致/);
  });
});

test("--check 在没有快照时接受空的引导索引，缺少索引则失败", () => {
  withRepo(
    (root) => {
      writeFiles(root, {
        "docs/versions/index.json": `${JSON.stringify(
          { schema: 1, note: "bootstrap", latest: null, stable_latest: null, prerelease_latest: null, versions: [] },
          null,
          2,
        )}\n`,
      });
    },
    (root) => {
      const empty = run(root, ["--check"]);
      assert.equal(empty.status, 0, empty.stderr);
      assert.match(empty.stdout, /0 个版本/);
      rmSync(join(root, "docs", "versions", "index.json"));
      const missing = run(root, ["--check"]);
      assert.equal(missing.status, 1);
      assert.match(missing.stderr, /缺少 docs\/versions\/index\.json/);
    },
  );
});

// ---------- 多版本 / 排序 ----------

test("多版本按 SemVer 排序，latest / stable_latest / prerelease_latest 正确", () => {
  withRepo(
    (root) => {
      commitAll(root);
      tag(root, "v0.1.0");
      writeFileSync(join(root, "docs/dev/a.md"), `${DOC_A}\n0.2 改动\n`);
      commitAll(root, "0.2 rc");
      tag(root, "v0.2.0-rc.1");
      writeFileSync(join(root, "docs/dev/a.md"), `${DOC_A}\n0.2 正式\n`);
      commitAll(root, "0.2");
      tag(root, "v0.2.0");
    },
    (root) => {
      for (const name of ["v0.1.0", "v0.2.0-rc.1", "v0.2.0"]) {
        const result = run(root, ["--tag", name]);
        assert.equal(result.status, 0, result.stderr);
      }
      const rootIndex = readJson(snapshotFile(root, "index.json"));
      assert.deepEqual(
        rootIndex.versions.map((entry) => entry.tag),
        ["v0.2.0", "v0.2.0-rc.1", "v0.1.0"],
      );
      assert.equal(rootIndex.latest, "v0.2.0");
      assert.equal(rootIndex.stable_latest, "v0.2.0");
      assert.equal(rootIndex.prerelease_latest, "v0.2.0-rc.1");
      const byTag = Object.fromEntries(rootIndex.versions.map((entry) => [entry.tag, entry]));
      assert.equal(byTag["v0.2.0"].stable, true);
      assert.equal(byTag["v0.2.0-rc.1"].stable, false);
      assert.equal(byTag["v0.2.0"].latest, true);
      assert.equal(byTag["v0.2.0-rc.1"].latest, false);
      assert.equal(byTag["v0.1.0"].latest, false);
      assert.equal(run(root, ["--check"]).status, 0);
      assert.equal(run(root, ["--check", "--check-git"]).status, 0);
    },
  );
});

test("tag 被移动后重新生成需要 --force；--check-git 能发现与 tag 不一致", () => {
  withRepo(
    (root) => {
      commitAll(root);
      tag(root, "v0.1.0");
    },
    (root) => {
      assert.equal(run(root, ["--tag", "v0.1.0"]).status, 0);
      // 把 tag 移到新 commit（模拟 tag 被移动），旧快照的 commit 就过期了。
      writeFileSync(join(root, "docs/dev/a.md"), `${DOC_A}\n新提交\n`);
      commitAll(root, "move tag");
      git(root, ["tag", "-f", "v0.1.0", "-m", "moved"]);
      const fail = run(root, ["--tag", "v0.1.0"]);
      assert.equal(fail.status, 1);
      assert.match(fail.stderr, /commit 不同/);
      const forced = run(root, ["--tag", "v0.1.0", "--force"]);
      assert.equal(forced.status, 0, forced.stderr);
      assert.equal(run(root, ["--check", "--check-git"]).status, 0);

      git(root, ["tag", "-d", "v0.1.0"]);
      const noTag = run(root, ["--check", "--check-git"]);
      assert.equal(noTag.status, 1);
      assert.match(noTag.stderr, /本地没有 tag/);
    },
  );
});

// ---------- 错误路径 ----------

test("非法 tag / 缺 tag / manifest 非法 / 白名单越界会失败", () => {
  withRepo(
    (root) => {
      commitAll(root);
      tag(root, "v0.1.0");
    },
    (root) => {
      const badName = run(root, ["--tag", "0.1.0"]);
      assert.equal(badName.status, 1);
      assert.match(badName.stderr, /不是 vMAJOR\.MINOR\.PATCH/);
      const noTag = run(root, ["--tag", "v9.9.9"]);
      assert.equal(noTag.status, 1);
      assert.match(noTag.stderr, /本地没有 tag/);

      // 在 tag 之后把 manifest 改成越界路径，但生成仍读 tag 上的合法 manifest；
      // 新 tag 才会看到非法 manifest。
      writeFiles(root, {
        "scripts/docs/manifest.json": `${JSON.stringify({ ...MANIFEST, docs: ["../evil.md"] }, null, 2)}\n`,
      });
      commitAll(root, "bad manifest");
      tag(root, "v0.2.0");
      const traversal = run(root, ["--tag", "v0.2.0"]);
      assert.equal(traversal.status, 1);
      assert.match(traversal.stderr, /相对路径/);
    },
  );
});

test("发布集外的相对链接会失败（复制到快照后会断链）", () => {
  withRepo(
    (root) => {
      writeFiles(root, {
        "docs/dev/a.md": "# A\n\n见 [C](../other/c.md) 与 [外链](https://example.com/x.md)。\n",
        "docs/dev/b.md": "# B\n",
      });
      commitAll(root);
      tag(root, "v0.1.0");
    },
    (root) => {
      const result = run(root, ["--tag", "v0.1.0"]);
      assert.equal(result.status, 1);
      assert.match(result.stderr, /断链|发布白名单/);
      assert.equal(existsSync(join(root, "docs", "versions")), false);
    },
  );
});

test("用法错误：--check 与 --tag 互斥、缺 --tag", () => {
  const noTag = spawnSync(process.execPath, [SNAPSHOT], { encoding: "utf8" });
  assert.equal(noTag.status, 2);
  assert.match(noTag.stderr, /缺少 --tag/);
  const both = spawnSync(process.execPath, [SNAPSHOT, "--check", "--tag", "v0.1.0"], { encoding: "utf8" });
  assert.equal(both.status, 2);
  const dryAndCheck = spawnSync(process.execPath, [SNAPSHOT, "--check", "--dry-run"], { encoding: "utf8" });
  assert.equal(dryAndCheck.status, 2);
  const help = spawnSync(process.execPath, [SNAPSHOT, "--help"], { encoding: "utf8" });
  assert.equal(help.status, 0);
  assert.match(help.stdout, /docs 版本快照/);
});
