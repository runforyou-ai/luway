//go:build server

package servicesession

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/actions/conversationaccess"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/actions/servicecategory"
	"github.com/runforyou-ai/luway/internal/actions/servicestate"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// serviceSummaryListLimit 是客户历史周期小结的读取条数上限。
const serviceSummaryListLimit = 50

// 周期小结的字段校验码。
const (
	ValidationCategoryIDInvalid conversationaction.ValidationCode = "category_id_invalid"
)

// ConflictReasonServiceSessionNotClosed 表示服务周期尚未关闭，不能修改小结。
const ConflictReasonServiceSessionNotClosed = "service_session_not_closed"

// ErrServiceSessionNotFound 表示服务周期不存在或当前身份无权访问。
var ErrServiceSessionNotFound = errors.New("service session not found")

// ServiceSessionSummary 表示一个已关闭服务周期的结束方式与小结。
type ServiceSessionSummary struct {
	ServiceSessionID string                              `bun:"id"`
	ConversationID   string                              `bun:"conversation_id"`
	Source           domain.ServiceSource                `bun:"source"`
	ChannelType      *domain.ChannelType                 `bun:"channel_type"`
	ChannelName      *string                             `bun:"channel_name"`
	ClosedAt         time.Time                           `bun:"closed_at"`
	CloseReason      domain.ServiceSessionCloseReason    `bun:"close_reason"`
	Status           *domain.ServiceSessionSummaryStatus `bun:"summary_status"`
	Summary          *string                             `bun:"summary"`
	Resolved         *bool                               `bun:"resolved"`
	CategoryID       *string                             `bun:"category_id"`
	CategoryName     *string                             `bun:"category_name"`
	EditedAt         *time.Time                          `bun:"summary_edited_at"`
	EditedByName     *string                             `bun:"edited_by_name"`
}

// ServiceSummaries 表示服务会话当前周期的交接摘要及其转人工事件消息编号，与同一发起人已关闭周期的小结。
type ServiceSummaries struct {
	Handoff          *domain.HandoffSummary
	HandoffMessageID string
	Sessions         []ServiceSessionSummary
}

// serviceSessionSummaryQuery 构造已关闭周期小结的查询，含渠道、咨询分类与最后修改人。
func serviceSessionSummaryQuery(db bun.IDB, workspaceID string) *bun.SelectQuery {
	return db.NewSelect().
		TableExpr("service_sessions AS ss").
		ColumnExpr("ss.id, ss.conversation_id, svc.source, ch.type AS channel_type, ch.name AS channel_name, ss.closed_at, ss.close_reason").
		ColumnExpr("ss.summary_status, ss.summary, ss.resolved, ss.category_id, sc.name AS category_name, ss.summary_edited_at, editor.display_name AS edited_by_name").
		Join("JOIN service_conversations AS svc ON svc.id = ss.service_conversation_id AND svc.workspace_id = ss.workspace_id").
		Join("LEFT JOIN channel_conversations AS cc ON cc.conversation_id = svc.conversation_id AND cc.workspace_id = svc.workspace_id").
		Join("LEFT JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id AND ci.workspace_id = cc.workspace_id").
		Join("LEFT JOIN channels AS ch ON ch.id = ci.channel_id AND ch.workspace_id = ci.workspace_id").
		Join("LEFT JOIN service_categories AS sc ON sc.id = ss.category_id AND sc.workspace_id = ss.workspace_id").
		Join("LEFT JOIN workspace_identities AS editor ON editor.id = ss.summary_edited_by_identity_id AND editor.workspace_id = ss.workspace_id").
		Where("ss.workspace_id = ? AND ss.status = ?", workspaceID, domain.ServiceSessionStatusClosed)
}

// ListServiceSummariesQuery 读取服务会话的交接摘要与客户历史周期小结。
type ListServiceSummariesQuery struct{ db *bun.DB }

// NewListServiceSummariesQuery 创建客户周期小结查询。
func NewListServiceSummariesQuery(db *bun.DB) *ListServiceSummariesQuery {
	return &ListServiceSummariesQuery{db: db}
}

// Execute 返回当前开放周期的交接摘要，以及同一客户在各渠道已关闭周期的小结，按关闭时间从新到旧排列。
func (q *ListServiceSummariesQuery) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string) (ServiceSummaries, error) {
	result := ServiceSummaries{Sessions: make([]ServiceSessionSummary, 0)}
	err := q.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if err := conversationaccess.RequireReadable(ctx, tx, identity, conversationID); err != nil {
			return err
		}
		var current struct {
			RequesterSubjectID string                 `bun:"requester_subject_id"`
			HandoffSummary     *domain.HandoffSummary `bun:"handoff_summary,type:jsonb"`
			HandoffMessageID   *string                `bun:"handoff_message_id"`
		}
		err := tx.NewSelect().
			TableExpr("service_conversations AS svc").
			ColumnExpr("svc.requester_subject_id").
			ColumnExpr("CASE WHEN ss.status = ? THEN ss.handoff_summary END AS handoff_summary", domain.ServiceSessionStatusOpen).
			ColumnExpr("ss.handoff_message_id::text AS handoff_message_id").
			Join("JOIN service_sessions AS ss ON ss.id = svc.current_service_session_id AND ss.workspace_id = svc.workspace_id").
			Where("svc.workspace_id = ? AND svc.conversation_id = ?", identity.Workspace.ID, conversationID).
			Scan(ctx, &current)
		if errors.Is(err, sql.ErrNoRows) {
			return conversationaction.ErrConversationNotFound
		}
		if err != nil {
			return fmt.Errorf("load customer conversation for summaries: %w", err)
		}
		result.Handoff, result.HandoffMessageID = current.HandoffSummary, support.Deref(current.HandoffMessageID)
		if err := serviceSessionSummaryQuery(tx, identity.Workspace.ID).
			Where("svc.requester_subject_id = ?", current.RequesterSubjectID).
			OrderExpr("ss.closed_at DESC, ss.id DESC").
			Limit(serviceSummaryListLimit).
			Scan(ctx, &result.Sessions); err != nil {
			return fmt.Errorf("load customer service session summaries: %w", err)
		}
		return nil
	})
	if err != nil {
		return ServiceSummaries{}, err
	}
	return result, nil
}

// UpdateServiceSessionSummaryInput 定义客服填写的小结、是否解决与咨询分类；Resolved 与 CategoryID 为空表示不标注。
type UpdateServiceSessionSummaryInput struct {
	ServiceSessionID string
	Summary          string
	Resolved         *bool
	CategoryID       *string
}

// UpdateServiceSessionSummaryAction 保存客服修改的周期小结。
type UpdateServiceSessionSummaryAction struct{ db *bun.DB }

// NewUpdateServiceSessionSummaryAction 创建周期小结修改操作。
func NewUpdateServiceSessionSummaryAction(db *bun.DB) *UpdateServiceSessionSummaryAction {
	return &UpdateServiceSessionSummaryAction{db: db}
}

// Execute 校验并保存已关闭周期的小结、是否解决与咨询分类，记录修改人；保存后 AI 不覆盖该周期的小结。
func (a *UpdateServiceSessionSummaryAction) Execute(ctx context.Context, identity *servermodels.Identity, input UpdateServiceSessionSummaryInput) (ServiceSessionSummary, error) {
	input.ServiceSessionID, _ = str.NormalizeUUID(input.ServiceSessionID)
	input.Summary = strings.TrimSpace(input.Summary)
	if input.CategoryID != nil {
		categoryID, _ := str.NormalizeUUID(*input.CategoryID)
		input.CategoryID = &categoryID
	}
	var output ServiceSessionSummary
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		locked, err := chatstate.LockServiceSessionByID(ctx, tx, identity.Workspace.ID, input.ServiceSessionID)
		if errors.Is(err, servicestate.ErrServiceSessionNotFound) {
			return ErrServiceSessionNotFound
		}
		if err != nil {
			return err
		}
		conversation, session := locked.Conversation, locked.Session
		if err := conversationaccess.RequireReadable(ctx, tx, identity, conversation.ID); err != nil {
			return err
		}
		if domain.ServiceSessionStatus(session.Status) != domain.ServiceSessionStatusClosed {
			return &conversationaction.ConflictError{Reason: ConflictReasonServiceSessionNotClosed}
		}
		// 保留周期当前的咨询分类时不要求分类未归档。
		if input.CategoryID != nil && (session.CategoryID == nil || *session.CategoryID != *input.CategoryID) {
			category, err := servicecategory.FindActive(ctx, tx, identity.Workspace.ID, *input.CategoryID)
			if err != nil {
				return err
			}
			if category == nil {
				return &conversationaction.ValidationError{Fields: map[string]conversationaction.ValidationCode{"categoryId": ValidationCategoryIDInvalid}}
			}
		}
		// 空白小结按未填写保存；无实质诉求的周期保存时未填写任何内容则保持该标记。
		summary := support.NilIfZero(input.Summary)
		status := domain.ServiceSessionSummaryReady
		if summary == nil && input.Resolved == nil && input.CategoryID == nil && session.SummaryStatus != nil &&
			domain.ServiceSessionSummaryStatus(*session.SummaryStatus) == domain.ServiceSessionSummaryNoRequest {
			status = domain.ServiceSessionSummaryNoRequest
		}
		editedAt, err := serverstorage.Now(ctx, tx)
		if err != nil {
			return err
		}
		session.SummaryStatus, session.Summary, session.Resolved, session.CategoryID = new(string(status)), summary, input.Resolved, input.CategoryID
		session.SummaryEditedByID, session.SummaryEditedAt = &identity.WorkspaceIdentity.ID, &editedAt
		if err := servicestate.SaveSummary(ctx, tx, session, servicestate.SummaryStatusColumn, servicestate.SummaryTextColumn, servicestate.ResolvedColumn,
			servicestate.CategoryColumn, servicestate.SummaryEditedByColumn, servicestate.SummaryEditedAtColumn); err != nil {
			return err
		}
		if err := chatstate.TouchConversation(ctx, tx, conversation, domain.ConversationChangeService); err != nil {
			return err
		}
		return serviceSessionSummaryQuery(tx, identity.Workspace.ID).Where("ss.id = ?", session.ID).Scan(ctx, &output)
	})
	if err != nil {
		return ServiceSessionSummary{}, fmt.Errorf("update service session summary: %w", err)
	}
	return output, nil
}
