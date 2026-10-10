//go:build server

package conversation

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// MaxMessageBodyRunes 是消息正文的最大字符数。
const MaxMessageBodyRunes = 4000

// InternalTextMessageFields 是内部会话文本消息的基础字段，ReplyToMessageID 为空表示不引用。
type InternalTextMessageFields struct {
	ConversationID   string
	ClientMessageID  string
	Body             string
	ReplyToMessageID string
}

// NormalizeInternalTextMessageInput 规范化内部会话文本消息的基础字段：正文去掉首尾空白，编号转为小写规范形式。
func NormalizeInternalTextMessageInput(input InternalTextMessageFields) InternalTextMessageFields {
	input.Body = strings.TrimSpace(input.Body)
	input.ConversationID, _ = str.NormalizeUUID(input.ConversationID)
	input.ClientMessageID, _ = str.NormalizeUUID(input.ClientMessageID)
	if input.ReplyToMessageID != "" {
		input.ReplyToMessageID, _ = str.NormalizeUUID(input.ReplyToMessageID)
	}
	return input
}

// MaxWriteAttempts 是并发唯一约束冲突时的最大写入尝试次数。
const MaxWriteAttempts = 3

// RunInTxWithUniqueRetry 在实时通知事务中执行写入，遇到 constraintNames 中的并发唯一约束冲突时整体重试，最多执行 MaxWriteAttempts 次；fn 每次执行都须重新写入调用方的结果。
func RunInTxWithUniqueRetry(ctx context.Context, db *bun.DB, constraintNames map[string]struct{}, fn func(context.Context, bun.Tx) error) error {
	var err error
	for attempt := 1; attempt <= MaxWriteAttempts; attempt++ {
		if err = realtime.RunInTx(ctx, db, fn); err == nil {
			return nil
		}
		constraint, retryable := RetryableUniqueViolation(err, constraintNames)
		if !retryable {
			return err
		}
		slog.InfoContext(ctx, "并发唯一约束冲突，重试写入事务", "constraint", constraint, "attempt", attempt)
	}
	return fmt.Errorf("unique conflict retries exhausted: %w", err)
}

// RetryableUniqueViolation 返回允许重试的并发唯一约束。
func RetryableUniqueViolation(err error, constraintNames map[string]struct{}) (string, bool) {
	constraint, ok := pgerr.UniqueViolation(err)
	if !ok {
		return "", false
	}
	_, retryable := constraintNames[constraint]
	return constraint, retryable
}

// MemberConversationMessage 构造成员消息时间线结果。
func MemberConversationMessage(message *servermodels.Message, subjectID string, identity servermodels.WorkspaceIdentity) ConversationMessage {
	name := identity.DisplayName
	identityType := domain.WorkspaceIdentityType(identity.Type)
	return ConversationMessage{
		ClientMessageID: message.ClientMessageID, ID: message.ID, Type: domain.MessageType(message.Type), Visibility: domain.MessageVisibility(message.Visibility), Body: message.Body, Language: message.Language,
		OriginatedAt: message.OriginatedAt, CreatedAt: message.CreatedAt, MentionAll: message.MentionAll, MessageSeq: message.MessageSeq,
		Sender: &ConversationMessageSender{
			ChatSubjectID: subjectID, Kind: domain.ChatSubjectKindWorkspaceIdentity,
			SourceID: identity.ID, DisplayName: &name, AvatarFileID: identity.AvatarFileID, IdentityType: &identityType,
		},
	}
}
