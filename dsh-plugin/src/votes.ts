import { createElement, useRef, useState } from "react";
import type { DshContext } from "./dsh.js";
import { t } from "./i18n/index.js";
import { currentSessionOf } from "./host-session.js";
import { askNote } from "./vote-note.js";
import { markVote, type VoteRequest } from "./vote-mark.js";

// 反馈（M8）：每条助手消息尾的 👍👎，经 remote 的 dshEE/vote 打到 POST /api/dsh/feedback。

type VoteRemote = { vote: (request: VoteRequest) => Promise<unknown> };
type VoteBindings = { appBindings: () => Promise<unknown> };

/** 在 assistant-actions 槽位（0.2.2 起有；旧宿主没有就直接不出按钮）注册 👍👎。 */
export function registerVotes(ctx: DshContext, remote: VoteRemote & VoteBindings, unwrapRemote: (value: unknown) => any): void {
  if (!ctx?.slots?.inject) return;
  const buttonStyle = { border: 0, background: "transparent", cursor: "pointer", fontSize: 13, padding: "1px 4px", opacity: 0.65 };
  const VoteButtons = (props: any) => {
    const messageId = String(props?.messageId ?? props?.message_id ?? props?.node?.id ?? props?.id ?? "");
    const [voted, setVoted] = useState(0);
    // 正在投：ref 同步挡住双击（state 要等下一次渲染才生效），state 用来禁用按钮
    const [pending, setPending] = useState(false);
    const pendingRef = useRef(false);
    const mark = (value: 1 | -1) => markVote({
      pending: pendingRef,
      voted: () => voted,
      setPending,
      setVoted,
      askNote: () => askNote(typeof window !== "undefined" ? window : undefined, t("votes.notePrompt")),
      sessionId: () => currentSessionOf(ctx.sessions?.list?.getSnapshot?.()),
      messageId,
      appBindings: () => remote.appBindings(),
      vote: (request) => remote.vote(request),
      unwrapRemote,
    }, value);
    const disabled = voted !== 0 || pending;
    return createElement("span", { "data-dsh-ee-ui": "votes", style: { display: "inline-flex", gap: 2 } },
      createElement("button", { type: "button", title: t("votes.helpful"), "aria-label": t("votes.helpful"), disabled, style: { ...buttonStyle, opacity: voted === 1 ? 1 : buttonStyle.opacity }, onClick: () => void mark(1) }, "👍"),
      createElement("button", { type: "button", title: t("votes.notHelpful"), "aria-label": t("votes.notHelpful"), disabled, style: { ...buttonStyle, opacity: voted === -1 ? 1 : buttonStyle.opacity }, onClick: () => void mark(-1) }, "👎"));
  };
  try {
    ctx.slots.inject("conversation.chat.assistant-actions", () =>
      ctx.slots.register({ name: "conversation.chat.assistant-actions", id: "dsh-ee-vote", order: 100, label: t("votes.label") }, VoteButtons));
  } catch {
    // 旧版宿主没有这个槽位：先不上按钮（计划里的降级方案是看板上的文字反馈，见 docs）
  }
}
