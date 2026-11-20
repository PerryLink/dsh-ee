// pin 校验（promo/openmotion/pin.mjs）的测试，不依赖真的 OpenMotion 源码：
// 用临时目录造出「HEAD 就是 pin」「HEAD 不是 pin」「不是 git 目录」几种 OPENMOTION_DIR，
// 再跑一遍 build-film.mjs，确认它在读模板前就按规则拦住 / 放行。
// 跑法：node --test promo/openmotion/pin.test.mjs
import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { after, test } from "node:test";
import { PINNED_OPENMOTION, checkPin, outDirReadme } from "./pin.mjs";

const HERE = dirname(fileURLToPath(import.meta.url));
const BUILD_FILM = join(HERE, "build-film.mjs");

const tempDirs = [];

function tempDir() {
  const dir = mkdtempSync(join(tmpdir(), "dsh-ee-openmotion-pin-"));
  tempDirs.push(dir);
  return dir;
}

function git(dir, ...args) {
  return execFileSync("git", [
    "-c", "init.defaultBranch=main",
    "-c", "user.name=pin-test",
    "-c", "user.email=pin-test@example.com",
    "-c", "commit.gpgsign=false",
    "-C", dir,
    ...args,
  ], { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }).trim();
}

/** 造一个临时 git 仓库：head 省略时提交一个空 commit，否则把 HEAD 指到给定 sha。 */
function makeRepo(head) {
  const dir = tempDir();
  git(dir, "init", "-q");
  if (head) {
    // git rev-parse 只解析引用、不检查对象是否存在，所以不用真造出一个 sha 等于 pin 的提交。
    mkdirSync(join(dir, ".git", "refs", "heads"), { recursive: true });
    writeFileSync(join(dir, ".git", "refs", "heads", "main"), `${head}\n`);
  } else {
    git(dir, "commit", "-q", "--allow-empty", "-m", "pin test");
  }
  return { dir, head: git(dir, "rev-parse", "HEAD") };
}

/** 跑真正的脚本；两个开关先清空，免得受开发机上的环境变量影响。 */
function runBuildFilm(env) {
  return spawnSync(process.execPath, [BUILD_FILM], {
    encoding: "utf8",
    env: { ...process.env, OPENMOTION_ALLOW_UNPINNED: "", OPENMOTION_REQUIRE_PIN: "", ...env },
    timeout: 60_000,
  });
}

after(() => {
  for (const dir of tempDirs) rmSync(dir, { recursive: true, force: true });
});

test("① HEAD 就是 pin 时校验通过", () => {
  const repo = makeRepo(PINNED_OPENMOTION.commit);
  assert.equal(repo.head, PINNED_OPENMOTION.commit);
  const pin = checkPin(repo.dir, {});
  assert.equal(pin.ok, true);
  assert.equal(pin.level, "ok");
  assert.equal(pin.status, "match");
  assert.equal(pin.head, PINNED_OPENMOTION.commit);
  assert.match(pin.message, /v0\.1\.0/);
  assert.match(pin.message, new RegExp(PINNED_OPENMOTION.commit));
});

test("① 集成：HEAD 就是 pin 时脚本继续读模板（随后因缺模板失败，不是 pin 错误）", () => {
  const repo = makeRepo(PINNED_OPENMOTION.commit);
  const run = runBuildFilm({ OPENMOTION_DIR: repo.dir });
  assert.equal(run.status, 1);
  assert.match(run.stdout, /OpenMotion pin：v0\.1\.0/);
  assert.match(run.stderr, /jsdom/);
  assert.doesNotMatch(run.stderr, /OpenMotion pin 校验失败/);
});

test("② HEAD 不是 pin 时报错，错误里有 pin 的 tag 和 commit", () => {
  const repo = makeRepo();
  const pin = checkPin(repo.dir, {});
  assert.equal(pin.ok, false);
  assert.equal(pin.level, "error");
  assert.equal(pin.status, "mismatch");
  assert.equal(pin.head, repo.head);
  assert.match(pin.message, /v0\.1\.0/);
  assert.match(pin.message, new RegExp(PINNED_OPENMOTION.commit));
  assert.match(pin.message, /OPENMOTION_ALLOW_UNPINNED=1/);
});

test("② 集成：HEAD 不是 pin 时脚本在读模板前退出 1", () => {
  const repo = makeRepo();
  const run = runBuildFilm({ OPENMOTION_DIR: repo.dir });
  assert.equal(run.status, 1);
  assert.match(run.stderr, /OpenMotion pin 校验失败/);
  assert.match(run.stderr, /v0\.1\.0/);
  assert.match(run.stderr, new RegExp(PINNED_OPENMOTION.commit));
  assert.doesNotMatch(run.stderr, /jsdom/);
});

test("③ OPENMOTION_ALLOW_UNPINNED=1 时 HEAD 不一致只警告", () => {
  const repo = makeRepo();
  const pin = checkPin(repo.dir, { OPENMOTION_ALLOW_UNPINNED: "1" });
  assert.equal(pin.ok, true);
  assert.equal(pin.level, "warn");
  assert.equal(pin.status, "mismatch");
  assert.match(pin.message, /OPENMOTION_ALLOW_UNPINNED=1/);
  assert.match(pin.message, new RegExp(PINNED_OPENMOTION.commit));
});

test("④ 不是 git 目录时警告但不拦截", () => {
  const dir = tempDir();
  const pin = checkPin(dir, {});
  assert.equal(pin.ok, true);
  assert.equal(pin.level, "warn");
  assert.equal(pin.status, "unknown");
  assert.equal(pin.head, null);
  assert.match(pin.message, /v0\.1\.0/);
  assert.match(pin.message, new RegExp(PINNED_OPENMOTION.commit));
});

test("⑤ 不是 git 目录 + OPENMOTION_REQUIRE_PIN=1 时失败", () => {
  const dir = tempDir();
  const pin = checkPin(dir, { OPENMOTION_REQUIRE_PIN: "1" });
  assert.equal(pin.ok, false);
  assert.equal(pin.level, "error");
  assert.equal(pin.status, "unknown");
  assert.match(pin.message, /OPENMOTION_REQUIRE_PIN=1/);
  assert.match(pin.message, new RegExp(PINNED_OPENMOTION.commit));
});

test("git 仓库里读不到 HEAD（还没有提交）也按「无法确认」处理", () => {
  const dir = tempDir();
  git(dir, "init", "-q");
  const pin = checkPin(dir, {});
  assert.equal(pin.status, "unknown");
  assert.equal(pin.ok, true);
  assert.match(pin.message, /读不到 HEAD/);
});

test("生成目录的 README 记下 pin 和这次实际用的 HEAD", () => {
  const pin = checkPin(makeRepo(PINNED_OPENMOTION.commit).dir, {});
  const readme = outDirReadme(pin, "zh");
  assert.match(readme, /v0\.1\.0/);
  assert.match(readme, new RegExp(PINNED_OPENMOTION.commit));
  assert.match(readme, /git HEAD：`[0-9a-f]{40}`/);
  assert.match(readme, /与 pin 一致/);
  assert.match(readme, /zh/);
});
