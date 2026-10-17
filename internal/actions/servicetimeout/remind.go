//go:build server

// Package servicetimeout 按企业超时设置提醒负责人和队列客服处理等待中的客户，回收负责人超时未回复的客服处理周期，并在客户超时未回复 AI 员工时跟进与关单。
package servicetimeout

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/runforyou-ai/luway/internal/actions/notificationtask"
	"github.com/runforyou-ai/luway/internal/actions/serviceroute"
	"github.com/runforyou-ai/luway/internal/actions/servicestate"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/uptrace/bun"
)

func queueReminderRecipients(db bun.IDB, workspaceID, teamID any) *bun.SelectQuery {
	query := db.NewSelect().TableExpr("workspace_identities AS oi").
		Join("JOIN users AS u ON u.workspace_id = oi.workspace_id AND u.identity_id = oi.id").
		Where("oi.workspace_id = ? AND oi.type = ? AND oi.work_status = ?", workspaceID, domain.WorkspaceIdentityTypeUser, domain.WorkStatusWorking).
		Where("(? IS NULL OR EXISTS (SELECT 1 FROM team_members AS tm WHERE tm.workspace_id = oi.workspace_id AND tm.identity_id = oi.id AND tm.team_id = ?))", teamID, teamID)
	return serviceroute.ApplyServiceHandlingUserConditions(query)
}

// remind 在事务中锁定周期并复核仍需提醒后写入提醒时间，并为负责人或队列对应的工作中客服登记提醒通知任务；没有收件人时本轮提醒保持未发出。
func (w *Worker) remind(ctx context.Context, input ProcessInput) error {
	return realtime.RunInTx(ctx, w.db, func(ctx context.Context, tx bun.Tx) error {
		loaded, err := loadSession(ctx, tx, input.WorkspaceID, input.ServiceSessionID, true)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		session := loaded.Session
		action := loaded.dueAction()
		if action != actionRemindAssignee && action != actionRemindQueue {
			return nil
		}
		reason := domain.ServiceAttentionResponseOverdue
		// 负责人提醒只发给负责人；队列提醒发给团队中或全企业工作中的客服。
		var recipients *bun.SelectQuery
		if action == actionRemindAssignee {
			recipients = tx.NewSelect().TableExpr("workspace_identities AS oi").
				Join("JOIN users AS u ON u.workspace_id = oi.workspace_id AND u.identity_id = oi.id").
				Where("oi.workspace_id = ? AND oi.type = ? AND oi.id = ?", session.WorkspaceID, domain.WorkspaceIdentityTypeUser, *session.AssigneeIdentityID)
		} else {
			reason = domain.ServiceAttentionQueueWaiting
			recipients = queueReminderRecipients(tx, session.WorkspaceID, session.TeamID)
		}
		var userIDs []string
		if err := recipients.ColumnExpr("u.id").Scan(ctx, &userIDs); err != nil {
			return fmt.Errorf("load service session reminder recipients: %w", err)
		}
		// 没有收件人时不记录提醒，由之后的扫描在有人工作时补发。
		if len(userIDs) == 0 {
			return nil
		}
		if err := servicestate.Begin(session).Remind(loaded.Now).Save(ctx, tx, w.enqueuer); err != nil {
			return err
		}
		for _, userID := range userIDs {
			if err := notificationtask.EnqueueServiceAttention(ctx, tx, w.enqueuer, session, userID, reason); err != nil {
				return err
			}
		}
		slog.InfoContext(logscope.WithWorkspace(ctx, session.WorkspaceID), "客服处理周期等待超时，已提醒",
			"conversation_id", session.ConversationID,
			"service_session_id", session.ID, "reason", reason, "recipient_count", len(userIDs))
		return nil
	})
}
