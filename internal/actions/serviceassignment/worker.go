//go:build server

package serviceassignment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// Worker 执行客服处理周期的分配与成员补分配任务。
type Worker struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
}

// NewWorker 创建客服处理周期分配任务执行器。
func NewWorker(db *bun.DB, enqueuer servertask.TxEnqueuer) *Worker {
	return &Worker{db: db, enqueuer: enqueuer}
}

// Assign 为仍在队列中的客服处理周期挑选接待量最少的可分配成员；没有可分配成员时周期留在队列。
func (w *Worker) Assign(ctx context.Context, input AssignInput) error {
	queued := &servermodels.ServiceSession{}
	err := w.db.NewSelect().Model(queued).Column("ss.id", "ss.conversation_id", "ss.status", "ss.team_id", "ss.assignee_identity_id").
		Where("ss.workspace_id = ? AND ss.id = ?", input.WorkspaceID, input.ServiceSessionID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load queued service session: %w", err)
	}
	if domain.ServiceSessionStatus(queued.Status) != domain.ServiceSessionStatusOpen || queued.AssigneeIdentityID != nil {
		return nil
	}
	return realtime.RunInTx(ctx, w.db, func(ctx context.Context, tx bun.Tx) error {
		member, err := LockQueueMember(ctx, tx, input.WorkspaceID, queued.ID, queued.TeamID, input.ExcludeIdentityID)
		if err != nil || member == nil {
			return err
		}
		locked, err := chatstate.LockServiceSession(ctx, tx, input.WorkspaceID, queued.ConversationID)
		if err != nil {
			return err
		}
		session := locked.Session
		// 周期已关闭、已有负责人或队列已变化时由引起变化的操作负责后续分配。
		if session.ID != queued.ID || domain.ServiceSessionStatus(session.Status) != domain.ServiceSessionStatusOpen ||
			session.AssigneeIdentityID != nil || !chatstate.SameTeam(session.TeamID, queued.TeamID) {
			return nil
		}
		return Assign(ctx, tx, w.enqueuer, locked, member)
	})
}

// Backfill 按等待时间从成员所在团队队列和公共队列逐个补充分配，直到接待量达到上限或没有等待中的周期，成员自己发起的请求不补入；每个周期单独提交，已被其他操作处理的周期跳过后继续。
func (w *Worker) Backfill(ctx context.Context, input BackfillInput) error {
	var userID string
	err := w.db.NewSelect().Model((*servermodels.User)(nil)).Column("u.id").
		Where("u.workspace_id = ? AND u.identity_id = ?", input.WorkspaceID, input.IdentityID).
		Scan(ctx, &userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load backfill member: %w", err)
	}
	skipped := make([]string, 0, 1)
	if input.ExcludeServiceSessionID != "" {
		skipped = append(skipped, input.ExcludeServiceSessionID)
	}
	assignedCount := 0
	for {
		outcome := backfillFinished
		err := realtime.RunInTx(ctx, w.db, func(ctx context.Context, tx bun.Tx) error {
			member, err := lockMember(ctx, tx, input.WorkspaceID, userID, func(query *bun.SelectQuery) *bun.SelectQuery { return query })
			if err != nil || member == nil {
				return err
			}
			queued := &servermodels.ServiceSession{}
			query := tx.NewSelect().Model(queued).Column("ss.id", "ss.conversation_id", "ss.team_id").
				Where("ss.workspace_id = ? AND ss.status = ? AND ss.assignee_identity_id IS NULL", input.WorkspaceID, domain.ServiceSessionStatusOpen).
				Where("ss.team_id IS NULL OR EXISTS (SELECT 1 FROM team_members AS tm WHERE tm.workspace_id = ss.workspace_id AND tm.team_id = ss.team_id AND tm.identity_id = ?)", member.IdentityID).
				// 成员不补入自己发起的服务请求。
				Where(`NOT EXISTS (
					SELECT 1 FROM service_conversations AS requested_svc
					JOIN chat_subjects AS requested_cs ON requested_cs.workspace_id = requested_svc.workspace_id AND requested_cs.id = requested_svc.requester_subject_id
					WHERE requested_svc.workspace_id = ss.workspace_id AND requested_svc.id = ss.service_conversation_id AND requested_cs.kind = ? AND requested_cs.source_id = ?)`,
					domain.ChatSubjectKindWorkspaceIdentity, member.IdentityID).
				OrderExpr("ss.awaiting_reply_since ASC NULLS LAST, ss.created_at ASC, ss.id ASC").
				Limit(1)
			if len(skipped) > 0 {
				query = query.Where("ss.id NOT IN (?)", bun.List(skipped))
			}
			err = query.Scan(ctx)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("load next queued service session: %w", err)
			}
			locked, err := chatstate.LockServiceSession(ctx, tx, input.WorkspaceID, queued.ConversationID)
			if err != nil {
				return err
			}
			session := locked.Session
			// 读取后周期已被分配、关闭或换队列时跳过该周期，继续补下一条。
			if session.ID != queued.ID || domain.ServiceSessionStatus(session.Status) != domain.ServiceSessionStatusOpen ||
				session.AssigneeIdentityID != nil || !chatstate.SameTeam(session.TeamID, queued.TeamID) {
				skipped = append(skipped, queued.ID)
				outcome = backfillSkipped
				return nil
			}
			if err := Assign(ctx, tx, w.enqueuer, locked, member); err != nil {
				return err
			}
			outcome = backfillAssigned
			return nil
		})
		if err != nil {
			return err
		}
		switch outcome {
		case backfillAssigned:
			assignedCount++
		case backfillFinished:
			if assignedCount > 0 {
				slog.InfoContext(logscope.WithWorkspace(ctx, input.WorkspaceID), "成员补分配完成", "identity_id", input.IdentityID, "assigned_count", assignedCount)
			}
			return nil
		}
	}
}

// backfillOutcome 表示补分配单次事务的结果。
type backfillOutcome int

const (
	// backfillFinished 表示成员已不可分配或没有等待中的周期。
	backfillFinished backfillOutcome = iota
	// backfillAssigned 表示本次事务分配了一条周期。
	backfillAssigned
	// backfillSkipped 表示本次读取的周期已被其他操作处理。
	backfillSkipped
)
