//go:build server

package agentrun

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/customernotify"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/actions/servicesummary"
	websearchaction "github.com/runforyou-ai/luway/internal/actions/websearch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime/runstream"
	"github.com/runforyou-ai/luway/internal/integration/websearch"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/webfetch"
	"github.com/uptrace/bun"
)

const (
	agentRunTimeout       = 5 * time.Minute
	agentHistoryLimit     = 100
	agentRunErrorMaxRunes = 4000
)

// ExecuteAction 执行并收尾一次 Agent 业务运行。
type ExecuteAction struct {
	db          *bun.DB
	enqueuer    servertask.TxEnqueuer
	runtime     agentruntime.Runtime
	invoker     *modelcall.Invoker
	attachments *AttachmentReader
	knowledge   KnowledgeRetrieval
	webSearch   websearchaction.Searcher
	webFetch    *webfetch.Client
	emailSender customernotify.Sender
	runningMu   sync.Mutex
	runningRuns map[string]*runningAgentRun
}

type executionContext struct {
	Run              servermodels.AgentRun         `bun:",embed"`
	AgentName        string                        `bun:"agent_name"`
	MaxOutputTokens  int64                         `bun:"max_output_tokens"`
	ContextWindow    int64                         `bun:"context_window"`
	InputModalities  []domain.AIModelInputModality `bun:"input_modalities,type:jsonb"`
	Instruction      string                        `bun:"instruction"`
	KnowledgeBaseIDs []string                      `bun:"knowledge_base_ids,type:jsonb"`
	ModelID          string                        `bun:"model_id"`
	HandlesCustomers bool                          `bun:"handles_customers"`
	OrganizationName string                        `bun:"organization_name"`
	AgentID          string                        `bun:"agent_id"`
	// ComputerID 是仅服务负责人本人的 AI 员工使用的电脑，其他 AI 员工为空。
	ComputerID *string `bun:"computer_id"`
}

// NewExecuteAction 创建 Agent Worker Action，联网搜索与网页读取使用默认客户端；emailSender 为空表示部署未配置邮件发送。
func NewExecuteAction(db *bun.DB, enqueuer servertask.TxEnqueuer, runtime agentruntime.Runtime, invoker *modelcall.Invoker, attachments *AttachmentReader, knowledge KnowledgeRetrieval, emailSender customernotify.Sender) *ExecuteAction {
	return &ExecuteAction{
		db: db, enqueuer: enqueuer, runtime: runtime, invoker: invoker, attachments: attachments, knowledge: knowledge,
		webSearch: websearch.NewClient(), webFetch: webfetch.NewClient(common.WebFetchUserAgent()), emailSender: emailSender,
		runningRuns: make(map[string]*runningAgentRun),
	}
}

// runAssignment 表示一次已认领运行的执行指派：有效配置、运行期依赖与本次执行的取消与流式句柄。
type runAssignment struct {
	Execution      executionContext
	Policy         agentRunPolicy
	Assignment     agentruntime.Assignment
	Models         agentruntime.ModelFactory
	MCPConnections []agentruntime.MCPServer
	Knowledge      agentruntime.KnowledgeSearch
	WebSearch      agentruntime.WebSearch
	WebFetch       agentruntime.WebFetch
	History        agentruntime.CustomerHistorySearch
	Memory         agentruntime.MemoryLoader
	Computer       agentruntime.Computer
	// ComputerCapabilities 是个人 AI 员工使用的电脑在本次执行开始时上报的执行能力。
	ComputerCapabilities domain.ComputerCapabilities
	Running              *runningAgentRun
	RunCtx               context.Context
	Release              func() // 结束本次执行的输入状态、取消注册与运行 context。
}

// Execute 取得执行指派、运行 TurnLoop 并收尾，只保存吸收完当前输入后的稳定回复。
func (a *ExecuteAction) Execute(ctx context.Context, input RunInput) error {
	if !common.ValidUUID(input.RunID) {
		return servertask.Permanent(errors.New("agent run id is invalid"))
	}
	assigned, assignErr := a.assign(ctx, input.RunID)
	if assigned.Release != nil {
		defer assigned.Release()
	}
	if assignErr != nil || assigned.Running == nil {
		return assignErr
	}
	result, runErr := a.runAssigned(assigned)
	return a.settle(ctx, assigned, result, runErr)
}

// assign 取得一次运行的执行指派：标记运行中、注册取消句柄、解析有效配置并装配运行期依赖；运行已进入终态时返回空指派。
func (a *ExecuteAction) assign(ctx context.Context, runID string) (runAssignment, error) {
	execution, policy, terminal, err := a.begin(ctx, runID)
	if err != nil || terminal {
		return runAssignment{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, agentRunTimeout)
	running, unregister, err := a.registerRunContext(ctx, execution.Run.ID, cancel)
	if err != nil {
		cancel()
		return runAssignment{}, err
	}
	// 生成期间向会话受众发布 AI 员工正在输入。
	stopTyping := a.startServerRunTyping(runCtx, &execution.Run)
	assigned := runAssignment{
		Execution: execution, Policy: policy, Running: running, RunCtx: runCtx,
		Release: func() {
			stopTyping()
			unregister()
			cancel()
		},
	}
	if running.attempt > 1 {
		taskExecution, _ := servertask.CurrentExecution(ctx)
		slog.Warn("Agent 任务重新计算", "agent_run_id", execution.Run.ID, "task_run_id", taskExecution.TaskRunID,
			"attempt", running.attempt, "stream_id", running.streamID)
	}
	// 关联客户会话的执行范围以客服周期为锚点检索同一客户的历史沟通。
	historySessionID := ""
	if historyPolicy, ok := policy.(customerHistoryPolicy); ok {
		if historySessionID, err = historyPolicy.historyServiceSession(ctx, a.db, &execution.Run); err != nil {
			return assigned, err
		}
	}
	shared, err := a.loadRunCapabilities(ctx, &execution.Run)
	if err != nil {
		return assigned, err
	}
	assigned.MCPConnections = shared.mcpServers
	assigned.Knowledge, err = loadKnowledgeSearch(ctx, a.db, a.knowledge, runScope(&execution.Run), execution.KnowledgeBaseIDs)
	if err != nil {
		return assigned, fmt.Errorf("load agent run knowledge bases: %w", err)
	}
	capabilities := shared.capabilities
	capabilities.Knowledge = assigned.Knowledge != nil
	capabilities.CustomerHistory = historySessionID != ""
	// 个人 AI 员工的文件、命令、本机 MCP 与技能操作派发到其使用的电脑，并读取其记忆。
	if execution.ComputerID != nil {
		computer, err := loadRunComputer(ctx, a.db, execution.Run.OrganizationID, *execution.ComputerID)
		if err != nil {
			return assigned, err
		}
		assigned.Computer = &runComputer{db: a.db, organizationID: execution.Run.OrganizationID, computerID: computer.ID, folder: execution.Run.ConversationID}
		assigned.ComputerCapabilities = computer.Capabilities
		assigned.MCPConnections = append(assigned.MCPConnections, agentruntime.ComputerMCPServers(assigned.Computer, computer.Capabilities)...)
		capabilities.ComputerTools = slices.DeleteFunc(agentruntime.ComputerTools(), func(name string) bool {
			return name == agentruntime.SkillToolName && len(computer.Capabilities.Skills) == 0
		})
		capabilities.Memory = true
	}
	assigned.Assignment, err = a.resolveAssignment(ctx, execution, policy, capabilities)
	if err != nil {
		return assigned, err
	}
	if assigned.Assignment.Memory {
		organizationID, agentID := execution.Run.OrganizationID, execution.AgentID
		assigned.Memory = func(ctx context.Context) ([]agentruntime.MemoryEntry, error) {
			return loadAgentMemoryEntries(ctx, a.db, organizationID, agentID)
		}
	}
	if assigned.Models, err = a.runModels(ctx, &execution.Run, execution.ModelID); err != nil {
		return assigned, err
	}
	// 联网搜索与网页读取按有效配置的工具清单提供。
	if slices.Contains(assigned.Assignment.Tools, agentruntime.WebSearchToolName) {
		assigned.WebSearch = shared.webSearch
	}
	if slices.Contains(assigned.Assignment.Tools, agentruntime.WebFetchToolName) {
		assigned.WebFetch = a.webFetch.Read
	}
	if slices.Contains(assigned.Assignment.Tools, agentruntime.CustomerHistoryToolName) {
		organizationID := execution.Run.OrganizationID
		assigned.History = func(ctx context.Context, query string) (agentruntime.CustomerHistoryResult, error) {
			return servicesummary.SearchHistory(ctx, a.db, organizationID, historySessionID, nil, query)
		}
	}
	return assigned, nil
}

// runCapabilities 是运行能力：本次挂载的企业 MCP 服务、企业联网搜索与据此填写的能力声明。
type runCapabilities struct {
	mcpServers   []agentruntime.MCPServer
	webSearch    agentruntime.WebSearch // 企业未启用联网搜索时为 nil。
	capabilities agentruntime.Capabilities
}

// loadRunCapabilities 读取运行挂载的 MCP 服务与企业联网搜索设置，能力声明填写 MCP 服务、客户验证、联网搜索与网页读取，其余能力由调用方补充。
func (a *ExecuteAction) loadRunCapabilities(ctx context.Context, run *servermodels.AgentRun) (runCapabilities, error) {
	mcpServers, err := loadRunMCPServers(ctx, a.db, run)
	if err != nil {
		return runCapabilities{}, fmt.Errorf("load agent run mcp servers: %w", err)
	}
	webSearch, err := loadRunWebSearch(ctx, a.db, a.webSearch, run.OrganizationID)
	if err != nil {
		return runCapabilities{}, fmt.Errorf("load agent run web search: %w", err)
	}
	serverNames := make([]string, 0, len(mcpServers.Servers))
	for _, server := range mcpServers.Servers {
		serverNames = append(serverNames, server.Name)
	}
	return runCapabilities{
		mcpServers: mcpServers.Servers, webSearch: webSearch,
		capabilities: agentruntime.Capabilities{
			WebSearch: webSearch != nil, WebFetch: true, MCPServers: serverNames, CustomerLoginRequired: mcpServers.CustomerLoginRequired,
		},
	}, nil
}

// runAssigned 按执行指派运行 TurnLoop，已保存恢复状态时从中继续，运行时限到期时统一以超时原因返回。
func (a *ExecuteAction) runAssigned(assigned runAssignment) (agentruntime.RunResult, error) {
	execution, running := assigned.Execution, assigned.Running
	feed := &databaseInputFeed{db: a.db, enqueuer: a.enqueuer, execution: execution, policy: assigned.Policy, attachments: a.attachments}
	resume, err := loadResume(assigned.RunCtx, a.db, &execution.Run)
	if err != nil {
		return agentruntime.RunResult{}, err
	}
	// 场景、依据策略、指令、模型参数与输入模态以有效配置为准，模型来源取当前配置。
	result, err := a.runtime.Run(assigned.RunCtx, agentruntime.RunRequest{
		RunID:                 execution.Run.ID,
		Assignment:            assigned.Assignment,
		Models:                assigned.Models,
		KnowledgeSearch:       assigned.Knowledge,
		WebSearch:             assigned.WebSearch,
		WebFetch:              assigned.WebFetch,
		CustomerHistorySearch: assigned.History,
		ReadAttachment: func(ctx context.Context, messageID string) ([]byte, error) {
			return a.attachments.Content(ctx, &execution.Run, messageID)
		},
		MCPConnections:       assigned.MCPConnections,
		Computer:             assigned.Computer,
		ComputerCapabilities: assigned.ComputerCapabilities,
		Memory:               assigned.Memory,
		StreamID:             running.streamID,
		Attempt:              running.attempt,
		OnStream: func(delta runstream.Delta) {
			// 运行 context 已取消时丢弃增量。
			if assigned.RunCtx.Err() == nil {
				running.stream.Publish(delta)
			}
		},
		Journal: &runJournal{db: a.db, run: &execution.Run},
		Resume:  resume,
	}, feed)
	if err != nil && errors.Is(assigned.RunCtx.Err(), context.DeadlineExceeded) && !errors.Is(err, context.DeadlineExceeded) {
		err = fmt.Errorf("%w: %w", err, context.DeadlineExceeded)
	}
	return result, err
}

// settle 按运行结果收尾：抑制失效结果、原子写入回复或标记失败，并保留已产生的过程内容。
func (a *ExecuteAction) settle(ctx context.Context, assigned runAssignment, result agentruntime.RunResult, runErr error) error {
	execution := assigned.Execution
	if runErr == nil && result.Suspended {
		if err := a.suspend(ctx, &execution.Run); err != nil {
			return err
		}
		slog.Info("Agent 运行挂起等待外部结果", "agent_run_id", execution.Run.ID)
		return nil
	}
	if errors.Is(runErr, errAgentRunSuppressed) {
		// 运行吸收后续输入时已失去资格，保留此前已产生的过程内容。
		return a.persistPartialProcess(ctx, &execution.Run, result)
	}
	if runErr == nil {
		completed, completeErr := a.complete(ctx, execution, assigned.Policy, result)
		if completeErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("persist completed agent run: %w", completeErr)
		}
		// 迟到结果被门禁抑制或运行已结束时保留已产生的过程内容。
		if completed {
			return nil
		}
		return a.persistPartialProcess(ctx, &execution.Run, result)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	terminal, failErr := a.fail(ctx, execution.Run.ID, assigned.Policy, runErr, "")
	if failErr != nil {
		return fmt.Errorf("agent run failed: %v; persist failure: %w", runErr, failErr)
	}
	// 失败与被取消的运行同样保留已产生的过程内容。
	if processErr := a.persistPartialProcess(ctx, &execution.Run, result); processErr != nil {
		return fmt.Errorf("agent run failed: %v; persist partial process: %w", runErr, processErr)
	}
	if terminal {
		return nil
	}
	return servertask.Permanent(fmt.Errorf("execute agent run: %w", runErr))
}

// begin 将待执行或崩溃恢复中的业务运行标记为运行中并登记当前任务为执行方，读取配置并返回本次运行的运行策略；
// 运行已进入终态、处于挂起或已由其他任务执行时返回 true，本次任务无需执行。
func (a *ExecuteAction) begin(ctx context.Context, runID string) (executionContext, agentRunPolicy, bool, error) {
	initial := &servermodels.AgentRun{}
	if err := a.db.NewSelect().Model(initial).Where("agr.id = ?", runID).Scan(ctx); errors.Is(err, sql.ErrNoRows) {
		return executionContext{}, nil, false, servertask.Permanent(errors.New("agent run not found"))
	} else if err != nil {
		return executionContext{}, nil, false, fmt.Errorf("load agent run: %w", err)
	}
	if agentRunStatusTerminal(initial.Status) {
		return executionContext{}, nil, true, nil
	}
	policy, err := a.policyForRun(ctx, initial)
	if err != nil {
		return executionContext{}, nil, false, servertask.Permanent(err)
	}
	taskExecution, _ := servertask.CurrentExecution(ctx)
	terminal := false
	err = realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		locked, err := lockAgentRun(ctx, tx, policy, initial)
		if err != nil {
			return err
		}
		run := locked.Run
		// 挂起的运行由恢复投递的任务继续；执行中的运行只由登记的任务在崩溃后继续。
		if agentRunStatusTerminal(run.Status) || run.Status == string(domain.AgentRunStatusWaiting) || ownedByOtherTask(ctx, run) {
			terminal = true
			return nil
		}
		if run.Status != string(domain.AgentRunStatusQueued) && run.Status != string(domain.AgentRunStatusRunning) {
			return servertask.Permanent(fmt.Errorf("unsupported agent run status %q", run.Status))
		}
		queued := run.Status == string(domain.AgentRunStatusQueued)
		if _, err := tx.NewUpdate().Model(run).
			Set("status = ?", domain.AgentRunStatusRunning).
			Set("task_run_id = NULLIF(?, '')::uuid", taskExecution.TaskRunID).
			Set("started_at = COALESCE(started_at, now())").
			Set("updated_at = now()").WherePK().Exec(ctx); err != nil {
			return err
		}
		// 运行期间以 AI 员工的聊天主体发布输入状态。
		if _, err := chatstate.EnsureOrganizationIdentityChatSubject(ctx, tx, run.OrganizationID, run.AgentIdentityID, uuid.NewV7().String()); err != nil {
			return err
		}
		// 排队运行转为运行中时推进会话版本。
		if !queued {
			return nil
		}
		return chatstate.TouchConversation(ctx, tx, locked.PolicyContext.Conversation, domain.ConversationChangeTimeline)
	})
	if err != nil {
		return executionContext{}, nil, false, fmt.Errorf("begin agent run: %w", err)
	}
	if terminal {
		return executionContext{}, nil, true, nil
	}
	execution, terminal, err := a.loadExecution(ctx, runID)
	if err != nil || terminal {
		return executionContext{}, nil, terminal, err
	}
	return execution, policy, false, nil
}

// loadExecution 读取运行中的业务运行及其锁定配置版本的执行配置，运行已进入终态时返回 true；运行未处于运行中或锁定配置已失效时返回永久错误。
func (a *ExecuteAction) loadExecution(ctx context.Context, runID string) (executionContext, bool, error) {
	execution := executionContext{}
	err := a.db.NewSelect().
		TableExpr("agent_runs AS agr").
		ColumnExpr("agr.*").
		ColumnExpr("oi.display_name AS agent_name").
		ColumnExpr("aim.input_modalities").
		ColumnExpr("ar.configuration->'knowledgeBaseIds' AS knowledge_base_ids").
		ColumnExpr("? = ANY(a.service_audiences) AS handles_customers, o.name AS organization_name", domain.ServiceAudienceCustomer).
		ColumnExpr("a.id::text AS agent_id, a.computer_id").
		Join("JOIN agents AS a ON a.identity_id = agr.agent_identity_id AND a.organization_id = agr.organization_id").
		Join("JOIN organizations AS o ON o.id = agr.organization_id").
		Apply(func(query *bun.SelectQuery) *bun.SelectQuery {
			return withManagedAgentConfiguration(query, "agr.agent_revision_id")
		}).
		Where("agr.id = ?", runID).
		Where("agr.status = ?", domain.AgentRunStatusRunning).
		Scan(ctx, &execution)
	if errors.Is(err, sql.ErrNoRows) {
		var status string
		if reloadErr := a.db.NewSelect().Model((*servermodels.AgentRun)(nil)).
			Column("status").Where("agr.id = ?", runID).Scan(ctx, &status); reloadErr != nil {
			return executionContext{}, false, fmt.Errorf("reload unavailable agent run status: %w", reloadErr)
		}
		if agentRunStatusTerminal(status) {
			return executionContext{}, true, nil
		}
		return executionContext{}, false, servertask.Permanent(fmt.Errorf("load agent run execution: %w", err))
	}
	if err != nil {
		return executionContext{}, false, fmt.Errorf("load agent run execution: %w", err)
	}
	return execution, false, nil
}

// withManagedAgentConfiguration 为已关联 agents AS a 的查询补充指定配置版本的模型和系统指令列，只保留有效的托管对话模型配置。
func withManagedAgentConfiguration(query *bun.SelectQuery, revisionIDColumn string) *bun.SelectQuery {
	return agentConfigurationColumns(joinAgentConfiguration(query, revisionIDColumn))
}

// runScope 返回 AI 员工为该运行发起的模型调用归属。
func runScope(run *servermodels.AgentRun) modelcall.Scope {
	return modelcall.AgentScope(run.OrganizationID, run.AgentIdentityID, domain.AIModelCallSourceAgentRun, run.ID)
}

// runModels 按运行锁定配置版本的模型编号解析对话模型，返回把每次模型请求记为该运行调用的模型组件工厂；模型已不可用时返回永久错误。
func (a *ExecuteAction) runModels(ctx context.Context, run *servermodels.AgentRun, modelID string) (agentruntime.ModelFactory, error) {
	model, err := aimodel.Resolve(ctx, a.db, run.OrganizationID, modelID, domain.AIModelUsageAgent)
	if errors.Is(err, aimodel.ErrUnavailable) {
		return nil, servertask.Permanent(fmt.Errorf("resolve agent run model: %w", err))
	}
	if err != nil {
		return nil, fmt.Errorf("resolve agent run model: %w", err)
	}
	return a.invoker.ChatModels(runScope(run), model), nil
}

// agentConfigurationColumns 为已关联配置版本与模型的查询补充模型和系统指令列。
func agentConfigurationColumns(query *bun.SelectQuery) *bun.SelectQuery {
	return query.
		ColumnExpr("aim.id::text AS model_id, aim.max_output_tokens AS max_output_tokens, aim.context_window AS context_window").
		ColumnExpr("ar.configuration->>'systemInstruction' AS instruction")
}

// managedAgentModel 定义 AI 员工当前托管配置中的对话模型和系统指令。
type managedAgentModel struct {
	ModelID         string `bun:"model_id"`
	MaxOutputTokens int64  `bun:"max_output_tokens"`
	ContextWindow   int64  `bun:"context_window"`
	Instruction     string `bun:"instruction"`
}

// modelConfig 解析托管对话模型，返回经统一调用入口按 scope 记录调用的模型参数；模型已不可用时返回 aimodel.ErrUnavailable。
func (m managedAgentModel) modelConfig(ctx context.Context, db bun.IDB, invoker *modelcall.Invoker, scope modelcall.Scope) (agentruntime.ModelConfig, error) {
	return agentModelConfig(ctx, db, invoker, scope, m.ModelID)
}

// agentModelConfig 解析 AI 员工对话模型，返回经统一调用入口按 scope 记录调用的模型参数；模型已不可用时返回 aimodel.ErrUnavailable。
func agentModelConfig(ctx context.Context, db bun.IDB, invoker *modelcall.Invoker, scope modelcall.Scope, modelID string) (agentruntime.ModelConfig, error) {
	model, err := aimodel.Resolve(ctx, db, scope.OrganizationID, modelID, domain.AIModelUsageAgent)
	if err != nil {
		return agentruntime.ModelConfig{}, err
	}
	return invoker.ModelConfig(scope, model), nil
}

// joinAgentConfiguration 为已关联 agents AS a 的查询关联身份、指定配置版本与对话模型，保留有效的托管对话模型配置。
func joinAgentConfiguration(query *bun.SelectQuery, revisionIDColumn string) *bun.SelectQuery {
	query = query.
		Join("JOIN organization_identities AS oi ON oi.id = a.identity_id AND oi.organization_id = a.organization_id").
		Join("JOIN agent_revisions AS ar ON ar.id = " + revisionIDColumn + " AND ar.agent_id = a.id AND ar.organization_id = a.organization_id").
		Where("ar.schema_version = 1")
	query = aimodel.Join(query, "ar.model_id", "a.organization_id", domain.AIModelUsageAgent)
	return query.Where("ar.execution_mode = ? AND aim.id IS NOT NULL", domain.AgentExecutionModeManaged)
}

// ownedByOtherTask 判断排队或执行中的运行是否已登记由当前任务以外的任务执行。
func ownedByOtherTask(ctx context.Context, run *servermodels.AgentRun) bool {
	execution, _ := servertask.CurrentExecution(ctx)
	return (run.Status == string(domain.AgentRunStatusQueued) || run.Status == string(domain.AgentRunStatusRunning)) &&
		run.TaskRunID != nil && execution.TaskRunID != "" && *run.TaskRunID != execution.TaskRunID
}

// agentRunStatusTerminal 判断 Agent Run 是否已经进入不可覆盖的终态。
func agentRunStatusTerminal(status string) bool {
	return status == string(domain.AgentRunStatusSucceeded) ||
		status == string(domain.AgentRunStatusFailed) ||
		status == string(domain.AgentRunStatusCancelled)
}

// policyForRun 根据执行范围类型和会话形态选择运行策略。
func (a *ExecuteAction) policyForRun(ctx context.Context, run *servermodels.AgentRun) (agentRunPolicy, error) {
	switch domain.AgentExecutionScopeKind(run.ScopeKind) {
	case domain.AgentExecutionScopeServiceSession:
		return customerRunPolicy{enqueuer: a.enqueuer}, nil
	case domain.AgentExecutionScopeConversation:
		var conversationType string
		if err := a.db.NewSelect().Model((*servermodels.Conversation)(nil)).
			Column("type").
			Where("cv.organization_id = ? AND cv.id = ?", run.OrganizationID, run.ConversationID).
			Scan(ctx, &conversationType); err != nil {
			return nil, fmt.Errorf("load agent run conversation type: %w", err)
		}
		switch domain.ConversationType(conversationType) {
		case domain.ConversationTypeGroup:
			return groupMentionRunPolicy{scheduler: NewScheduler(a.enqueuer)}, nil
		case domain.ConversationTypeCopilot:
			return copilotRunPolicy{}, nil
		default:
			return agentChatRunPolicy{enqueuer: a.enqueuer}, nil
		}
	default:
		return nil, fmt.Errorf("unsupported agent execution scope %q", run.ScopeKind)
	}
}

// agentResultMessage 构造 Run 的主结果消息，幂等键为 agent:<run_id>。
func agentResultMessage(run *servermodels.AgentRun, messageID, participantID string, messageType domain.MessageType, content string, serviceSessionID *string) *servermodels.Message {
	idempotencyKey := "agent:" + run.ID
	return &servermodels.Message{
		ID: messageID, OrganizationID: run.OrganizationID, ConversationID: run.ConversationID,
		ServiceSessionID: serviceSessionID, SenderParticipantID: &participantID,
		Type: string(messageType), Body: content, IdempotencyKey: &idempotencyKey,
	}
}

// appendAgentMessage 在 Run 终态门禁通过后追加结果消息，与运行终态共用事务；可能承载服务周期的会话同时登记服务周期变化；幂等重放时核对已有消息的类型与正文，不把另一类消息当作本次写入。
func appendAgentMessage(ctx context.Context, db bun.IDB, conversation *servermodels.Conversation, message *servermodels.Message) (*servermodels.Message, bool, error) {
	message.OriginatedAt = time.Now().UTC()
	appended, inserted, err := chatstate.AppendMessage(ctx, db, conversation, message)
	if err != nil {
		return nil, false, err
	}
	if !inserted && (appended.Type != message.Type || appended.Body != message.Body) {
		return nil, false, fmt.Errorf("agent message idempotency key %q holds a different message", *message.IdempotencyKey)
	}
	// 终态运行派生业务查询与服务记录，客户会话与 AI 聊天随结果消息登记服务周期变化。
	if inserted && (conversation.Type == string(domain.ConversationTypeChannel) || conversation.Type == string(domain.ConversationTypeAgent)) {
		if err := chatstate.NotifyConversationChanged(ctx, db, conversation, domain.ConversationChangeService); err != nil {
			return nil, false, err
		}
	}
	return appended, inserted, nil
}

// complete 按运行策略抑制失效结果或原子写入回复并推进消费序号，返回本次是否写入了完整结果与过程内容。
func (a *ExecuteAction) complete(ctx context.Context, execution executionContext, policy agentRunPolicy, result agentruntime.RunResult) (bool, error) {
	content := strings.TrimSpace(result.Content)
	handoff := result.Decision.Kind == domain.AgentRunOutcomeHandoff
	if handoff && domain.AgentExecutionScopeKind(execution.Run.ScopeKind) != domain.AgentExecutionScopeServiceSession {
		return false, errors.New("agent runtime returned a handoff outside customer service")
	}
	if (content == "" && !handoff) || result.EndSeq <= 0 {
		return false, errors.New("agent runtime returned an invalid result")
	}
	usage, err := json.Marshal(result.Usage)
	if err != nil {
		return false, fmt.Errorf("encode agent run usage: %w", err)
	}
	plan, err := encodeRunPlan(result.Plan)
	if err != nil {
		return false, err
	}
	messageID := uuid.NewV7().String()
	if handoff {
		return a.completeCustomerHandoff(ctx, execution, policy, result, usage)
	}
	suppressed := false
	completed := false
	err = realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		locked, err := lockAgentRun(ctx, tx, policy, &execution.Run)
		if err != nil {
			return fmt.Errorf("lock agent run for completion: %w", err)
		}
		policyContext, lane, run := locked.PolicyContext, locked.Lane, locked.Run
		if agentRunStatusTerminal(run.Status) {
			return nil
		}
		allowed, err := policy.prepareLocked(ctx, tx, policyContext, run)
		if err != nil {
			return err
		}
		if !allowed {
			suppressed = true
			if err := chatstate.TouchConversation(ctx, tx, policyContext.Conversation, domain.ConversationChangeTimeline|domain.ConversationChangeService); err != nil {
				return err
			}
			return scheduleNextRun(ctx, tx, a.enqueuer, policy, policyContext, run.OrganizationID, domain.AgentExecutionScopeKind(run.ScopeKind), run.ScopeID)
		}
		if run.Status != string(domain.AgentRunStatusRunning) || run.InputEndSeq == nil ||
			*run.InputEndSeq != result.EndSeq || run.InputStartSeq != lane.ProcessedSeq+1 {
			return errors.New("agent run completion boundary is inconsistent")
		}
		if err := policy.persistMessage(ctx, tx, policyContext, run, messageID, domain.MessageTypeText, content); err != nil {
			return err
		}
		if mentioning, ok := policy.(mentionReplyPolicy); ok {
			if err := mentioning.applyMentions(ctx, tx, policyContext, run, messageID, content); err != nil {
				return err
			}
		}
		if deciding, ok := policy.(decisionPolicy); ok {
			if err := deciding.applyDecision(ctx, tx, policyContext, run, lane, result, messageID); err != nil {
				return err
			}
		}
		// 在最终消息事务中写入成功运行的完整过程。
		if err := agentprocess.Sync(ctx, tx, run, result.Blocks, result.Calls); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model(run).
			Set("status = ?", domain.AgentRunStatusSucceeded).
			Set("outcome = ?", result.Decision.Outcome()).
			Set("response_message_id = ?", messageID).
			Set("usage = ?::jsonb", string(usage)).
			Set("plan = ?::jsonb", plan).
			Set("last_error = NULL").
			Set("error_code = NULL").
			Set("completed_at = now()").
			Set("updated_at = now()").
			WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("complete agent run: %w", err)
		}
		if _, err := tx.NewUpdate().Model(lane).
			Set("processed_seq = ?", result.EndSeq).
			Set("updated_at = now()").
			WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("advance processed agent input sequence: %w", err)
		}
		if err := scheduleNextRun(ctx, tx, a.enqueuer, policy, policyContext, run.OrganizationID, domain.AgentExecutionScopeKind(run.ScopeKind), run.ScopeID); err != nil {
			return err
		}
		completed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	if suppressed && domain.AgentExecutionScopeKind(execution.Run.ScopeKind) == domain.AgentExecutionScopeServiceSession {
		// 记录客服门禁抑制的迟到结果。
		slog.Warn("客户 Agent 迟到结果已抑制",
			"agent_run_id", execution.Run.ID,
			"conversation_id", execution.Run.ConversationID,
		)
	}
	if completed {
		logCompletedRun(execution, result.EndSeq, messageID)
	}
	return completed, nil
}

// persistPartialProcess 在运行以失败或取消结束后保留已产生的过程内容、任务清单与用量，并推进会话版本让成员重读。运行仍可继续或已成功收尾时不写入。
func (a *ExecuteAction) persistPartialProcess(ctx context.Context, initial *servermodels.AgentRun, partial agentruntime.RunResult) error {
	if len(partial.Blocks) == 0 {
		return nil
	}
	usage, err := json.Marshal(partial.Usage)
	if err != nil {
		return fmt.Errorf("encode partial agent run usage: %w", err)
	}
	plan, err := encodeRunPlan(partial.Plan)
	if err != nil {
		return err
	}
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		conversation, err := chatstate.LockConversation(ctx, tx, initial.OrganizationID, initial.ConversationID)
		if err != nil {
			return err
		}
		run := &servermodels.AgentRun{}
		if err := tx.NewSelect().Model(run).Where("agr.id = ?", initial.ID).For("UPDATE").Scan(ctx); err != nil {
			return fmt.Errorf("lock agent run for partial process: %w", err)
		}
		// 成功运行在结果事务中已写入完整过程。
		if !agentRunStatusTerminal(run.Status) || run.Status == string(domain.AgentRunStatusSucceeded) {
			return nil
		}
		if err := agentprocess.Sync(ctx, tx, run, partial.Blocks, partial.Calls); err != nil {
			return err
		}
		if err := agentprocess.SettleEndedRuns(ctx, tx, run.OrganizationID, run.ID); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model(run).
			Set("usage = ?::jsonb", string(usage)).
			Set("plan = ?::jsonb", plan).
			Set("updated_at = now()").
			WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("persist partial agent run usage: %w", err)
		}
		return chatstate.TouchConversation(ctx, tx, conversation, domain.ConversationChangeTimeline|domain.ConversationChangeService)
	})
}

// encodeRunPlan 编码运行的任务清单，没有任务时返回 nil 使字段保持为空。
func encodeRunPlan(plan []runstream.PlanTask) (*string, error) {
	if len(plan) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("encode agent run plan: %w", err)
	}
	text := string(encoded)
	return &text, nil
}

// logCompletedRun 记录关联客服周期的 Agent 完成结果。
func logCompletedRun(execution executionContext, endSeq int64, messageID string) {
	if domain.AgentExecutionScopeKind(execution.Run.ScopeKind) != domain.AgentExecutionScopeServiceSession {
		return
	}
	slog.Info("客户 Agent 运行完成",
		"agent_run_id", execution.Run.ID,
		"conversation_id", execution.Run.ConversationID,
		"service_session_id", execution.Run.ScopeID,
		"input_start_seq", execution.Run.InputStartSeq,
		"input_end_seq", endSeq,
		"response_message_id", messageID,
	)
}

// fail 按运行策略取消失效运行或标记失败：客服运行转交人工，其他运行写入错误消息、记录错误码并为剩余输入补建下一次运行；policy 为空时按运行解析，code 为空表示没有稳定错误码。
func (a *ExecuteAction) fail(ctx context.Context, runID string, policy agentRunPolicy, runErr error, code domain.AgentRunErrorCode) (bool, error) {
	// 限制持久化错误详情长度。
	message := "agent run failed"
	if runErr != nil {
		runes := []rune(runErr.Error())
		if len(runes) > agentRunErrorMaxRunes {
			runes = runes[:agentRunErrorMaxRunes]
		}
		message = string(runes)
	}
	initial := &servermodels.AgentRun{}
	if err := a.db.NewSelect().Model(initial).Where("agr.id = ?", runID).Scan(ctx); err != nil {
		return false, err
	}
	// 挂起的运行没有执行中的任务，失败收尾不改变它。
	if agentRunStatusTerminal(initial.Status) || initial.Status == string(domain.AgentRunStatusWaiting) {
		return true, nil
	}
	if policy == nil {
		resolved, err := a.policyForRun(ctx, initial)
		if err != nil {
			return false, err
		}
		policy = resolved
	}
	if domain.AgentExecutionScopeKind(initial.ScopeKind) == domain.AgentExecutionScopeServiceSession {
		reason := domain.AgentHandoffReasonRuntimeFailed
		if errors.Is(runErr, context.DeadlineExceeded) {
			reason = domain.AgentHandoffReasonTimeout
		}
		return a.failCustomerRun(ctx, initial, policy, message, reason)
	}
	terminal := false
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		locked, err := lockAgentRun(ctx, tx, policy, initial)
		if err != nil {
			return fmt.Errorf("lock agent run for failure: %w", err)
		}
		policyContext, lane, run := locked.PolicyContext, locked.Lane, locked.Run
		if agentRunStatusTerminal(run.Status) {
			terminal = true
			return nil
		}
		allowed, err := policy.prepareLocked(ctx, tx, policyContext, run)
		if err != nil {
			return err
		}
		if !allowed {
			terminal = true
			if err := chatstate.TouchConversation(ctx, tx, policyContext.Conversation, domain.ConversationChangeTimeline|domain.ConversationChangeService); err != nil {
				return err
			}
			return scheduleNextRun(ctx, tx, a.enqueuer, policy, policyContext, run.OrganizationID, domain.AgentExecutionScopeKind(run.ScopeKind), run.ScopeID)
		}
		// 挂起或已由其他任务执行的运行，失败收尾不改变它。
		if run.Status == string(domain.AgentRunStatusWaiting) || ownedByOtherTask(ctx, run) {
			terminal = true
			return nil
		}
		if run.Status != string(domain.AgentRunStatusQueued) && run.Status != string(domain.AgentRunStatusRunning) {
			return fmt.Errorf("cannot fail agent run in status %q", run.Status)
		}
		failureEnd := run.InputStartSeq
		if run.InputEndSeq != nil {
			failureEnd = *run.InputEndSeq
		}
		if run.InputStartSeq != lane.ProcessedSeq+1 || failureEnd < run.InputStartSeq || failureEnd > lane.DesiredSeq {
			return errors.New("agent run failure boundary is inconsistent")
		}
		failedSeqs, err := claimLaneInputs(ctx, tx, run, lane.ProcessedSeq, failureEnd)
		if err != nil {
			return err
		}
		if int64(len(failedSeqs)) != failureEnd-lane.ProcessedSeq {
			return errors.New("failed agent input sequence is not contiguous")
		}
		messageID := uuid.NewV7().String()
		if err := policy.persistMessage(ctx, tx, policyContext, run, messageID, domain.MessageTypeAgentError, ""); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model(run).
			Set("status = ?", domain.AgentRunStatusFailed).
			Set("response_message_id = ?", messageID).
			Set("input_end_seq = ?", failureEnd).
			Set("last_error = ?", message).
			Set("error_code = NULLIF(?, '')", code).
			Set("completed_at = now()").
			Set("updated_at = now()").
			WherePK().
			Exec(ctx); err != nil {
			return err
		}
		if err := agentprocess.SettleEndedRuns(ctx, tx, run.OrganizationID, run.ID); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model(lane).
			Set("processed_seq = ?", failureEnd).
			Set("updated_at = now()").
			WherePK().Exec(ctx); err != nil {
			return err
		}
		return scheduleNextRun(ctx, tx, a.enqueuer, policy, policyContext, run.OrganizationID, domain.AgentExecutionScopeKind(run.ScopeKind), run.ScopeID)
	})
	return terminal, err
}

// FinalizeFailure 在任务达到最终失败时收敛 Agent 业务运行。
func (a *ExecuteAction) FinalizeFailure(ctx context.Context, input RunInput, runErr error) error {
	if !common.ValidUUID(input.RunID) {
		return errors.New("agent run id is invalid")
	}
	_, err := a.fail(ctx, input.RunID, nil, runErr, "")
	return err
}
