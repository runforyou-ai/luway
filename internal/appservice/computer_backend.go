package appservice

import "context"

// ComputerBackend 定义执行器以电脑身份调用服务端的契约。
//
// 每个方法必须携带一条 appservice:route 指令，格式为：
//
//	appservice:route <HTTP方法> <路径> [status=201] [unbounded]
//
// appservicegen 按指令生成电脑认证分发、net/http 路由与执行器客户端；unbounded 标记的方法在执行器客户端中不设请求时限。
// 每个调用以 RequestMeta.Token 携带的电脑凭据认证，与成员登录会话无关。
type ComputerBackend interface {
	// GetComputerRealtimeConnection 换取本电脑的短期实时凭据和工作通知频道。
	//appservice:route GET /computer/realtime-connection
	GetComputerRealtimeConnection(context.Context, RequestMeta) (RealtimePeerConnection, error)
	// HeartbeatComputer 以当前电脑凭据记录最近在线时间。
	//appservice:route POST /computer/heartbeat
	HeartbeatComputer(context.Context, RequestMeta) error
	// ReportComputerCapabilities 上报本电脑的操作系统平台、执行能力、执行器版本与同时执行的操作上限。
	//appservice:route PUT /computer/capabilities
	ReportComputerCapabilities(context.Context, RequestMeta, ComputerCapabilitiesInput) error
	// ClaimComputerOperations 领取派发给本电脑的待执行操作，并返回应当中止的操作。
	//appservice:route POST /computer/operations/claim
	ClaimComputerOperations(context.Context, RequestMeta, ComputerClaimInput) (ComputerOperationList, error)
	// CompleteComputerOperation 上报本电脑执行一次操作的结果。
	//appservice:route POST /computer/operations/{operationID}/result
	CompleteComputerOperation(context.Context, RequestMeta, string, ComputerOutcomeInput) error
	// ReportComputerOperationUpdates 上报本电脑执行中的一次操作的过程更新。
	//appservice:route POST /computer/operations/{operationID}/updates
	ReportComputerOperationUpdates(context.Context, RequestMeta, string, ComputerUpdatesInput) error
	// ListComputerSharedFiles 返回本电脑执行中的命令或本机 Agent 轮次所属会话共享文件区的文件清单与下载地址。
	//appservice:route GET /computer/operations/{operationID}/shared-files
	ListComputerSharedFiles(context.Context, RequestMeta, string) (ComputerSharedFileList, error)
	// CreateComputerSharedUpload 为写回会话共享文件区中的一个文件创建内容上传。
	//appservice:route POST /computer/operations/{operationID}/shared-files/uploads status=201
	CreateComputerSharedUpload(context.Context, RequestMeta, string, ComputerSharedUploadInput) (ComputerSharedUpload, error)
	// CommitComputerSharedFile 把上传完成的内容按同步基准写回会话共享文件区。
	//appservice:route POST /computer/operations/{operationID}/shared-files/commit unbounded
	CommitComputerSharedFile(context.Context, RequestMeta, string, ComputerSharedCommitInput) (ComputerSharedCommit, error)
	// RequestComputerPermission 上报本电脑上本机 Agent 在执行中的一轮里请求的权限。
	//appservice:route POST /computer/operations/{operationID}/permissions
	RequestComputerPermission(context.Context, RequestMeta, string, ComputerPermissionInput) error
}
