import fs from "node:fs";
import path from "node:path";
import { dshHome } from "./config.js";
import type { DshContext } from "./dsh.js";

interface Mirror { url: string; username?: string; secret?: string }
export interface Mirrors { pypi?: Mirror; maven?: Mirror }
type Kind = keyof Mirrors;
const xml = (value: string) => value.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&apos;" })[c]!);

export class DependencyMirrors {
  private dir: string;
  private active = new Map<Kind, { host: string; previous: Record<string, string | undefined> }>();
  private disposed = false;

  constructor(ctx: DshContext) {
    const profile = ctx.get("profileContext")?.dir;
    this.dir = path.join(typeof profile === "string" && profile ? profile : dshHome(), "dsh-ee");
  }

  hosts(): { kind: Kind; host: string }[] {
    return [...this.active].map(([kind, value]) => ({ kind, host: value.host }));
  }

  apply(mirrors?: Mirrors): void {
    if (mirrors === undefined || this.disposed) return;
    for (const kind of ["pypi", "maven"] as const) {
      const mirror = mirrors[kind];
      const file = path.join(this.dir, kind === "pypi" ? "pip.conf" : "settings.xml");
      if (!mirror?.url) {
        fs.rmSync(file, { force: true });
        this.restore(kind);
        continue;
      }
      const url = new URL(mirror.url);
      if (!["http:", "https:"].includes(url.protocol) || url.username || url.password || /[\r\n\0]/.test(mirror.url + (mirror.username ?? "") + (mirror.secret ?? ""))) throw new Error("Invalid dependency mirror");
      let content: string;
      if (kind === "pypi") {
        if (mirror.username) {
          url.username = encodeURIComponent(mirror.username);
          url.password = encodeURIComponent(mirror.secret ?? "");
        }
        content = `[global]\nindex-url = ${url.href}\n`;
        if (url.protocol === "http:") content += `trusted-host = ${url.hostname}\n`;
      } else {
        content = `<?xml version="1.0" encoding="UTF-8"?>\n<settings xmlns="http://maven.apache.org/SETTINGS/1.2.0">\n  <mirrors><mirror><id>dsh-ee-mirror</id><mirrorOf>*</mirrorOf><url>${xml(mirror.url)}</url></mirror></mirrors>\n${mirror.username ? `  <servers><server><id>dsh-ee-mirror</id><username>${xml(mirror.username)}</username><password>${xml(mirror.secret ?? "")}</password></server></servers>\n` : ""}</settings>\n`;
      }
      fs.mkdirSync(this.dir, { recursive: true, mode: 0o700 });
      fs.chmodSync(this.dir, 0o700);
      const existing = fs.lstatSync(file, { throwIfNoEntry: false });
      if (existing?.isSymbolicLink()) throw new Error("Invalid dependency mirror file");
      if (existing) fs.chmodSync(file, 0o600);
      if (!existing || fs.readFileSync(file, "utf8") !== content) fs.writeFileSync(file, content, { mode: 0o600 });
      fs.chmodSync(file, 0o600);
      if (!this.active.has(kind)) {
        const keys = kind === "pypi" ? ["PIP_CONFIG_FILE", "PIP_INDEX_URL", "PIP_EXTRA_INDEX_URL"] : ["MAVEN_ARGS"];
        this.active.set(kind, { host: url.host, previous: Object.fromEntries(keys.map((key) => [key, process.env[key]])) });
      }
      this.active.get(kind)!.host = url.host;
      if (kind === "pypi") {
        process.env.PIP_CONFIG_FILE = file;
        delete process.env.PIP_INDEX_URL;
        delete process.env.PIP_EXTRA_INDEX_URL;
      } else {
        const quoted = `"${file.replace(/([\\"])/g, "\\$1")}"`;
        const original = this.active.get(kind)!.previous.MAVEN_ARGS;
        process.env.MAVEN_ARGS = `-s ${quoted}${original ? ` ${original}` : ""}`;
      }
    }
  }

  private restore(kind: Kind): void {
    const state = this.active.get(kind);
    if (!state) return;
    for (const [key, value] of Object.entries(state.previous)) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
    this.active.delete(kind);
  }

  dispose(): void {
    this.disposed = true;
    this.restore("pypi");
    this.restore("maven");
  }
}
