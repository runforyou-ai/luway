//go:build server

// Package serviceissue 读取问题会话：推断满意度为不满意或任一质检标记成立的已关闭客服周期，供 AI 表现与团队表现报表共用。
package serviceissue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/contactname"
	"github.com/runforyou-ai/luway/internal/actions/knowledgegap"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ErrNotFound 表示当前企业中不存在指定周期的质检结果。
var ErrNotFound = errors.New("service issue not found")

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

// List 定义一页问题会话与总条数。
type List struct {
	Issues   []Issue
	Page     int
	PageSize int
	Total    int
}

// Detail 定义问题会话详情：周期信息、质检结论与周期内的对客沟通。
type Detail struct {
	Issue
	Messages []knowledgegap.Message
}

// selectSQL 读取问题会话的展示字段，第一个 %s 拼入不含参数的来源集合 src，第二个拼入筛选与排序。
var selectSQL = `
SELECT ss.id, ss.conversation_id, ss.opening_message_id, ch.type AS channel_type, ch.name AS channel_name,
	coalesce(` + contactname.Expr("c", "ci.display_name") + `, requester_oi.display_name) AS requester_name, c.number AS requester_contact_number,
	coalesce(ci.avatar_file_id, requester_oi.avatar_file_id)::text AS requester_avatar_file_id,
	ss.closed_at, CASE WHEN ss.summary_status = ? THEN ss.summary END AS summary,
	coalesce(?, '') AS preview,
	ssr.satisfaction, ssr.ai_incorrect, ssr.ai_missed_handoff, ssr.ai_poor_attitude, ssr.human_incorrect, ssr.human_poor_attitude
FROM %s
JOIN service_sessions ss ON ss.id = src.id AND ss.workspace_id = src.workspace_id
JOIN service_session_reviews ssr ON ssr.workspace_id = ss.workspace_id AND ssr.service_session_id = ss.id
JOIN service_conversations svc ON svc.id = ss.service_conversation_id AND svc.workspace_id = ss.workspace_id
JOIN chat_subjects requester_cs ON requester_cs.id = svc.requester_subject_id AND requester_cs.workspace_id = svc.workspace_id
LEFT JOIN contacts c ON c.id = requester_cs.source_id AND c.workspace_id = requester_cs.workspace_id AND requester_cs.kind = ?
LEFT JOIN workspace_identities requester_oi ON requester_oi.id = requester_cs.source_id AND requester_oi.workspace_id = requester_cs.workspace_id AND requester_cs.kind = ?
LEFT JOIN channel_conversations cc ON cc.conversation_id = svc.conversation_id AND cc.workspace_id = svc.workspace_id
LEFT JOIN channel_identities ci ON ci.id = cc.channel_identity_id AND ci.workspace_id = cc.workspace_id
LEFT JOIN channels ch ON ch.id = ci.channel_id AND ch.workspace_id = ci.workspace_id
LEFT JOIN messages opening ON opening.id = ss.opening_message_id AND opening.workspace_id = ss.workspace_id AND opening.deleted_at IS NULL
%s`

// selectArgs 返回 selectSQL 中展示字段与关联的参数。
func selectArgs() []any {
	return []any{domain.ServiceSessionSummaryReady, messagequery.Summary("opening"), domain.ChatSubjectKindContact, domain.ChatSubjectKindWorkspaceIdentity}
}

// ListIssues 在报表公共集合 closed 上按问题类型条件分页读取问题会话；全部与不满意类型的条件含一个满意度占位符，由此处传入不满意取值。
func ListIssues(ctx context.Context, db bun.IDB, scope string, scopeArgs []any, issue domain.ServiceIssueType, condition string, page, pageSize int) (*List, error) {
	args := slices.Clone(scopeArgs)
	if issue == domain.ServiceIssueTypeAll || issue == domain.ServiceIssueTypeDissatisfied {
		args = append(args, domain.ServiceSessionSatisfactionDissatisfied)
	}
	scope += `, issues AS (SELECT closed.id, closed.workspace_id, closed.closed_at FROM closed WHERE ` + condition + `)`
	return ListPage(ctx, db, scope, args, page, pageSize)
}

// ListPage 按关闭时间倒序读取一页问题会话；scope 为以名为 issues 的公共集合（含 id、workspace_id 与 closed_at）结尾的 WITH 子句，scopeArgs 为其参数。
func ListPage(ctx context.Context, db bun.IDB, scope string, scopeArgs []any, page, pageSize int) (*List, error) {
	list := &List{Issues: []Issue{}, Page: page, PageSize: pageSize}
	if err := db.NewRaw(scope+` SELECT count(*) FROM issues`, scopeArgs...).Scan(ctx, &list.Total); err != nil {
		return nil, fmt.Errorf("count service issues: %w", err)
	}
	if err := db.NewRaw(scope+fmt.Sprintf(selectSQL, "issues src", "ORDER BY src.closed_at DESC, src.id DESC LIMIT ? OFFSET ?"),
		slices.Concat(scopeArgs, selectArgs(), []any{pageSize, (page - 1) * pageSize})...).
		Scan(ctx, &list.Issues); err != nil {
		return nil, fmt.Errorf("list service issues: %w", err)
	}
	return list, nil
}

// Query 读取问题会话详情。
type Query struct{ db *bun.DB }

// NewQuery 创建问题会话详情查询。
func NewQuery(db *bun.DB) *Query { return &Query{db: db} }

// Execute 返回当前企业中已质检的已关闭周期的质检结论与对客沟通。
func (q *Query) Execute(ctx context.Context, identity *servermodels.Identity, serviceSessionID string) (*Detail, error) {
	workspaceID := identity.Workspace.ID
	detail := &Detail{}
	err := q.db.NewRaw(fmt.Sprintf(selectSQL, "service_sessions src", "WHERE src.workspace_id = ? AND src.id = ? AND src.status = ?"),
		slices.Concat(selectArgs(), []any{workspaceID, serviceSessionID, domain.ServiceSessionStatusClosed})...).
		Scan(ctx, &detail.Issue)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load service issue: %w", err)
	}
	if detail.Messages, err = knowledgegap.Transcript(ctx, q.db, workspaceID, serviceSessionID); err != nil {
		return nil, err
	}
	return detail, nil
}
