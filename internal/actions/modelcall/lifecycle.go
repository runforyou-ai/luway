//go:build server

package modelcall

import (
	"context"
	"database/sql"
	"errors"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// Facts 定义模型调用的身份、用量与上游成功事实。
type Facts struct {
	CallID      string
	WorkspaceID string
	ModelID     string
	Scope       domain.AIModelScope
	Usage       Usage
	Succeeded   bool
}

// Lifecycle 在调用记录的事务内处理调用开始与结束。
type Lifecycle interface {
	Begin(context.Context, bun.Tx, Facts) error
	Finish(context.Context, bun.Tx, Facts) error
}

// Rejection 表示携带稳定原因码和本地化词条标识的调用拒绝。
type Rejection struct {
	Reason     string
	MessageKey string
}

// Error 返回调用拒绝的语言无关原因码。
func (e *Rejection) Error() string { return e.Reason }

// lockRunning 锁定进行中的调用，已经结束或不存在时返回空值。
func lockRunning(ctx context.Context, tx bun.Tx, id string) (*servermodels.AIModelCall, error) {
	record := &servermodels.AIModelCall{}
	err := tx.NewSelect().Model(record).Where("amc.id = ?", id).For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if record.Status != string(domain.AIModelCallStatusRunning) {
		return nil, nil
	}
	return record, nil
}

// finishLocked 在已锁定调用的事务内处理结束回调并写入调用终态。
func (i *Invoker) finishLocked(ctx context.Context, tx bun.Tx, record *servermodels.AIModelCall, usage Usage, status domain.AIModelCallStatus, message string, succeeded bool) error {
	if i.lifecycle != nil {
		if err := i.lifecycle.Finish(ctx, tx, Facts{CallID: record.ID, WorkspaceID: record.WorkspaceID, ModelID: record.ModelID, Scope: domain.AIModelScope(record.ModelScope), Usage: usage, Succeeded: succeeded}); err != nil {
			return err
		}
	}
	_, err := tx.NewUpdate().Model(record).
		Set("status = ?", status).
		Set("input_tokens = ?", usage.InputTokens).
		Set("cached_input_tokens = ?", usage.CachedInputTokens).
		Set("output_tokens = ?", usage.OutputTokens).
		Set("error_message = ?", message).
		Set("finished_at = now()").WherePK().Exec(ctx)
	return err
}
