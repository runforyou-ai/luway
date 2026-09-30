package domain

// MCPServerType 定义 MCP 服务的传输类型。
type MCPServerType string

const (
	MCPServerTypeSSE            MCPServerType = "sse"
	MCPServerTypeStreamableHTTP MCPServerType = "streamable-http"
)

// MCPTool 定义远程工具目录中的名称和描述。
type MCPTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// MCPToolPurpose 定义管理员为 MCP 工具标记的用途。
type MCPToolPurpose string

const (
	// MCPToolPurposeQuery 表示只读查询，客服场景挂载并以其结果作为回答依据。
	MCPToolPurposeQuery MCPToolPurpose = "query"
	// MCPToolPurposeAction 表示会产生副作用的操作，客服场景不挂载。
	MCPToolPurposeAction MCPToolPurpose = "action"
)

// Valid 判断用途是否为已定义的值。
func (p MCPToolPurpose) Valid() bool {
	return p == MCPToolPurposeQuery || p == MCPToolPurposeAction
}
