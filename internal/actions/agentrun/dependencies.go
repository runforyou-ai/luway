//go:build server

package agentrun

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/luway/internal/actions/businesssystem"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/actions/servicesummary"
	"github.com/runforyou-ai/luway/internal/actions/tooldecision"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
)

// dependencySources 是装配运行依赖的来源：知识检索的调用归属、业务系统的挂载方式、客户历史的检索范围与运行的服务对象；
// conversation 为真时运行属于真实会话，另提供联网搜索、网页读取与会话共享文件区。
type dependencySources struct {
	scope        modelcall.Scope
	mount        businesssystem.MountOptions
	history      *customerHistoryScope // 为空时不提供客户历史检索。
	audience     runAudience
	conversation bool
}

// customerHistoryScope 是客户历史检索的范围：以服务周期为锚点，closedBefore 非空时只检索在其之前关闭的其他周期。
type customerHistoryScope struct {
	serviceSessionID string
	closedBefore     *time.Time
}

// runDependencies 是一次运行可提供的业务依赖，以及工作区电脑名称与客户未验证身份等只影响指令的事实。
type runDependencies struct {
	agentruntime.Dependencies
	workspaceComputer     string
	customerLoginRequired bool
}

// capabilities 返回依赖能提供的执行侧能力，用于解析有效配置。
func (d runDependencies) capabilities() agentruntime.Capabilities {
	capabilities := d.Dependencies.Capabilities()
	capabilities.WorkspaceComputer, capabilities.CustomerLoginRequired = d.workspaceComputer, d.customerLoginRequired
	return capabilities
}

// loadDependencies 按来源装配一次运行可提供的全部业务依赖，Execute 与 Replay 共用；运行按有效配置的工具清单取用，清单未列出的依赖不注册工具。
// 业务系统调用会话的生命周期跟随 ctx。文件、命令、本机 MCP、技能、浏览器与桌面操作派发到 AI 员工使用的电脑：个人电脑由负责人本人使用，不按级别限制；
// 工作区电脑按 AI 员工的授权限制，服务客户时文件操作限定在会话文件夹内；本机 Agent 取电脑上报且 AI 员工启用的部分，服务客户时不提供。
func (a *ExecuteAction) loadDependencies(ctx context.Context, execution executionContext, sources dependencySources) (runDependencies, error) {
	run := &execution.Run
	loaded := runDependencies{}
	var err error
	loaded.KnowledgeSearch, err = loadKnowledgeSearch(ctx, a.db, a.knowledge, sources.scope, execution.KnowledgeBaseIDs)
	if err != nil {
		return loaded, fmt.Errorf("load agent run knowledge bases: %w", err)
	}
	businessTools, err := businesssystem.LoadRunTools(ctx, a.db, a.connector, run.WorkspaceID, sources.mount)
	if err != nil {
		return loaded, fmt.Errorf("load agent run business systems: %w", err)
	}
	// 暂停确认后在运行内执行的调用，派发前按当前配置复核授权。
	workspaceID, scopes := run.WorkspaceID, NewRunScopes(a.enqueuer)
	checkDecided := func(ctx context.Context, callID string) (string, error) {
		return tooldecision.CheckDecidedCall(ctx, a.db, scopes, workspaceID, callID)
	}
	for index := range businessTools.Systems {
		businessTools.Systems[index].Caller = decidedCaller{BusinessCaller: businessTools.Systems[index].Caller, check: checkDecided}
	}
	loaded.BusinessSystems, loaded.customerLoginRequired = businessTools.Systems, businessTools.CustomerLoginRequired
	if history := sources.history; history != nil {
		loaded.CustomerHistorySearch = func(ctx context.Context, query string) (agentcontract.CustomerHistoryResult, error) {
			return servicesummary.SearchHistory(ctx, a.db, workspaceID, history.serviceSessionID, history.closedBefore, query)
		}
	}
	if sources.conversation {
		if loaded.WebSearch, err = loadRunWebSearch(ctx, a.db, a.webSearch, run.WorkspaceID); err != nil {
			return loaded, fmt.Errorf("load agent run web search: %w", err)
		}
		loaded.WebFetch = a.webFetch.Read
		if a.sharedFiles != nil {
			files, err := loadRunSharedFiles(ctx, a.db, a.sharedFiles, a.documents, run)
			if err != nil {
				return loaded, err
			}
			loaded.SharedFiles = files
		}
	}
	if execution.ComputerID != nil {
		computer, err := loadRunComputer(ctx, a.db, run.WorkspaceID, *execution.ComputerID)
		if err != nil {
			return loaded, err
		}
		loaded.Computer = &runComputer{
			db: a.db, workspaceID: run.WorkspaceID, agentID: execution.AgentID, computerID: computer.ID,
			folder: run.ConversationID, confined: sources.audience.customer, checkDecided: checkDecided,
		}
		loaded.ComputerCapabilities = computer.Capabilities
		loaded.ComputerCapabilities.LocalAgents = slices.DeleteFunc(slices.Clone(computer.Capabilities.LocalAgents), func(agent domain.ComputerLocalAgent) bool {
			return sources.audience.customer || !slices.Contains(execution.LocalAgents, agent.Name)
		})
		if computer.Kind == domain.ComputerKindWorkspace {
			loaded.ComputerAccess = agentruntime.ComputerAccess{Grant: cmp.Or(execution.ComputerGrant, &domain.ToolGrant{}), Interventions: sources.audience.interventions}
			loaded.workspaceComputer = computer.Name
		}
	}
	if execution.Personal {
		agentID := execution.AgentID
		loaded.Memory = func(ctx context.Context) ([]agentruntime.MemoryEntry, error) {
			return loadAgentMemoryEntries(ctx, a.db, workspaceID, agentID)
		}
	}
	return loaded, nil
}

// decidedCaller 在业务系统调用前复核暂停确认后执行的调用，授权已不允许时不发出请求，原因交给模型。
type decidedCaller struct {
	agentcontract.BusinessCaller
	check func(context.Context, string) (string, error)
}

// Call 复核当前调用后调用业务系统工具。
func (c decidedCaller) Call(ctx context.Context, name string, arguments json.RawMessage) (string, error) {
	if call, ok := einorun.CallFrom(ctx); ok {
		failure, err := c.check(ctx, call.RecordID)
		if err != nil {
			return "", err
		}
		if failure != "" {
			return "", errors.New(failure)
		}
	}
	return c.BusinessCaller.Call(ctx, name, arguments)
}
