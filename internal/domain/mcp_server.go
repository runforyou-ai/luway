package domain

// MCPServerType 定义 MCP 服务的传输类型。
type MCPServerType string

const (
	MCPServerTypeSSE            MCPServerType = "sse"
	MCPServerTypeStreamableHTTP MCPServerType = "streamable-http"
)
