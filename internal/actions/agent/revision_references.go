//go:build server

package agent

import (
	"context"
	"fmt"
	"slices"
	"uuid"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// revisionReference 定义配置版本中一类引用所在的引用表、引用列与从配置快照中移除该引用的方式。
// 配置快照按 managedRevisionConfigurationV1 严格解码，不接受未知字段；该结构新增字段时须同时提升 executionSchemaVersion。
type revisionReference struct {
	table  string
	column string
	remove func(configuration *managedRevisionConfigurationV1, referenceID string)
}

var (
	// knowledgeBaseReference 是配置版本绑定的知识库。
	knowledgeBaseReference = revisionReference{
		table: "agent_revision_knowledge_bases", column: "knowledge_base_id",
		remove: func(configuration *managedRevisionConfigurationV1, referenceID string) {
			configuration.KnowledgeBaseIDs = slices.DeleteFunc(configuration.KnowledgeBaseIDs, func(id string) bool { return id == referenceID })
		},
	}
	// businessSystemReference 是配置版本授权的业务系统，移除后其余授权保持原顺序。
	businessSystemReference = revisionReference{
		table: "agent_revision_business_systems", column: "business_system_id",
		remove: func(configuration *managedRevisionConfigurationV1, referenceID string) {
			configuration.BusinessSystems = slices.DeleteFunc(configuration.BusinessSystems, func(grant domain.BusinessSystemGrant) bool {
				return grant.BusinessSystemID == referenceID
			})
		},
	}
)

// RemoveBusinessSystemFromRevisions 在已锁定业务系统的事务内为授权了该业务系统的 AI 员工创建移除该授权的新版本，保留历史配置。
func RemoveBusinessSystemFromRevisions(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, businessSystemID string) (int, error) {
	return removeRevisionReference(ctx, tx, identity, businessSystemReference, businessSystemID)
}

// RemoveKnowledgeBaseFromRevisions 在已锁定知识库的事务内为引用该知识库的 AI 员工创建移除该知识库的新版本，保留历史配置。
func RemoveKnowledgeBaseFromRevisions(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, knowledgeBaseID string) (int, error) {
	return removeRevisionReference(ctx, tx, identity, knowledgeBaseReference, knowledgeBaseID)
}

// removeRevisionReference 为当前版本引用 referenceID 的 AI 员工创建移除该引用的新版本。
func removeRevisionReference(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, reference revisionReference, referenceID string) (int, error) {
	// 当前版本引用该记录的员工，引用表以 r 为别名。
	referencing := func(query *bun.SelectQuery) *bun.SelectQuery {
		return query.Join("JOIN ? AS r ON r.workspace_id = a.workspace_id AND r.agent_id = a.id AND r.revision_id = a.active_revision_id", bun.Ident(reference.table)).
			Where("a.workspace_id = ?", identity.Workspace.ID).
			Where("r.? = ?", bun.Ident(reference.column), referenceID)
	}
	// 被引用记录的锁阻止新增引用，先确定候选员工，再按固定顺序锁定。
	var agentIDs []string
	if err := referencing(tx.NewSelect().Model((*servermodels.Agent)(nil)).Column("a.id")).Scan(ctx, &agentIDs); err != nil {
		return 0, err
	}
	if len(agentIDs) == 0 {
		return 0, nil
	}
	if err := tx.NewSelect().Model((*servermodels.Agent)(nil)).Column("id").
		Where("a.workspace_id = ?", identity.Workspace.ID).Where("a.id IN (?)", bun.List(agentIDs)).
		OrderExpr("a.id ASC").For("UPDATE").Scan(ctx, &agentIDs); err != nil {
		return 0, err
	}
	// 等待员工锁期间可能切换了版本，取锁后重新读取仍引用该记录的当前版本。
	var current []servermodels.AgentRevision
	if err := referencing(tx.NewSelect().Model((*servermodels.Agent)(nil)).
		ColumnExpr("ar.id, ar.agent_id, ar.execution_mode, ar.model_id, ar.schema_version, ar.configuration").
		Join("JOIN agent_revisions AS ar ON ar.id = a.active_revision_id AND ar.workspace_id = a.workspace_id AND ar.agent_id = a.id")).
		Where("a.id IN (?)", bun.List(agentIDs)).OrderExpr("a.id ASC").Scan(ctx, &current); err != nil {
		return 0, err
	}
	if len(current) == 0 {
		return 0, nil
	}
	revisionAgentIDs := make([]string, len(current))
	revisionIDs := make([]string, len(current))
	for index, revision := range current {
		configuration, err := decodeRevisionConfiguration(revision)
		if err != nil {
			return 0, fmt.Errorf("decode agent %q revision: %w", revision.AgentID, err)
		}
		reference.remove(&configuration, referenceID)
		revisionID := uuid.NewV7().String()
		if err := insertRevision(ctx, tx, identity, revision.AgentID, revisionID, domain.AgentExecutionMode(revision.ExecutionMode), revision.ModelID, configuration); err != nil {
			return 0, err
		}
		revisionAgentIDs[index], revisionIDs[index] = revision.AgentID, revisionID
	}
	if _, err := tx.NewUpdate().Model((*servermodels.Agent)(nil)).
		TableExpr("unnest(?::uuid[], ?::uuid[]) AS revision(agent_id, id)", pgdialect.Array(revisionAgentIDs), pgdialect.Array(revisionIDs)).
		Set("active_revision_id = revision.id").
		Where("a.workspace_id = ? AND a.id = revision.agent_id", identity.Workspace.ID).Exec(ctx); err != nil {
		return 0, err
	}
	return len(current), nil
}
