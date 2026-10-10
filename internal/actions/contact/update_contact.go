//go:build server

package contact

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// UpdateContactAction 修改外部联系人。
type UpdateContactAction struct {
	db *bun.DB
}

// NewUpdateContactAction 创建联系人修改操作。
func NewUpdateContactAction(db *bun.DB) *UpdateContactAction {
	return &UpdateContactAction{db: db}
}

// Execute 校验并更新当前企业的联系人及联系方式。
func (a *UpdateContactAction) Execute(ctx context.Context, identity *servermodels.Identity, contactID string, input ContactInput) (*ContactDetail, error) {
	input, fields := normalizeContactInput(input)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}

	var detail *ContactDetail
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		var stored struct {
			SourceChannelID string `bun:"source_channel_id"`
			DisplayName     string `bun:"display_name"`
		}
		if err := tx.NewSelect().
			TableExpr("contacts AS c").
			Column("c.source_channel_id").
			ColumnExpr("COALESCE(c.display_name, '') AS display_name").
			Where("id = ?", contactID).
			Where("workspace_id = ?", identity.Workspace.ID).
			Where("deleted_at IS NULL").
			For("UPDATE").
			Scan(ctx, &stored); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if stored.SourceChannelID != input.ChannelID {
			return &ValidationError{Fields: map[string]ValidationCode{"channelId": ValidationChannelImmutable}}
		}
		query := tx.NewUpdate().
			Table("contacts").
			Set("stage = ?", input.Stage)
		if input.DisplayName != "" {
			query = query.Set("display_name = ?", input.DisplayName)
		} else {
			query = query.Set("display_name = NULL")
		}
		if input.Notes != "" {
			query = query.Set("notes = ?", input.Notes)
		} else {
			query = query.Set("notes = NULL")
		}
		_, err := query.
			Where("id = ?", contactID).
			Where("workspace_id = ?", identity.Workspace.ID).
			Where("deleted_at IS NULL").
			Exec(ctx)
		if err != nil {
			return err
		}
		primaryEmailChanged, err := replaceMethods(ctx, tx, identity.Workspace.ID, contactID, input.Methods)
		if err != nil {
			return err
		}
		// 档案名称或首选邮箱变化会改变成员界面名称，在联系人写入完成后推进其全部客户会话的版本。
		if stored.DisplayName != input.DisplayName || primaryEmailChanged {
			if err := chatstate.TouchContactProfileConversations(ctx, tx, identity.Workspace.ID, contactID); err != nil {
				return err
			}
		}
		loaded, err := loadContactDetail(ctx, tx, identity.Workspace.ID, contactID)
		if err != nil {
			return err
		}
		detail = loaded
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("update contact: %w", err)
	}
	return detail, nil
}
