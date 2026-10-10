//go:build server

package agentrun

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// BehaviorProfile 返回 AI 员工按当前接待开关、工作区电脑授权与是否启用本机 Agent 适用的内置工作规则与可用工具，供管理界面只读展示；computerGrant 为空表示不使用工作区电脑。
func BehaviorProfile(handlesCustomers bool, computerGrant *domain.ToolGrant, localAgents bool, workspaceName string) (string, []string) {
	tools := []string{agentruntime.KnowledgeToolName, agentruntime.WebSearchToolName, agentruntime.WebFetchToolName, agentruntime.BusinessSystemToolCategory}
	if handlesCustomers {
		tools = []string{agentruntime.KnowledgeToolName, agentruntime.CustomerHistoryToolName, "ask_customer", "handoff_to_human", "resolve_conversation", agentruntime.BusinessSystemToolCategory}
	}
	if computerGrant != nil {
		tools = append(tools, agentruntime.GrantedComputerTools(computerGrant, localAgents)...)
	}
	return agentruntime.AgentBaseline(handlesCustomers, workspaceName, ""), tools
}

// assignmentFacts 汇总解析有效配置所需的业务事实。
func (e executionContext) assignmentFacts(scene agentruntime.SceneContext) agentruntime.AssignmentFacts {
	return agentruntime.AssignmentFacts{
		HandlesCustomers: e.HandlesCustomers,
		WorkspaceName:    e.WorkspaceName,
		AgentName:        e.AgentName,
		Instruction:      e.Instruction,
		Model: agentruntime.AssignmentModel{
			ModelID:         e.ModelID,
			MaxOutputTokens: e.MaxOutputTokens,
			ContextWindow:   e.ContextWindow,
			InputModalities: e.InputModalities,
		},
		Scene: scene,
	}
}

// resolveAssignment 首次执行时按业务事实与执行侧能力解析有效配置并固定为运行快照；重复执行尝试直接沿用已写入的快照，
// 运行按快照的工具清单注册工具，快照列出而当前依赖不可用的工具仍然注册，调用时返回不可用。
func (a *ExecuteAction) resolveAssignment(ctx context.Context, execution executionContext, policy agentRunPolicy, capabilities agentruntime.Capabilities) (agentruntime.Assignment, error) {
	assignment := agentruntime.Assignment{}
	if len(execution.Run.BehaviorSnapshot) > 0 {
		if err := json.Unmarshal(execution.Run.BehaviorSnapshot, &assignment); err != nil {
			return agentruntime.Assignment{}, fmt.Errorf("decode agent run assignment: %w", err)
		}
		return assignment, nil
	}
	scene, err := policy.sceneContext(ctx, a.db, execution)
	if err != nil {
		return agentruntime.Assignment{}, fmt.Errorf("load agent run scene context: %w", err)
	}
	assignment = agentruntime.ResolveAssignment(execution.assignmentFacts(scene), capabilities)
	encoded, err := json.Marshal(assignment)
	if err != nil {
		return agentruntime.Assignment{}, fmt.Errorf("encode agent run assignment: %w", err)
	}
	result, err := a.db.NewUpdate().Model((*servermodels.AgentRun)(nil)).
		Set("behavior_snapshot = ?::jsonb", string(encoded)).
		Where("agr.id = ?", execution.Run.ID).
		Where("agr.behavior_snapshot IS NULL").
		Exec(ctx)
	if err != nil {
		return agentruntime.Assignment{}, fmt.Errorf("persist agent run assignment: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected > 0 {
		return assignment, nil
	}
	// 并发的执行尝试已先写入快照，沿用那一份。
	var persisted json.RawMessage
	if err := a.db.NewSelect().Model((*servermodels.AgentRun)(nil)).
		Column("behavior_snapshot").Where("agr.id = ?", execution.Run.ID).Scan(ctx, &persisted); err != nil {
		return agentruntime.Assignment{}, fmt.Errorf("reload agent run assignment: %w", err)
	}
	if err := json.Unmarshal(persisted, &assignment); err != nil {
		return agentruntime.Assignment{}, fmt.Errorf("decode persisted agent run assignment: %w", err)
	}
	return assignment, nil
}
