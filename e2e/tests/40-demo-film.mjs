// 演示站首页的产品短片（c854fe3）：DSH_EE_DEMO_MODE=1 时 / 首屏播放一分钟短片。
// 短片是代码画的、画面在影子 DOM 里，按界面语言出中文 / 英文 / 日文（浏览器日语 + 界面英文时出日文）。
// 这里确认：默认 enforce CSP 下短片能画出来、没有 CSP 违规和 JS 报错、自动播放推进、切语言画面跟着换。
// 隔离模式起一个 demo 实例；非隔离模式跳过（需要独立实例才能打开 DSH_EE_DEMO_MODE）。
import { assert, assertEq, newPage, step } from "../lib.mjs";
import { findFreePort, isManaged, startManagedServer } from "../server.mjs";

const cspViolation = /content security policy|refused to (load|execute|apply|frame|connect)/i;

/** 把页面上的 CSP 违规记进 t.errors（run.mjs 会判失败），和 38-csp 一样只看 console error。 */
function watchCSP(t, page) {
  page.on("console", (msg) => {
    if (msg.type() === "error" && cspViolation.test(msg.text())) t.errors.push("CSP 违规：" + msg.text());
  });
}

/** 读短片状态：data-film-lang / data-paused，加上影子 DOM 里画出来的第一幕。 */
async function filmState(page) {
  return page.evaluate(() => {
    const box = document.querySelector("[data-intro-film]");
    if (!box) return null;
    // 影子根挂在 [data-intro-film] 里那层固定 1280×720 的容器上；短片样式与画面都在影子根里。
    const host = [...box.querySelectorAll("div")].find((el) => el.shadowRoot);
    const root = host?.shadowRoot ?? null;
    return {
      lang: box.getAttribute("data-film-lang"),
      paused: box.getAttribute("data-paused"),
      ready: !!root?.querySelector("#root"),
      scenes: root?.querySelectorAll(".scene").length ?? 0,
      text: root?.querySelector(".scene")?.textContent?.trim() ?? "",
      styles: root?.querySelectorAll("style").length ?? 0,
      progress: Number.parseFloat(box.children[1]?.style.width ?? "0") || 0,
    };
  });
}

/** 等到短片按目标语言画出来（en / zh / ja 的画面是按需加载的三份）。 */
async function waitFilm(page, lang) {
  await page.waitForFunction(
    (want) => {
      const box = document.querySelector("[data-intro-film]");
      const host = box && [...box.querySelectorAll("div")].find((el) => el.shadowRoot);
      return box?.getAttribute("data-film-lang") === want && !!host?.shadowRoot?.querySelector("#root");
    },
    lang,
    { timeout: 20000 },
  );
  return filmState(page);
}

export default async function (t) {
  if (!isManaged()) {
    step("非隔离模式：跳过演示站首页短片用例（需要 E2E_MANAGED=1 起 DSH_EE_DEMO_MODE 的独立实例）");
    return;
  }
  // 隔离模式下 run.mjs 已经占着 E2E_BASE 的端口，demo 实例另找一个空闲端口。
  const demo = await startManagedServer({ port: await findFreePort(), env: { DSH_EE_DEMO_MODE: "1" } });
  try {
    // ---- 英文界面：短片出现、影子 DOM 里有画面、enforce CSP 下无违规、自动播放往前走 ----
    const page = await newPage(t, { lang: "en" });
    watchCSP(t, page);
    const resp = await page.goto(`${demo.url}/`);
    assert(resp && resp.ok(), `演示站首页打开失败：${resp?.status()}`);
    const policy = resp.headers()["content-security-policy"] || "";
    assert(policy.startsWith("default-src 'self';"), "演示站首页应在默认 enforce CSP 下：" + policy);
    let film = await waitFilm(page, "en");
    assert(film.ready, "[data-intro-film] 的影子 DOM 里应有 #root");
    assert(film.scenes > 1, "短片应包含多个场景（.scene）：" + film.scenes);
    assert(film.styles >= 1, "影子 DOM 里应有短片自带的样式（页面样式进不去）");
    assert(film.text.length > 0, "第一幕应有文字：" + JSON.stringify(film.text));
    assertEq(film.lang, "en", "英文界面的短片语言");
    assertEq(await page.evaluate(() => document.documentElement.lang), "en", "页面语言");
    // 自动播放：可见 + 没暂停 + 进度条真的在走。字体与画面都是本地的，无网 / 无系统字体也能画。
    await page.locator("[data-intro-film]").scrollIntoViewIfNeeded();
    await page.waitForFunction(
      () => (Number.parseFloat(document.querySelector("[data-intro-film]")?.children[1]?.style.width ?? "0") || 0) > 0.2,
      null,
      { timeout: 15000 },
    );
    film = await filmState(page);
    assertEq(film.paused, "false", "短片应自动播放（data-paused）");
    assert(film.progress > 0.2, "播放进度应前进：" + film.progress);
    step("英文界面：短片在影子 DOM 里画出来，默认 enforce CSP 下无违规，自动播放推进");

    // ---- 切语言：英文 → 中文，短片画面跟着换 ----
    const before = film.text;
    await page.locator("select:visible").first().selectOption("zh-CN");
    await waitFilm(page, "zh");
    await page.waitForFunction(
      (prev) => {
        const box = document.querySelector("[data-intro-film]");
        const host = box && [...box.querySelectorAll("div")].find((el) => el.shadowRoot);
        return (host?.shadowRoot?.querySelector(".scene")?.textContent ?? "").trim() !== prev;
      },
      before,
      { timeout: 20000 },
    );
    film = await filmState(page);
    assertEq(await page.evaluate(() => document.documentElement.lang), "zh-CN", "切换后的页面语言");
    assert(/[\u4e00-\u9fff]/.test(film.text), "中文界面第一幕应是中文画面：" + JSON.stringify(film.text));
    step("切到中文：短片画面换成中文，html lang 跟着变");

    // ---- 浏览器是日语、界面回落英文时出日文版 ----
    const jp = await newPage(t, { lang: "en", locale: "ja-JP" });
    watchCSP(t, jp);
    await jp.goto(`${demo.url}/`);
    const ja = await waitFilm(jp, "ja");
    assert(/[\u3040-\u30ff]/.test(ja.text), "界面英文 + 浏览器日语时应出日文画面：" + JSON.stringify(ja.text));
    step("界面英文 + 浏览器日语：短片出日文画面");
  } finally {
    await demo.stop();
  }
}
