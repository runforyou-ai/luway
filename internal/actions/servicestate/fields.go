//go:build server

package servicestate

import (
	"context"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// OpenInput 定义新服务周期的归属、序号、首条消息、开启时间、初始负责方、接待的 AI 员工与访客上下文；AgentIdentityID 为空表示周期由真人或队列首接待。
type OpenInput struct {
	ID                    string
	WorkspaceID           string
	ConversationID        string
	ServiceConversationID string
	Sequence              int64
	OpeningMessageID      string
	OpenedAt              time.Time
	TeamID                *string
	AssigneeIdentityID    *string
	AgentIdentityID       *string
	VisitorContext        *domain.VisitorContext
}

// Open 在调用方持有服务会话锁的事务中写入新的开放周期：有负责人时记负责时间，无负责人时从首条消息起计入队列；真人或队列首接待的周期从开启起需要真人，由真人负责时同时记为真人首次负责。
func Open(ctx context.Context, db bun.IDB, input OpenInput) (*servermodels.ServiceSession, error) {
	var assignedAt, queuedAt *time.Time
	if input.AssigneeIdentityID != nil {
		assignedAt = new(input.OpenedAt)
	} else {
		queuedAt = new(input.OpenedAt)
	}
	var humanRequestedAt, humanAssignedAt *time.Time
	if input.AgentIdentityID == nil {
		humanRequestedAt, humanAssignedAt = new(input.OpenedAt), assignedAt
	}
	session := &servermodels.ServiceSession{
		ID: input.ID, WorkspaceID: input.WorkspaceID,
		ConversationID: input.ConversationID, ServiceConversationID: input.ServiceConversationID,
		Sequence: input.Sequence, Status: string(domain.ServiceSessionStatusOpen),
		TeamID: input.TeamID, AssigneeIdentityID: input.AssigneeIdentityID, AgentIdentityID: input.AgentIdentityID,
		OpeningMessageID: input.OpeningMessageID, LastMessageID: input.OpeningMessageID,
		LastMessageAt: input.OpenedAt,
		AssignedAt:    assignedAt, AssigneeAssignedAt: assignedAt, QueuedAt: queuedAt, StatusChangedAt: input.OpenedAt,
		HumanRequestedAt: humanRequestedAt, HumanAssignedAt: humanAssignedAt,
		VisitorContext: input.VisitorContext,
	}
	if _, err := db.NewInsert().Model(session).
		Column("id", "workspace_id", "conversation_id", "service_conversation_id", "sequence", "status", "team_id", "assignee_identity_id", "agent_identity_id", "opening_message_id", "last_message_id", "last_message_at", "assigned_at", "assignee_assigned_at", "queued_at", "status_changed_at", "human_requested_at", "human_assigned_at", "visitor_context").
		Returning("*").
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("create service session: %w", err)
	}
	return session, nil
}

// SummaryColumn 是周期小结、咨询分类与交接摘要中可单独写入的列。
type SummaryColumn string

const (
	// SummaryStatusColumn 是小结状态。
	SummaryStatusColumn SummaryColumn = "summary_status"
	// SummaryTextColumn 是小结正文。
	SummaryTextColumn SummaryColumn = "summary"
	// ResolvedColumn 是周期是否解决。
	ResolvedColumn SummaryColumn = "resolved"
	// CategoryColumn 是周期的咨询分类。
	CategoryColumn SummaryColumn = "category_id"
	// SummaryEditedByColumn 是修改小结的成员身份。
	SummaryEditedByColumn SummaryColumn = "summary_edited_by_identity_id"
	// SummaryEditedAtColumn 是成员修改小结的时间。
	SummaryEditedAtColumn SummaryColumn = "summary_edited_at"
	// HandoffMessageColumn 是最近一次转人工事件。
	HandoffMessageColumn SummaryColumn = "handoff_message_id"
	// HandoffSummaryColumn 是交接摘要。
	HandoffSummaryColumn SummaryColumn = "handoff_summary"
)

// SaveSummary 在调用方持有周期锁的事务中按周期内存值写入指定的小结、咨询分类与交接摘要列并推进 updated_at。
func SaveSummary(ctx context.Context, db bun.IDB, session *servermodels.ServiceSession, columns ...SummaryColumn) error {
	if err := update(ctx, db, session, arr.Map(columns, func(column SummaryColumn) string { return string(column) })...); err != nil {
		return fmt.Errorf("save service session summary fields: %w", err)
	}
	return nil
}

// SaveRating 在调用方持有周期锁的事务中按周期内存值写入访客评价的是否解决、评价内容与评价时间并推进 updated_at。
func SaveRating(ctx context.Context, db bun.IDB, session *servermodels.ServiceSession) error {
	if err := update(ctx, db, session, "rating_resolved", "rating_comment", "rated_at"); err != nil {
		return fmt.Errorf("save service session rating: %w", err)
	}
	return nil
}

// SaveVisitorContext 在调用方持有周期锁的事务中按周期内存值写入访客上下文并推进 updated_at。
func SaveVisitorContext(ctx context.Context, db bun.IDB, session *servermodels.ServiceSession) error {
	if err := update(ctx, db, session, "visitor_context"); err != nil {
		return fmt.Errorf("save service session visitor context: %w", err)
	}
	return nil
}

// MoveTeamToPublicQueue 在删除团队的事务中把该团队的全部周期并入公共队列，队列中的周期清空本轮提醒时间以重新计算队列等待提醒，返回被移动周期的编号、状态与负责人。
func MoveTeamToPublicQueue(ctx context.Context, db bun.IDB, workspaceID, teamID string) ([]servermodels.ServiceSession, error) {
	moved := make([]servermodels.ServiceSession, 0)
	if _, err := db.NewUpdate().Model(&moved).
		Set("team_id = NULL").
		Set("reminded_at = CASE WHEN assignee_identity_id IS NULL THEN NULL ELSE reminded_at END").
		Where("workspace_id = ?", workspaceID).
		Where("team_id = ?", teamID).
		Returning("id, status, assignee_identity_id").
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("move team service sessions to public queue: %w", err)
	}
	return moved, nil
}
