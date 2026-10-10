//go:build server

package channel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/runforyou-ai/luway/internal/actions/agentcancel"
	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

// WeComBotChannelDetail 定义企业微信智能机器人渠道详情、凭据与长连接状态。
type WeComBotChannelDetail struct {
	MessageChannelRecord
	BotID      string
	Secret     string
	Connection *servermodels.ChannelConnection
}

// WeComBotConnectionInput 定义机器人长连接凭据。
type WeComBotConnectionInput struct {
	BotID  string
	Secret string
}

// GetWeComBotChannelQuery 读取当前企业的单个企业微信智能机器人渠道。
type GetWeComBotChannelQuery struct {
	db *bun.DB
}

// NewGetWeComBotChannelQuery 创建企业微信智能机器人渠道详情查询。
func NewGetWeComBotChannelQuery(db *bun.DB) *GetWeComBotChannelQuery {
	return &GetWeComBotChannelQuery{db: db}
}

// Execute 返回当前企业的企业微信智能机器人渠道详情。
func (q *GetWeComBotChannelQuery) Execute(ctx context.Context, identity *servermodels.Identity, channelID string) (*WeComBotChannelDetail, error) {
	return loadWeComBotChannelDetail(ctx, q.db, identity.Workspace.ID, channelID)
}

// loadWeComBotChannelDetail 读取指定企业中的企业微信智能机器人渠道、凭据与长连接状态。
func loadWeComBotChannelDetail(ctx context.Context, db bun.IDB, workspaceID, channelID string) (*WeComBotChannelDetail, error) {
	channel := &servermodels.Channel{}
	err := db.NewSelect().Model(channel).
		Where("c.id = ? AND c.workspace_id = ? AND c.type = ?", channelID, workspaceID, domain.ChannelTypeWeComBot).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get WeCom bot channel: %w", err)
	}
	detail := &WeComBotChannelDetail{MessageChannelRecord: *NewMessageChannelRecord(channel), BotID: support.Deref(channel.ProviderAccountID)}
	if err := db.NewSelect().Model((*servermodels.WeComBotChannelSetting)(nil)).Column("secret").
		Where("channel_id = ? AND workspace_id = ?", channelID, workspaceID).
		Scan(ctx, &detail.Secret); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get WeCom bot channel settings: %w", err)
	}
	// 只展示渠道路由租约仍有效时的连接状态，租约过期表示暂无服务端实例接管。
	connection := &servermodels.ChannelConnection{}
	err = db.NewSelect().Model(connection).
		Where("chc.channel_id = ? AND chc.workspace_id = ?", channelID, workspaceID).
		Where("?", servertask.RouteHeld(channeladapter.ConnectionRoute(channelID))).
		Scan(ctx)
	if err == nil {
		detail.Connection = connection
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get WeCom bot channel connection: %w", err)
	}
	return detail, nil
}

// SaveWeComBotConnectionAction 保存企业微信智能机器人的长连接凭据。
type SaveWeComBotConnectionAction struct {
	db *bun.DB
}

// NewSaveWeComBotConnectionAction 创建机器人凭据保存操作。
func NewSaveWeComBotConnectionAction(db *bun.DB) *SaveWeComBotConnectionAction {
	return &SaveWeComBotConnectionAction{db: db}
}

// Execute 保存机器人编号与密钥；更换机器人时终止旧机器人尚未发出的消息与进行中的 AI 运行，并解除全部外部账号的成员绑定。持有连接的服务端实例在下次核对时按新凭据重连。
func (a *SaveWeComBotConnectionAction) Execute(ctx context.Context, identity *servermodels.Identity, channelID string, input WeComBotConnectionInput) (*WeComBotChannelDetail, error) {
	input.BotID, input.Secret = strings.TrimSpace(input.BotID), strings.TrimSpace(input.Secret)
	workspaceID := identity.Workspace.ID
	var cancelledRuns int
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		channel := &servermodels.Channel{}
		err := tx.NewSelect().Model(channel).
			Where("c.id = ? AND c.workspace_id = ? AND c.type = ?", channelID, workspaceID, domain.ChannelTypeWeComBot).
			For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		// 同一机器人在整个部署中只能由一个渠道连接，按机器人编号加事务锁后检查。
		if err := serverstorage.XactLock(ctx, tx, serverstorage.LockWeComBot, input.BotID); err != nil {
			return err
		}
		used, err := tx.NewSelect().Model((*servermodels.Channel)(nil)).
			Where("type = ? AND provider_account_id = ? AND id <> ?", domain.ChannelTypeWeComBot, input.BotID, channelID).Exists(ctx)
		if err != nil {
			return err
		}
		if used {
			return ErrWeComBotInUse
		}
		if channel.ProviderAccountID != nil && *channel.ProviderAccountID != input.BotID {
			if cancelledRuns, err = agentcancel.CancelChannelRuns(ctx, tx, workspaceID, channelID, domain.AgentRunErrorCodeAccountChanged); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE channel_message_deliveries SET status = 'failed', last_error = 'account_changed' WHERE channel_id = ? AND workspace_id = ? AND status IN ('pending', 'retry_wait')", channelID, workspaceID); err != nil {
				return err
			}
			// 外部账号编号只在同一个机器人所属企业内有意义，更换机器人后原有绑定与未使用的绑定链接全部失效。
			if _, err := tx.ExecContext(ctx, "UPDATE channel_identities SET user_identity_id = NULL WHERE channel_id = ? AND workspace_id = ? AND user_identity_id IS NOT NULL", channelID, workspaceID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM channel_binding_tokens AS cbt USING channel_identities AS ci WHERE ci.id = cbt.channel_identity_id AND ci.workspace_id = cbt.workspace_id AND ci.channel_id = ? AND cbt.workspace_id = ? AND cbt.used_at IS NULL", channelID, workspaceID); err != nil {
				return err
			}
		}
		if _, err := tx.NewUpdate().Model(channel).Set("provider_account_id = ?", input.BotID).WherePK().Exec(ctx); err != nil {
			return err
		}
		setting := &servermodels.WeComBotChannelSetting{ChannelID: channelID, WorkspaceID: workspaceID, Secret: input.Secret}
		_, err = tx.NewInsert().Model(setting).Column("channel_id", "workspace_id", "secret").
			On("CONFLICT (channel_id) DO UPDATE SET secret = EXCLUDED.secret").Exec(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	if cancelledRuns > 0 {
		slog.InfoContext(logscope.WithWorkspace(ctx, workspaceID), "企业微信机器人更换后取消渠道运行", "channel_id", channelID, "cancelled_run_count", cancelledRuns, "reason", domain.AgentRunErrorCodeAccountChanged)
	}
	return loadWeComBotChannelDetail(ctx, a.db, workspaceID, channelID)
}
