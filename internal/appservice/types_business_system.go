package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// MCPServerType 定义 MCP 服务的连接方式。
type MCPServerType = domain.MCPServerType

// BusinessSystemTransport 定义业务系统的传输方式。
type BusinessSystemTransport = domain.BusinessSystemTransport

// BusinessSystemCredentialKind 定义业务系统的认证方式。
type BusinessSystemCredentialKind = domain.BusinessSystemCredentialKind

// BusinessSystemMCPConnection 定义 MCP 业务系统的服务地址与连接方式。
type BusinessSystemMCPConnection struct {
	URL        string        `json:"url"`
	ServerType MCPServerType `json:"serverType"`
}

// BusinessSystemHTTPConnection 定义 HTTP 业务系统的接口根地址与 OpenAPI 文档来源：SpecURL 非空时从该地址读取文档，否则使用 Spec 正文；业务系统列表中 Spec 为空。
type BusinessSystemHTTPConnection struct {
	BaseURL string `json:"baseUrl"`
	SpecURL string `json:"specUrl"`
	Spec    string `json:"spec"`
}

// BusinessSystemCredential 定义业务系统凭据：Token 用于令牌与自定义请求头方式，HeaderName 是自定义请求头方式的请求头名称。
type BusinessSystemCredential struct {
	Kind       BusinessSystemCredentialKind `json:"kind"`
	HeaderName string                       `json:"headerName"`
	Token      string                       `json:"token"`
	Username   string                       `json:"username"`
	Password   string                       `json:"password"`
}

// OperationLevel 定义工具的操作级别。
type OperationLevel = domain.OperationLevel

// ContextValue 定义可绑定到工具参数或连接请求头的可信上下文值。
type ContextValue = domain.ContextValue

// HeaderBinding 定义一个连接请求头绑定的可信上下文值。
type HeaderBinding struct {
	Header string       `json:"header"`
	Value  ContextValue `json:"value"`
}

// ParameterBinding 定义一个工具参数绑定的可信上下文值。
type ParameterBinding struct {
	Parameter string       `json:"parameter"`
	Value     ContextValue `json:"value"`
}

// ToolFacts 定义工具的只读、可撤销与对外发信事实。
type ToolFacts struct {
	ReadOnly   bool `json:"readOnly"`
	Reversible bool `json:"reversible"`
	Outbound   bool `json:"outbound"`
}

// BusinessToolHTTP 定义 HTTP 业务系统工具对应的接口方法与路径。
type BusinessToolHTTP struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// BusinessTool 定义业务系统工具目录中的一个工具：默认事实、管理员设置与据此推导的生效事实和操作级别；HTTP 为 HTTP 业务系统工具对应的接口。
type BusinessTool struct {
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	HTTP         *BusinessToolHTTP `json:"http"`
	Parameters   []string          `json:"parameters"`
	DefaultFacts ToolFacts         `json:"defaultFacts"`
	// ReadOnly、Reversible 与 Outbound 是管理员对事实的修正，为空时使用默认事实。
	ReadOnly          *bool              `json:"readOnly"`
	Reversible        *bool              `json:"reversible"`
	Outbound          *bool              `json:"outbound"`
	Disabled          bool               `json:"disabled"`
	ParameterBindings []ParameterBinding `json:"parameterBindings"`
	Facts             ToolFacts          `json:"facts"`
	Level             OperationLevel     `json:"level"`
}

// BusinessSystem 定义工作区业务系统，MCP 与 HTTP 只有传输方式对应的一项非空。
type BusinessSystem struct {
	ID             string                        `json:"id"`
	Name           string                        `json:"name"`
	Transport      BusinessSystemTransport       `json:"transport"`
	MCP            *BusinessSystemMCPConnection  `json:"mcp"`
	HTTP           *BusinessSystemHTTPConnection `json:"http"`
	Credential     BusinessSystemCredential      `json:"credential"`
	HeaderBindings []HeaderBinding               `json:"headerBindings"`
	Tools          []BusinessTool                `json:"tools"`
	ToolsUpdatedAt *time.Time                    `json:"toolsUpdatedAt"`
	ToolsUpdating  bool                          `json:"toolsUpdating"`
	ToolsError     string                        `json:"toolsError"`
	CreatedAt      time.Time                     `json:"createdAt"`
	UpdatedAt      time.Time                     `json:"updatedAt"`
}

// BusinessSystemInput 定义业务系统可编辑字段，按传输方式填写 MCP 或 HTTP 连接配置。
type BusinessSystemInput struct {
	Name           string                        `json:"name" validate:"notblank,max=100" msg:"notblank=field.business_system_name_required,max=field.business_system_name_too_long"`
	Transport      BusinessSystemTransport       `json:"transport"`
	MCP            *BusinessSystemMCPConnection  `json:"mcp"`
	HTTP           *BusinessSystemHTTPConnection `json:"http"`
	Credential     BusinessSystemCredential      `json:"credential"`
	HeaderBindings []HeaderBinding               `json:"headerBindings"`
}

// BusinessToolSettingInput 定义业务系统中一个工具的管理员设置，事实为空时使用默认事实。
type BusinessToolSettingInput struct {
	ToolName          string             `json:"toolName"`
	ReadOnly          *bool              `json:"readOnly"`
	Reversible        *bool              `json:"reversible"`
	Outbound          *bool              `json:"outbound"`
	Disabled          bool               `json:"disabled"`
	ParameterBindings []ParameterBinding `json:"parameterBindings"`
}

// BusinessSystemList 定义工作区业务系统列表。
type BusinessSystemList struct {
	BusinessSystems []BusinessSystem `json:"businessSystems"`
}

// BusinessSystemConnectionInput 定义业务系统连接测试字段，按传输方式填写 MCP 或 HTTP 连接配置。
type BusinessSystemConnectionInput struct {
	Transport  BusinessSystemTransport       `json:"transport"`
	MCP        *BusinessSystemMCPConnection  `json:"mcp"`
	HTTP       *BusinessSystemHTTPConnection `json:"http"`
	Credential BusinessSystemCredential      `json:"credential"`
}
