//go:build server

package teamperformance

import (
	"context"

	"github.com/runforyou-ai/luway/internal/actions/serviceissue"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// issueConditions 是各问题类型在公共集合 closed 上的筛选条件，只包含真人负责过的周期，满意度取值由调用方按顺序传入。
var issueConditions = map[domain.ServiceIssueType]string{
	domain.ServiceIssueTypeAll:               "closed.human_assigned_at IS NOT NULL AND (closed.satisfaction = ? OR closed.human_incorrect OR closed.human_poor_attitude)",
	domain.ServiceIssueTypeDissatisfied:      "closed.human_assigned_at IS NOT NULL AND closed.satisfaction = ?",
	domain.ServiceIssueTypeHumanIncorrect:    "closed.human_incorrect",
	domain.ServiceIssueTypeHumanPoorAttitude: "closed.human_poor_attitude",
}

// IssueListQuery 读取真人接待的问题会话。
type IssueListQuery struct{ db *bun.DB }

// NewIssueListQuery 创建真人接待问题会话查询。
func NewIssueListQuery(db *bun.DB) *IssueListQuery { return &IssueListQuery{db: db} }

// Execute 按关闭时间倒序返回统计范围内一页指定类型的真人接待问题会话。
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
