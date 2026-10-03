package appservice

import "context"

// ComputerBackend 定义执行器以电脑身份调用服务端的契约。
//
// 每个方法必须携带一条 appservice:route 指令，格式为：
//
//	appservice:route <HTTP方法> <路径> [status=201] [query=<参数名>]
//
// appservicegen 按指令生成电脑认证分发与 Gin 路由和 Handler，生成结果写入
// direct/computer_backend_gen.go 与 internal/api/computer_service_gen.go，不生成 Service 委托、API Proxy 转发和前端绑定；
// 执行器使用自带的客户端调用。每个调用以 RequestMeta.Token 携带的电脑凭据认证，与成员登录会话无关。
type ComputerBackend interface {
	// ReportComputerCapabilities 上报本电脑的执行能力、执行器版本与同时执行的操作上限。
	//appservice:route PUT /computer/capabilities
	ReportComputerCapabilities(context.Context, RequestMeta, ComputerCapabilitiesInput) error
	// ClaimComputerOperations 领取派发给本电脑的待执行操作，并返回应当中止的操作。
	//appservice:route POST /computer/operations/claim
	ClaimComputerOperations(context.Context, RequestMeta, ComputerClaimInput) (ComputerOperationList, error)
	// CompleteComputerOperation 上报本电脑执行一次操作的结果。
	//appservice:route POST /computer/operations/:operationID/result
	CompleteComputerOperation(context.Context, RequestMeta, string, ComputerOutcomeInput) error
}
