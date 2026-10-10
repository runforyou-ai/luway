package domain

import (
	"encoding/base64"
	"encoding/json"
	"maps"
	"slices"

	"github.com/runforyou-ai/support"
)

// BusinessSystemTransport 定义业务系统的传输方式。
type BusinessSystemTransport string

const (
	// BusinessSystemTransportMCP 表示经 MCP 服务提供工具。
	BusinessSystemTransportMCP BusinessSystemTransport = "mcp"
	// BusinessSystemTransportHTTP 表示按 OpenAPI 文档调用 HTTP 接口。
	BusinessSystemTransportHTTP BusinessSystemTransport = "http"
)

// BusinessSystemConnection 定义业务系统的连接配置，只填写传输方式对应的一项。
type BusinessSystemConnection struct {
	MCP  *MCPConnection  `json:"mcp,omitempty"`
	HTTP *HTTPConnection `json:"http,omitempty"`
}

// MCPConnection 定义 MCP 业务系统的服务地址与连接方式。
type MCPConnection struct {
	URL        string        `json:"url"`
	ServerType MCPServerType `json:"serverType"`
}

// HTTPConnection 定义 HTTP 业务系统的接口根地址与 OpenAPI 文档来源：SpecURL 非空时从该地址读取文档，否则使用 Spec 中保存的文档正文。
type HTTPConnection struct {
	BaseURL string `json:"baseUrl"`
	SpecURL string `json:"specUrl,omitempty"`
	Spec    string `json:"spec,omitempty"`
}

// BusinessSystemCredentialKind 定义业务系统的认证方式。
type BusinessSystemCredentialKind string

const (
	// BusinessSystemCredentialNone 表示不认证。
	BusinessSystemCredentialNone BusinessSystemCredentialKind = "none"
	// BusinessSystemCredentialBearer 表示以 Authorization: Bearer 携带令牌。
	BusinessSystemCredentialBearer BusinessSystemCredentialKind = "bearer"
	// BusinessSystemCredentialHeader 表示以自定义请求头携带密钥。
	BusinessSystemCredentialHeader BusinessSystemCredentialKind = "header"
	// BusinessSystemCredentialBasic 表示以 HTTP Basic 携带用户名与密码。
	BusinessSystemCredentialBasic BusinessSystemCredentialKind = "basic"
)

// BusinessSystemCredential 定义工作区共享的业务系统凭据：Token 用于 bearer 与 header 方式，HeaderName 是 header 方式的请求头名称。
type BusinessSystemCredential struct {
	Kind       BusinessSystemCredentialKind `json:"kind"`
	HeaderName string                       `json:"headerName,omitempty"`
	Token      string                       `json:"token,omitempty"`
	Username   string                       `json:"username,omitempty"`
	Password   string                       `json:"password,omitempty"`
}

// Header 返回凭据附加到请求上的请求头名称与取值，不认证时返回空名称。
func (c BusinessSystemCredential) Header() (string, string) {
	switch c.Kind {
	case BusinessSystemCredentialBearer:
		return "Authorization", "Bearer " + c.Token
	case BusinessSystemCredentialHeader:
		return c.HeaderName, c.Token
	case BusinessSystemCredentialBasic:
		return "Authorization", "Basic " + base64.StdEncoding.EncodeToString([]byte(c.Username+":"+c.Password))
	}
	return "", ""
}

// BusinessTool 定义业务系统工具目录中的一个工具：名称、描述、输入参数定义、服务方声明的特性提示，以及 HTTP 业务系统中工具对应的接口。
type BusinessTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
	// ReadOnlyHint 是服务方声明的只读提示，未声明时为空。
	ReadOnlyHint *bool `json:"readOnlyHint,omitempty"`
	// DestructiveHint 是服务方声明的写操作不可撤销提示，未声明时为空。
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
	// HTTP 是 HTTP 业务系统中工具对应的接口，MCP 工具为空。
	HTTP *HTTPOperation `json:"http,omitempty"`
}

// HTTPOperation 定义工具对应的 HTTP 接口：方法、相对接口根地址的路径模板、各参数的请求位置与请求体的媒体类型。
type HTTPOperation struct {
	Method      string          `json:"method"`
	Path        string          `json:"path"`
	Parameters  []HTTPParameter `json:"parameters"`
	ContentType string          `json:"contentType,omitempty"`
}

// HTTPParameterIn 定义工具参数在 HTTP 请求中的位置。
type HTTPParameterIn string

const (
	HTTPParameterInPath   HTTPParameterIn = "path"
	HTTPParameterInQuery  HTTPParameterIn = "query"
	HTTPParameterInHeader HTTPParameterIn = "header"
	HTTPParameterInBody   HTTPParameterIn = "body"
)

// HTTPParameter 定义一个工具参数的请求位置：Name 是工具输入中的参数名，Key 是请求中的原名称；请求体参数的 Key 为空时参数值即整个请求体；Style 与 Explode 是查询参数中数组与对象的序列化方式，取值同 OpenAPI 参数定义。
type HTTPParameter struct {
	Name    string          `json:"name"`
	In      HTTPParameterIn `json:"in"`
	Key     string          `json:"key,omitempty"`
	Style   string          `json:"style,omitempty"`
	Explode bool            `json:"explode,omitempty"`
}

// DefaultHTTPReadOnly 判断 HTTP 方法默认是否只读：GET 与 HEAD 为只读。
func DefaultHTTPReadOnly(method string) bool { return method == "GET" || method == "HEAD" }

// Parameters 返回输入参数定义中的顶层参数名称，按名称排序。
func (t BusinessTool) Parameters() []string {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if len(t.InputSchema) == 0 || json.Unmarshal(t.InputSchema, &schema) != nil {
		return []string{}
	}
	return slices.Sorted(maps.Keys(schema.Properties))
}

// BusinessToolSetting 定义管理员对业务系统中一个工具的设置：事实修正、停用与参数绑定；事实为空时使用工具目录给出的默认值。
type BusinessToolSetting struct {
	ReadOnly          *bool                   `json:"readOnly,omitempty"`
	Reversible        *bool                   `json:"reversible,omitempty"`
	Outbound          *bool                   `json:"outbound,omitempty"`
	Disabled          bool                    `json:"disabled,omitempty"`
	ParameterBindings map[string]ContextValue `json:"parameterBindings,omitempty"`
}

// Empty 判断设置是否与默认一致。
func (s BusinessToolSetting) Empty() bool {
	return s.ReadOnly == nil && s.Reversible == nil && s.Outbound == nil && !s.Disabled && len(s.ParameterBindings) == 0
}

// DefaultToolFacts 按工具目录的特性提示给出默认事实：未声明只读提示的 HTTP 接口按方法判断只读，其余按 HintedToolFacts 推导。
func DefaultToolFacts(tool BusinessTool) ToolFacts {
	facts := HintedToolFacts(tool.ReadOnlyHint, tool.DestructiveHint)
	if tool.HTTP != nil && tool.ReadOnlyHint == nil {
		facts.ReadOnly = DefaultHTTPReadOnly(tool.HTTP.Method)
	}
	return facts
}

// EffectiveToolFacts 合并工具目录的默认事实与管理员修正。
func EffectiveToolFacts(tool BusinessTool, setting BusinessToolSetting) ToolFacts {
	facts := DefaultToolFacts(tool)
	return ToolFacts{
		ReadOnly:   support.DerefOr(setting.ReadOnly, facts.ReadOnly),
		Reversible: support.DerefOr(setting.Reversible, facts.Reversible),
		Outbound:   support.DerefOr(setting.Outbound, facts.Outbound),
	}
}

// IdentityBound 判断业务系统的请求头绑定或工具的参数绑定是否包含调用方身份。
func IdentityBound(headerBindings map[string]ContextValue, setting BusinessToolSetting) bool {
	for _, value := range headerBindings {
		if value.Identity() {
			return true
		}
	}
	for _, value := range setting.ParameterBindings {
		if value.Identity() {
			return true
		}
	}
	return false
}

// ContextValue 定义可绑定到工具参数或连接请求头的可信上下文值。
type ContextValue string

const (
	// ContextValueCustomerUserID 是已核验客户在业务系统中的用户编号。
	ContextValueCustomerUserID ContextValue = "customer_user_id"
	// ContextValueCustomerEmail 是已核验客户的主要邮箱。
	ContextValueCustomerEmail ContextValue = "customer_email"
	// ContextValueMemberUserID 是运行所服务成员的用户编号。
	ContextValueMemberUserID ContextValue = "member_user_id"
	// ContextValueMemberEmail 是运行所服务成员的邮箱。
	ContextValueMemberEmail ContextValue = "member_email"
	// ContextValueConversationID 是运行所属会话的编号。
	ContextValueConversationID ContextValue = "conversation_id"
)

// contextValues 列出全部可信上下文值。
var contextValues = []ContextValue{ContextValueCustomerUserID, ContextValueCustomerEmail, ContextValueMemberUserID, ContextValueMemberEmail, ContextValueConversationID}

// Valid 判断上下文值是否为已定义的值。
func (v ContextValue) Valid() bool { return slices.Contains(contextValues, v) }

// Identity 判断上下文值是否代表调用方身份：客户或成员的编号与邮箱。
func (v ContextValue) Identity() bool { return v != ContextValueConversationID }

// BusinessSystemGrant 定义 AI 员工对一个业务系统的授权：可执行的最高级别、L2 是否需要确认与是否允许对外发信。
type BusinessSystemGrant struct {
	BusinessSystemID string `json:"id"`
	ToolGrant
	Outbound bool `json:"outbound"`
}

// RequiresApproval 判断授权是否允许需要审批的工具：最高级别为 L3 或允许对外发信。
func (g BusinessSystemGrant) RequiresApproval() bool {
	return g.ToolGrant.RequiresApproval() || g.Outbound
}

// Permit 判断授权是否允许指定级别与对外发信事实的工具，并给出执行前需要的人工介入：对外发信需要审批，其余按级别授权判断。
func (g BusinessSystemGrant) Permit(level OperationLevel, outbound bool) (bool, ToolIntervention) {
	if outbound && !g.Outbound {
		return false, ToolInterventionNone
	}
	permitted, intervention := g.ToolGrant.Permit(level)
	if permitted && outbound {
		return true, ToolInterventionApproval
	}
	return permitted, intervention
}
