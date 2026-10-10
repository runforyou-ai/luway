package appservice

//go:generate go run github.com/runforyou-ai/luway/internal/tools/appservicegen

// Backend 定义企业服务端对外提供的业务调用，是各端前端、HTTP 路由与认证分发的唯一契约源。
//
// 每个方法必须携带一条 appservice:route 指令，格式为：
//
//	appservice:route <HTTP方法> <路径> [status=201] [query=<参数名>] [auth=public|account|admin] [perm=<权限代码>|none] [manual]
//
// appservicegen 按指令生成服务端认证分发、net/http 路由与前端 TypeScript 契约和调用函数；
// manual 标记的方法由 api 包手写注册 HTTP 路由。路径相对服务端 /api，其中的 {参数} 依次对应签名中的 string 参数，
// GET 方法的结构体参数按 query 标签绑定查询参数，其余方法的结构体参数绑定 JSON 请求体。
//
// auth 默认为 member：服务端分发层先解析登录账号在请求目标工作区中的成员身份，按 perm 选项
// 校验成员角色是否授予该权限，再把身份交给业务实现，业务实现不重复处理认证。member 方法必须
// 声明 perm：取值为 domain 中的权限代码，所有成员都可调用时写 perm=none。只需要登录账号的方法标记 auth=account，
// 只允许平台管理员调用的平台级管理方法标记 auth=admin，无需登录的方法标记 auth=public。
//
// Backend 按业务域嵌入 backend_<域>.go 中的子接口，方法与指令写在子接口中，方法名在全部子接口间唯一；
// appservicegen 按嵌入顺序展开子接口，子接口内按声明顺序输出方法。
type Backend interface {
	AuthBackend
	ProductDocsBackend
	DirectoryBackend
	FileBackend
	ConversationBackend
	InboxBackend
	CustomerServiceBackend
	TranslationBackend
	AgentBackend
	ToolDecisionBackend
	ChannelBackend
	TelegramBackend
	WechatBackend
	AgentEvaluationBackend
	PersonalAgentBackend
	InvitationBackend
	PlatformBackend
	SeatBackend
	KnowledgeBackend
	ContactBackend
	IntegrationBackend
	WebSearchBackend
	AIPerformanceBackend
	TeamPerformanceBackend
	ServiceIssueBackend
	KnowledgeGapBackend
	ComputerManagementBackend
}
