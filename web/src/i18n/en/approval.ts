import type { approval as zh } from "../zh/approval";
import type { Messages } from "../types";

export const approval = {
  label: "Writes",
  modes: {
    read_only: "Read-only",
    manual: "Approve each",
    auto: "Auto-approve",
    full: "Full access",
  },
  modeHints: {
    read_only: "The bot can only read enterprise data. Every write is blocked.",
    manual: "Each time the bot wants to change data, an approval card appears in the chat. Nothing runs until you approve it.",
    auto: "Writes that match the auto-approval rules run right away. Other writes still show an approval card.",
    full: "The bot's writes run immediately without approval. Every write is still recorded in the audit log.",
  },
  fullBadge: "Full access",
  fullBadgeTitle: "This bot's writes run without approval",
  fullNotice: "Full access is on: {{bot}} changes enterprise data without asking you first. Every write is recorded in the audit log.",
  changeLabel: "Approval mode",
  changeFailed: "Couldn't change the approval mode: {{reason}}",
  locked: "The approval mode is set by your administrator",
  fullDialog: {
    title: "Turn on full access for \"{{bot}}\"?",
    body: "With full access, this bot creates, updates and deletes records, sends messages and makes other changes to enterprise data without showing an approval card first.",
    risk1: "The model may misread your intent and make changes you didn't plan, with no one checking before they run.",
    risk2: "Malicious content in the chat or in enterprise data could trick the bot into making writes.",
    risk3: "Some changes can't be undone. Every write is still recorded in the audit log.",
    ack: "I understand these risks and want to turn on full access",
    confirm: "Turn on full access",
  },
  card: {
    title: "Approval needed",
    desc: "{{bot}} wants to change data in {{system}}. Nothing runs until you approve it.",
    descNoSystem: "{{bot}} wants to change data in an enterprise system. Nothing runs until you approve it.",
    tool: "Action",
    args: "Data affected (arguments)",
    expires: "Valid until {{time}}. If it isn't approved by then, it won't run.",
    approve: "Approve",
    reject: "Reject",
    deciding: "Submitting…",
    failed: "Couldn't submit: {{reason}}",
  },
  admin: {
    mode: "Write approval mode",
    rules: "Auto-approval rules",
    rulesHint: "One per line, matching the full tool name connection__tool. Use * as a wildcard, e.g. wiki__create_page, wiki__*, *__create_*. Writes that match no rule still show an approval card. A bare * isn't allowed (use full access instead).",
    employeeEditable: "Let the employee switch the approval mode in the chat",
    employeeEditableHint: "The employee can switch modes on the bot chat page (turning on full access still needs the risk confirmation). Only administrators set the auto-approval rules.",
  },
} satisfies Messages<typeof zh>;
