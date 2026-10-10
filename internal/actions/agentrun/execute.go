//go:build server

package agentrun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
	"uuid"

	"github.com/runforyou-ai/einorun/llm"
	"github.com/runforyou-ai/einorun/stream"
	"github.com/runforyou-ai/luway/internal/actions/businesssystem"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/conversationfile"
	"github.com/runforyou-ai/luway/internal/actions/customernotify"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	websearchaction "github.com/runforyou-ai/luway/internal/actions/websearch"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/websearch"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/webfetch"
	"github.com/uptrace/bun"
)

const (
	// agentRunTimeout 是一次执行尝试的运行时限。
	agentRunTimeout = 5 * time.Minute
	// agentHistoryLimit 是进入模型上下文的最近会话消息条数上限。
	agentHistoryLimit = 100
	// agentRunErrorMaxRunes 是运行记录保存的错误详情最大字符数。
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
	connector   *businesssystem.Connector
	emailSender customernotify.Sender
	// sharedFiles 与 documents 为运行提供会话共享文件区，未配置时文件工具只操作电脑。
	sharedFiles *conversationfile.Store
	documents   DocumentConverter
	runningMu   sync.Mutex
	runningRuns map[string]*runningAgentRun
	// admissions 是本实例已准入的运行执行数，在 runningMu 下递增，作为运行流登记的准入序号。
	admissions uint64
}

// executionContext 是运行记录与其锁定配置版本的执行配置、AI 员工资料和所用电脑。
type executionContext struct {
	Run              servermodels.AgentRun         `bun:",embed"`
	AgentName        string                        `bun:"agent_name"`
	MaxOutputTokens  int64                         `bun:"max_output_tokens"`
	ContextWindow    int64                         `bun:"context_window"`
	InputModalities  []domain.AIModelInputModality `bun:"input_modalities,type:jsonb"`
	Instruction      string                        `bun:"instruction"`
	KnowledgeBaseIDs []string                      `bun:"knowledge_base_ids,type:jsonb"`
	BusinessSystems  []domain.BusinessSystemGrant  `bun:"business_systems,type:jsonb"`
	ModelID          string                        `bun:"model_id"`
	HandlesCustomers bool                          `bun:"handles_customers"`
	WorkspaceName    string                        `bun:"workspace_name"`
	AgentID          string                        `bun:"agent_id"`
	// ComputerID 是 AI 员工使用的电脑：个人 AI 员工为负责人的个人电脑，服务型 AI 员工为工作区电脑，未使用电脑时为空。
	ComputerID *string `bun:"computer_id"`
	// ComputerGrant 是服务型 AI 员工对所用工作区电脑的授权，个人 AI 员工与未使用电脑时为空。
	ComputerGrant *domain.ToolGrant `bun:"computer_grant,type:jsonb"`
	// LocalAgents 是 AI 员工启用的本机 Agent 名称。
	LocalAgents []string `bun:"local_agents,type:jsonb"`
	// Personal 表示 AI 员工仅服务负责人本人。
	Personal bool `bun:"personal"`
}

// NewExecuteAction 创建 Agent Worker Action，联网搜索、网页读取与业务系统调用使用默认客户端；emailSender 为空表示部署未配置邮件发送；
// sharedFiles 为运行提供所属会话的共享文件区，documents 把文档转换为只读文本，sharedFiles 为空时文件工具只操作电脑。
func NewExecuteAction(db *bun.DB, enqueuer servertask.TxEnqueuer, runtime agentruntime.Runtime, invoker *modelcall.Invoker, attachments *AttachmentReader, knowledge KnowledgeRetrieval,
	emailSender customernotify.Sender, sharedFiles *conversationfile.Store, documents DocumentConverter) *ExecuteAction {
	return &ExecuteAction{
		db: db, enqueuer: enqueuer, runtime: runtime, invoker: invoker, attachments: attachments, knowledge: knowledge,
		webSearch: websearch.NewClient(), webFetch: webfetch.NewClient(common.WebFetchUserAgent()), connector: businesssystem.NewDefaultConnector(),
		emailSender: emailSender, sharedFiles: sharedFiles, documents: documents,
		runningRuns: make(map[string]*runningAgentRun),
	}
}

// runAssignment 表示一次已认领运行的执行指派：有效配置、模型、按有效配置取用的业务依赖与本次执行的取消与流式句柄。
type runAssignment struct {
	Execution    executionContext
	Policy       agentRunPolicy
	Assignment   agentruntime.Assignment
	Models       llm.ModelFactory
	Dependencies agentruntime.Dependencies
	Running      *runningAgentRun
	RunCtx       context.Context
	Release      func() // 结束本次执行的输入状态、取消注册与运行 context。
}

// Execute 取得执行指派、运行 TurnLoop 并收尾，只保存吸收完当前输入后的稳定回复。
func (a *ExecuteAction) Execute(ctx context.Context, input RunInput) error {
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
	running, unregister, err := a.registerRunContext(ctx, execution.Run.WorkspaceID, execution.Run.ID, cancel)
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
		slog.WarnContext(ctx, "Agent 任务重新计算", "agent_run_id", execution.Run.ID, "attempt", running.attempt, "stream_id", running.streamID)
	}
	// 关联客户会话的执行范围以客服周期为锚点检索同一客户的历史沟通。
	var history *customerHistoryScope
	if historyPolicy, ok := policy.(customerHistoryPolicy); ok {
		sessionID, err := historyPolicy.historyServiceSession(ctx, a.db, &execution.Run)
		if err != nil {
			return assigned, err
		}
		if sessionID != "" {
			history = &customerHistoryScope{serviceSessionID: sessionID}
		}
	}
	audience, err := loadRunAudience(ctx, a.db, &execution.Run)
	if err != nil {
		return assigned, err
	}
	values, err := loadRunValues(ctx, a.db, &execution.Run)
	if err != nil {
		return assigned, err
	}
	// 业务系统调用会话的生命周期跟随运行 context。
	loaded, err := a.loadDependencies(runCtx, execution, dependencySources{
		scope:   runScope(&execution.Run),
		mount:   businesssystem.MountOptions{Grants: execution.BusinessSystems, Values: values, Interventions: audience.interventions},
		history: history, audience: audience, conversation: true,
	})
	assigned.Dependencies = loaded.Dependencies
	if err != nil {
		return assigned, err
	}
	// 首次执行按当前依赖解析有效配置并写入快照；重复执行尝试沿用快照，运行按快照的工具清单取用依赖。
	if assigned.Assignment, err = a.resolveAssignment(ctx, execution, policy, loaded.capabilities()); err != nil {
		return assigned, err
	}
	if assigned.Models, err = a.runModels(ctx, &execution.Run, execution.ModelID); err != nil {
		return assigned, err
	}
	return assigned, nil
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
		RunID:        execution.Run.ID,
		Assignment:   assigned.Assignment,
		Models:       assigned.Models,
		Dependencies: assigned.Dependencies,
		ReadAttachment: func(ctx context.Context, messageID string) ([]byte, error) {
			return a.attachments.Content(ctx, &execution.Run, messageID)
		},
		StreamID: running.streamID,
		OnStream: func(delta stream.Delta) {
			// 运行 context 已取消时丢弃增量。
			if assigned.RunCtx.Err() == nil {
				running.stream.Publish(delta)
			}
		},
		Journal: &runJournal{db: a.db, enqueuer: a.enqueuer, policy: assigned.Policy, run: &execution.Run},
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
	// 本次执行尝试已失去运行时不收尾，由持有运行的尝试继续。
	if errors.Is(runErr, servertask.ErrExecutionLost) {
		return runErr
	}
	if runErr == nil && result.Suspended {
		if err := a.suspend(ctx, &execution.Run); err != nil {
			return err
		}
		slog.InfoContext(ctx, "Agent 运行挂起等待外部结果", "agent_run_id", execution.Run.ID)
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
		locked, err := lockAgentRun(ctx, tx, policy, initial, true)
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
			Set("task_attempt = ?", taskExecution.Attempt).
			Set("task_instance_id = NULLIF(?, '')::uuid", taskExecution.InstanceID).
			Set("started_at = COALESCE(started_at, now())").WherePK().Exec(ctx); err != nil {
			return err
		}
		// 运行期间以 AI 员工的聊天主体发布输入状态。
		if _, err := chatstate.EnsureSubject(ctx, tx, run.WorkspaceID, domain.ChatSubjectKindWorkspaceIdentity, run.AgentIdentityID, uuid.NewV7().String()); err != nil {
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
		ColumnExpr("ar.configuration->'businessSystems' AS business_systems").
		ColumnExpr("? = ANY(a.service_audiences) AS handles_customers, o.name AS workspace_name", domain.ServiceAudienceCustomer).
		ColumnExpr("a.id::text AS agent_id, a.computer_id, a.computer_grant, a.local_agents, ? = ANY(a.service_audiences) AS personal", domain.ServiceAudiencePersonal).
		Join("JOIN agents AS a ON a.identity_id = agr.agent_identity_id AND a.workspace_id = agr.workspace_id").
		Join("JOIN workspaces AS o ON o.id = agr.workspace_id").
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

// checkExecution 校验已锁定的运行没有被同一任务更新的尝试接手：运行登记的是当前任务且尝试序号更大，或执行期间尝试序号相同而执行实例不同时返回 servertask.ErrExecutionLost；
// claim 为真表示本次尝试开始认领运行，停机后按原尝试序号重新投递的任务由新实例接手；直接调用与最终失败收尾不校验，登记其他任务的运行由 ownedByOtherTask 判断。
func checkExecution(ctx context.Context, run *servermodels.AgentRun, claim bool) error {
	execution, ok := servertask.CurrentExecution(ctx)
	if !ok || execution.Finalizing || run.TaskRunID == nil || *run.TaskRunID != execution.TaskRunID {
		return nil
	}
	if run.TaskAttempt > execution.Attempt ||
		(!claim && run.TaskAttempt == execution.Attempt && run.TaskInstanceID != nil && *run.TaskInstanceID != execution.InstanceID) {
		return servertask.ErrExecutionLost
	}
	return nil
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
	return runPolicy(ctx, a.db, a.enqueuer, run)
}

// runPolicy 根据执行范围类型和会话形态选择运行策略。
func runPolicy(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, run *servermodels.AgentRun) (agentRunPolicy, error) {
	switch domain.AgentExecutionScopeKind(run.ScopeKind) {
	case domain.AgentExecutionScopeServiceSession:
		return customerRunPolicy{enqueuer: enqueuer}, nil
	case domain.AgentExecutionScopeConversation:
		var conversationType string
		if err := db.NewSelect().Model((*servermodels.Conversation)(nil)).
			Column("type").
			Where("cv.workspace_id = ? AND cv.id = ?", run.WorkspaceID, run.ConversationID).
			Scan(ctx, &conversationType); err != nil {
			return nil, fmt.Errorf("load agent run conversation type: %w", err)
		}
		switch domain.ConversationType(conversationType) {
		case domain.ConversationTypeGroup:
			return groupMentionRunPolicy{scheduler: NewScheduler(enqueuer)}, nil
		case domain.ConversationTypeCopilot:
			return copilotRunPolicy{enqueuer: enqueuer}, nil
		default:
			return agentChatRunPolicy{enqueuer: enqueuer}, nil
		}
	default:
		return nil, fmt.Errorf("unsupported agent execution scope %q", run.ScopeKind)
	}
}
