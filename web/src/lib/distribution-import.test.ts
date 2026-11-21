import { test } from "node:test";
import assert from "node:assert/strict";
import { installerDetails, installerMatchesTarget, releaseFeedURL } from "./distribution-import.ts";

test("installer filename detection excludes platform suffixes", () => {
  assert.deepEqual(installerDetails("DeepSeek-Harness-0.2.2-rc.5-arm64-mac.zip"), { version: "0.2.2-rc.5", target: "mac-arm64" });
  assert.deepEqual(installerDetails("dsh-1.4.0-x64.exe"), { version: "1.4.0", target: "win-x64" });
  assert.deepEqual(installerDetails("DSH-1.2.3-aarch64.dmg"), { version: "1.2.3", target: "mac-arm64" });
  assert.deepEqual(installerDetails("DSH-1.2.3-intel.zip"), { version: "1.2.3", target: "mac-x64" });
  assert.deepEqual(installerDetails("DSH-1.2.3-x86_64.dmg"), { version: "1.2.3", target: "mac-x64" });
  assert.deepEqual(installerDetails("DSH-1.2.3.MSI"), { version: "1.2.3", target: "win-x64" });
  assert.deepEqual(installerDetails(""), { version: "" });
  assert.deepEqual(installerDetails("unknown.tar"), { version: "" });
});

test("platform pairing rejects incompatible or unsupported extensions", () => {
  assert.equal(installerMatchesTarget("dsh.exe", "mac-arm64"), false);
  assert.equal(installerMatchesTarget("dsh.zip", "win-x64"), false);
  assert.equal(installerMatchesTarget("dsh.MSI", "win-x64"), true);
  assert.equal(installerMatchesTarget("dsh.dmg", "mac-x64"), true);
  assert.equal(installerMatchesTarget("", "mac-x64"), false);
  assert.equal(installerMatchesTarget("dsh.tar", "mac-arm64"), false);
  assert.equal(releaseFeedURL("win-x64"), "https://download.deepseek.com/dsh-desk/feeds/win-x64/nightly.yml");
});
