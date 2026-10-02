//go:build server

package agentrun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/servicecategory"
	"github.com/runforyou-ai/luway/internal/actions/servicesummary"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/uptrace/bun"
)

// ErrReplayConfigurationUnavailable 表示回放指定的配置版本不是可用的托管对话模型配置。
var ErrReplayConfigurationUnavailable = errors.New("agent revision is not an available managed configuration")

// ReplayInput 定义一次离线回放：用 AI 员工的指定配置版本处理给定的服务场景上下文。
type ReplayInput struct {
	ReplayID       string // 回放编号，用于日志与运行流标识。
	OrganizationID string
	AgentID        string
	RevisionID     string
	Audience       domain.ServiceAudience
	Customer       *ServiceSessionCustomer // 提问客户的已验证身份，为空时按客户查询的服务不挂载。
	History        *ReplayHistory          // 非空时提供客户历史检索。
	Messages       []agentruntime.Message
}

// ReplayHistory 限定回放中的客户历史检索：以来源周期的发起人为准，只检索在 ClosedBefore 之前关闭的其他周期。
type ReplayHistory struct {
	ServiceSessionID string
	ClosedBefore     time.Time
}

// Replay 以与服务周期运行相同的有效配置、依据检查、知识库检索、查询类 MCP 工具与终止工具处理一次输入，不写入会话、消息、服务周期和运行记录；运行出错时一并返回已产生的内容块与用量。
func (a *ExecuteAction) Replay(ctx context.Context, input ReplayInput) (agentruntime.RunResult, error) {
	execution := executionContext{}
	err := a.db.NewSelect().
		TableExpr("agents AS a").
		// 以子查询提供指定的配置版本编号，供配置关联使用。
		Join("CROSS JOIN (SELECT ?::uuid AS id) AS replay_revision", input.RevisionID).
		ColumnExpr("oi.display_name AS agent_name").
		ColumnExpr("aim.input_modalities").
		ColumnExpr("ar.configuration->'knowledgeBaseIds' AS knowledge_base_ids").
		ColumnExpr("aim.id::text AS model_id, ? = ANY(a.service_audiences) AS handles_customers, o.name AS organization_name", domain.ServiceAudienceCustomer).
		Join("JOIN organizations AS o ON o.id = a.organization_id").
		Apply(func(query *bun.SelectQuery) *bun.SelectQuery {
			return withManagedAgentConfiguration(query, "replay_revision.id")
		}).
		Where("a.organization_id = ? AND a.id = ?", input.OrganizationID, input.AgentID).
		Scan(ctx, &execution)
	if errors.Is(err, sql.ErrNoRows) {
		return agentruntime.RunResult{}, ErrReplayConfigurationUnavailable
	}
	if err != nil {
		return agentruntime.RunResult{}, fmt.Errorf("load replay configuration: %w", err)
	}
	execution.Run.ID, execution.Run.OrganizationID = input.ReplayID, input.OrganizationID
	scene := agentruntime.SceneEmployeeService
	mount := mcpMount{Service: true}
	if input.Audience == domain.ServiceAudienceCustomer {
		scene = agentruntime.SceneCustomer
		// 提问客户未验证身份时，按客户查询的服务与未验证客户的渠道会话一样不挂载。
		mount.Customer = func(context.Context) (ServiceSessionCustomer, error) {
			if input.Customer == nil {
				return ServiceSessionCustomer{}, nil
			}
			return *input.Customer, nil
		}
	}
	categories, err := servicecategory.Active(ctx, a.db, input.OrganizationID)
	if err != nil {
		return agentruntime.RunResult{}, err
	}
	handoffCategories := make([]agentruntime.HandoffCategory, 0, len(categories))
	for _, category := range categories {
		handoffCategories = append(handoffCategories, agentruntime.HandoffCategory{ID: category.ID, Name: category.Name, Description: category.Description})
	}
	knowledge, err := loadKnowledgeSearch(ctx, a.db, a.knowledge, input.OrganizationID, execution.KnowledgeBaseIDs)
	if err != nil {
		return agentruntime.RunResult{}, fmt.Errorf("load replay knowledge bases: %w", err)
	}
	mcpServers, err := loadMCPServers(ctx, a.db, input.OrganizationID, input.RevisionID, mount)
	if err != nil {
		return agentruntime.RunResult{}, fmt.Errorf("load replay mcp servers: %w", err)
	}
	serverNames := make([]string, 0, len(mcpServers.Servers))
	for _, server := range mcpServers.Servers {
		serverNames = append(serverNames, server.Name)
	}
	assignment := agentruntime.ResolveAssignment(
		execution.assignmentFacts(agentruntime.SceneContext{Scene: scene, HandoffCategories: handoffCategories}),
		agentruntime.Capabilities{Knowledge: knowledge != nil, MCPServers: serverNames, CustomerLoginRequired: mcpServers.CustomerLoginRequired, CustomerHistory: input.History != nil},
	)
	var history agentruntime.CustomerHistorySearch
	if input.History != nil {
		scope := *input.History
		history = func(ctx context.Context, query string) (agentruntime.CustomerHistoryResult, error) {
			return servicesummary.SearchHistory(ctx, a.db, input.OrganizationID, scope.ServiceSessionID, &scope.ClosedBefore, query)
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, agentRunTimeout)
	defer cancel()
	result, err := a.runtime.Run(runCtx, agentruntime.RunRequest{
		RunID:                 input.ReplayID,
		Assignment:            assignment,
		Credentials:           agentruntime.ModelCredentials{APIKey: execution.APIKey, BaseURL: execution.APIURL},
		KnowledgeSearch:       knowledge,
		CustomerHistorySearch: history,
		MCPConnections:        mcpServers.Servers,
		Attempt:               1,
	}, &replayFeed{messages: input.Messages})
	if err != nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) && !errors.Is(err, context.DeadlineExceeded) {
		err = fmt.Errorf("%w: %w", err, context.DeadlineExceeded)
	}
	return result, err
}

// replayFeed 以一次性输入提供回放上下文：序号 1 是唯一的输入，认领后不再有后续输入。
type replayFeed struct {
	messages []agentruntime.Message
}

// Peek 在尚未越过唯一输入时返回它的信号。
func (f *replayFeed) Peek(_ context.Context, afterSeq int64) ([]agentruntime.Trigger, error) {
	if afterSeq >= 1 {
		return nil, nil
	}
	return []agentruntime.Trigger{{Seq: 1}}, nil
}

// Claim 返回回放的全部上下文消息。
func (f *replayFeed) Claim(context.Context, int64) (agentruntime.ClaimedInput, error) {
	return agentruntime.ClaimedInput{Messages: f.messages, EndSeq: 1}, nil
}
