//go:build server

// Package computer 实现个人电脑的注册、工作区电脑的添加与凭据重置、凭据认证、在线记录、操作领取与结果上报，并重新派发或结算执行器丢失、电脑离线或撤销以及超过执行时限的调用。
package computer

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// RegisterInput 定义执行器把所在电脑注册为个人电脑时上报的本机信息。
type RegisterInput struct {
	InstallID string
	Name      string
}

// Record 定义电脑记录，Online 表示执行器当前在线，Platform 在执行器尚未上报时为空，AgentCount 是使用该电脑的 AI 员工数。
type Record struct {
	ID         string
	Kind       domain.ComputerKind
	Name       string
	Platform   *domain.ComputerPlatform
	Online     bool
	AgentCount int
	LastSeenAt *time.Time
	CreatedAt  time.Time
	// LocalAgents 是电脑上报的本机 Agent。
	LocalAgents []domain.ComputerLocalAgent
}

// Registration 定义注册、添加或重置凭据的结果与只返回一次的电脑凭据。
type Registration struct {
	Record     Record
	Credential string
}

// Identity 定义以电脑凭据认证的电脑。
type Identity struct {
	WorkspaceID string
	ComputerID  string
}

// CapabilitiesInput 定义执行器上报的操作系统平台、执行能力、版本与同时执行的操作上限。
type CapabilitiesInput struct {
	Platform        domain.ComputerPlatform
	Capabilities    domain.ComputerCapabilities
	ExecutorVersion string
	MaxConcurrency  int
}

// Operation 定义执行器领取到的一次操作，编号即工具调用编号；Timeout 是执行器执行该操作的时限，委派本机 Agent 的一轮为零，表示不设时限。
type Operation struct {
	ID        string
	Operation domain.ComputerOperation
	Timeout   time.Duration
	// TraceID 是派发到电脑时的串联编号，执行器执行时沿用。
	TraceID string
}

// ClaimInput 定义一次领取的参数：最多领取的操作数，与执行器本机仍在执行的操作、仍持有的本机 Agent 会话和仍在等待裁决的权限请求。
type ClaimInput struct {
	Limit       int
	Running     []string
	Sessions    []string
	Permissions []string
}

// ClaimResult 定义一次领取的结果：新领取的操作，执行器正在执行而应当中止的操作编号，执行器持有而已释放的本机 Agent 会话编号，以及已有结果的权限请求。
type ClaimResult struct {
	Operations  []Operation
	Abort       []string
	Released    []string
	Permissions []PermissionResult
}

// PermissionResult 定义一个权限请求的结果：选用的处理方式编号，为空表示请求已取消。
type PermissionResult struct {
	ID       string
	OptionID string
}

// PermissionInput 定义执行器上报的本机 Agent 权限请求，编号由执行器分配。
type PermissionInput struct {
	ID         string
	Permission domain.LocalAgentPermission
}

// UpdateInput 定义执行器上报的一条过程更新。
type UpdateInput struct {
	Seq    int
	Update domain.ToolCallUpdate
}

// recordFromModel 转换电脑存储模型，agentCount 是使用该电脑的 AI 员工数，online 表示电脑在线。
func recordFromModel(input servermodels.Computer, agentCount int, online bool) Record {
	return Record{
		ID: input.ID, Kind: input.Kind, Name: input.Name, Platform: input.Platform,
		Online: online, AgentCount: agentCount, LastSeenAt: input.LastSeenAt, CreatedAt: input.CreatedAt,
		LocalAgents: input.Capabilities.LocalAgents,
	}
}
