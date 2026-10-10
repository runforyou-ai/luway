//go:build server

package aiperformance

import (
	"context"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/contactname"
	"github.com/runforyou-ai/luway/internal/actions/reportpage"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ServiceSessionListInput 定义 AI 员工服务记录的 AI 员工与分页。
type ServiceSessionListInput struct {
	AgentID  string
	Page     int
	PageSize int
}

// ServiceSession 定义 AI 员工接待的一个服务周期，即该 AI 员工在周期开启时或之后首次负责该周期；Preview 为周期首条消息摘要，Summary 只在小结已生成时有值。
type ServiceSession struct {
	ID                     string     `bun:"id"`
	ConversationID         string     `bun:"conversation_id"`
	OpeningMessageID       string     `bun:"opening_message_id"`
	Source                 string     `bun:"source"`
	Audience               string     `bun:"audience"`
	ChannelType            *string    `bun:"channel_type"`
	ChannelName            *string    `bun:"channel_name"`
	RequesterName          *string    `bun:"requester_name"`
	RequesterContactNumber *int64     `bun:"requester_contact_number"`
	RequesterAvatarFileID  *string    `bun:"requester_avatar_file_id"`
	Status                 string     `bun:"status"`
	OpenedAt               time.Time  `bun:"opened_at"`
	ClosedAt               *time.Time `bun:"closed_at"`
	CloseReason            *string    `bun:"close_reason"`
	Preview                string     `bun:"preview"`
	Summary                *string    `bun:"summary"`
	Resolved               *bool      `bun:"resolved"`
}

// ServiceSessionList 定义一页服务记录与总条数。
type ServiceSessionList struct {
	Sessions []ServiceSession
	Page     int
	PageSize int
	Total    int
}

// ServiceSessionListQuery 读取 AI 员工的服务记录。
type ServiceSessionListQuery struct{ db *bun.DB }

// NewServiceSessionListQuery 创建 AI 员工服务记录查询。
func NewServiceSessionListQuery(db *bun.DB) *ServiceSessionListQuery {
	return &ServiceSessionListQuery{db: db}
}

// Execute 按开启时间倒序返回一页由指定 AI 员工接待的服务周期。
func (q *ServiceSessionListQuery) Execute(ctx context.Context, identity *servermodels.Identity, input ServiceSessionListInput) (*ServiceSessionList, error) {
	page, pageSize, valid := common.NormalizePagination(input.Page, input.PageSize)
	if !valid {
		return nil, ErrPageSizeInvalid
	}
	workspaceID := identity.Workspace.ID
	condition, args := reportpage.AgentScope{AgentID: input.AgentID}.Condition("ss.agent_identity_id", workspaceID)
	list := &ServiceSessionList{Sessions: []ServiceSession{}, Page: page, PageSize: pageSize}
	total, err := q.db.NewSelect().TableExpr("service_sessions AS ss").
		Join("JOIN service_conversations AS svc ON svc.id = ss.service_conversation_id AND svc.workspace_id = ss.workspace_id").
		Join("JOIN chat_subjects AS requester_cs ON requester_cs.id = svc.requester_subject_id AND requester_cs.workspace_id = svc.workspace_id").
		Join("LEFT JOIN contacts AS c ON c.id = requester_cs.source_id AND c.workspace_id = requester_cs.workspace_id AND requester_cs.kind = ?", domain.ChatSubjectKindContact).
		Join("LEFT JOIN workspace_identities AS requester_oi ON requester_oi.id = requester_cs.source_id AND requester_oi.workspace_id = requester_cs.workspace_id AND requester_cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("LEFT JOIN channel_conversations AS cc ON cc.conversation_id = svc.conversation_id AND cc.workspace_id = svc.workspace_id").
		Join("LEFT JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id AND ci.workspace_id = cc.workspace_id").
		Join("LEFT JOIN channels AS ch ON ch.id = ci.channel_id AND ch.workspace_id = ci.workspace_id").
		Join("LEFT JOIN messages AS opening ON opening.id = ss.opening_message_id AND opening.workspace_id = ss.workspace_id AND opening.deleted_at IS NULL").
		ColumnExpr("ss.id, ss.conversation_id, ss.opening_message_id, svc.source, svc.audience, ch.type AS channel_type, ch.name AS channel_name").
		ColumnExpr("COALESCE("+contactname.Expr("c", "ci.display_name")+", requester_oi.display_name) AS requester_name").
		ColumnExpr("c.number AS requester_contact_number").
		ColumnExpr("COALESCE(ci.avatar_file_id, requester_oi.avatar_file_id)::text AS requester_avatar_file_id").
		ColumnExpr("ss.status, ss.created_at AS opened_at, ss.closed_at, ss.close_reason, ss.resolved").
		ColumnExpr("COALESCE(?, '') AS preview", messagequery.Summary("opening")).
		ColumnExpr("CASE WHEN ss.summary_status = ? THEN ss.summary END AS summary", domain.ServiceSessionSummaryReady).
		Where("ss.workspace_id = ?", workspaceID).
		Where(condition, args...).
		OrderExpr("ss.created_at DESC, ss.id DESC").
		Limit(int64(pageSize)).
		Offset(int64((page-1)*pageSize)).
		ScanAndCount(ctx, &list.Sessions)
	if err != nil {
		return nil, fmt.Errorf("list agent service sessions: %w", err)
	}
	list.Total = int(total)
	return list, nil
}
