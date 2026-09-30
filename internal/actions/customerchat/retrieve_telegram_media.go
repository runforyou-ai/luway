//go:build server

package customerchat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	fileaction "github.com/runforyou-ai/cervi/internal/actions/file"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/telegram"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	servertask "github.com/runforyou-ai/cervi/internal/task/server"
	"github.com/runforyou-ai/cervi/pkg/connectiontest"
	"github.com/uptrace/bun"
)

const (
	// RetrieveTelegramMediaActionName 是 Telegram 入站媒体取回任务的 Action 名称。
	RetrieveTelegramMediaActionName = "telegram.retrieve_media"
	// telegramMediaRetrieveMaxAttempts 按任务运行时的指数退避覆盖文件记录 24 小时过期窗口之内的重试。
	telegramMediaRetrieveMaxAttempts = 30
	telegramMediaDownloadTimeout     = 3 * time.Minute
)

// RetrieveTelegramMediaInput 定义一次媒体取回所需的消息、文件与平台引用。
type RetrieveTelegramMediaInput struct {
	OrganizationID string `json:"organizationId"`
	ChannelID      string `json:"channelId"`
	ConversationID string `json:"conversationId"`
	MessageID      string `json:"messageId"`
	FileID         string `json:"fileId"`
	TelegramFileID string `json:"telegramFileId"`
	BotID          int64  `json:"botId"`
}

// RetrieveTelegramMediaAction 下载 Telegram 入站媒体内容并把附件推进到终态。
type RetrieveTelegramMediaAction struct {
	db             *bun.DB
	api            telegram.MediaDownloader
	writer         fileaction.ContentWriter
	agentScheduler conversationaction.CustomerAgentMessageScheduler
}

// NewRetrieveTelegramMediaAction 创建 Telegram 媒体取回任务。
func NewRetrieveTelegramMediaAction(db *bun.DB, api telegram.MediaDownloader, writer fileaction.ContentWriter, agentScheduler conversationaction.CustomerAgentMessageScheduler) *RetrieveTelegramMediaAction {
	return &RetrieveTelegramMediaAction{db: db, api: api, writer: writer, agentScheduler: agentScheduler}
}

// Execute 每次重试重新调用 getFile 下载内容，写入存储后在同一事务内激活文件、置附件就绪并调度 AI 客服。
func (a *RetrieveTelegramMediaAction) Execute(ctx context.Context, input RetrieveTelegramMediaInput) error {
	if !common.ValidUUID(input.OrganizationID) || !common.ValidUUID(input.FileID) || !common.ValidUUID(input.MessageID) {
		return servertask.Permanent(errors.New("invalid Telegram media retrieval input"))
	}
	file := &servermodels.File{}
	err := a.db.NewSelect().Model(file).ColumnExpr("f.*").ColumnExpr("f.expires_at <= now() AS expired").
		Join("JOIN message_attachments AS ma ON ma.file_id = f.id AND ma.organization_id = f.organization_id AND ma.message_id = ?", input.MessageID).
		Where("f.id = ? AND f.organization_id = ?", input.FileID, input.OrganizationID).
		Where("ma.transfer_status = ?", domain.MessageAttachmentTransferPending).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load Telegram media file: %w", err)
	}
	if file.Expired {
		return servertask.Permanent(errors.New("Telegram media file expired before retrieval"))
	}
	var route struct {
		BotID *int64  `bun:"bot_id"`
		Token *string `bun:"bot_token"`
	}
	if err := a.db.NewSelect().TableExpr("telegram_channel_settings AS tcs").ColumnExpr("tcs.bot_id, tcs.bot_token").
		Where("tcs.channel_id = ? AND tcs.organization_id = ?", input.ChannelID, input.OrganizationID).Scan(ctx, &route); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return servertask.Permanent(errors.New("Telegram channel setting is missing"))
		}
		return fmt.Errorf("load Telegram media bot: %w", err)
	}
	// 文件引用只对签发它的机器人有效，机器人变化后无法取回。
	if route.BotID == nil || *route.BotID != input.BotID {
		return servertask.Permanent(errors.New("Telegram bot changed before media retrieval"))
	}
	if route.Token == nil || *route.Token == "" {
		return errors.New("Telegram bot token is unavailable")
	}
	downloadCtx, cancel := context.WithTimeout(ctx, telegramMediaDownloadTimeout)
	data, err := a.api.DownloadMedia(downloadCtx, *route.Token, input.TelegramFileID, domain.ChannelInboundAttachmentLimit(domain.ChannelTypeTelegram))
	cancel()
	if err != nil {
		// 网络、超时、限流和平台不可用等待重试，平台明确拒绝直接失败。
		_, kind, classified := connectiontest.Details(err)
		switch {
		case !classified:
			return err
		case kind == connectiontest.FailureTimeout, kind == connectiontest.FailureNetwork, kind == connectiontest.FailureTLS,
			kind == connectiontest.FailureUnavailable, kind == connectiontest.FailureRateLimited:
			return err
		default:
			return servertask.Permanent(fmt.Errorf("Telegram rejected media download: %w", err))
		}
	}
	file.ByteSize = int64(len(data))
	etag, err := a.writer.Save(ctx, file, data)
	if err != nil {
		return fmt.Errorf("write Telegram media content: %w", err)
	}
	saveCtx, cancelSave := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancelSave()
	return realtime.RunInTx(saveCtx, a.db, func(ctx context.Context, tx bun.Tx) error {
		conversation, err := chatstate.LockChannelConversation(ctx, tx, input.OrganizationID, input.ConversationID)
		if err != nil {
			return err
		}
		result, err := tx.NewUpdate().Model((*servermodels.File)(nil)).
			Set("status = ?", domain.FileStatusActive).
			Set("byte_size = ?", file.ByteSize).
			Set("etag = ?", common.OptionalString(etag)).
			Set("uploaded_at = now()").
			Set("expires_at = NULL").
			Set("updated_at = now()").
			Where("id = ? AND organization_id = ? AND status = ?", input.FileID, input.OrganizationID, domain.FileStatusPending).
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("activate Telegram media file: %w", err)
		}
		if rows, _ := result.RowsAffected(); rows == 0 {
			return nil
		}
		transitioned, err := a.finishAttachment(ctx, tx, conversation, input, domain.MessageAttachmentTransferReady, &file.ByteSize)
		if err != nil || !transitioned {
			return err
		}
		slog.Info("Telegram 入站媒体已取回", "channel_id", input.ChannelID, "message_id", input.MessageID, "file_id", input.FileID, "byte_size", file.ByteSize)
		return nil
	})
}

// FinalizeFailure 在重试耗尽或平台拒绝后把附件置为取回失败，回收文件记录并调度一次 AI 客服。
func (a *RetrieveTelegramMediaAction) FinalizeFailure(ctx context.Context, input RetrieveTelegramMediaInput, runErr error) error {
	if !common.ValidUUID(input.OrganizationID) || !common.ValidUUID(input.FileID) || !common.ValidUUID(input.MessageID) {
		return nil
	}
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		conversation, err := chatstate.LockChannelConversation(ctx, tx, input.OrganizationID, input.ConversationID)
		if err != nil {
			return err
		}
		transitioned, err := a.finishAttachment(ctx, tx, conversation, input, domain.MessageAttachmentTransferFailed, nil)
		if err != nil || !transitioned {
			return err
		}
		// 文件记录交给过期清理删除已写入的内容。
		if _, err := tx.NewUpdate().Model((*servermodels.File)(nil)).
			Set("status = ?", domain.FileStatusDeleting).
			Set("expires_at = now()").
			Set("updated_at = now()").
			Where("id = ? AND organization_id = ? AND status IN (?, ?)", input.FileID, input.OrganizationID, domain.FileStatusPending, domain.FileStatusUploaded).
			Exec(ctx); err != nil {
			return fmt.Errorf("retire Telegram media file: %w", err)
		}
		slog.Warn("Telegram 入站媒体取回失败", "channel_id", input.ChannelID, "message_id", input.MessageID, "file_id", input.FileID, "error", runErr)
		return nil
	})
}

// finishAttachment 把取回中的附件推进到终态并推进会话版本，只有完成状态转换的一次才调度 AI 客服。
func (a *RetrieveTelegramMediaAction) finishAttachment(ctx context.Context, tx bun.Tx, conversation *servermodels.Conversation, input RetrieveTelegramMediaInput, status domain.MessageAttachmentTransferStatus, byteSize *int64) (bool, error) {
	query := tx.NewUpdate().TableExpr("message_attachments").
		Set("transfer_status = ?", status).
		Set("updated_at = now()").
		Where("message_id = ? AND organization_id = ? AND transfer_status = ?", input.MessageID, input.OrganizationID, domain.MessageAttachmentTransferPending)
	if status == domain.MessageAttachmentTransferFailed {
		query = query.Set("file_id = NULL")
	} else {
		query = query.Set("byte_size = ?", *byteSize)
	}
	result, err := query.Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("finish Telegram media attachment: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return false, nil
	}
	if err := chatstate.TouchConversation(ctx, tx, conversation, domain.ConversationChangeTimeline); err != nil {
		return false, err
	}
	// 消息所属周期仍是当前周期时才触发 AI 客服。
	message := &servermodels.Message{}
	if err := tx.NewSelect().Model(message).Column("service_session_id").
		Where("msg.id = ? AND msg.organization_id = ? AND msg.conversation_id = ?", input.MessageID, input.OrganizationID, input.ConversationID).
		Scan(ctx); err != nil {
		return false, fmt.Errorf("load Telegram media message: %w", err)
	}
	session, err := chatstate.LockCurrentServiceSession(ctx, tx, input.OrganizationID, input.ConversationID)
	if err != nil {
		return false, err
	}
	if message.ServiceSessionID == nil || *message.ServiceSessionID != session.ID {
		return true, nil
	}
	if _, err := a.agentScheduler.ScheduleCustomerAuto(ctx, tx, input.OrganizationID, input.ConversationID, session.ID, input.MessageID); err != nil {
		return false, fmt.Errorf("schedule Telegram customer agent: %w", err)
	}
	return true, nil
}
