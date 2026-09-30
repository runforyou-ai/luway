package appservice

import "context"

// DeviceRunBackend 定义本机设备执行 Agent 运行的运行期调用。
//
// 每个方法必须携带一条 appservice:route 指令，格式为：
//
//	appservice:route <HTTP方法> <路径> [status=201] [query=<参数名>] [manual=proxy]
//
// appservicegen 按指令生成设备认证分发、Gin 路由与 Handler 和原生端 API Proxy 转发，
// 生成结果写入 direct/device_run_backend_gen.go、internal/api/device_run_service_gen.go
// 和 internal/apiproxy/device_run_backend_gen.go，不生成 Service 委托和前端绑定。
// 指令只接受 status、query 与 manual=proxy 选项，manual=proxy 的 API Proxy 转发在 apiproxy 包手写；
// 每个调用先校验登录令牌，再校验 DeviceHeader 指向的设备属于当前用户且未撤销。
//
// 本契约的消费者是原生端的设备执行循环，界面发起的设备与助理管理属于
// Backend，新增方法按消费者归入其中一个。
type DeviceRunBackend interface {
	// GetDeviceWork 返回本设备的工作水位与待领取运行。
	//appservice:route GET /devices/current/work
	GetDeviceWork(context.Context, RequestMeta) (DeviceWork, error)
	// ReportDeviceLocalAgents 上报本设备上已安装且可用的本机 Agent。
	//appservice:route PUT /devices/current/local-agents
	ReportDeviceLocalAgents(context.Context, RequestMeta, DeviceLocalAgentsInput) error
	// ClaimDeviceRun 领取派发给本设备的排队运行并取得租约。
	//appservice:route POST /agent-runs/:runID/claim
	ClaimDeviceRun(context.Context, RequestMeta, string) (DeviceRunClaim, error)
	// RenewDeviceRunLease 为本设备持有的运行续租，运行已结束时返回 ended。
	//appservice:route POST /agent-runs/:runID/lease
	RenewDeviceRunLease(context.Context, RequestMeta, string) (DeviceRunLease, error)
	// PeekDeviceRunInputs 返回本设备持有运行尚未认领的输入信号。
	//appservice:route GET /agent-runs/:runID/inputs
	PeekDeviceRunInputs(context.Context, RequestMeta, string, DeviceRunInputPeekInput) (DeviceRunInputSignals, error)
	// ClaimDeviceRunInputs 为本设备持有的运行认领输入并返回截至该边界的上下文消息。
	//appservice:route POST /agent-runs/:runID/inputs/claim
	ClaimDeviceRunInputs(context.Context, RequestMeta, string, DeviceRunInputClaimInput) (DeviceRunClaimedInput, error)
	// SearchDeviceRunKnowledge 在本设备持有运行绑定的知识库中检索。
	//appservice:route POST /agent-runs/:runID/knowledge/search
	SearchDeviceRunKnowledge(context.Context, RequestMeta, string, DeviceRunKnowledgeSearchInput) (DeviceRunKnowledgeSearchResult, error)
	// GetDeviceRunMemory 返回本设备持有运行所属助理的记忆，有效配置未启用记忆时为空。
	//appservice:route GET /agent-runs/:runID/memory
	GetDeviceRunMemory(context.Context, RequestMeta, string) (DeviceRunMemory, error)
	// SearchDeviceRunWeb 用企业配置的搜索服务为本设备持有的运行搜索互联网。
	//appservice:route POST /agent-runs/:runID/web/search
	SearchDeviceRunWeb(context.Context, RequestMeta, string, DeviceRunWebSearchInput) (DeviceRunWebSearchResult, error)
	// ListDeviceRunMCPTools 列出本设备持有运行绑定的企业 MCP 服务及其工具目录，不可用的服务不列出。
	//appservice:route GET /agent-runs/:runID/mcp/tools
	ListDeviceRunMCPTools(context.Context, RequestMeta, string) (DeviceRunMCPToolList, error)
	// CallDeviceRunMCPTool 为本设备持有的运行调用企业 MCP 工具。
	//appservice:route POST /agent-runs/:runID/mcp/tools/call manual=proxy
	CallDeviceRunMCPTool(context.Context, RequestMeta, string, DeviceRunMCPToolCallInput) (DeviceRunMCPToolCallResult, error)
	// CompleteDeviceRun 以成功结果收尾本设备持有的运行。
	//appservice:route POST /agent-runs/:runID/result
	CompleteDeviceRun(context.Context, RequestMeta, string, DeviceRunResultInput) error
	// FailDeviceRun 以失败原因收尾派发给本设备的运行。
	//appservice:route POST /agent-runs/:runID/failure
	FailDeviceRun(context.Context, RequestMeta, string, DeviceRunFailureInput) error
}
