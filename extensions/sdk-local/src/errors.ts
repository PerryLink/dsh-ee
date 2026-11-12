/**
 * 错误码（docs/plugins.md §6.6），和 Go SDK 是同一套。处理函数里 throw new ExtError(code, message)，
 * SDK 把它变成带 code 的工具结果，Agent 和 dsh-ee 据此告诉员工怎么办。
 */
export const Codes = {
  /** 这位员工还没在源系统登录，或登录过期了：调用 webSignIn 让他重新登录 */
  authorize: "authorize",
  /** 扩展没配好 */
  setup: "setup",
  /** 源系统说这位员工没权限：原样告诉员工，不重试 */
  forbidden: "forbidden",
  /** 源系统暂时不可用 */
  unavailable: "unavailable",
  not_found: "not_found",
  invalid: "invalid",
  /** 员工在网页上拒绝了这个写操作，或还没确认 */
  confirm: "confirm",
  internal: "internal",
} as const;

export type Code = (typeof Codes)[keyof typeof Codes];

export class ExtError extends Error {
  constructor(
    readonly code: Code | string,
    message: string,
  ) {
    super(message);
    this.name = "ExtError";
  }
}

export function codeOf(err: unknown): string {
  return err instanceof ExtError ? err.code : Codes.internal;
}
