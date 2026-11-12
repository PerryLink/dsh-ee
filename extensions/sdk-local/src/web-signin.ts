/**
 * webSignIn（docs/plugins.md §8，照 mygo-music 的 host.webSignIn）：在一个单独的浏览器窗口里打开系统自己的登录页，
 * 员工照常登录（扫码、短信验证都行），等指定的 cookie 都出现后关掉窗口，把 cookie 存在本机。
 * 大部分「没有接口的老系统」靠这个加上发 HTTP 请求就够了。
 *
 * 浏览器用 Playwright（可选依赖：npm i playwright && npx playwright install chromium，或 channel: "chrome" 用本机的 Chrome）。
 * cookie 只存在员工自己的电脑上（~/.dsh-ee/local/<扩展>/cookies.json，权限 600），不经过 dsh-ee。
 */
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { Codes, ExtError } from "./errors.js";

export interface StoredCookie {
  name: string;
  value: string;
  domain: string;
  path: string;
  /** Unix 秒；-1 表示会话 cookie */
  expires: number;
  secure?: boolean;
  httpOnly?: boolean;
}

/** 一个扩展在本机存的 cookie。 */
export class CookieStore {
  readonly file: string;

  constructor(extensionId: string, dir = process.env.DSH_EE_LOCAL_HOME ?? path.join(os.homedir(), ".dsh-ee", "local")) {
    const safe = extensionId.replace(/[^a-z0-9._-]/gi, "_");
    this.file = path.join(dir, safe, "cookies.json");
  }

  load(): StoredCookie[] {
    try {
      const v = JSON.parse(fs.readFileSync(this.file, "utf8"));
      return Array.isArray(v) ? v : [];
    } catch {
      return [];
    }
  }

  save(cookies: StoredCookie[]) {
    fs.mkdirSync(path.dirname(this.file), { recursive: true, mode: 0o700 });
    const tmp = `${this.file}.${process.pid}.tmp`;
    fs.writeFileSync(tmp, JSON.stringify(cookies, null, 2), { mode: 0o600 });
    fs.renameSync(tmp, this.file);
  }

  clear() {
    fs.rmSync(this.file, { force: true });
  }

  /** 这些名字的 cookie 都在、都没过期吗？ */
  has(names: string[], now = Date.now() / 1000): boolean {
    const live = this.load().filter((c) => c.expires < 0 || c.expires > now);
    return names.every((n) => live.some((c) => c.name === n));
  }
}

/** 按 RFC 6265 的域名和路径规则，拼出访问 url 时要带的 Cookie 请求头。 */
export function cookieHeader(cookies: StoredCookie[], url: string, now = Date.now() / 1000): string {
  const u = new URL(url);
  const host = u.hostname.toLowerCase();
  return cookies
    .filter((c) => {
      if (c.expires >= 0 && c.expires <= now) return false;
      if (c.secure && u.protocol !== "https:") return false;
      const d = c.domain.toLowerCase().replace(/^\./, "");
      if (host !== d && !host.endsWith("." + d)) return false;
      const p = c.path || "/";
      return u.pathname === p || u.pathname.startsWith(p.endsWith("/") ? p : p + "/");
    })
    .map((c) => `${c.name}=${c.value}`)
    .join("; ");
}

export interface WebSignInOptions {
  /** 系统自己的登录页 */
  url: string;
  /** 登录成功后会出现的 cookie 名字，都出现了就算登录好了 */
  cookiesOf: string[];
  /** 更细的判断：比如登录后会跳到的地址 */
  until?: (state: { url: string; cookies: StoredCookie[] }) => boolean;
  /** 存到哪个扩展名下 */
  store: CookieStore;
  /** 等员工登录最多多久，默认 5 分钟 */
  timeoutMs?: number;
  /** 用本机装的 Chrome / Edge，不用 Playwright 自带的 Chromium */
  channel?: "chrome" | "msedge";
  /** 测试用：替换 Playwright 的 chromium */
  launcher?: { launch: (opts: object) => Promise<any> };
}

/** 打开登录窗口，等员工登录完，存下 cookie 并返回。员工关掉窗口或超时时抛 authorize 错误。 */
export async function webSignIn(opts: WebSignInOptions): Promise<StoredCookie[]> {
  let launcher = opts.launcher;
  if (!launcher) {
    try {
      // 可选依赖：只有用 webSignIn 的扩展才需要装
      const pw = await import(/* @vite-ignore */ "playwright" as string);
      launcher = pw.chromium;
    } catch {
      throw new ExtError(Codes.setup, "登录窗口要用 Playwright：在扩展目录里运行 npm i playwright，再 npx playwright install chromium（或设置 channel: \"chrome\" 用本机的 Chrome）");
    }
  }
  const browser = await launcher!.launch({ headless: false, ...(opts.channel ? { channel: opts.channel } : {}) });
  try {
    const context = await browser.newContext();
    const page = await context.newPage();
    await page.goto(opts.url);
    const deadline = Date.now() + (opts.timeoutMs ?? 5 * 60_000);
    while (Date.now() < deadline) {
      if (page.isClosed?.()) throw new ExtError(Codes.authorize, "登录窗口被关掉了，没有完成登录");
      const cookies: StoredCookie[] = (await context.cookies()).map((c: any) => ({
        name: c.name,
        value: c.value,
        domain: c.domain,
        path: c.path,
        expires: c.expires,
        secure: c.secure,
        httpOnly: c.httpOnly,
      }));
      const named = opts.cookiesOf.every((n) => cookies.some((c) => c.name === n));
      if (named && (!opts.until || opts.until({ url: page.url(), cookies }))) {
        opts.store.save(cookies);
        return cookies;
      }
      await new Promise((r) => setTimeout(r, 500));
    }
    throw new ExtError(Codes.authorize, "等登录超时了：请重新调用，在弹出的窗口里登录");
  } finally {
    await browser.close().catch(() => undefined);
  }
}
