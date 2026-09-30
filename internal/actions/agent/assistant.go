//go:build server

package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

var (
	// ErrAssistantNotFound 表示当前企业中不存在指定助理，或当前成员不是其主人。
	ErrAssistantNotFound = errors.New("assistant not found")
	// ErrAssistantDeviceNotFound 表示当前成员名下不存在指定的未撤销电脑。
	ErrAssistantDeviceNotFound = errors.New("assistant device not found")
	// ErrAssistantOwnerInactive 表示助理主人已停用，助理不能恢复。
	ErrAssistantOwnerInactive = errors.New("assistant owner inactive")
)

// AssistantInput 定义助理的资料、执行配置与企业 MCP 服务，AvatarFileID 为空时保留当前头像。
type AssistantInput struct {
	DisplayName  string
	AvatarFileID string
	Execution    ExecutionInput
	MCPServerIDs []string
}

// Assistant 定义助理信息。
type Assistant struct {
	ID                string                  `bun:"id"`
	IdentityID        string                  `bun:"identity_id"`
	DisplayName       string                  `bun:"display_name"`
	AvatarFileID      *string                 `bun:"avatar_file_id"`
	OwnerUserID       string                  `bun:"owner_user_id"`
	OwnerIdentityID   string                  `bun:"owner_identity_id"`
	OwnerDisplayName  string                  `bun:"owner_display_name"`
	DeviceID          string                  `bun:"device_id"`
	DeviceName        string                  `bun:"device_name"`
	DeviceRevokedAt   *time.Time              `bun:"device_revoked_at"`
	DeviceLastSeenAt  *time.Time              `bun:"device_last_seen_at"`
	DeviceLocalAgents []domain.LocalAgentKind `bun:"device_local_agents,type:jsonb"`
	Status            domain.IdentityStatus   `bun:"status"`
	PausedAt          *time.Time              `bun:"paused_at"`
	CreatedAt         time.Time               `bun:"created_at"`
	Execution         ExecutionSummary        `bun:"-"`
}

// Presence 按账号状态、暂停、绑定电脑的撤销状态与最近在线时间计算助理当前是否可以处理新请求。
func (a Assistant) Presence(now time.Time) domain.AssistantPresence {
	return domain.ResolveAssistantPresence(a.Status, a.PausedAt != nil, a.DeviceRevokedAt != nil, a.DeviceLastSeenAt, now)
}

// assistantQuery 构造助理及其主人、绑定电脑的读取查询。
func assistantQuery(db bun.IDB, organizationID string) *bun.SelectQuery {
	return db.NewSelect().TableExpr("agents AS a").
		ColumnExpr("a.id::text AS id, a.identity_id::text AS identity_id, oi.display_name, oi.avatar_file_id::text AS avatar_file_id, a.status, a.paused_at, oi.created_at").
		ColumnExpr("a.owner_user_id::text AS owner_user_id, owner.id::text AS owner_identity_id, owner.display_name AS owner_display_name").
		ColumnExpr("a.device_id::text AS device_id, d.name AS device_name, d.revoked_at AS device_revoked_at, d.last_seen_at AS device_last_seen_at, d.local_agents AS device_local_agents").
		Join("JOIN organization_identities AS oi ON oi.id = a.identity_id AND oi.organization_id = a.organization_id AND oi.type = ?", domain.OrganizationIdentityTypeAssistant).
		Join("JOIN users AS u ON u.id = a.owner_user_id AND u.organization_id = a.organization_id").
		Join("JOIN organization_identities AS owner ON owner.id = u.identity_id AND owner.organization_id = u.organization_id").
		Join("JOIN devices AS d ON d.id = a.device_id AND d.organization_id = a.organization_id").
		Where("a.organization_id = ?", organizationID)
}

// loadAssistant 读取当前企业中的助理详情，ownerUserID 非空时只返回该成员名下的助理。
func loadAssistant(ctx context.Context, db bun.IDB, organizationID, assistantID, ownerUserID string) (*Assistant, error) {
	if !common.ValidUUID(assistantID) {
		return nil, ErrAssistantNotFound
	}
	query := assistantQuery(db, organizationID).Where("a.id = ?", assistantID)
	if ownerUserID != "" {
		query = query.Where("a.owner_user_id = ?", ownerUserID)
	}
	assistant := &Assistant{}
	if err := query.Scan(ctx, assistant); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAssistantNotFound
	} else if err != nil {
		return nil, err
	}
	summaries, err := loadAgentExecutionSummaries(ctx, db, organizationID, []string{assistant.ID})
	if err != nil {
		return nil, err
	}
	assistant.Execution = summaries[assistant.ID]
	return assistant, nil
}

// lockOwnAssistant 锁定当前成员名下的助理记录。
func lockOwnAssistant(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, assistantID string) (*servermodels.Agent, error) {
	if !common.ValidUUID(assistantID) {
		return nil, ErrAssistantNotFound
	}
	stored := &servermodels.Agent{}
	err := tx.NewSelect().Model(stored).
		Join("JOIN organization_identities AS oi ON oi.id = a.identity_id AND oi.organization_id = a.organization_id AND oi.type = ?", domain.OrganizationIdentityTypeAssistant).
		Where("a.organization_id = ? AND a.id = ? AND a.owner_user_id = ?", identity.Organization.ID, assistantID, identity.User.ID).
		For("UPDATE OF a").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAssistantNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock assistant: %w", err)
	}
	return stored, nil
}

// lockOwnActiveDevice 对当前成员名下未撤销的电脑取共享锁。
func lockOwnActiveDevice(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, deviceID string) error {
	if !common.ValidUUID(deviceID) {
		return ErrAssistantDeviceNotFound
	}
	exists, err := tx.NewSelect().Model((*servermodels.Device)(nil)).
		Where("d.organization_id = ? AND d.user_id = ? AND d.id = ? AND d.revoked_at IS NULL", identity.Organization.ID, identity.User.ID, deviceID).
		For("SHARE").
		Exists(ctx)
	if err != nil {
		return fmt.Errorf("lock assistant device: %w", err)
	}
	if !exists {
		return ErrAssistantDeviceNotFound
	}
	return nil
}
