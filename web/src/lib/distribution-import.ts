import type { DshTarget } from "./types";

export function installerDetails(filename: string): { version: string; target?: DshTarget } {
  const ext = filename.match(/\.(zip|dmg|exe|msi)$/i)?.[1].toLowerCase();
  const stem = filename.replace(/\.[^.]+$/, "");
  const version = stem.match(/\d+\.\d+\.\d+(?:-(?:alpha|beta|rc|preview|nightly|dev|canary)(?:[.-]?\d+)*)?(?:\+[A-Za-z0-9.]+)?/i)?.[0] ?? "";
  if (ext === "exe" || ext === "msi") return { version, target: "win-x64" };
  if (ext === "zip" || ext === "dmg") {
    if (/arm64|aarch64/i.test(stem)) return { version, target: "mac-arm64" };
    if (/x64|x86_64|intel/i.test(stem)) return { version, target: "mac-x64" };
  }
  return { version };
}

export function installerMatchesTarget(filename: string, target: DshTarget): boolean {
  return target === "win-x64" ? /\.(exe|msi)$/i.test(filename) : /\.(zip|dmg)$/i.test(filename);
}

export function releaseFeedURL(target: DshTarget): string {
  return `https://download.deepseek.com/dsh-desk/feeds/${target}/${target === "win-x64" ? "nightly.yml" : "nightly-mac.yml"}`;
}
