//go:build server

package channelinbound

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

func loadReceived(ctx context.Context, db bun.IDB, channel *servermodels.Channel, identity *servermodels.ChannelIdentity, input Input) (Result, bool, error) {
	message := &servermodels.Message{}
	err := db.NewSelect().Model(message).
		Where("msg.workspace_id = ?", channel.WorkspaceID).
		Where("msg.idempotency_key = ?", input.IdempotencyKey).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, fmt.Errorf("find idempotent inbound channel message: %w", err)
	}
	if message.ServiceSessionID == nil || message.SenderParticipantID == nil || message.DeletedAt != nil {
		return Result{}, true, conversationaction.ErrDataInvariant
	}
	if message.Type != string(input.messageType()) {
		return Result{}, true, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
	}
	var attachment *Attachment
	if input.Attachment != nil || input.ExternalMedia != nil {
		attachment = &Attachment{}
		if err := db.NewSelect().TableExpr("message_attachments AS ma").
			ColumnExpr("COALESCE(ma.file_id::text, '') AS id").
			ColumnExpr("ma.name, ma.content_type, ma.byte_size, ma.image_width, ma.image_height, ma.transfer_status").
			ColumnExpr("f.storage_key").
			Join("LEFT JOIN files AS f ON f.id = ma.file_id AND f.workspace_id = ma.workspace_id").
			Where("ma.message_id = ? AND ma.workspace_id = ?", message.ID, channel.WorkspaceID).
			Scan(ctx, attachment); err != nil {
			return Result{}, true, fmt.Errorf("load idempotent inbound attachment: %w", err)
		}
		// 访客附件核对文件编号；外部媒体的文件编号、字节数、文件名与类型随取回结果变化，只核对图片尺寸。
		if input.Attachment != nil && (attachment.ID != input.Attachment.FileID || attachment.ImageWidth != input.Attachment.ImageWidth || attachment.ImageHeight != input.Attachment.ImageHeight) {
			return Result{}, true, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
		}
		if media := input.ExternalMedia; media != nil && (attachment.ImageWidth != media.ImageWidth || attachment.ImageHeight != media.ImageHeight) {
			return Result{}, true, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
		}
	}
	storedReply := support.Deref(message.ReplyToMessageID)
	replyMatches := storedReply == input.ReplyToMessageID
	if input.ChannelMessage != nil {
		replyMatches, err = channeldelivery.MatchesInbound(ctx, db, message, channel.ID, input.ChannelMessage.deliveryInbound())
		if err != nil {
			return Result{}, true, err
		}
	}
	if !replyMatches || message.Body != input.Body || (input.RequestedConversationID != nil && *input.RequestedConversationID != message.ConversationID) {
		return Result{}, true, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
	}
	session := &servermodels.ServiceSession{}
	err = db.NewSelect().Model(session).
		Join("JOIN channel_conversations AS cc ON cc.workspace_id = ss.workspace_id AND cc.conversation_id = ss.conversation_id").
		Join("JOIN service_conversations AS svc ON svc.workspace_id = cc.workspace_id AND svc.conversation_id = cc.conversation_id").
		Join("JOIN conversation_participants AS cp ON cp.workspace_id = cc.workspace_id AND cp.conversation_id = cc.conversation_id AND cp.subject_id = svc.requester_subject_id").
		Where("cc.workspace_id = ?", channel.WorkspaceID).
		Where("cc.conversation_id = ?", message.ConversationID).
		Where("cc.channel_identity_id = ?", identity.ID).
		Where("ss.id = ?", *message.ServiceSessionID).
		Where("cp.id = ?", *message.SenderParticipantID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, true, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonIdempotencyMismatch}
	}
	if err != nil {
		return Result{}, true, fmt.Errorf("check idempotent inbound channel message ownership: %w", err)
	}
	result := newResult(session, identity.ID, message, false)
	result.Attachment = attachment
	if storedReply != "" {
		result.ReplyTo, err = conversationaction.LoadMessageReference(ctx, db, channel.WorkspaceID, message.ConversationID, storedReply)
		if err != nil {
			return Result{}, true, err
		}
	}
	return result, true, nil
}

// newResult 构造渠道入站结果。
