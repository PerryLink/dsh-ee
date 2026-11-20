import assert from "node:assert/strict";
import { test } from "node:test";
import { markVote, type VoteFlow, type VoteRequest } from "../src/vote-mark.js";

function flowFor(note: string | null, vote: (request: VoteRequest) => Promise<unknown> = async () => ({ ok: true })) {
  const calls: VoteRequest[] = [];
  const pendingStates: boolean[] = [];
  let voted = 0;
  const flow: VoteFlow = {
    pending: { current: false },
    voted: () => voted,
    setPending: (pending) => pendingStates.push(pending),
    setVoted: (value) => { voted = value; },
    askNote: () => note,
    sessionId: () => "s1",
    messageId: "m1",
    appBindings: async () => ({ crm: { sessionId: "s1" } }),
    vote: (request) => { calls.push(request); return vote(request); },
    unwrapRemote: (value) => value,
  };
  return { flow, calls, pendingStates, voted: () => voted };
}

test("👎 点了取消：不调 remote.vote，也不留在「正在投」", async () => {
  const { flow, calls, pendingStates, voted } = flowFor(null);
  await markVote(flow, -1);
  assert.equal(calls.length, 0);
  assert.equal(voted(), 0);
  assert.equal(flow.pending.current, false);
  assert.deepEqual(pendingStates, [true, false]);
});

test("请求没回来时再点是空操作：双击 👎 / 👍 只记一票", async () => {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  // DSH Desktop 上 askNote 立刻返回 ""（prompt 抛错），两下点击之间没有任何停顿
  const { flow, calls, voted } = flowFor("", async () => { await gate; return { ok: true }; });
  const first = markVote(flow, -1);
  assert.equal(flow.pending.current, true);
  await markVote(flow, -1);
  await markVote(flow, 1);
  release();
  await first;
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0], { app: "crm", session: "s1", message: "m1", vote: -1, note: "" });
  assert.equal(voted(), -1);
  assert.equal(flow.pending.current, false);
  // 投完了再点也不动
  await markVote(flow, 1);
  assert.equal(calls.length, 1);
});

test("投票失败：清掉「正在投」，按钮可以重试", async () => {
  let fail = true;
  const { flow, calls, voted } = flowFor("", async () => { if (fail) throw new Error("401"); return { ok: true }; });
  await markVote(flow, 1);
  assert.equal(voted(), 0);
  assert.equal(flow.pending.current, false);
  fail = false;
  await markVote(flow, 1);
  assert.equal(calls.length, 2);
  assert.equal(voted(), 1);
});
