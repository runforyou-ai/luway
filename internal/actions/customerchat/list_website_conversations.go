//go:build server

package customerchat

import (
	"context"
	"fmt"
	"time"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/actions/serviceroute"
	"github.com/runforyou-ai/luway/internal/common/customeridentity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// ListWebsiteConversationsQuery 读取网站访客的客户会话列表。
type ListWebsiteConversationsQuery struct {
	db *bun.DB
}

// conversationSummaryRow 是网站访客会话列表的一行：最近一条共享消息摘要与当前客服周期。
type conversationSummaryRow struct {
	LastMessageSeq            int64                         `bun:"last_message_seq"`
	ID                        string                        `bun:"id"`
	Title                     string                        `bun:"title"`
	LastMessageAt             time.Time                     `bun:"last_message_at"`
	Preview                   string                        `bun:"preview"`
	PreviewSenderIdentityType *domain.WorkspaceIdentityType `bun:"preview_sender_identity_type"`
	ServiceSessionID          string                        `bun:"service_session_id"`
	ServiceSessionStatus      string                        `bun:"service_session_status"`
	ServiceSessionTeamID      *string                       `bun:"service_session_team_id"`
	ServiceSessionAssigneeID  *string                       `bun:"service_session_assignee_id"`
}

// NewListWebsiteConversationsQuery 创建网站访客会话列表查询。
func NewListWebsiteConversationsQuery(db *bun.DB) *ListWebsiteConversationsQuery {
	return &ListWebsiteConversationsQuery{db: db}
}

// Execute 返回当前网站渠道身份最近的客户会话，以及渠道新会话与各会话当前的接待状态。
func (q *ListWebsiteConversationsQuery) Execute(ctx context.Context, channelID, externalID string) (WebsiteConversationDirectory, error) {
	fields := map[string]conversationaction.ValidationCode{}
	if !str.IsUUID(channelID) {
		fields["channelId"] = ValidationChannelIDInvalid
	}
	if !customeridentity.ValidExternalID(externalID) {
		fields["visitorToken"] = ValidationExternalIDInvalid
	}
	if len(fields) > 0 {
		return WebsiteConversationDirectory{}, &conversationaction.ValidationError{Fields: fields}
	}
	channel, err := loadWebsiteChannel(ctx, q.db, channelID)
	if err != nil {
		return WebsiteConversationDirectory{}, err
	}
	resolver := serviceroute.NewReceptionResolver(q.db, channel.WorkspaceID)
	directory := WebsiteConversationDirectory{Conversations: []ConversationSummary{}}
	if directory.NewSessionReception, err = resolver.ForNewSession(ctx, channel); err != nil {
		return WebsiteConversationDirectory{}, fmt.Errorf("resolve website new session reception: %w", err)
	}
	if directory.ReceptionRefreshAt, err = resolver.RefreshAt(ctx); err != nil {
		return WebsiteConversationDirectory{}, fmt.Errorf("resolve website reception refresh time: %w", err)
	}
	identity, found, err := loadWebsiteVisitorIdentity(ctx, q.db, channel, externalID)
	if err != nil {
		return WebsiteConversationDirectory{}, err
	}
	if !found {
		return directory, nil
	}
	var rows []conversationSummaryRow
	err = q.db.NewSelect().
		TableExpr("channel_conversations AS cc").
		ColumnExpr("cv.id AS id").
		ColumnExpr("cv.title AS title").
		ColumnExpr("msg.originated_at AS last_message_at").
		ColumnExpr("msg.message_seq AS last_message_seq").
		ColumnExpr("? AS preview", messagequery.Summary("msg")).
		ColumnExpr("preview_oi.type AS preview_sender_identity_type").
		ColumnExpr("current.id AS service_session_id").
		ColumnExpr("current.status AS service_session_status").
		ColumnExpr("current.team_id AS service_session_team_id").
		ColumnExpr("current.assignee_identity_id AS service_session_assignee_id").
		Join("JOIN conversations AS cv ON cv.id = cc.conversation_id AND cv.workspace_id = cc.workspace_id").
		Join(`JOIN LATERAL (
 SELECT visible.* FROM messages AS visible
 WHERE visible.workspace_id = cv.workspace_id AND visible.conversation_id = cv.id AND visible.type IN (?, ?) AND visible.visibility = ? AND visible.deleted_at IS NULL
 ORDER BY visible.message_seq DESC LIMIT 1
 ) AS msg ON TRUE`, domain.MessageTypeText, domain.MessageTypeAttachment, domain.MessageVisibilityShared).
		Join("LEFT JOIN conversation_participants AS preview_cp ON preview_cp.id = msg.sender_participant_id AND preview_cp.workspace_id = msg.workspace_id AND preview_cp.conversation_id = msg.conversation_id").
		Join("LEFT JOIN chat_subjects AS preview_cs ON preview_cs.id = preview_cp.subject_id AND preview_cs.workspace_id = preview_cp.workspace_id").
		Join("LEFT JOIN workspace_identities AS preview_oi ON preview_oi.id = preview_cs.source_id AND preview_oi.workspace_id = preview_cs.workspace_id AND preview_cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("JOIN service_conversations AS svc ON svc.workspace_id = cc.workspace_id AND svc.conversation_id = cc.conversation_id").
		Join("JOIN service_sessions AS current ON current.workspace_id = svc.workspace_id AND current.service_conversation_id = svc.id AND current.id = svc.current_service_session_id").
		Where("cc.workspace_id = ?", channel.WorkspaceID).
		Where("cc.channel_identity_id = ?", identity.ID).
		Where("cv.type = ?", domain.ConversationTypeChannel).
		Where("cv.status IN (?, ?)", domain.ConversationStatusActive, domain.ConversationStatusArchived).
		OrderExpr("msg.originated_at DESC, cv.id DESC").
		Limit(20).
		Scan(ctx, &rows)
	if err != nil {
		return WebsiteConversationDirectory{}, fmt.Errorf("list website conversations: %w", err)
	}
	for _, row := range rows {
		summary := conversationSummaryFromRow(row)
		if err := resolveSummaryReception(ctx, resolver, &summary); err != nil {
			return WebsiteConversationDirectory{}, err
		}
		directory.Conversations = append(directory.Conversations, summary)
	}
	return directory, nil
}

// conversationSummaryFromRow 转换网站访客会话摘要。
func conversationSummaryFromRow(row conversationSummaryRow) ConversationSummary {
	return ConversationSummary{
		ID: row.ID, Title: row.Title, Preview: row.Preview, PreviewSenderIdentityType: row.PreviewSenderIdentityType, LastMessageSeq: row.LastMessageSeq, LastMessageAt: row.LastMessageAt,
		ServiceSessionID: row.ServiceSessionID, ServiceSessionStatus: domain.ServiceSessionStatus(row.ServiceSessionStatus),
		ServiceSessionTeamID: row.ServiceSessionTeamID, ServiceSessionAssigneeID: row.ServiceSessionAssigneeID,
	}
}

// resolveSummaryReception 按会话摘要中的当前客服周期填充接待状态。
func resolveSummaryReception(ctx context.Context, resolver *serviceroute.ReceptionResolver, summary *ConversationSummary) error {
	reception, err := resolver.ForServiceSession(ctx, summary.ServiceSessionStatus, summary.ServiceSessionTeamID, summary.ServiceSessionAssigneeID)
	if err != nil {
		return fmt.Errorf("resolve website conversation reception: %w", err)
	}
	summary.Reception = reception
	return nil
}
