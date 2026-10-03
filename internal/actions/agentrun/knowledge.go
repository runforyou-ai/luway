//go:build server

package agentrun

import (
	"context"

	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/knowledgeretrieval"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// KnowledgeRetrieval 按调用归属的工作区和知识库范围构造检索来源，检索中的模型调用按调用归属记录。
type KnowledgeRetrieval interface {
	Sources(ctx context.Context, scope modelcall.Scope, knowledgeBaseIDs []string) ([]knowledgeretrieval.Source, error)
}

// loadKnowledgeSearch 按配置版本绑定且仍存在的同企业知识库构造检索函数，检索中的模型调用按 scope 记录；没有可用知识库时返回 nil。
func loadKnowledgeSearch(ctx context.Context, db bun.IDB, retrieval KnowledgeRetrieval, scope modelcall.Scope, knowledgeBaseIDs []string) (agentruntime.KnowledgeSearch, error) {
	if len(knowledgeBaseIDs) == 0 {
		return nil, nil
	}
	organizationID := scope.OrganizationID
	var ids []string
	err := db.NewSelect().Model((*servermodels.KnowledgeBase)(nil)).Column("kb.id").
		Where("kb.organization_id = ? AND kb.id IN (?)", organizationID, bun.In(knowledgeBaseIDs)).
		Order("kb.name").Scan(ctx, &ids)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return func(ctx context.Context, request knowledgeretrieval.Request) (knowledgeretrieval.Result, error) {
		sources, err := retrieval.Sources(ctx, scope, ids)
		if err != nil {
			return knowledgeretrieval.Result{}, err
		}
		return knowledgeretrieval.Search(ctx, sources, request)
	}, nil
}
