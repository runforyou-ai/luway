//go:build server

package aiperformance

import (
	"context"

	"github.com/runforyou-ai/luway/internal/actions/serviceissue"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// issueConditions 是各问题类型在公共集合 closed 上的筛选条件，满意度取值由调用方按顺序传入。
var issueConditions = map[domain.ServiceIssueType]string{
	domain.ServiceIssueTypeAll:             "(closed.satisfaction = ? OR closed.ai_incorrect OR closed.ai_missed_handoff OR closed.ai_poor_attitude)",
	domain.ServiceIssueTypeDissatisfied:    "closed.satisfaction = ?",
	domain.ServiceIssueTypeAIIncorrect:     "closed.ai_incorrect",
	domain.ServiceIssueTypeAIMissedHandoff: "closed.ai_missed_handoff",
	domain.ServiceIssueTypeAIPoorAttitude:  "closed.ai_poor_attitude",
}

// IssueListInput 定义问题会话的统计范围、问题类型与分页。
type IssueListInput struct {
	Input
	Issue    domain.ServiceIssueType
	Page     int
	PageSize int
}

// IssueListQuery 读取问题会话。
type IssueListQuery struct{ db *bun.DB }

// NewIssueListQuery 创建问题会话查询。
func NewIssueListQuery(db *bun.DB) *IssueListQuery { return &IssueListQuery{db: db} }

// Execute 按关闭时间倒序返回统计范围内一页指定类型的问题会话。
func (q *IssueListQuery) Execute(ctx context.Context, identity *servermodels.Identity, input IssueListInput) (*serviceissue.List, error) {
	page, pageSize, valid := common.NormalizePagination(input.Page, input.PageSize)
	if !valid {
		return nil, ErrPageSizeInvalid
	}
	condition, ok := issueConditions[input.Issue]
	if !ok {
		return nil, ErrIssueInvalid
	}
	scope, args := reportScope(identity, input.Input)
	return serviceissue.ListIssues(ctx, q.db, scope, args, input.Issue, condition, page, pageSize)
}
