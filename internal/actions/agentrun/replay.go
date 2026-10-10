//go:build server

package agentrun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/actions/businesssystem"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/actions/servicecategory"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// ErrReplayConfigurationUnavailable 表示回放指定的配置版本不是可用的托管对话模型配置。
var ErrReplayConfigurationUnavailable = errors.New("agent revision is not an available managed configuration")

// ReplayInput 定义一次离线回放：用 AI 员工的指定配置版本处理给定的服务场景上下文。
type ReplayInput struct {
	ReplayID    string // 回放编号，用于日志与运行流标识。
	WorkspaceID string
	AgentID     string
	RevisionID  string
	Audience    domain.ServiceAudience
	Customer    *ServiceSessionCustomer // 提问客户的已验证身份，为空时绑定客户身份的业务系统工具不挂载。
	Member      *ServiceMember          // 提问员工对应的成员，为空时绑定成员身份的业务系统工具不挂载。
	History     *ReplayHistory          // 非空时提供客户历史检索。
	Messages    []agentcontract.Message
}

// ReplayHistory 限定回放中的客户历史检索：以来源周期的发起人为准，只检索在 ClosedBefore 之前关闭的其他周期。
type ReplayHistory struct {
	ServiceSessionID string
	ClosedBefore     time.Time
}

// Replay 以与服务周期运行相同的有效配置、依据检查、知识库检索、只读业务系统工具与终止工具处理一次输入，不写入会话、消息、服务周期和运行记录；运行出错时一并返回已产生的内容块与用量。
func (a *ExecuteAction) Replay(ctx context.Context, input ReplayInput) (agentruntime.RunResult, error) {
	execution := executionContext{}
	err := a.db.NewSelect().
		TableExpr("agents AS a").
		// 以子查询提供指定的配置版本编号，供配置关联使用。
		Join("CROSS JOIN (SELECT ?::uuid AS id) AS replay_revision", input.RevisionID).
		ColumnExpr("oi.display_name AS agent_name").
		ColumnExpr("aim.input_modalities").
		ColumnExpr("ar.configuration->'knowledgeBaseIds' AS knowledge_base_ids").
		ColumnExpr("ar.configuration->'businessSystems' AS business_systems").
		ColumnExpr("? = ANY(a.service_audiences) AS handles_customers, o.name AS workspace_name", domain.ServiceAudienceCustomer).
		Join("JOIN workspaces AS o ON o.id = a.workspace_id").
		Apply(func(query *bun.SelectQuery) *bun.SelectQuery {
			return withManagedAgentConfiguration(query, "replay_revision.id")
		}).
		Where("a.workspace_id = ? AND a.id = ?", input.WorkspaceID, input.AgentID).
		Scan(ctx, &execution)
	if errors.Is(err, sql.ErrNoRows) {
		return agentruntime.RunResult{}, ErrReplayConfigurationUnavailable
	}
	if err != nil {
		return agentruntime.RunResult{}, fmt.Errorf("load replay configuration: %w", err)
	}
	execution.Run.ID, execution.Run.WorkspaceID = input.ReplayID, input.WorkspaceID
	// 回放只挂载只读业务系统工具；按受众提供提问客户或提问员工的身份，回放没有真实会话，不提供会话编号。
	scene := agentruntime.SceneEmployeeService
	mount := businesssystem.MountOptions{Grants: execution.BusinessSystems, Values: businesssystem.RunValues{}, ReadOnly: true}
	if input.Audience == domain.ServiceAudienceCustomer {
		scene = agentruntime.SceneCustomer
		if input.Customer != nil {
			input.Customer.addValues(mount.Values)
		}
	} else if input.Member != nil {
		input.Member.addValues(mount.Values)
	}
	categories, err := servicecategory.Active(ctx, a.db, input.WorkspaceID)
	if err != nil {
		return agentruntime.RunResult{}, err
	}
	handoffCategories := arr.Map(categories, func(category servermodels.ServiceCategory) agentruntime.HandoffCategory {
		return agentruntime.HandoffCategory{ID: category.ID, Name: category.Name, Description: category.Description}
	})
	// 回放中的模型调用记为本次评测的后台调用；依赖与服务周期运行使用同一装配函数，只替换知识检索的调用归属、业务系统挂载方式与客户历史的检索范围。
	scope := modelcall.SystemScope(input.WorkspaceID, domain.AIModelCallSourceAgentEvaluation, input.ReplayID)
	sources := dependencySources{scope: scope, mount: mount}
	if input.History != nil {
		closedBefore := input.History.ClosedBefore
		sources.history = &customerHistoryScope{serviceSessionID: input.History.ServiceSessionID, closedBefore: &closedBefore}
	}
	runCtx, cancel := context.WithTimeout(ctx, agentRunTimeout)
	defer cancel()
	loaded, err := a.loadDependencies(runCtx, execution, sources)
	if err != nil {
		return agentruntime.RunResult{}, fmt.Errorf("load replay dependencies: %w", err)
	}
	assignment := agentruntime.ResolveAssignment(
		execution.assignmentFacts(agentruntime.SceneContext{Scene: scene, HandoffCategories: handoffCategories}), loaded.capabilities(),
	)
	model, err := aimodel.Resolve(ctx, a.db, input.WorkspaceID, execution.ModelID, domain.AIModelUsageAgent)
	if errors.Is(err, aimodel.ErrUnavailable) {
		return agentruntime.RunResult{}, ErrReplayConfigurationUnavailable
	}
	if err != nil {
		return agentruntime.RunResult{}, fmt.Errorf("resolve replay model: %w", err)
	}
	result, err := a.runtime.Run(runCtx, agentruntime.RunRequest{
		RunID: input.ReplayID, Assignment: assignment, Models: a.invoker.ChatModels(scope, model), Dependencies: loaded.Dependencies,
	}, &replayFeed{messages: input.Messages})
	if err != nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) && !errors.Is(err, context.DeadlineExceeded) {
		err = fmt.Errorf("%w: %w", err, context.DeadlineExceeded)
	}
	return result, err
}

// replayFeed 以一次性输入提供回放上下文：序号 1 是唯一的输入。
type replayFeed struct {
	messages []agentcontract.Message
}

// Watch 不产生新增输入信号，回放只有唯一的输入。
func (f *replayFeed) Watch(context.Context) (<-chan struct{}, func(), error) {
	return nil, func() {}, nil
}

// Pending 在尚未越过唯一输入时返回它的序号。
func (f *replayFeed) Pending(_ context.Context, afterSeq int64) (int64, error) {
	if afterSeq >= 1 {
		return 0, nil
	}
	return 1, nil
}

// Claim 返回回放的全部上下文消息。
func (f *replayFeed) Claim(context.Context, int64) (einorun.Claim, error) {
	if len(f.messages) == 0 {
		return einorun.Claim{}, errors.New("replay has no context messages")
	}
	return einorun.Claim{Messages: feedMessages(f.messages), EndSeq: 1}, nil
}
