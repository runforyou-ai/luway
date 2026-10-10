//go:build server

package helpcenter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/knowledgeretrieval"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// searchArticleLimit 是一次搜索返回的文章数量上限。
const searchArticleLimit = 10

// ErrQueryInvalid 表示帮助中心搜索内容为空或超出长度。
var ErrQueryInvalid = errors.New("help center query is invalid")

// Retrieval 构造只召回帮助中心文章的知识库检索来源，检索中的模型调用按调用归属记录。
type Retrieval interface {
	ArticleSources(ctx context.Context, scope modelcall.Scope, knowledgeBaseIDs []string) ([]knowledgeretrieval.Source, error)
}

// SearchQuery 在网站渠道发布的文章中检索访客输入的内容。
type SearchQuery struct {
	db        *bun.DB
	retrieval Retrieval
}

// NewSearchQuery 创建帮助中心搜索查询。
func NewSearchQuery(db *bun.DB, retrieval Retrieval) *SearchQuery {
	return &SearchQuery{db: db, retrieval: retrieval}
}

// Execute 返回按相关度排序并按文章去重的已发布文章。
func (q *SearchQuery) Execute(ctx context.Context, channelID, query string) ([]ArticleSummary, error) {
	query = strings.TrimSpace(query)
	if query == "" || utf8.RuneCountInString(query) > domain.KnowledgeRetrievalQueryMaxLength {
		return nil, ErrQueryInvalid
	}
	scope, err := loadScope(ctx, q.db, channelID)
	if err != nil {
		return nil, err
	}
	if len(scope.Bases) == 0 {
		return []ArticleSummary{}, nil
	}
	// 访客搜索中的模型调用记为该网站渠道的后台调用。
	callScope := modelcall.SystemScope(scope.WorkspaceID, domain.AIModelCallSourceChannel, channelID)
	sources, err := q.retrieval.ArticleSources(ctx, callScope, arr.Map(scope.Bases, func(base servermodels.KnowledgeBase) string { return base.ID }))
	if err != nil {
		return nil, fmt.Errorf("load help center sources: %w", err)
	}
	retrieved, err := knowledgeretrieval.Search(ctx, sources, knowledgeretrieval.Request{Queries: []string{query}})
	if err != nil {
		return nil, fmt.Errorf("search help center: %w", err)
	}
	// 按命中顺序去重文章。
	unique := arr.UniqueBy(retrieved.Records, func(record knowledgeretrieval.Record) string { return record.DocumentID })
	return arr.OrEmpty(arr.Map(unique[:min(len(unique), searchArticleLimit)], func(record knowledgeretrieval.Record) ArticleSummary {
		return ArticleSummary{ID: record.DocumentID, Title: record.DocumentName}
	})), nil
}
