// OpenMotion 模板的版本校验。
//
// 成片的画面来自本机 OpenMotion 源码里的模板
// （library/templates/catalog/motion-f6f244d1-c549-4081-8a5d-f0e1e3660044/source）。
// 换了 OpenMotion 的版本，同一个 build-film.mjs 会静默产出不一样的画面，所以读模板前先核对
// OPENMOTION_DIR 的 git HEAD 是不是 promo/README.md 里 pin 的版本：
//   - HEAD 与 pin 一致：通过；
//   - HEAD 不一致：默认报错（OPENMOTION_ALLOW_UNPINNED=1 时只警告）；
//   - 判断不了版本（不是 git 目录 / 没有 git / 读不到 HEAD）：默认警告（OPENMOTION_REQUIRE_PIN=1 时报错）。
// 只核对 git HEAD，不核对模板文件本身的哈希。
import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
import { join } from "node:path";

/** 生成短片用的 OpenMotion 版本；promo/README.md 写的是同一个 pin，升级时两边一起改。 */
export const PINNED_OPENMOTION = Object.freeze({
  tag: "v0.1.0",
  commit: "19f7424e0cf45606c463216f41c6f8a9db393bfe",
});

const ALLOW_UNPINNED = "OPENMOTION_ALLOW_UNPINNED";
const REQUIRE_PIN = "OPENMOTION_REQUIRE_PIN";
const PIN_TEXT = `${PINNED_OPENMOTION.tag}（${PINNED_OPENMOTION.commit}）`;

/** dir 的 git HEAD；不是 git 仓库、本机没有 git 或读不到 HEAD 都返回 null。 */
function readHead(dir) {
  try {
    const head = execFileSync("git", ["-C", dir, "rev-parse", "HEAD"], {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"],
    }).trim().toLowerCase();
    return /^[0-9a-f]{40}$/.test(head) ? head : null;
  } catch {
    return null;
  }
}

function isPinned(head) {
  const actual = head.toLowerCase();
  const pinned = PINNED_OPENMOTION.commit;
  return actual === pinned || actual.startsWith(pinned) || pinned.startsWith(actual);
}

function verdict(ok, level, status, head, message) {
  return { ok, level, status, head, message };
}

function noVersionVerdict(env, dir, reason) {
  if (env[REQUIRE_PIN] === "1") {
    return verdict(false, "error", "unknown", null,
      `OpenMotion pin 校验失败：无法确认 OPENMOTION_DIR=${dir} 的版本（${reason}），` +
        `而 ${REQUIRE_PIN}=1 要求是 ${PIN_TEXT}。`);
  }
  return verdict(true, "warn", "unknown", null,
    `OpenMotion pin 警告：无法确认 OPENMOTION_DIR=${dir} 的版本（${reason}），期望 ${PIN_TEXT}；` +
      `画面可能来自别的模板版本（要严格失败可设 ${REQUIRE_PIN}=1）。`);
}

/**
 * 核对 dir（一般是 OPENMOTION_DIR）的 OpenMotion 版本。
 * 返回 { ok, level, status, head, message }：ok=false 时调用方应打印 message 后退出 1；
 * level=warn 时打印 message 继续；level=ok 时 message 是版本信息。
 */
export function checkPin(dir, env = process.env) {
  const head = readHead(dir);
  if (!head) {
    const reason = existsSync(join(dir, ".git"))
      ? "它是 git 仓库，但读不到 HEAD（git 不可用，或仓库里还没有提交）"
      : "它不是 git 仓库，或本机没有 git";
    return noVersionVerdict(env, dir, reason);
  }
  if (!isPinned(head)) {
    const detail = `OPENMOTION_DIR=${dir} 的 git HEAD 是 ${head}，pin 的是 ${PIN_TEXT}`;
    if (env[ALLOW_UNPINNED] === "1" && env[REQUIRE_PIN] !== "1") {
      return verdict(true, "warn", "mismatch", head,
        `OpenMotion pin 警告：${detail}；按 ${ALLOW_UNPINNED}=1 继续，画面可能和 pin 的模板不同。`);
    }
    return verdict(false, "error", "mismatch", head,
      `OpenMotion pin 校验失败：${detail}。换模板版本会静默产出不一样的画面，` +
        `请先切到 pin 的版本（git -C ${dir} checkout ${PINNED_OPENMOTION.commit}；本地没有这个 commit 就先 git -C ${dir} fetch --tags）；` +
        `确知没有影响时可用 ${ALLOW_UNPINNED}=1 跳过校验，真要升级 OpenMotion 就改 pin.mjs 和 promo/README.md 里的 pin。`);
  }
  return verdict(true, "ok", "match", head,
    `OpenMotion pin：${PIN_TEXT}，与 OPENMOTION_DIR 的 git HEAD（${head}）一致。`);
}

/** 生成目录里放的来源说明：pin 加上这次实际用的 HEAD，方便从工程目录追溯画面从哪来。 */
export function outDirReadme(pin, lang) {
  const head = pin.head ? `\`${pin.head}\`` : "无法确认";
  const check = pin.status === "match"
    ? "与 pin 一致"
    : pin.status === "mismatch"
      ? `不一致（按 ${ALLOW_UNPINNED}=1 放行，画面可能来自别的模板版本）`
      : "无法确认（按宽松模式继续，画面可能来自别的模板版本）";
  return [
    `# dsh-ee 产品短片（${lang}）`,
    "",
    "由 `promo/openmotion/build-film.mjs` 生成，不要手改；改文字去改那个脚本里的 COPY，再重新生成。",
    "",
    "- 模板：OpenMotion「Glean Agents 发布」（Hyperframes 工程）",
    `- OpenMotion pin：${PIN_TEXT}`,
    `- 生成时 \`OPENMOTION_DIR\` 的 git HEAD：${head}`,
    `- pin 校验：${check}`,
    "",
  ].join("\n");
}
