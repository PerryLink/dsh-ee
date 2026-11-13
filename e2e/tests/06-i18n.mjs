// 多语言：按浏览器语言自动选择、切换后记住，所有页面都没有漏翻成原始 key
import { ADMIN_PW, BASE, assert, assertEq, login, newPage, step } from "../lib.mjs";

const pages = ["/workbench", "/apps", "/hub", "/hub?tab=skill", "/hub?tab=connection", "/dsh", "/setup", "/publish", "/docs", "/docs/overview", "/docs/harness-quickstart", "/examples", "/examples/dsh-claim-check", "/tokens", "/device", "/account",
  "/admin", "/admin/stats", "/admin/audit", "/admin/users", "/admin/roles", "/admin/menus", "/admin/providers", "/admin/quotas", "/admin/tokens",
  "/admin/plugins", "/admin/connections", "/admin/review", "/admin/registry", "/admin/upstream", "/admin/distribution", "/admin/settings"];
// i18n 模块名.键名 的样子；排除域名、文件名
const keyLike = /\b(common|shell|api|pages|pageDesc|menuGroups|status|kinds|plugin|login|account|keys|tokens|device|workbench|apps|market|pluginDetail|download|setup|publish|audit|landing|users|roles|perms|menusAdmin|providers|quotas|adminKeys|dashboard|stats|pluginsAdmin|review|registryAdmin|distributionAdmin|upstreamAdmin|settingsAdmin|ldapAdmin|auditLog|hub|asset|assetKinds|editor|scopeEditor|connections|connectAdmin|setupConnect|reviewAssets|pluginPolicy|devdocs)\.[a-zA-Z][\w.-]*\b/g;
// 测试数据：用户名、包名等带 lib.mjs 的随机后缀（Date.now().toString(36)，8 位）
const notKey = (h) => /^[a-z]+\.(?=[a-z0-9]*\d)[a-z0-9]{8}$/.test(h) || /\.(com|internal|json|pem|tgz|sh|ps1|zst|npmrc)\b/.test(h) || h.startsWith("keys.self") || h.startsWith("plugin.");

export default async function (t) {
  for (const [locale, want] of [["zh-CN", "zh-CN"], ["zh-TW", "zh-CN"], ["en-US", "en"], ["ja-JP", "en"]]) {
    const p = await newPage(t, { lang: null, locale });
    await p.goto(`${BASE}/login`);
    await p.waitForTimeout(300);
    assertEq(await p.evaluate(() => document.documentElement.lang), want, `浏览器语言 ${locale}`);
  }
  step("浏览器语言 zh-* → 简体中文，其它 → 英文");

  for (const lang of ["zh-CN", "en"]) {
    const page = await newPage(t, { lang });
    await login(page, "admin", ADMIN_PW);
    const raw = [];
    for (const path of pages) {
      await page.goto(BASE + path);
      await page.waitForTimeout(300);
      // data-raw：原样展示的数据（如审计日志里的权限点代码），不是界面文案
      const text = await page.evaluate(
        () =>
          [...document.querySelectorAll("[data-raw]")].reduce((s, e) => s.replace(e.innerText, ""), document.body.innerText) +
          " " +
          [...document.querySelectorAll("[placeholder],[aria-label],[title]")]
            .map((e) => [e.getAttribute("placeholder"), e.getAttribute("aria-label"), e.getAttribute("title")].join(" "))
            .join(" "),
      );
      const hits = [...new Set(text.match(keyLike) ?? [])].filter((h) => !notKey(h));
      if (hits.length) raw.push(`${path}: ${hits.join(", ")}`);
    }
    assert(raw.length === 0, `[${lang}] 页面上出现了未翻译的 key：\n    ${raw.join("\n    ")}`);
    step(`[${lang}] ${pages.length} 个页面没有原始 key`);
  }

  const page = await newPage(t, { lang: null, locale: "en-US" });
  await login(page, "admin", ADMIN_PW);
  await page.goto(`${BASE}/workbench`);
  await page.locator("aside select").selectOption("zh-CN");
  await page.reload();
  await page.waitForTimeout(300);
  assertEq(await page.evaluate(() => document.documentElement.lang), "zh-CN", "切换后刷新");
  assertEq(await page.evaluate(() => localStorage.getItem("dsh-ee:lang")), "zh-CN", "记住的语言");
  step("切换到中文后刷新仍是中文");
}
