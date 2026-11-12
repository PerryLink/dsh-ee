import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { uploadPackage } from "../src/upload.js";

function tmpFile(name: string, content = "tgz-bytes") {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "ee-upload-"));
  const file = path.join(dir, name);
  fs.writeFileSync(file, content);
  return file;
}

test("上传 .tgz：原样发给 dsh-ee，文件内容不进返回值", async () => {
  const file = tmpFile("pkg-1.0.0.tgz", "PACKAGE-BYTES");
  let seen: any;
  const http = { async fetch(p: string, init: RequestInit, timeout: number) { seen = { p, init, timeout }; return new Response(JSON.stringify({ name: "@acme/x", version: "1.0.0", status: "pending" }), { status: 201 }); } };
  const result = await uploadPackage(http as any, { path: file });
  assert.equal(seen.p, "/api/agent/packages");
  assert.equal(seen.init.method, "POST");
  assert.equal(new Headers(seen.init.headers).get("content-type"), "application/octet-stream");
  assert.equal(Buffer.from(seen.init.body).toString(), "PACKAGE-BYTES");
  assert.equal(seen.timeout, 0, "大包上传不应使用 30s 默认超时");
  assert.equal(result.isError, false);
  assert.ok(result.content[0].text.includes("@acme/x"));
  assert.ok(!result.content[0].text.includes("PACKAGE-BYTES"));
});

test("上传失败：回传服务端的原话；本地问题不发请求", async () => {
  let calls = 0;
  const http = { async fetch() { calls++; return new Response(JSON.stringify({ error: "tarball 太大" }), { status: 413 }); } };
  const file = tmpFile("big.tgz");
  const rejected = await uploadPackage(http as any, { path: file });
  assert.equal(rejected.isError, true);
  assert.match(rejected.content[0].text, /tarball 太大/);
  assert.equal(calls, 1);

  for (const bad of [{}, { path: "/no/such/file.tgz" }, { path: tmpFile("notes.txt") }, { path: path.dirname(file) + "/" }]) {
    const r = await uploadPackage(http as any, bad as any);
    assert.equal(r.isError, true);
  }
  assert.equal(calls, 1, "本地校验失败不该发请求");

  const offline = { async fetch() { throw new TypeError("fetch failed"); } };
  const r = await uploadPackage(offline as any, { path: file });
  assert.match(r.content[0].text, /连不上 dsh-ee/);
});
