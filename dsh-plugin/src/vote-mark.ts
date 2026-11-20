// 👍👎 的投票流程，单独成文件：不依赖 react，好测。

export type VoteRequest = { app: string; session: string; message: string; vote: 1 | -1; note: string };

export interface VoteFlow {
  /** 同步的「正在投」标记（React 里是 useRef）：第一个 await 之前就置上，双击第二下直接返回 */
  pending: { current: boolean };
  /** 已投的值（0 = 没投） */
  voted: () => number;
  /** 正在投的状态变化，用来禁用按钮 */
  setPending: (pending: boolean) => void;
  setVoted: (value: 1 | -1) => void;
  /** 👎 时问说明；返回 null = 用户取消，不投 */
  askNote: () => string | null;
  sessionId: () => string;
  messageId: string;
  appBindings: () => Promise<unknown>;
  vote: (request: VoteRequest) => Promise<unknown>;
  unwrapRemote: (value: unknown) => any;
}

/**
 * 投一票。服务端是直接插行的（不去重），所以请求还没回来时再点必须是空操作：
 * DSH Desktop 上 window.prompt 会立刻抛错（askNote 返回 ""），没有这个标记时双击 👎 会记两票、两条「没答上」。
 */
export async function markVote(flow: VoteFlow, value: 1 | -1): Promise<void> {
  if (flow.voted() || flow.pending.current) return;
  flow.pending.current = true;
  flow.setPending(true);
  try {
    const sessionId = flow.sessionId();
    let note = "";
    if (value < 0) {
      const asked = flow.askNote();
      if (asked === null) return; // 点了「取消」：不投这一票
      note = asked;
    }
    let app = "";
    try {
      const bindings = flow.unwrapRemote(await flow.appBindings()) as Record<string, { sessionId?: string }> | undefined;
      app = Object.entries(bindings ?? {}).find(([, b]) => b?.sessionId === sessionId)?.[0] ?? "";
    } catch { /* 取不到绑定就只记消息 */ }
    try {
      // 服务端拒绝（401/5xx）时 remote 也会 resolve 成 { ok: false }，必须解包后再标记已投票。
      flow.unwrapRemote(await flow.vote({ app, session: sessionId, message: flow.messageId, vote: value, note }));
      flow.setVoted(value);
    } catch {
      // 保持按钮可点，让用户能重试
    }
  } finally {
    flow.pending.current = false;
    flow.setPending(false);
  }
}
