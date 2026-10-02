//go:build server

package agentrun

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// BehaviorProfile 返回 AI 员工按当前接待开关适用的内置工作规则与可用工具，供管理界面只读展示。
func BehaviorProfile(handlesCustomers bool, organizationName string) (string, []string) {
	if handlesCustomers {
		return agentruntime.AgentBaseline(handlesCustomers, organizationName, ""), []string{"search_knowledge", agentruntime.CustomerHistoryToolName, "ask_customer", "handoff_to_human", "resolve_conversation", "mcp"}
	}
	return agentruntime.AgentBaseline(handlesCustomers, organizationName, ""), []string{"search_knowledge", agentruntime.WebSearchToolName, agentruntime.WebFetchToolName, "mcp"}
}

// assignmentFacts 汇总解析有效配置所需的业务事实。
func (e executionContext) assignmentFacts(scene agentruntime.SceneContext) agentruntime.AssignmentFacts {
	return agentruntime.AssignmentFacts{
		HandlesCustomers: e.HandlesCustomers,
		OrganizationName: e.OrganizationName,
		AgentName:        e.AgentName,
		Instruction:      e.Instruction,
		Model: agentruntime.AssignmentModel{
			ModelID:         e.ModelID,
			Brand:           e.Brand,
			Identifier:      e.ModelIdentifier,
			MaxOutputTokens: e.MaxOutputTokens,
			ContextWindow:   e.ContextWindow,
			InputModalities: e.InputModalities,
		},
		LocalAgent: e.LocalAgentKind,
		Scene:      scene,
	}
}

// resolveAssignment 首次执行时按业务事实与执行侧能力解析有效配置并固定为运行快照；重复执行尝试直接沿用已写入的快照，不再查询场景事实。
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
		Set("updated_at = now()").
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
