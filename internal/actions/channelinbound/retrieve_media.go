//go:build server

package channelinbound

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

const (
	// RetrieveMediaActionName 是渠道入站媒体取回任务的 Action 名称。
	RetrieveMediaActionName = "channel.retrieve_media"
	// MediaRetrieveMaxAttempts 按任务运行时的指数退避覆盖文件记录 24 小时过期窗口之内的重试。
	MediaRetrieveMaxAttempts    = 30
	channelMediaDownloadTimeout = 3 * time.Minute
)

// RetrieveMediaInput 定义一次媒体取回所需的消息、文件与平台引用。
type RetrieveMediaInput struct {
	WorkspaceID    string `json:"workspaceId"`
	ChannelID      string `json:"channelId"`
	ConversationID string `json:"conversationId"`
	MessageID      string `json:"messageId"`
	FileID         string `json:"fileId"`
	// AccountID 是签发媒体引用的平台账号。
	AccountID string `json:"accountId"`
	MediaRef  string `json:"mediaRef"`
}

// RetrieveMediaAction 经渠道适配器下载入站媒体内容并把附件推进到终态。
type RetrieveMediaAction struct {
	db             *bun.DB
	adapters       *channeladapter.Registry
	writer         fileaction.ContentWriter
	agentScheduler conversationaction.CustomerAgentMessageScheduler
}

// NewRetrieveMediaAction 创建渠道媒体取回任务。
func NewRetrieveMediaAction(db *bun.DB, adapters *channeladapter.Registry, writer fileaction.ContentWriter, agentScheduler conversationaction.CustomerAgentMessageScheduler) *RetrieveMediaAction {
	return &RetrieveMediaAction{db: db, adapters: adapters, writer: writer, agentScheduler: agentScheduler}
}

// Execute 每次重试重新下载内容，写入存储后在同一事务内激活文件、置附件就绪并调度 AI 客服。
func (a *RetrieveMediaAction) Execute(ctx context.Context, input RetrieveMediaInput) error {
	if !str.IsUUID(input.WorkspaceID) || !str.IsUUID(input.FileID) || !str.IsUUID(input.MessageID) {
		return servertask.Permanent(errors.New("invalid channel media retrieval input"))
	}
	file := &servermodels.File{}
	err := a.db.NewSelect().Model(file).ColumnExpr("f.*").ColumnExpr("f.expires_at <= now() AS expired").
		Join("JOIN message_attachments AS ma ON ma.file_id = f.id AND ma.workspace_id = f.workspace_id AND ma.message_id = ?", input.MessageID).
		Where("f.id = ? AND f.workspace_id = ?", input.FileID, input.WorkspaceID).
		Where("ma.transfer_status = ?", domain.MessageAttachmentTransferPending).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load channel media file: %w", err)
	}
	if file.Expired {
		return servertask.Permanent(errors.New("channel media file expired before retrieval"))
	}
	var channelType domain.ChannelType
	if err := a.db.NewSelect().TableExpr("channels").Column("type").
		Where("id = ? AND workspace_id = ?", input.ChannelID, input.WorkspaceID).Scan(ctx, &channelType); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return servertask.Permanent(errors.New("media channel is missing"))
		}
		return fmt.Errorf("load media channel: %w", err)
	}
	source, ok := channeladapter.Lookup[channeladapter.MediaSource](a.adapters, channelType)
	if !ok {
		return servertask.Permanent(fmt.Errorf("channel type %s does not support media retrieval", channelType))
	}
	downloadCtx, cancel := context.WithTimeout(ctx, channelMediaDownloadTimeout)
	downloaded, err := source.DownloadMedia(downloadCtx, channeladapter.Target{WorkspaceID: input.WorkspaceID, ChannelID: input.ChannelID, AccountID: input.AccountID}, input.MediaRef, domain.ChannelCapabilitiesOf(channelType).InboundAttachmentLimit)
	cancel()
	if errors.Is(err, channeladapter.ErrMediaRejected) {
		return servertask.Permanent(err)
	}
	if err != nil {
		return err
	}
	data := downloaded.Data
	file.ByteSize = int64(len(data))
	// 平台随内容给出文件名时采用该文件名；入站时未知的内容类型按内容识别，无扩展名的文件名补上对应扩展名。
	if downloaded.FileName != "" {
		file.OriginalName = downloaded.FileName
	}
	if file.ContentType == "application/octet-stream" {
		file.ContentType = strings.SplitN(http.DetectContentType(data), ";", 2)[0]
		if extensions, _ := mime.ExtensionsByType(file.ContentType); path.Ext(file.OriginalName) == "" && len(extensions) > 0 {
			file.OriginalName += extensions[0]
		}
	}
	etag, err := a.writer.Save(ctx, file, data)
	if err != nil {
		return fmt.Errorf("write channel media content: %w", err)
	}
	saveCtx, cancelSave := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancelSave()
	return realtime.RunInTx(saveCtx, a.db, func(ctx context.Context, tx bun.Tx) error {
		conversation, err := chatstate.LockChannelConversation(ctx, tx, input.WorkspaceID, input.ConversationID)
		if err != nil {
			return err
		}
		result, err := tx.NewUpdate().Model((*servermodels.File)(nil)).
			Set("status = ?", domain.FileStatusActive).
			Set("byte_size = ?", file.ByteSize).
			Set("original_name = ?", file.OriginalName).
			Set("content_type = ?", file.ContentType).
			Set("etag = ?", support.NilIfZero(etag)).
			Set("uploaded_at = now()").
			Set("expires_at = NULL").
			Where("id = ? AND workspace_id = ? AND status = ?", input.FileID, input.WorkspaceID, domain.FileStatusPending).
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("activate channel media file: %w", err)
		}
		if rows, _ := result.RowsAffected(); rows == 0 {
			return nil
		}
		transitioned, err := a.finishAttachment(ctx, tx, conversation, input, domain.MessageAttachmentTransferReady, file)
		if err != nil || !transitioned {
			return err
		}
		slog.InfoContext(ctx, "渠道入站媒体已取回", "channel_id", input.ChannelID, "message_id", input.MessageID, "file_id", input.FileID, "byte_size", file.ByteSize)
		return nil
	})
}

// FinalizeFailure 在重试耗尽或平台拒绝后把附件置为取回失败，回收文件记录并调度一次 AI 客服。
func (a *RetrieveMediaAction) FinalizeFailure(ctx context.Context, input RetrieveMediaInput, runErr error) error {
	if !str.IsUUID(input.WorkspaceID) || !str.IsUUID(input.FileID) || !str.IsUUID(input.MessageID) {
		return nil
	}
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		conversation, err := chatstate.LockChannelConversation(ctx, tx, input.WorkspaceID, input.ConversationID)
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
			Where("id = ? AND workspace_id = ? AND status IN (?, ?)", input.FileID, input.WorkspaceID, domain.FileStatusPending, domain.FileStatusUploaded).
			Exec(ctx); err != nil {
			return fmt.Errorf("retire channel media file: %w", err)
		}
		slog.WarnContext(ctx, "渠道入站媒体取回失败", "channel_id", input.ChannelID, "message_id", input.MessageID, "file_id", input.FileID, "error", runErr)
		return nil
	})
}

// finishAttachment 把取回中的附件推进到终态并推进会话版本，就绪时同步文件的字节数、文件名与内容类型，只有完成状态转换的一次才调度 AI 客服。
func (a *RetrieveMediaAction) finishAttachment(ctx context.Context, tx bun.Tx, conversation *servermodels.Conversation, input RetrieveMediaInput, status domain.MessageAttachmentTransferStatus, file *servermodels.File) (bool, error) {
	query := tx.NewUpdate().TableExpr("message_attachments").
		Set("transfer_status = ?", status).
		Where("message_id = ? AND workspace_id = ? AND transfer_status = ?", input.MessageID, input.WorkspaceID, domain.MessageAttachmentTransferPending)
	if status == domain.MessageAttachmentTransferFailed {
		query = query.Set("file_id = NULL")
	} else {
		query = query.Set("byte_size = ?, name = ?, content_type = ?", file.ByteSize, file.OriginalName, file.ContentType)
	}
	result, err := query.Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("finish channel media attachment: %w", err)
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
		Where("msg.id = ? AND msg.workspace_id = ? AND msg.conversation_id = ?", input.MessageID, input.WorkspaceID, input.ConversationID).
		Scan(ctx); err != nil {
		return false, fmt.Errorf("load channel media message: %w", err)
	}
	session, err := chatstate.LockCurrentServiceSession(ctx, tx, input.WorkspaceID, input.ConversationID)
	if err != nil {
		return false, err
	}
	if message.ServiceSessionID == nil || *message.ServiceSessionID != session.ID {
		return true, nil
	}
	if _, err := a.agentScheduler.ScheduleCustomerAuto(ctx, tx, input.WorkspaceID, input.ConversationID, session.ID, input.MessageID); err != nil {
		return false, fmt.Errorf("schedule channel customer agent: %w", err)
	}
	return true, nil
}
