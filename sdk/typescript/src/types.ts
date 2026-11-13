// 本 SDK 用到的 dsh-ee L1 契约类型（HTTP + MCP）。
//
// 契约来源：
//   - GET /.well-known/dsh-ee、GET /v1/capabilities、GET /v1/apps（harness v1 中立契约）
//   - GET /v1/models、POST /v1/chat/completions（OpenAI chat completions 子集）
//   - POST /api/device/code、POST /api/device/token（OAuth 设备码风格）
//   - POST /mcp（MCP streamable HTTP；tools/list 支持 cursor 分页，写确认结果带结果级 _meta）
//
// 这里只描述 dsh-ee 实际返回的字段；第三方上游模型参数等透传字段用索引签名兜底。

/** 可注入的 fetch 实现；默认使用全局 fetch（Node 18+ / 浏览器）。 */
export type FetchLike = (url: string, init?: RequestInit) => Promise<Response>;

/** 服务端返回的限流状态（X-RateLimit-*，见 dsh-ee 的 /v1/* 与 /mcp 令牌级限流）。 */
export interface RateLimitInfo {
  /** 窗口内允许的请求数（X-RateLimit-Limit）。 */
  limit: number;
  /** 当前窗口剩余请求数（X-RateLimit-Remaining）。 */
  remaining: number;
  /** 窗口重置时间，Unix 秒（X-RateLimit-Reset）。 */
  reset: number;
  /** 窗口重置时间（由 reset 换算）。 */
  resetAt: Date;
}

// ---- 发现与能力 ----

/** GET /.well-known/dsh-ee 的响应。 */
export interface HarnessDiscovery {
  name: string;
  version: string;
  api_version: string;
  device_authorization_endpoint: string;
  token_endpoint: string;
  scopes_supported: string[];
  models: {
    protocol: string;
    streaming: boolean;
  };
  mcp: {
    transport: string;
    protocol_versions: string[];
  };
  apps: {
    web: boolean;
    data_tools: boolean;
  };
  limits: {
    max_request_bytes: number;
    max_response_bytes: number;
  };
  deprecation: {
    sunset_header: string;
  };
}

/** 中立微应用描述（GET /v1/capabilities 的 apps[]、GET /v1/apps）。 */
export interface HarnessApp {
  name: string;
  title: string;
  description?: string;
  icon?: string;
  starters: string[];
  launch: string;
  delivery: string;
  version: number;
  web: boolean;
}

/** GET /v1/capabilities 的响应；匿名请求只得到公开子集。 */
export interface HarnessCapabilities {
  api_version: string;
  authenticated: boolean;
  server: {
    name: string;
    version: string;
  };
  user: {
    username: string;
    department?: string;
  } | null;
  scopes: string[];
  models: string[];
  tools: {
    count: number;
  };
  apps: HarnessApp[];
  revision: string;
}

/** GET /v1/apps 的响应。 */
export interface HarnessAppsPage {
  api_version: string;
  revision: string;
  apps: HarnessApp[];
}

/** GET /v1/apps/{name} 的响应。 */
export interface HarnessAppDetail {
  api_version: string;
  app: HarnessApp;
}

// ---- 模型网关 ----

/** GET /v1/models 的一项（对外模型名，不是上游厂商模型名）。 */
export interface ModelInfo {
  id: string;
  object: string;
  owned_by: string;
}

/** OpenAI chat completions 消息；content 也可为多段内容数组，原样透传。 */
export interface ChatMessage {
  role: string;
  content?: string | null | ChatContentPart[];
  name?: string;
  tool_call_id?: string;
  [key: string]: unknown;
}

export interface ChatContentPart {
  type: string;
  text?: string;
  [key: string]: unknown;
}

/**
 * POST /v1/chat/completions 的请求体。
 *
 * dsh-ee 只实现 OpenAI chat completions 的一个子集，其余字段会原样转发给上游；
 * 因此这里保留索引签名，`temperature`、`max_tokens`、`tools` 等字段可以直接传。
 */
export interface ChatCompletionRequest {
  model: string;
  messages: ChatMessage[];
  stream?: boolean;
  [key: string]: unknown;
}

export interface ChatUsage {
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  [key: string]: unknown;
}

export interface ChatCompletionChoice {
  index: number;
  message: ChatMessage;
  finish_reason?: string | null;
  logprobs?: unknown;
  [key: string]: unknown;
}

/** 非流式 chat completion 响应；dsh-ee 原样转发上游响应。 */
export interface ChatCompletion {
  id: string;
  object: string;
  created: number;
  model: string;
  choices: ChatCompletionChoice[];
  usage?: ChatUsage;
  [key: string]: unknown;
}

export interface ChatCompletionChunkChoice {
  index: number;
  delta: {
    role?: string;
    content?: string | null;
    [key: string]: unknown;
  };
  finish_reason?: string | null;
  [key: string]: unknown;
}

/** 流式 chat completion 的一个 SSE data 分块；最后一块可能带 usage。 */
export interface ChatCompletionChunk {
  id: string;
  object: string;
  created: number;
  model: string;
  choices: ChatCompletionChunkChoice[];
  usage?: ChatUsage | null;
  [key: string]: unknown;
}

// ---- 设备码 ----

/** 设备码流程可申请的 scope（与 dsh-ee 设备码端点一致）。 */
export type DshDeviceScope =
  | 'models'
  | 'registry:read'
  | 'registry:publish'
  | 'hub'
  | 'connections'
  | 'agent';

/** POST /api/device/code 的请求体。 */
export interface DeviceCodeRequest {
  /** 显示在网页确认页上的客户端名，例如 "my-harness"。 */
  clientName?: string;
  clientOs?: string;
  clientVersion?: string;
  /** 不传时 dsh-ee 使用 models, registry:read, hub, connections, agent。 */
  scopes?: DshDeviceScope[];
  /** 「开始使用」页的一次性接入码，可省略。 */
  setupCode?: string;
}

/** POST /api/device/code 的响应。 */
export interface DeviceCode {
  device_code: string;
  user_code: string;
  verification_uri: string;
  verification_uri_complete: string;
  /** 建议轮询间隔（秒）。 */
  interval: number;
  /** 设备码有效期（秒）。 */
  expires_in: number;
}

/** 设备码轮询可能返回的错误码。 */
export type DeviceFlowErrorCode =
  | 'authorization_pending'
  | 'slow_down'
  | 'access_denied'
  | 'expired_token'
  | 'rate_limited';

/** POST /api/device/token 成功时的响应。 */
export interface DeviceToken {
  access_token: string;
  scopes: string[];
  user: {
    username: string;
    department?: string;
  };
  /** 令牌过期时间（RFC3339 字符串）。 */
  expires_at: string;
}

/** waitForDeviceToken 的进度回调。 */
export interface DeviceTokenWaitOptions {
  signal?: AbortSignal;
  /** 覆盖轮询间隔（毫秒）；默认取设备码响应里的 interval。 */
  intervalMs?: number;
  /** 本地最多等待多久（毫秒）；默认取设备码响应的 expires_in。 */
  timeoutMs?: number;
  /** 每次轮询前回调，便于打印「还在等待」或展示授权码。 */
  onPending?: (info: { code: DeviceFlowErrorCode; attempt: number }) => void;
}

// ---- MCP ----

export type JsonRpcId = number | string;

export interface JsonRpcRequest {
  jsonrpc: '2.0';
  id?: JsonRpcId;
  method: string;
  params?: unknown;
}

export interface JsonRpcErrorObject {
  code: number;
  message: string;
  data?: unknown;
}

export interface McpServerInfo {
  name: string;
  version: string;
}

/** MCP initialize 的 result；dsh-ee 会回 protocolVersion / capabilities / serverInfo / instructions。 */
export interface McpInitializeResult {
  protocolVersion: string;
  capabilities: Record<string, unknown>;
  serverInfo: McpServerInfo;
  instructions?: string;
  [key: string]: unknown;
}

/** tools/list 的一项。 */
export interface McpTool {
  name: string;
  title?: string;
  description?: string;
  inputSchema: unknown;
  annotations?: {
    readOnlyHint?: boolean;
    [key: string]: unknown;
  };
  _meta?: Record<string, unknown>;
  [key: string]: unknown;
}

export interface McpToolsPage {
  tools: McpTool[];
  /** 不透明游标，原样传给下一次 tools/list；没有下一页时不存在。 */
  nextCursor?: string;
}

/** tools/call 结果里的内容块（目前 dsh-ee 只返回 text）。 */
export interface McpToolContent {
  type: string;
  text?: string;
  [key: string]: unknown;
}

/** tools/call 的结果（MCP CallToolResult）。 */
export interface McpToolCallResult {
  content: McpToolContent[];
  isError?: boolean;
  _meta?: Record<string, unknown>;
  [key: string]: unknown;
}

/**
 * dsh-ee 写操作需要人工确认时，结果级 `_meta["dsh-ee/confirmation"]` 的结构。
 * 拿到后展示 url 让用户确认，再用完全相同的参数重试工具调用。
 */
export interface DshConfirmation {
  id: number | string;
  url: string;
  /** 确认失效时间（RFC3339 UTC）。 */
  expires_at: string;
  /** 解析后的失效时间。 */
  expiresAt?: Date;
}

/** listTools 的选项。limit=0 表示不分页，一次取全量。 */
export interface ListToolsOptions {
  /** 第一页可显式指定页大小；服务端默认 100，上限 500。 */
  limit?: number;
  /** 从上一页拿到的 nextCursor 继续翻页。 */
  cursor?: string;
  signal?: AbortSignal;
}

/** listAllTools 的选项。 */
export interface ListAllToolsOptions {
  /** 每次请求的页大小；不传用服务端默认值。 */
  pageSize?: number;
  signal?: AbortSignal;
  /** 每拿到一页回调一次，便于边翻边用。 */
  onPage?: (page: McpToolsPage) => void;
}

/** callTool 的选项。 */
export interface CallToolOptions {
  signal?: AbortSignal;
}

/** MCP 客户端信息，initialize 时上报。 */
export interface McpClientInfo {
  name: string;
  version: string;
}
