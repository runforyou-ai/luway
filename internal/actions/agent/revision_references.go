//go:build server

package agent

import (
	"context"
	"uuid"

	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

const (
	revisionMCPServerIDs     = "mcpServerIds"
	revisionKnowledgeBaseIDs = "knowledgeBaseIds"
)

// RemoveMCPServerFromRevisions 在已锁定服务的事务内为引用该服务的 AI 员工创建移除该服务的新版本，保留历史配置。
func RemoveMCPServerFromRevisions(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, mcpServerID string) (int, error) {
	return removeRevisionReference(ctx, tx, identity, revisionMCPServerIDs, mcpServerID, false)
}

// RemoveMCPServerFromPersonalAgents 在已锁定服务的事务内为引用该服务的个人 AI 员工创建移除该服务的新版本，用于服务改为按客户查询时。
func RemoveMCPServerFromPersonalAgents(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, mcpServerID string) (int, error) {
	return removeRevisionReference(ctx, tx, identity, revisionMCPServerIDs, mcpServerID, true)
}

// RemoveKnowledgeBaseFromRevisions 在已锁定知识库的事务内为引用该知识库的 AI 员工创建移除该知识库的新版本，保留历史配置。
func RemoveKnowledgeBaseFromRevisions(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, knowledgeBaseID string) (int, error) {
	return removeRevisionReference(ctx, tx, identity, revisionKnowledgeBaseIDs, knowledgeBaseID, false)
}

// removeRevisionReference 为当前版本的 key 数组引用 referenceID 的 AI 员工创建移除该引用的新版本，personalOnly 为 true 时只处理个人 AI 员工。
func removeRevisionReference(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, key, referenceID string, personalOnly bool) (int, error) {
	// 被引用记录的锁阻止新增引用，先确定候选员工，再按固定顺序锁定。
	revisions := tx.NewSelect().Model((*servermodels.AgentRevision)(nil)).Column("id").
		Where("organization_id = ?", identity.Organization.ID).
		Where("configuration->? @> jsonb_build_array(?::text)", key, referenceID)
	candidates := tx.NewSelect().Model((*servermodels.Agent)(nil)).Column("id").
		Where("a.organization_id = ?", identity.Organization.ID).
		Where("a.active_revision_id IN (?)", revisions)
	if personalOnly {
		candidates = candidates.Where(personalAgentCondition)
	}
	var agentIDs []string
	if err := candidates.Scan(ctx, &agentIDs); err != nil {
		return 0, err
	}
	if len(agentIDs) == 0 {
		return 0, nil
	}
	// 按员工编号锁定候选员工。
	if err := tx.NewSelect().Model((*servermodels.Agent)(nil)).Column("id").
		Where("a.organization_id = ?", identity.Organization.ID).Where("a.id IN (?)", bun.In(agentIDs)).
		OrderExpr("a.id ASC").For("UPDATE").Scan(ctx, &agentIDs); err != nil {
		return 0, err
	}
	// 等待员工锁期间可能切换了版本，取锁后重新读取并只移除该引用。
	var rewritten []*servermodels.AgentRevision
	if err := tx.NewSelect().Model(&rewritten).
		Column("organization_id", "agent_id", "execution_mode", "schema_version").
		ColumnExpr("jsonb_set(ar.configuration, ARRAY[?::text], (ar.configuration->?) - ?::text) AS configuration", key, key, referenceID).
		Join("JOIN agents AS a ON a.active_revision_id = ar.id AND a.organization_id = ar.organization_id AND a.id = ar.agent_id").
		Where("a.organization_id = ?", identity.Organization.ID).Where("a.id IN (?)", bun.In(agentIDs)).
		Where("ar.configuration->? @> jsonb_build_array(?::text)", key, referenceID).Scan(ctx); err != nil {
		return 0, err
	}
	if len(rewritten) == 0 {
		return 0, nil
	}
	revisionAgentIDs := make([]string, len(rewritten))
	revisionIDs := make([]string, len(rewritten))
	for index, revision := range rewritten {
		revision.ID = uuid.NewV7().String()
		revision.CreatedByUserID = identity.User.ID
		revisionAgentIDs[index], revisionIDs[index] = revision.AgentID, revision.ID
	}
	if _, err := tx.NewInsert().Model(&rewritten).
		Column("id", "organization_id", "agent_id", "execution_mode", "schema_version", "configuration", "created_by_user_id").Exec(ctx); err != nil {
		return 0, err
	}
	if _, err := tx.NewUpdate().Model((*servermodels.Agent)(nil)).
		TableExpr("unnest(?::uuid[], ?::uuid[]) AS revision(agent_id, id)", pgdialect.Array(revisionAgentIDs), pgdialect.Array(revisionIDs)).
		Set("active_revision_id = revision.id").Set("updated_at = now()").
		Where("a.organization_id = ? AND a.id = revision.agent_id", identity.Organization.ID).Exec(ctx); err != nil {
		return 0, err
	}
	return len(rewritten), nil
}
