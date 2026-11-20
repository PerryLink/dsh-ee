import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { setTimeout as sleep } from "node:timers/promises";
import { FeishuChatRelay } from "../src/feishu-chat.js";
import { normalizeConfig } from "../src/config.js";
import { setLang } from "../src/i18n/index.js";

async function until(check: () => boolean) {
  for (let i = 0; i < 500; i++) { if (check()) return; await sleep(2); }
  assert.fail("condition did not become true");
}

function pending(signal: AbortSignal): Promise<never> {
  return new Promise((_, reject) => {
    if (signal.aborted) reject(signal.reason);
    else signal.addEventListener("abort", () => reject(signal.reason), { once: true });
  });
}

function fixture(t: any) {
  setLang("en");
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "feishu-chat-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const hooks: any[] = [];
  const listeners = new Set<() => void>();
  const rows: Record<string, any> = {};
  const messages: any[] = [];
  const sent: { id: string; text: string }[] = [];
  const replies: { url: string; text: string }[] = [];
  const calls: string[] = [];
  let token: string | undefined = "token-one";
  let created = 0;
  let onSend: ((id: string, text: string) => Promise<void>) | undefined;
  const emit = (id: string, running: boolean) => {
    rows[id] = { id, running, completed: !running };
    for (const listener of listeners) listener();
  };
  const ctx: any = {
    on: (_name: string, hook: any) => hooks.push(hook),
    get: () => undefined,
    workspaces: { initializeDefault: async () => ({ workspaceId: "workspace" }) },
    sessions: {
      list: { getSnapshot: () => ({ current: "", phase: "ready", byId: rows }), subscribe: (fn: () => void) => { listeners.add(fn); return () => listeners.delete(fn); } },
      create: async () => { const id = `session-${++created}`; rows[id] = { id, running: false }; return id; },
      using: async (_id: string, _options: any, fn: any) => fn({ ready: Promise.resolve({ session: {} }) }),
    },
    conversation: { sendSession: async (_session: any, text: string) => {
      const id = Object.keys(rows).at(-1)!;
      sent.push({ id, text });
      if (onSend) return onSend(id, text);
      emit(id, true);
      setTimeout(() => emit(id, false), 5);
    } },
  };
  const http = { fetch: async (url: string, init?: RequestInit) => {
    calls.push(url);
    if (init?.method === "POST") {
      replies.push({ url, text: JSON.parse(String(init.body)).text });
      return new Response('{"ok":true}');
    }
    if (messages.length) {
      const item = messages.shift();
      if (item instanceof Error) throw item;
      return typeof item === "number" ? new Response(null, { status: item }) : Response.json(item);
    }
    return pending(init?.signal as AbortSignal);
  } };
  const config = normalizeConfig({ base: "https://ee.example", serverName: "company" });
  const tokens = { resolve: async () => token ? { value: token, source: "test" } : undefined };
  const makeRelay = (delay?: (ms: number, signal: AbortSignal) => Promise<void>) => {
    const relay = new FeishuChatRelay(ctx, config, tokens, http, {
      file: path.join(dir, "feishu-chat.json"), intervalMs: 5,
      delay: delay ?? ((_ms, signal) => sleep(2, undefined, { signal })),
    });
    t.after(() => relay.stop());
    return relay;
  };
  return { ctx, hooks, messages, sent, replies, calls, rows, emit, makeRelay, dir,
    setToken: (value?: string) => { token = value; }, setOnSend: (fn: typeof onSend) => { onSend = fn; }, created: () => created };
}

test("delivers sequentially into one session and posts localized fallback after completion", async (t) => {
  const f = fixture(t);
  f.messages.push({ id: 1, text: "first request" }, { id: 2, text: "second request" });
  const relay = f.makeRelay(); relay.start(); relay.start();
  await until(() => f.replies.length === 2);
  assert.equal(f.created(), 1);
  assert.deepEqual(f.sent.map((s) => s.id), ["session-1", "session-1"]);
  assert.match(f.sent[0].text, /first request/);
  assert.match(f.sent[0].text, /ee_feishu.*action=reply, id=1/s);
  assert.match(f.replies[0].text, /Handled in DSH/);
  assert.equal(f.replies[0].url, "/api/me/feishu-chat/1/reply");
  const saved = JSON.parse(fs.readFileSync(path.join(f.dir, "feishu-chat.json"), "utf8"));
  assert.equal(saved.sessionId, "session-1");
  await relay.stop();
  f.messages.push({ id: 3, text: "after restart" });
  const restarted = f.makeRelay(); restarted.start();
  await until(() => f.replies.length === 3);
  assert.equal(f.created(), 1);
  await restarted.stop();
  delete f.rows["session-1"];
  f.messages.push({ id: 4, text: "deleted session" });
  f.makeRelay().start();
  await until(() => f.replies.length === 4);
  assert.equal(f.created(), 2);
});

test("only a reply from the matching bridge tool, session and message suppresses fallback", async (t) => {
  const f = fixture(t);
  let nextCalls = 0;
  f.setOnSend(async (id, text) => {
    f.emit(id, true);
    const messageId = text.includes("id=11") ? 11 : 12;
    for (const hook of f.hooks) await hook({ name: "mcp__company__ee_feishu", agent: { session: { id } }, arguments: { action: "reply", id: messageId === 11 ? 11 : 999 } }, async () => { nextCalls++; });
    f.emit(id, false);
  });
  f.messages.push({ id: 11, text: "reply with tool" }, { id: 12, text: "wrong id" });
  f.makeRelay().start();
  await until(() => f.replies.length === 1);
  assert.equal(nextCalls, 2);
  assert.equal(f.replies[0].url, "/api/me/feishu-chat/12/reply");
});

test("404 waits a minute and dispose cancels backoff without another request", async (t) => {
  const f = fixture(t);
  f.messages.push(404);
  const delays: number[] = [];
  const relay = f.makeRelay((ms, signal) => { delays.push(ms); return pending(signal); });
  relay.start();
  await until(() => delays.length === 1);
  assert.deepEqual(delays, [60_000]);
  await relay.stop();
  assert.equal(f.calls.length, 1);
});

test("network errors and 401 back off exponentially", async (t) => {
  const f = fixture(t);
  f.messages.push(new Error("offline"), 401, new Error("offline"));
  const delays: number[] = [];
  const relay = f.makeRelay(async (ms, signal) => { delays.push(ms); if (delays.length === 3) return pending(signal); });
  relay.start(); await until(() => delays.length === 3);
  assert.deepEqual(delays, [1000, 2000, 4000]);
});

test("send errors get a localized failure reply", async (t) => {
  const f = fixture(t);
  f.setOnSend(async () => { throw new Error("send rejected"); });
  f.messages.push({ id: 20, text: "work" });
  f.makeRelay().start(); await until(() => f.replies.length === 1);
  assert.match(f.replies[0].text, /Could not handle/);
});

test("dispose aborts long polling and logout cancels an active turn without fallback", async (t) => {
  const f = fixture(t);
  const relay = f.makeRelay(); relay.start();
  await until(() => f.calls.length === 1);
  await relay.stop();
  assert.equal(f.calls.length, 1);
  f.setOnSend(async (id) => { f.emit(id, true); });
  f.messages.push({ id: 30, text: "work" });
  const active = f.makeRelay(); active.start(); await until(() => f.sent.length === 1);
  f.setToken(undefined);
  await sleep(20);
  f.emit("session-1", false);
  await active.stop();
  assert.equal(f.replies.length, 0);
  assert.equal(f.calls.length, 2);
});

test("204 polls again and does not create a session", async (t) => {
  const f = fixture(t);
  f.messages.push(204);
  f.makeRelay().start(); await until(() => f.calls.length === 2);
  assert.equal(f.created(), 0);
});

test("dispose does not wait for a host send that never settles", async (t) => {
  const f = fixture(t);
  f.setOnSend(() => new Promise<void>(() => {}));
  f.messages.push({ id: 40, text: "blocked host" });
  const relay = f.makeRelay(); relay.start();
  await until(() => f.sent.length === 1);
  await relay.stop();
  assert.equal(f.replies.length, 0);
});
