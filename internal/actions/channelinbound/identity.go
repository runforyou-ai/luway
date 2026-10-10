//go:build server

package channelinbound

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ErrIdentityUnbound 表示服务员工的渠道身份尚未绑定成员。
var ErrIdentityUnbound = errors.New("channel identity is not bound to a member")

// Attachment 定义入站附件的元数据与内容存储位置。
type Attachment struct {
	conversationaction.MessageAttachment
	StorageBackend domain.FileStorageBackend `bun:"storage_backend"`
	StorageKey     string                    `bun:"storage_key"`
}

// LoadBoundIdentity 读取并锁定服务员工渠道中已绑定在职成员的渠道身份，渠道身份不存在、未绑定或成员已停用时返回 ErrIdentityUnbound。
func LoadBoundIdentity(ctx context.Context, db bun.IDB, channel *servermodels.Channel, externalID string) (*servermodels.ChannelIdentity, error) {
	identity := &servermodels.ChannelIdentity{}
	err := db.NewSelect().Model(identity).
		Join("JOIN users AS u ON u.workspace_id = ci.workspace_id AND u.identity_id = ci.user_identity_id").
		Where("ci.workspace_id = ? AND ci.channel_id = ? AND ci.external_id = ?", channel.WorkspaceID, channel.ID, externalID).
		Where("u.status = ?", domain.IdentityStatusActive).
		For("UPDATE OF ci").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrIdentityUnbound
	}
	if err != nil {
		return nil, fmt.Errorf("load bound channel identity: %w", err)
	}
	return identity, nil
}
