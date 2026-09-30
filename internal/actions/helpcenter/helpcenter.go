//go:build server

// Package helpcenter 读取网站渠道帮助中心发布的文章合集与文章。
package helpcenter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

var (
	// ErrChannelNotFound 表示网站渠道不存在或已停用。
	ErrChannelNotFound = errors.New("help center channel not found")
	// ErrArticleNotFound 表示文章不存在或不在渠道发布范围内。
	ErrArticleNotFound = errors.New("help center article not found")
)

// publishScope 定义网站渠道帮助中心所属企业和发布的知识库。
type publishScope struct {
	OrganizationID string
	// Bases 是发布的知识库，按名称排序。
	Bases []servermodels.KnowledgeBase
}

// BaseIDs 返回发布的知识库编号。
func (s publishScope) BaseIDs() []string {
	ids := make([]string, 0, len(s.Bases))
	for _, base := range s.Bases {
		ids = append(ids, base.ID)
	}
	return ids
}

// Collection 定义一个文章合集，对应一个发布的知识库。
type Collection struct {
	ID          string
	Name        string
	Description string
	Articles    []ArticleSummary
}

// ArticleSummary 定义合集中的一篇文章。
type ArticleSummary struct {
	ID    string
	Title string
}

// Article 定义文章详情，正文为 Markdown。
type Article struct {
	ID             string
	CollectionID   string
	CollectionName string
	Title          string
	Body           string
	UpdatedAt      time.Time
}

// articleRow 定义文章查询的一行，文档知识库取在线编写的文档，问答知识库取条目的主问题与答案。
type articleRow struct {
	ID              string    `bun:"id"`
	KnowledgeBaseID string    `bun:"knowledge_base_id"`
	Title           string    `bun:"title"`
	Body            string    `bun:"body"`
	UpdatedAt       time.Time `bun:"updated_at"`
}

// loadScope 读取已启用网站渠道的帮助中心发布范围。
func loadScope(ctx context.Context, db bun.IDB, channelID string) (publishScope, error) {
	if !common.ValidUUID(channelID) {
		return publishScope{}, ErrChannelNotFound
	}
	channel := &servermodels.Channel{}
	err := db.NewSelect().
		Model(channel).
		Column("organization_id").
		Where("c.id = ? AND c.type = ? AND c.enabled = TRUE", channelID, domain.ChannelTypeWebsite).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return publishScope{}, ErrChannelNotFound
	}
	if err != nil {
		return publishScope{}, fmt.Errorf("load help center channel: %w", err)
	}
	scope := publishScope{OrganizationID: channel.OrganizationID, Bases: make([]servermodels.KnowledgeBase, 0)}
	if err := db.NewSelect().
		Model(&scope.Bases).
		Column("kb.id", "kb.name", "kb.category", "kb.description").
		Join("JOIN website_channel_knowledge_bases AS wckb ON wckb.knowledge_base_id = kb.id AND wckb.organization_id = kb.organization_id").
		Where("wckb.channel_id = ? AND wckb.organization_id = ?", channelID, channel.OrganizationID).
		OrderExpr("kb.name, kb.id").
		Scan(ctx); err != nil {
		return publishScope{}, fmt.Errorf("load help center knowledge bases: %w", err)
	}
	return scope, nil
}

// articleQueries 返回发布范围内文章的查询，文档与问答各一条，按创建顺序排列；articleID 非空时只查询该文章并读取正文。
func articleQueries(db bun.IDB, scope publishScope, articleID string) []*bun.SelectQuery {
	var documentBases, qaBases []string
	for _, base := range scope.Bases {
		if base.Category == string(domain.KnowledgeBaseCategoryQA) {
			qaBases = append(qaBases, base.ID)
		} else {
			documentBases = append(documentBases, base.ID)
		}
	}
	queries := make([]*bun.SelectQuery, 0, 2)
	if len(documentBases) > 0 {
		query := db.NewSelect().
			TableExpr("knowledge_documents AS kd").
			ColumnExpr("kd.id, kd.knowledge_base_id, kd.title, kd.updated_at").
			Where("kd.knowledge_base_id IN (?) AND kd.source_kind = ?", bun.In(documentBases), domain.KnowledgeDocumentSourceText).
			OrderExpr("kd.created_at, kd.id")
		if articleID != "" {
			query = query.ColumnExpr("kdc.content AS body").
				Join("JOIN knowledge_document_contents AS kdc ON kdc.document_id = kd.id").
				Where("kd.id = ?", articleID)
		}
		queries = append(queries, query)
	}
	if len(qaBases) > 0 {
		query := db.NewSelect().
			TableExpr("knowledge_qa_entries AS kqe").
			ColumnExpr("kqe.id, kqe.knowledge_base_id, question.content AS title, kqe.updated_at").
			Join("JOIN knowledge_qa_contents AS question ON question.entry_id = kqe.id AND question.kind = ?", domain.KnowledgeQAContentPrimaryQuestion).
			Where("kqe.knowledge_base_id IN (?)", bun.In(qaBases)).
			OrderExpr("kqe.created_at, kqe.id")
		if articleID != "" {
			query = query.ColumnExpr("answer.content AS body").
				Join("JOIN knowledge_qa_contents AS answer ON answer.entry_id = kqe.id AND answer.kind = ?", domain.KnowledgeQAContentAnswer).
				Where("kqe.id = ?", articleID)
		}
		queries = append(queries, query)
	}
	return queries
}

// GetHelpCenterQuery 读取网站渠道帮助中心的文章合集。
type GetHelpCenterQuery struct {
	db *bun.DB
}

// NewGetHelpCenterQuery 创建帮助中心合集查询。
func NewGetHelpCenterQuery(db *bun.DB) *GetHelpCenterQuery {
	return &GetHelpCenterQuery{db: db}
}

// Execute 返回按知识库名称排序的文章合集，不含文章的合集不返回。
func (q *GetHelpCenterQuery) Execute(ctx context.Context, channelID string) ([]Collection, error) {
	scope, err := loadScope(ctx, q.db, channelID)
	if err != nil {
		return nil, err
	}
	articles := make(map[string][]ArticleSummary, len(scope.Bases))
	for _, query := range articleQueries(q.db, scope, "") {
		var rows []articleRow
		if err := query.Scan(ctx, &rows); err != nil {
			return nil, fmt.Errorf("list help center articles: %w", err)
		}
		for _, row := range rows {
			articles[row.KnowledgeBaseID] = append(articles[row.KnowledgeBaseID], ArticleSummary{ID: row.ID, Title: row.Title})
		}
	}
	collections := make([]Collection, 0, len(scope.Bases))
	for _, base := range scope.Bases {
		if len(articles[base.ID]) == 0 {
			continue
		}
		collections = append(collections, Collection{ID: base.ID, Name: base.Name, Description: base.Description, Articles: articles[base.ID]})
	}
	return collections, nil
}

// GetArticleQuery 读取网站渠道帮助中心的单篇文章。
type GetArticleQuery struct {
	db *bun.DB
}

// NewGetArticleQuery 创建帮助中心文章查询。
func NewGetArticleQuery(db *bun.DB) *GetArticleQuery {
	return &GetArticleQuery{db: db}
}

// Execute 返回发布范围内的文章详情。
func (q *GetArticleQuery) Execute(ctx context.Context, channelID, articleID string) (Article, error) {
	scope, err := loadScope(ctx, q.db, channelID)
	if err != nil {
		return Article{}, err
	}
	if !common.ValidUUID(articleID) {
		return Article{}, ErrArticleNotFound
	}
	for _, query := range articleQueries(q.db, scope, articleID) {
		var row articleRow
		err := query.Scan(ctx, &row)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return Article{}, fmt.Errorf("get help center article: %w", err)
		}
		article := Article{ID: row.ID, CollectionID: row.KnowledgeBaseID, Title: row.Title, Body: row.Body, UpdatedAt: row.UpdatedAt}
		for _, base := range scope.Bases {
			if base.ID == row.KnowledgeBaseID {
				article.CollectionName = base.Name
			}
		}
		return article, nil
	}
	return Article{}, ErrArticleNotFound
}
