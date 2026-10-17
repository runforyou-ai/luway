//go:build server

package knowledgegap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/contactprofile"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ErrIssueNotFound 表示当前企业中不存在指定周期的质检结果。
var ErrIssueNotFound = errors.New("service issue not found")

// Issue 定义一个问题会话；OpeningMessageID 为周期首条消息，Summary 只在小结已生成时有值，Preview 为周期首条消息摘要，质检标记不适用时为空。
type Issue struct {
	ServiceSessionID       string    `bun:"id"`
	ConversationID         string    `bun:"conversation_id"`
	OpeningMessageID       string    `bun:"opening_message_id"`
	ChannelType            *string   `bun:"channel_type"`
	ChannelName            *string   `bun:"channel_name"`
	RequesterName          *string   `bun:"requester_name"`
	RequesterContactNumber *int64    `bun:"requester_contact_number"`
	RequesterAvatarFileID  *string   `bun:"requester_avatar_file_id"`
	ClosedAt               time.Time `bun:"closed_at"`
	Summary                *string   `bun:"summary"`
	Preview                string    `bun:"preview"`
	Satisfaction           *string   `bun:"satisfaction"`
	AIIncorrect            *bool     `bun:"ai_incorrect"`
	AIMissedHandoff        *bool     `bun:"ai_missed_handoff"`
	AIPoorAttitude         *bool     `bun:"ai_poor_attitude"`
	HumanIncorrect         *bool     `bun:"human_incorrect"`
	HumanPoorAttitude      *bool     `bun:"human_poor_attitude"`
}

// IssueList 定义一页问题会话与总条数。
type IssueList struct {
	Issues   []Issue
	Page     int
	PageSize int
	Total    int
}

// IssueDetail 定义问题会话详情：周期信息、质检结论与周期内的对客沟通。
type IssueDetail struct {
	Issue
	Messages []Message
}

// issueRequesterJoins 与 issueRequesterJoinArgs 是发起人与渠道展示的共用 JOIN。
var issueRequesterJoins, issueRequesterJoinArgs = contactprofile.RequesterChannelJoins()

// issueSelectSQL 读取问题会话的展示字段，第一个 %s 拼入不含参数的来源集合 src，第二个拼入筛选与排序。
var issueSelectSQL = `
SELECT ss.id, ss.conversation_id, ss.opening_message_id, ch.type AS channel_type, ch.name AS channel_name,
	` + contactprofile.RequesterNameExpr("c", "ci.display_name", "requester_oi") + ` AS requester_name, c.number AS requester_contact_number,
	` + contactprofile.RequesterAvatarExpr("ci", "requester_oi") + ` AS requester_avatar_file_id,
	ss.closed_at, CASE WHEN ss.summary_status = ? THEN ss.summary END AS summary,
	coalesce(?, '') AS preview,
	ssr.satisfaction, ssr.ai_incorrect, ssr.ai_missed_handoff, ssr.ai_poor_attitude, ssr.human_incorrect, ssr.human_poor_attitude
FROM %s
JOIN service_sessions ss ON ss.id = src.id AND ss.workspace_id = src.workspace_id
JOIN service_session_reviews ssr ON ssr.workspace_id = ss.workspace_id AND ssr.service_session_id = ss.id
JOIN service_conversations svc ON svc.id = ss.service_conversation_id AND svc.workspace_id = ss.workspace_id
` + issueRequesterJoins + `
LEFT JOIN messages opening ON opening.id = ss.opening_message_id AND opening.workspace_id = ss.workspace_id AND opening.deleted_at IS NULL
%s`

// issueSelectArgs 返回 issueSelectSQL 中展示字段与关联的参数。
func issueSelectArgs() []any {
	return slices.Concat([]any{domain.ServiceSessionSummaryReady, messagequery.Summary("opening")}, issueRequesterJoinArgs)
}

// ListIssues 在报表公共集合 closed 上按问题类型条件分页读取问题会话；全部与不满意类型的条件含一个满意度占位符，由此处传入不满意取值。
func ListIssues(ctx context.Context, db bun.IDB, scope string, scopeArgs []any, issue domain.ServiceIssueType, condition string, page, pageSize int) (*IssueList, error) {
	args := slices.Clone(scopeArgs)
	if issue == domain.ServiceIssueTypeAll || issue == domain.ServiceIssueTypeDissatisfied {
		args = append(args, domain.ServiceSessionSatisfactionDissatisfied)
	}
	scope += `, issues AS (SELECT closed.id, closed.workspace_id, closed.closed_at FROM closed WHERE ` + condition + `)`
	return ListIssuePage(ctx, db, scope, args, page, pageSize)
}

// ListIssuePage 按关闭时间倒序读取一页问题会话；scope 为以名为 issues 的公共集合（含 id、workspace_id 与 closed_at）结尾的 WITH 子句，scopeArgs 为其参数。
func ListIssuePage(ctx context.Context, db bun.IDB, scope string, scopeArgs []any, page, pageSize int) (*IssueList, error) {
	list := &IssueList{Issues: []Issue{}, Page: page, PageSize: pageSize}
	if err := db.NewRaw(scope+` SELECT count(*) FROM issues`, scopeArgs...).Scan(ctx, &list.Total); err != nil {
		return nil, fmt.Errorf("count service issues: %w", err)
	}
	if err := db.NewRaw(scope+fmt.Sprintf(issueSelectSQL, "issues src", "ORDER BY src.closed_at DESC, src.id DESC LIMIT ? OFFSET ?"),
		slices.Concat(scopeArgs, issueSelectArgs(), []any{pageSize, (page - 1) * pageSize})...).
		Scan(ctx, &list.Issues); err != nil {
		return nil, fmt.Errorf("list service issues: %w", err)
	}
	return list, nil
}

// IssueQuery 读取问题会话详情。
type IssueQuery struct{ db *bun.DB }

// NewIssueQuery 创建问题会话详情查询。
func NewIssueQuery(db *bun.DB) *IssueQuery { return &IssueQuery{db: db} }

// Execute 返回当前企业中已质检的已关闭周期的质检结论与对客沟通。
func (q *IssueQuery) Execute(ctx context.Context, identity *servermodels.Identity, serviceSessionID string) (*IssueDetail, error) {
	workspaceID := identity.Workspace.ID
	detail := &IssueDetail{}
	err := q.db.NewRaw(fmt.Sprintf(issueSelectSQL, "service_sessions src", "WHERE src.workspace_id = ? AND src.id = ? AND src.status = ?"),
		slices.Concat(issueSelectArgs(), []any{workspaceID, serviceSessionID, domain.ServiceSessionStatusClosed})...).
		Scan(ctx, &detail.Issue)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrIssueNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load service issue: %w", err)
	}
	if detail.Messages, err = Transcript(ctx, q.db, workspaceID, serviceSessionID); err != nil {
		return nil, err
	}
	return detail, nil
}
