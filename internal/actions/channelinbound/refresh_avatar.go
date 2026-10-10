//go:build server

package channelinbound

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// RefreshAvatarActionName 是渠道身份头像同步任务的 Action 名称。
const RefreshAvatarActionName = "channel.refresh_avatar"

// avatarRefreshInterval 是同一渠道身份两次头像同步之间的最短间隔。
const avatarRefreshInterval = "24 hours"

// avatarRefreshTimeout 是读取与下载一次平台头像的总时限。
const avatarRefreshTimeout = 15 * time.Second

// RefreshAvatarInput 是同步单个渠道身份头像的任务参数，AccountID 是发起同步时渠道连接的平台账号。
type RefreshAvatarInput struct {
	WorkspaceID       string `json:"workspaceId"`
	ChannelID         string `json:"channelId"`
	AccountID         string `json:"accountId"`
	ChannelIdentityID string `json:"channelIdentityId"`
}

// avatarImporter 把下载的头像写为可激活的工作区文件。
type avatarImporter interface {
	Execute(context.Context, fileaction.ImportInput) (*servermodels.File, error)
}

// RefreshAvatarAction 按渠道适配器读取对方当前头像并写入渠道身份。
type RefreshAvatarAction struct {
	db       *bun.DB
	adapters *channeladapter.Registry
	files    avatarImporter
}

// NewRefreshAvatarAction 创建渠道身份头像同步任务。
func NewRefreshAvatarAction(db *bun.DB, adapters *channeladapter.Registry, files avatarImporter) *RefreshAvatarAction {
	return &RefreshAvatarAction{db: db, adapters: adapters, files: files}
}

// Execute 在渠道仍接待客户且平台账号未变化时同步头像；平台读取失败只记录日志，写入失败返回错误。
func (a *RefreshAvatarAction) Execute(ctx context.Context, input RefreshAvatarInput) error {
	var source struct {
		Type            domain.ChannelType `bun:"type"`
		CreatedByUserID string             `bun:"created_by_user_id"`
		ExternalID      string             `bun:"external_id"`
	}
	err := a.db.NewSelect().TableExpr("channel_identities AS ci").
		ColumnExpr("c.type, c.created_by_user_id, ci.external_id").
		Join("JOIN channels AS c ON c.id = ci.channel_id AND c.workspace_id = ci.workspace_id").
		Where("ci.id = ? AND ci.workspace_id = ? AND ci.channel_id = ?", input.ChannelIdentityID, input.WorkspaceID, input.ChannelID).
		Where("c.provider_account_id = ?", input.AccountID).
		Where(channelaction.AcceptsCustomersCondition("c")).
		Scan(ctx, &source)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load channel identity avatar source: %w", err)
	}
	avatars, ok := channeladapter.Lookup[channeladapter.Avatars](a.adapters, source.Type)
	if !ok {
		return nil
	}
	target := channeladapter.Target{WorkspaceID: input.WorkspaceID, ChannelID: input.ChannelID, AccountID: input.AccountID, Recipient: source.ExternalID}
	refreshCtx, cancel := context.WithTimeout(ctx, avatarRefreshTimeout)
	defer cancel()
	avatar, err := avatars.CurrentAvatar(refreshCtx, target)
	if err != nil {
		slog.WarnContext(ctx, "读取渠道身份头像失败", "channel_id", input.ChannelID, "error", err)
		return nil
	}
	if avatar == nil {
		return a.apply(ctx, input, nil)
	}
	existing, err := a.findFile(ctx, input.WorkspaceID, avatar.ID)
	if err != nil {
		return err
	}
	if existing != nil {
		return a.apply(ctx, input, existing)
	}
	image, err := avatars.DownloadAvatar(refreshCtx, target, *avatar)
	if err != nil {
		slog.WarnContext(ctx, "下载渠道身份头像失败", "channel_id", input.ChannelID, "error", err)
		return nil
	}
	// 按头像内容类型确定固定文件名。
	fileName := "avatar.jpg"
	switch image.ContentType {
	case "image/png":
		fileName = "avatar.png"
	case "image/webp":
		fileName = "avatar.webp"
	}
	imported, err := a.files.Execute(ctx, fileaction.ImportInput{
		WorkspaceID: input.WorkspaceID, CreatedByUserID: source.CreatedByUserID, ExternalID: avatar.ID,
		FileName: fileName, ContentType: image.ContentType, Data: image.Data,
	})
	if err != nil {
		return err
	}
	return a.apply(ctx, input, imported)
}

// findFile 按头像的平台稳定编号复用已写入的工作区文件。
func (a *RefreshAvatarAction) findFile(ctx context.Context, workspaceID, externalID string) (*servermodels.File, error) {
	record := &servermodels.File{}
	err := a.db.NewSelect().Model(record).
		Where("f.workspace_id = ?", workspaceID).
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
		return nil, fmt.Errorf("find channel identity avatar file: %w", err)
	}
	return record, nil
}

// apply 原子切换渠道身份的头像文件引用、回收不再引用的旧文件，并推进该渠道身份所在会话的版本；next 为空时清除头像。
func (a *RefreshAvatarAction) apply(ctx context.Context, input RefreshAvatarInput, next *servermodels.File) error {
	workspaceID, identityID := input.WorkspaceID, input.ChannelIdentityID
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		current := &servermodels.ChannelIdentity{}
		if err := tx.NewSelect().Model(current).
			Column("id", "workspace_id", "channel_id", "avatar_file_id").
			Where("ci.id = ? AND ci.workspace_id = ? AND ci.channel_id = ?", identityID, workspaceID, input.ChannelID).
			For("UPDATE").
			Scan(ctx); err != nil {
			return fmt.Errorf("lock channel identity avatar: %w", err)
		}
		if (next == nil && current.AvatarFileID == nil) || (next != nil && current.AvatarFileID != nil && *current.AvatarFileID == next.ID) {
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
			Where("f.workspace_id = ? AND f.id IN (?)", workspaceID, bun.List(fileIDs)).
			OrderExpr("f.id").For("UPDATE").Scan(ctx); err != nil {
			return fmt.Errorf("lock channel identity avatar files: %w", err)
		}

		var nextFileID any
		if next != nil {
			result, err := tx.NewUpdate().Model((*servermodels.File)(nil)).
				Set("status = ?", domain.FileStatusActive).
				Set("expires_at = NULL").
				Where("id = ? AND workspace_id = ? AND purpose = ?", next.ID, workspaceID, domain.FilePurposeContactAvatar).
				Where("(status = ? OR (status = ? AND expires_at > now()))", domain.FileStatusActive, domain.FileStatusUploaded).
				Exec(ctx)
			if err != nil {
				return fmt.Errorf("activate channel identity avatar file: %w", err)
			}
			if rows, err := result.RowsAffected(); err != nil {
				return fmt.Errorf("read channel identity avatar activation count: %w", err)
			} else if rows == 0 {
				return fileaction.ErrFileNotFound
			}
			nextFileID = next.ID
		}

		if _, err := tx.NewUpdate().Model((*servermodels.ChannelIdentity)(nil)).
			Set("avatar_file_id = ?", nextFileID).
			Where("id = ? AND workspace_id = ?", identityID, workspaceID).
			Exec(ctx); err != nil {
			return fmt.Errorf("update channel identity avatar: %w", err)
		}
		if previous := current.AvatarFileID; previous != nil {
			if _, err := tx.NewUpdate().Model((*servermodels.File)(nil)).
				Set("status = ?", domain.FileStatusDeleting).
				Set("expires_at = now()").
				Where("id = ? AND workspace_id = ? AND purpose = ? AND status = ?", *previous, workspaceID, domain.FilePurposeContactAvatar, domain.FileStatusActive).
				Where("NOT EXISTS (SELECT 1 FROM channel_identities AS other WHERE other.workspace_id = f.workspace_id AND other.avatar_file_id = f.id)").
				Exec(ctx); err != nil {
				return fmt.Errorf("retire previous channel identity avatar: %w", err)
			}
		}
		return chatstate.TouchChannelIdentityConversations(ctx, tx, workspaceID, identityID)
	})
}
