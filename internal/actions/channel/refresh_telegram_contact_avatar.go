//go:build server

package channel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// RefreshTelegramContactAvatarActionName 是 Telegram 联系人头像同步任务的 Action 名称。
const RefreshTelegramContactAvatarActionName = "telegram.refresh_contact_avatar"

const telegramAvatarRefreshTimeout = 15 * time.Second

// RefreshTelegramContactAvatarInput 定义同步单个 Telegram 渠道身份头像的任务参数。
type RefreshTelegramContactAvatarInput struct {
	OrganizationID    string `json:"organizationId"`
	ChannelID         string `json:"channelId"`
	ChannelIdentityID string `json:"channelIdentityId"`
	SenderID          int64  `json:"senderId"`
}

// telegramContactAvatarImporter 把 Telegram 头像写为可激活的企业文件。
type telegramContactAvatarImporter interface {
	Execute(context.Context, fileaction.ImportInput) (*servermodels.File, error)
}

// RefreshTelegramContactAvatarAction 按入站消息同步 Telegram 渠道身份的头像。
type RefreshTelegramContactAvatarAction struct {
	db          *bun.DB
	avatarAPI   telegram.ProfilePhotoAPI
	avatarFiles telegramContactAvatarImporter
}

// NewRefreshTelegramContactAvatarAction 创建 Telegram 联系人头像同步操作。
func NewRefreshTelegramContactAvatarAction(db *bun.DB, avatarAPI telegram.ProfilePhotoAPI, avatarFiles telegramContactAvatarImporter) *RefreshTelegramContactAvatarAction {
	return &RefreshTelegramContactAvatarAction{db: db, avatarAPI: avatarAPI, avatarFiles: avatarFiles}
}

// Execute 使用渠道当前机器人凭据同步头像；渠道已停用或缺少凭据时结束，读取 Telegram 头像失败只记录日志，写入失败返回错误。
func (a *RefreshTelegramContactAvatarAction) Execute(ctx context.Context, input RefreshTelegramContactAvatarInput) error {
	var channel struct {
		CreatedByUserID string  `bun:"created_by_user_id"`
		BotToken        *string `bun:"bot_token"`
	}
	err := a.db.NewSelect().TableExpr("channels AS c").
		ColumnExpr("c.created_by_user_id, tcs.bot_token").
		Join("JOIN telegram_channel_settings AS tcs ON tcs.channel_id = c.id AND tcs.organization_id = c.organization_id").
		Where("c.id = ? AND c.organization_id = ? AND c.type = ? AND c.enabled", input.ChannelID, input.OrganizationID, domain.ChannelTypeTelegram).
		Scan(ctx, &channel)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (channel.BotToken == nil || *channel.BotToken == "")) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load Telegram avatar channel: %w", err)
	}
	refreshCtx, cancel := context.WithTimeout(ctx, telegramAvatarRefreshTimeout)
	defer cancel()
	err = a.refreshTelegramContactAvatar(refreshCtx, input.ChannelID, input.OrganizationID, channel.CreatedByUserID, input.ChannelIdentityID, *channel.BotToken, input.SenderID)
	var remote *telegramAvatarRemoteError
	if errors.As(err, &remote) {
		logTelegramRemoteFailure("读取 Telegram 用户头像失败", input.ChannelID, remote.err)
		return nil
	}
	return err
}

// telegramAvatarRemoteError 标记读取 Telegram 头像信息或内容时的远端失败。
type telegramAvatarRemoteError struct{ err error }

// Error 返回远端失败原因。
func (e *telegramAvatarRemoteError) Error() string { return e.err.Error() }

// Unwrap 返回远端失败原因。
func (e *telegramAvatarRemoteError) Unwrap() error { return e.err }

// refreshTelegramContactAvatar 读取 Telegram 用户当前头像并持久化到渠道身份。
func (a *RefreshTelegramContactAvatarAction) refreshTelegramContactAvatar(
	ctx context.Context,
	channelID, organizationID, createdByUserID, identityID, token string,
	senderID int64,
) error {
	photo, err := a.avatarAPI.GetUserProfilePhoto(ctx, token, senderID)
	if err != nil {
		return &telegramAvatarRemoteError{err: err}
	}
	if photo == nil {
		return a.applyTelegramContactAvatar(ctx, channelID, organizationID, identityID, nil)
	}
	existing, err := a.findTelegramContactAvatarFile(ctx, organizationID, photo.UniqueID)
	if err != nil {
		return err
	}
	if existing != nil {
		return a.applyTelegramContactAvatar(ctx, channelID, organizationID, identityID, existing)
	}
	if a.avatarFiles == nil {
		return errors.New("Telegram contact avatar importer is unavailable")
	}
	downloaded, err := a.avatarAPI.DownloadPhoto(ctx, token, photo.FileID)
	if err != nil {
		return &telegramAvatarRemoteError{err: err}
	}
	// 返回已校验头像内容的固定文件名。
	fileName := "telegram-avatar.jpg"
	switch downloaded.ContentType {
	case "image/png":
		fileName = "telegram-avatar.png"
	case "image/webp":
		fileName = "telegram-avatar.webp"
	}
	imported, err := a.avatarFiles.Execute(ctx, fileaction.ImportInput{
		OrganizationID: organizationID, CreatedByUserID: createdByUserID,
		ExternalID:  photo.UniqueID,
		FileName:    fileName,
		ContentType: downloaded.ContentType, Data: downloaded.Data,
	})
	if err != nil {
		return err
	}
	return a.applyTelegramContactAvatar(ctx, channelID, organizationID, identityID, imported)
}

// findTelegramContactAvatarFile 按 Telegram 文件唯一标识复用已写入的企业文件。
func (a *RefreshTelegramContactAvatarAction) findTelegramContactAvatarFile(ctx context.Context, organizationID, externalID string) (*servermodels.File, error) {
	record := &servermodels.File{}
	err := a.db.NewSelect().Model(record).
		Where("f.organization_id = ?", organizationID).
		Where("f.purpose = ?", domain.FilePurposeContactAvatar).
		Where("f.external_id = ?", externalID).
		Where("(f.status = ? OR (f.status = ? AND f.expires_at > now()))", domain.FileStatusActive, domain.FileStatusUploaded).
		OrderExpr("CASE WHEN f.status = ? THEN 0 ELSE 1 END", domain.FileStatusActive).
		OrderExpr("f.created_at DESC, f.id DESC").
		Limit(1).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find Telegram contact avatar file: %w", err)
	}
	return record, nil
}

// applyTelegramContactAvatar 原子切换头像文件引用、回收旧文件，并推进该渠道身份所在客户会话的版本。
func (a *RefreshTelegramContactAvatarAction) applyTelegramContactAvatar(
	ctx context.Context,
	channelID, organizationID, identityID string,
	next *servermodels.File,
) error {
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		current := &servermodels.ContactChannelIdentity{}
		if err := tx.NewSelect().Model(current).
			Column("id", "organization_id", "channel_id", "avatar_file_id").
			Where("cci.id = ?", identityID).
			Where("cci.organization_id = ?", organizationID).
			Where("cci.channel_id = ?", channelID).
			For("UPDATE").
			Scan(ctx); err != nil {
			return fmt.Errorf("lock Telegram contact avatar: %w", err)
		}
		if next == nil && current.AvatarFileID == nil {
			return nil
		}
		if next != nil && current.AvatarFileID != nil && *current.AvatarFileID == next.ID {
			return nil
		}

		// 按编号顺序锁定新旧头像文件。
		fileIDs := make([]string, 0, 2)
		if next != nil {
			fileIDs = append(fileIDs, next.ID)
		}
		if current.AvatarFileID != nil {
			fileIDs = append(fileIDs, *current.AvatarFileID)
		}
		var files []servermodels.File
		if err := tx.NewSelect().Model(&files).Column("id").
			Where("f.organization_id = ? AND f.id IN (?)", organizationID, bun.In(fileIDs)).
			OrderExpr("f.id").For("UPDATE").Scan(ctx); err != nil {
			return fmt.Errorf("lock Telegram contact avatar files: %w", err)
		}

		var nextFileID any
		if next != nil {
			result, err := tx.NewUpdate().Model((*servermodels.File)(nil)).
				Set("status = ?", domain.FileStatusActive).
				Set("expires_at = NULL").
				Set("updated_at = now()").
				Where("id = ?", next.ID).
				Where("organization_id = ?", organizationID).
				Where("purpose = ?", domain.FilePurposeContactAvatar).
				Where("(status = ? OR (status = ? AND expires_at > now()))", domain.FileStatusActive, domain.FileStatusUploaded).
				Exec(ctx)
			if err != nil {
				return fmt.Errorf("activate Telegram contact avatar file: %w", err)
			}
			rows, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("read Telegram contact avatar activation count: %w", err)
			}
			if rows == 0 {
				return fileaction.ErrFileNotFound
			}
			nextFileID = next.ID
		}

		previousFileID := current.AvatarFileID
		result, err := tx.NewUpdate().Model((*servermodels.ContactChannelIdentity)(nil)).
			Set("avatar_file_id = ?", nextFileID).
			Set("updated_at = now()").
			Where("id = ?", identityID).
			Where("organization_id = ?", organizationID).
			Where("channel_id = ?", channelID).
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("update Telegram contact avatar: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read Telegram contact avatar update count: %w", err)
		}
		if rows == 0 {
			return errors.New("Telegram contact avatar was not updated")
		}
		if previousFileID != nil && (next == nil || *previousFileID != next.ID) {
			if _, err := tx.NewUpdate().Model((*servermodels.File)(nil)).
				Set("status = ?", domain.FileStatusDeleting).
				Set("expires_at = now()").
				Set("updated_at = now()").
				Where("id = ?", *previousFileID).
				Where("organization_id = ?", organizationID).
				Where("purpose = ?", domain.FilePurposeContactAvatar).
				Where("status = ?", domain.FileStatusActive).
				Where("NOT EXISTS (SELECT 1 FROM contact_channel_identities AS other WHERE other.organization_id = f.organization_id AND other.avatar_file_id = f.id)").
				Exec(ctx); err != nil {
				return fmt.Errorf("retire previous Telegram contact avatar: %w", err)
			}
		}
		return chatstate.TouchChannelIdentityConversations(ctx, tx, organizationID, identityID)
	})
}
