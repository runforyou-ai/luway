//go:build server

package agentrun

import (
	"context"
	"errors"
	"fmt"

	"github.com/runforyou-ai/einorun/llm"
	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// withManagedAgentConfiguration 为已关联 agents AS a 的查询补充指定配置版本的模型和系统指令列，只保留有效的托管对话模型配置。
func withManagedAgentConfiguration(query *bun.SelectQuery, revisionIDColumn string) *bun.SelectQuery {
	return agentConfigurationColumns(joinAgentConfiguration(query, revisionIDColumn))
}

// runScope 返回 AI 员工为该运行发起的模型调用归属。
func runScope(run *servermodels.AgentRun) modelcall.Scope {
	return modelcall.AgentScope(run.WorkspaceID, run.AgentIdentityID, domain.AIModelCallSourceAgentRun, run.ID)
}

// runModels 按运行锁定配置版本的模型编号解析对话模型，返回把每次模型请求记为该运行调用的模型组件工厂；模型已不可用时返回永久错误。
func (a *ExecuteAction) runModels(ctx context.Context, run *servermodels.AgentRun, modelID string) (llm.ModelFactory, error) {
	model, err := aimodel.Resolve(ctx, a.db, run.WorkspaceID, modelID, domain.AIModelUsageAgent)
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
	model, err := aimodel.Resolve(ctx, db, scope.WorkspaceID, modelID, domain.AIModelUsageAgent)
	if err != nil {
		return agentruntime.ModelConfig{}, err
	}
	return invoker.ModelConfig(scope, model), nil
}

// joinAgentConfiguration 为已关联 agents AS a 的查询关联身份、指定配置版本与对话模型，保留有效的托管对话模型配置。
func joinAgentConfiguration(query *bun.SelectQuery, revisionIDColumn string) *bun.SelectQuery {
	query = query.
		Join("JOIN workspace_identities AS oi ON oi.id = a.identity_id AND oi.workspace_id = a.workspace_id").
		Join("JOIN agent_revisions AS ar ON ar.id = " + revisionIDColumn + " AND ar.agent_id = a.id AND ar.workspace_id = a.workspace_id").
		Where("ar.schema_version = 1")
	query = aimodel.Join(query, "ar.model_id", "a.workspace_id", domain.AIModelUsageAgent)
	return query.Where("ar.execution_mode = ? AND aim.id IS NOT NULL", domain.AgentExecutionModeManaged)
}
