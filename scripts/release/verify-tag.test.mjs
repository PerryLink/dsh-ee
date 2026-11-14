// scripts/release/verify-tag.mjs 的测试：合法/非法 tag、commit/sha 一致性、mainline、注解 tag、
// GITHUB_OUTPUT 与 JSON 输出。测试在临时 git 仓库里打本地 tag，不碰真实仓库、不推送任何东西。
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import { test } from "node:test";

const HERE = dirname(fileURLToPath(import.meta.url));
const VERIFY = join(HERE, "verify-tag.mjs");

function git(root, args) {
  const result = spawnSync("git", ["-C", root, ...args], { encoding: "utf8" });
  assert.equal(result.status, 0, `git ${args.join(" ")} 失败：${result.stderr}`);
  return result.stdout.trim();
}

function makeRepo() {
  const root = mkdtempSync(join(tmpdir(), "dsh-verify-tag-"));
  git(root, ["init", "-q", "-b", "main"]);
  git(root, ["config", "user.email", "test@example.com"]);
  git(root, ["config", "user.name", "test"]);
  writeFileSync(join(root, "README.md"), "# fixture\n");
  git(root, ["add", "-A"]);
  git(root, ["commit", "-q", "-m", "init"]);
  return root;
}

function commitMore(root, message = "more") {
  writeFileSync(join(root, `f-${Date.now()}.txt`), message);
  git(root, ["add", "-A"]);
  git(root, ["commit", "-q", "-m", message]);
  return git(root, ["rev-parse", "HEAD"]);
}

function run(root, args, env = {}) {
  const result = spawnSync(process.execPath, [VERIFY, "--root", root, ...args], {
    encoding: "utf8",
    env: { ...process.env, GITHUB_OUTPUT: "", ...env },
  });
  return { status: result.status, stdout: result.stdout ?? "", stderr: result.stderr ?? "" };
}

function withRepo(fn) {
  const root = makeRepo();
  try {
    fn(root);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}

test("合法的轻量 tag / 预发布 tag 通过，并写 GITHUB_OUTPUT", () => {
  withRepo((root) => {
    const commit = git(root, ["rev-parse", "HEAD"]);
    git(root, ["tag", "v0.1.0"]);
    const outputFile = join(root, "..", `gh-output-${process.pid}-${Date.now()}.txt`);
    const result = run(root, ["--tag", "v0.1.0", "--sha", commit, "--ref", "main"], { GITHUB_OUTPUT: outputFile });
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stdout, /tag ok：v0\.1\.0 → 版本 0\.1\.0/);
    const output = readFileSync(outputFile, "utf8");
    rmSync(outputFile, { force: true });
    assert.match(output, /^tag=v0\.1\.0$/m);
    assert.match(output, /^version=0\.1\.0$/m);
    assert.match(output, new RegExp(`^commit=${commit}$`, "m"));

    commitMore(root, "rc");
    git(root, ["tag", "v0.2.0-rc.1"]);
    const rc = run(root, ["--tag", "v0.2.0-rc.1", "--no-require-head", "--json"]);
    assert.equal(rc.status, 0, rc.stderr);
    const parsed = JSON.parse(rc.stdout);
    assert.equal(parsed.tag, "v0.2.0-rc.1");
    assert.equal(parsed.version, "0.2.0-rc.1");
    assert.equal(parsed.prerelease, "rc.1");
    assert.equal(parsed.annotated, false);
  });
});

test("注解 tag 的 commit 与 tag 对象 SHA 都接受", () => {
  withRepo((root) => {
    git(root, ["tag", "-a", "v0.1.0", "-m", "release 0.1.0"]);
    const commit = git(root, ["rev-parse", "HEAD"]);
    const tagObject = git(root, ["rev-parse", "v0.1.0"]);
    assert.notEqual(commit, tagObject);

    const byCommit = run(root, ["--tag", "v0.1.0", "--sha", commit]);
    assert.equal(byCommit.status, 0, byCommit.stderr);
    const byTagObject = run(root, ["--tag", "v0.1.0", "--sha", tagObject]);
    assert.equal(byTagObject.status, 0, byTagObject.stderr);
    const json = JSON.parse(run(root, ["--tag", "v0.1.0", "--json"]).stdout);
    assert.equal(json.annotated, true);
    assert.equal(json.commit, commit);

    const strict = run(root, ["--tag", "v0.1.0", "--require-annotated"]);
    assert.equal(strict.status, 0, strict.stderr);
  });
});

test("轻量 tag 在 --require-annotated 下失败", () => {
  withRepo((root) => {
    git(root, ["tag", "v0.1.0"]);
    const result = run(root, ["--tag", "v0.1.0", "--require-annotated"]);
    assert.equal(result.status, 1);
    assert.match(result.stderr, /轻量 tag/);
  });
});

test("非法 tag 名失败（缺 v / 非 SemVer / 前导零 / 空预发布）", () => {
  withRepo((root) => {
    git(root, ["tag", "1.2.3"]);
    git(root, ["tag", "v1.2"]);
    git(root, ["tag", "v01.2.3"]);
    git(root, ["tag", "v1.2.3-"]);
    git(root, ["tag", "release-1.2.3"]);
    for (const tag of ["1.2.3", "v1.2", "v01.2.3", "v1.2.3-", "release-1.2.3"]) {
      const result = run(root, ["--tag", tag, "--no-require-head"]);
      assert.equal(result.status, 1, `tag ${tag} 应该失败`);
      assert.match(result.stderr, /不是 vMAJOR\.MINOR\.PATCH/);
    }
    const missing = run(root, ["--tag", "v9.9.9"]);
    assert.equal(missing.status, 1);
    assert.match(missing.stderr, /本地没有 tag/);
  });
});

test("HEAD / --sha 与 tag 指向不一致时失败", () => {
  withRepo((root) => {
    git(root, ["tag", "v0.1.0"]);
    commitMore(root, "after tag");
    const headFail = run(root, ["--tag", "v0.1.0"]);
    assert.equal(headFail.status, 1);
    assert.match(headFail.stderr, /HEAD/);
    const headOk = run(root, ["--tag", "v0.1.0", "--no-require-head"]);
    assert.equal(headOk.status, 0, headOk.stderr);
    const shaFail = run(root, ["--tag", "v0.1.0", "--no-require-head", "--sha", "0".repeat(40)]);
    assert.equal(shaFail.status, 1);
    assert.match(shaFail.stderr, /--sha/);
  });
});

test("--ref 要求 tag 的 commit 在主干上", () => {
  withRepo((root) => {
    git(root, ["checkout", "-q", "-b", "side"]);
    const sideCommit = commitMore(root, "side work");
    git(root, ["tag", "-a", "v0.1.0", "-m", "side release"]);
    git(root, ["checkout", "-q", "main"]);
    const fail = run(root, ["--tag", "v0.1.0", "--no-require-head", "--ref", "main"]);
    assert.equal(fail.status, 1);
    assert.match(fail.stderr, /不在 main 上/);
    const pass = run(root, ["--tag", "v0.1.0", "--no-require-head", "--ref", sideCommit]);
    assert.equal(pass.status, 0, pass.stderr);
    const badRef = run(root, ["--tag", "v0.1.0", "--no-require-head", "--ref", "nope"]);
    assert.equal(badRef.status, 1);
    assert.match(badRef.stderr, /解析不到 commit/);
  });
});

test("非 git 仓库 / 缺参数返回明确错误", () => {
  const plain = mkdtempSync(join(tmpdir(), "dsh-verify-tag-plain-"));
  try {
    const notRepo = run(plain, ["--tag", "v0.1.0"]);
    assert.equal(notRepo.status, 1);
    assert.match(notRepo.stderr, /不是 git 仓库/);
  } finally {
    rmSync(plain, { recursive: true, force: true });
  }
  const usage = spawnSync(process.execPath, [VERIFY], { encoding: "utf8" });
  assert.equal(usage.status, 2);
  assert.match(usage.stderr, /缺少 --tag/);
  const unknown = spawnSync(process.execPath, [VERIFY, "--tag", "v0.1.0", "--bogus"], { encoding: "utf8" });
  assert.equal(unknown.status, 2);
  const help = spawnSync(process.execPath, [VERIFY, "--help"], { encoding: "utf8" });
  assert.equal(help.status, 0);
  assert.match(help.stdout, /发布 tag 校验/);
});
