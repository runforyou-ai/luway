//go:build server

package knowledgegap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// AcceptInput 定义加入知识库的问答：EntryID 为空时在知识库中新建问答，否则更新该问答；AddToEvaluation 为 true 时同时把提问加入负责 AI 员工的评测。
type AcceptInput struct {
	KnowledgeBaseID string
	EntryID         string
	QA              knowledgebase.QAInput
	AddToEvaluation bool
}

// EvaluationCases 在待补知识加入知识库的事务中把提问加入负责 AI 员工的评测，标准答案为保存的问答答案。
type EvaluationCases interface {
	AddFromKnowledgeGap(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, gap *servermodels.KnowledgeGap, expectedAnswer string) error
}

// AcceptAction 把待补知识整理的问答加入知识库。
type AcceptAction struct {
	db         *bun.DB
	save       *knowledgebase.SaveQAEntryAction
	evaluation EvaluationCases
}

// NewAcceptAction 创建加入知识库操作。
func NewAcceptAction(db *bun.DB, save *knowledgebase.SaveQAEntryAction, evaluation EvaluationCases) *AcceptAction {
	return &AcceptAction{db: db, save: save, evaluation: evaluation}
}

// Execute 在同一事务中保存问答、按需加入评测并把待补知识记为已加入知识库；待处理与已忽略的条目都可以加入，操作者须负责接待 AI 员工或拥有 AI 员工管理权限。
func (a *AcceptAction) Execute(ctx context.Context, identity *servermodels.Identity, id string, input AcceptInput) error {
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		gap, err := lockGap(ctx, tx, identity, id)
		if err != nil {
			return err
		}
		if domain.KnowledgeGapStatus(gap.Status) == domain.KnowledgeGapStatusAccepted {
			return ErrHandled
		}
		entry, err := a.save.ExecuteInTx(ctx, tx, identity, input.KnowledgeBaseID, input.EntryID, input.QA)
		if err != nil {
			return err
		}
		if input.AddToEvaluation {
			if err := a.evaluation.AddFromKnowledgeGap(ctx, tx, identity, gap, entry.Answer); err != nil {
				return err
			}
		}
		if _, err := tx.NewUpdate().Model(gap).
			Set("status = ?", domain.KnowledgeGapStatusAccepted).
			Set("knowledge_base_id = ?", input.KnowledgeBaseID).
			Set("qa_entry_id = ?", entry.ID).
			Set("handled_by_identity_id = ?", identity.WorkspaceIdentity.ID).
			Set("handled_at = now()").
			WherePK().
			Exec(ctx); err != nil {
			return fmt.Errorf("accept knowledge gap: %w", err)
		}
		realtime.Notify(ctx, realtime.ServiceInboxKnowledgeGapsChanged(identity.Workspace.ID))
		return nil
	})
}

// DismissAction 忽略待补知识。
type DismissAction struct{ db *bun.DB }

// NewDismissAction 创建忽略待补知识操作。
func NewDismissAction(db *bun.DB) *DismissAction { return &DismissAction{db: db} }

// Execute 把待处理的条目记为已忽略；已忽略的条目保持不变，已加入知识库的条目不能忽略，操作者须负责接待 AI 员工或拥有 AI 员工管理权限。
func (a *DismissAction) Execute(ctx context.Context, identity *servermodels.Identity, id string) error {
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		gap, err := lockGap(ctx, tx, identity, id)
		if err != nil {
			return err
		}
		switch domain.KnowledgeGapStatus(gap.Status) {
		case domain.KnowledgeGapStatusAccepted:
			return ErrHandled
		case domain.KnowledgeGapStatusDismissed:
			return nil
		}
		if _, err := tx.NewUpdate().Model(gap).
			Set("status = ?", domain.KnowledgeGapStatusDismissed).
			Set("handled_by_identity_id = ?", identity.WorkspaceIdentity.ID).
			Set("handled_at = now()").
			WherePK().
			Exec(ctx); err != nil {
			return fmt.Errorf("dismiss knowledge gap: %w", err)
		}
		realtime.Notify(ctx, realtime.ServiceInboxKnowledgeGapsChanged(identity.Workspace.ID))
		return nil
	})
}

// lockGap 读取并锁定当前企业的待补知识，当前成员不能处理时返回 ErrForbidden。
func lockGap(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, id string) (*servermodels.KnowledgeGap, error) {
	gap := &servermodels.KnowledgeGap{}
	err := tx.NewSelect().Model(gap).Where("kg.workspace_id = ? AND kg.id = ?", identity.Workspace.ID, id).For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock knowledge gap: %w", err)
	}
	handleable, err := tx.NewSelect().TableExpr("service_sessions AS ss").
		Where("ss.workspace_id = ? AND ss.id = ?", identity.Workspace.ID, gap.ServiceSessionID).
		Where("?", handleableSQL(identity)).
		Exists(ctx)
	if err != nil {
		return nil, fmt.Errorf("check knowledge gap handler: %w", err)
	}
	if !handleable {
		return nil, ErrForbidden
	}
	return gap, nil
}
