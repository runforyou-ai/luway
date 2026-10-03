//go:build server

package platform

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/runforyou-ai/luway/internal/actions/credit"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// maxCreditNoteLength 是积分调整备注的最大字符数。
const maxCreditNoteLength = 500

const (
	ValidationDailyCreditGrantInvalid common.FieldCode = "PLATFORM_DAILY_CREDIT_GRANT_INVALID"
	ValidationCreditAmountInvalid     common.FieldCode = "PLATFORM_CREDIT_AMOUNT_INVALID"
	ValidationCreditNoteInvalid       common.FieldCode = "PLATFORM_CREDIT_NOTE_INVALID"
)

// UpdateDailyCreditGrantAction 修改每个工作区每天赠送的积分。
type UpdateDailyCreditGrantAction struct {
	db *bun.DB
}

// NewUpdateDailyCreditGrantAction 创建每日赠送积分修改操作。
func NewUpdateDailyCreditGrantAction(db *bun.DB) *UpdateDailyCreditGrantAction {
	return &UpdateDailyCreditGrantAction{db: db}
}

// Execute 校验积分后由仍有效的平台管理员保存每日赠送积分，从工作区下一次发放起生效。
func (a *UpdateDailyCreditGrantAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, amount int64) (Settings, error) {
	if amount < 0 || amount > domain.MaxCreditAmount {
		return Settings{}, &common.FieldError{Fields: map[string]common.FieldCode{"dailyCreditGrant": ValidationDailyCreditGrantInvalid}}
	}
	return updatePlatform(ctx, a.db, operator, func(ctx context.Context, tx bun.Tx, platform *servermodels.Platform) error {
		platform.DailyCreditGrant = amount
		_, err := tx.NewUpdate().Model(platform).
			Column("daily_credit_grant").
			Set("updated_at = now()").
			WherePK().
			Exec(ctx)
		return err
	})
}

// WorkspaceCreditsQuery 读取工作区的积分余额与流水。
type WorkspaceCreditsQuery struct {
	db *bun.DB
}

// NewWorkspaceCreditsQuery 创建工作区积分查询。
func NewWorkspaceCreditsQuery(db *bun.DB) *WorkspaceCreditsQuery {
	return &WorkspaceCreditsQuery{db: db}
}

// Balance 返回工作区的可用积分与今天的每日赠送，工作区不存在时返回 ErrWorkspaceNotFound。
func (q *WorkspaceCreditsQuery) Balance(ctx context.Context, workspaceID string) (credit.Balance, error) {
	if err := requireWorkspace(ctx, q.db, workspaceID); err != nil {
		return credit.Balance{}, err
	}
	return credit.GetBalance(ctx, q.db, workspaceID)
}

// Entries 按发生时间倒序返回工作区积分流水，工作区不存在时返回 ErrWorkspaceNotFound。
func (q *WorkspaceCreditsQuery) Entries(ctx context.Context, workspaceID string, page, pageSize int) (credit.EntryList, error) {
	if err := requireWorkspace(ctx, q.db, workspaceID); err != nil {
		return credit.EntryList{}, err
	}
	return credit.ListEntries(ctx, q.db, workspaceID, page, pageSize)
}

// AdjustWorkspaceCreditsAction 由平台管理员手动增加或扣减工作区积分。
type AdjustWorkspaceCreditsAction struct {
	db *bun.DB
}

// NewAdjustWorkspaceCreditsAction 创建工作区积分调整操作。
func NewAdjustWorkspaceCreditsAction(db *bun.DB) *AdjustWorkspaceCreditsAction {
	return &AdjustWorkspaceCreditsAction{db: db}
}

// Execute 校验积分与备注后由仍有效的平台管理员调整工作区积分，返回实际变动积分；扣减最多扣到余额为 0，余额为 0 时返回 credit.ErrInsufficient。
func (a *AdjustWorkspaceCreditsAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, workspaceID string, amount int64, note string) (int64, error) {
	note = strings.TrimSpace(note)
	fields := map[string]common.FieldCode{}
	if amount == 0 || amount > domain.MaxCreditAmount || amount < -domain.MaxCreditAmount {
		fields["amount"] = ValidationCreditAmountInvalid
	}
	if note == "" || utf8.RuneCountInString(note) > maxCreditNoteLength {
		fields["note"] = ValidationCreditNoteInvalid
	}
	if len(fields) > 0 {
		return 0, &common.FieldError{Fields: fields}
	}
	if !common.ValidUUID(workspaceID) {
		return 0, ErrWorkspaceNotFound
	}
	var applied int64
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := LockAdmin(ctx, tx, operator); err != nil {
			return err
		}
		if err := requireWorkspace(ctx, tx, workspaceID); err != nil {
			return err
		}
		var err error
		applied, err = credit.Adjust(ctx, tx, workspaceID, operator.Account.ID, amount, note, time.Now())
		return err
	})
	if err != nil {
		return 0, err
	}
	return applied, nil
}

// requireWorkspace 确认工作区存在且在平台工作区列表中，不存在时返回 ErrWorkspaceNotFound。
func requireWorkspace(ctx context.Context, db bun.IDB, workspaceID string) error {
	if !common.ValidUUID(workspaceID) {
		return ErrWorkspaceNotFound
	}
	var id string
	err := db.NewSelect().Model((*servermodels.Organization)(nil)).
		ColumnExpr("o.id::text").
		Where("o.id = ?", workspaceID).
		Where("o.lifecycle_status IN (?)", bun.In(listedLifecycleStatuses)).
		Scan(ctx, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrWorkspaceNotFound
	}
	return err
}
