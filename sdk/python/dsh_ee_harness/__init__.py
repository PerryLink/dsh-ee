"""dsh-ee Harness Python 客户端（最小实现，仅标准库）。"""

from .client import (
    MCP_DEFAULT_PROTOCOL_VERSION,
    DeviceFlowError,
    DshClient,
    DshClientError,
    DshJsonRpcError,
    DshNetworkError,
    McpSession,
    RateLimit,
    Response,
    Transport,
    extract_confirmation,
    is_pending_confirmation,
    parse_rate_limit_headers,
    parse_retry_after,
    urllib_transport,
)

__all__ = [
    "DshClient",
    "DshClientError",
    "DshNetworkError",
    "DshJsonRpcError",
    "DeviceFlowError",
    "McpSession",
    "RateLimit",
    "Response",
    "Transport",
    "urllib_transport",
    "extract_confirmation",
    "is_pending_confirmation",
    "parse_rate_limit_headers",
    "parse_retry_after",
    "MCP_DEFAULT_PROTOCOL_VERSION",
]

__version__ = "0.1.0"
