//go:build server

package customerservice

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

const (
	ValidationReclaimNotAfterRemind ValidationCode = "SERVICE_TIMEOUT_RECLAIM_NOT_AFTER_REMINDER"
)

// LoadServiceTimeouts 读取企业客服超时时长。
func LoadServiceTimeouts(ctx context.Context, db bun.IDB, workspaceID string) (domain.ServiceTimeouts, error) {
	setting := &servermodels.CustomerServiceSetting{}
	if err := db.NewSelect().Model(setting).
		Column("response_reminder_minutes", "response_reclaim_minutes", "queue_reminder_minutes", "ai_follow_up_minutes", "ai_close_minutes").
		Where("css.workspace_id = ?", workspaceID).
		Scan(ctx); err != nil {
		return domain.ServiceTimeouts{}, fmt.Errorf("load service timeouts: %w", err)
	}
	return domain.ServiceTimeouts{
		ResponseReminderMinutes: setting.ResponseReminderMinutes,
		ResponseReclaimMinutes:  setting.ResponseReclaimMinutes,
		QueueReminderMinutes:    setting.QueueReminderMinutes,
		AIFollowUpMinutes:       setting.AIFollowUpMinutes,
		AICloseMinutes:          setting.AICloseMinutes,
	}, nil
}

// GetServiceTimeoutsQuery 读取当前企业的客服超时时长。
type GetServiceTimeoutsQuery struct {
	db *bun.DB
}

// NewGetServiceTimeoutsQuery 创建客服超时时长读取查询。
func NewGetServiceTimeoutsQuery(db *bun.DB) *GetServiceTimeoutsQuery {
	return &GetServiceTimeoutsQuery{db: db}
}

// Execute 返回当前企业的客服超时时长。
func (q *GetServiceTimeoutsQuery) Execute(ctx context.Context, identity *servermodels.Identity) (domain.ServiceTimeouts, error) {
	return LoadServiceTimeouts(ctx, q.db, identity.Workspace.ID)
}

// UpdateServiceTimeoutsAction 修改当前企业的客服超时时长。
type UpdateServiceTimeoutsAction struct {
	db *bun.DB
}

// NewUpdateServiceTimeoutsAction 创建客服超时时长修改操作。
func NewUpdateServiceTimeoutsAction(db *bun.DB) *UpdateServiceTimeoutsAction {
	return &UpdateServiceTimeoutsAction{db: db}
}

// Execute 校验回收时长大于提醒时长并保存客服超时时长。
func (a *UpdateServiceTimeoutsAction) Execute(ctx context.Context, identity *servermodels.Identity, input domain.ServiceTimeouts) (domain.ServiceTimeouts, error) {
	if input.ResponseReclaimMinutes <= input.ResponseReminderMinutes {
		return domain.ServiceTimeouts{}, &ValidationError{Fields: map[string]ValidationCode{"responseReclaimMinutes": ValidationReclaimNotAfterRemind}}
	}
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		setting := &servermodels.CustomerServiceSetting{
			WorkspaceID:             identity.Workspace.ID,
			ResponseReminderMinutes: input.ResponseReminderMinutes, ResponseReclaimMinutes: input.ResponseReclaimMinutes,
			QueueReminderMinutes: input.QueueReminderMinutes, AIFollowUpMinutes: input.AIFollowUpMinutes, AICloseMinutes: input.AICloseMinutes,
		}
		if err := saveSetting(ctx, tx, setting, "response_reminder_minutes", "response_reclaim_minutes", "queue_reminder_minutes", "ai_follow_up_minutes", "ai_close_minutes"); err != nil {
			return fmt.Errorf("save service timeouts: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.ServiceTimeouts{}, err
	}
	return input, nil
}
