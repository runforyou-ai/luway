//go:build server

// Package servicesession 处理客服服务周期的接待、转接、关闭、重开、客服回复与服务摘要。
package servicesession

import (
	"time"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/domain"
)

const (
	ValidationTargetTeamIDInvalid       conversationaction.ValidationCode = "target_team_id_invalid"
	ValidationTransferTargetKindInvalid conversationaction.ValidationCode = "transfer_target_kind_invalid"
	ValidationMentionIdentityIDsInvalid conversationaction.ValidationCode = "mention_identity_ids_invalid"
	ValidationMessageVisibilityInvalid  conversationaction.ValidationCode = "message_visibility_invalid"
	ValidationTranslationInvalid        conversationaction.ValidationCode = "translation_invalid"
)

const (
	// ConflictReasonServiceHandlingRequired 表示当前成员未开启处理服务请求。
	ConflictReasonServiceHandlingRequired = "service_handling_required"
	// ConflictReasonServiceSessionOwnRequest 表示成员处理或承接自己发起的服务请求。
	ConflictReasonServiceSessionOwnRequest = "service_session_own_request"
	// ConflictReasonTransferTeamUnavailable 表示转交目标团队内没有开启接待的真人成员。
	ConflictReasonTransferTeamUnavailable = "transfer_team_unavailable"
	// ConflictReasonServiceSessionAlreadyOpen 表示服务周期已经打开。
	ConflictReasonServiceSessionAlreadyOpen = "service_session_already_open"
	// ConflictReasonNoteMentionTargetInvalid 表示内部备注的提醒目标不是本企业有效成员。
	ConflictReasonNoteMentionTargetInvalid = "note_mention_target_invalid"
	// ConflictReasonChannelAttachmentUnsupported 表示来源渠道尚不支持外发附件。
	ConflictReasonChannelAttachmentUnsupported = "channel_attachment_unsupported"
	// ConflictReasonCaptionTooLong 表示附件说明超过来源渠道的字符上限。
	ConflictReasonCaptionTooLong = "caption_too_long"
	// ConflictReasonTranslationTooLong 表示翻译发送的译文超过来源渠道的文本字符上限。
	ConflictReasonTranslationTooLong = "translation_too_long"
)

// ServiceSessionAssignee 定义服务周期负责人。
type ServiceSessionAssignee struct {
	IdentityID   string
	Type         domain.OrganizationIdentityType
	DisplayName  string
	AvatarFileID *string
}

// ServiceSessionResult 定义服务周期命令结果。
type ServiceSessionResult struct {
	ID       string
	Status   domain.ServiceSessionStatus
	Assignee *ServiceSessionAssignee
	ClosedAt *time.Time
}

// TransferServiceSessionInput 定义服务周期的转交去向；成员去向用 IdentityID，团队去向用 TeamID，公共队列两者都不填。
type TransferServiceSessionInput struct {
	ConversationID string
	TargetKind     domain.ServiceSessionTargetKind
	TeamID         string
	IdentityID     string
}

// ServiceTextMessageInput 定义成员发送的服务会话文本消息。
type ServiceTextMessageInput struct {
	ReplyToMessageID string
	ConversationID   string
	ClientMessageID  string
	Body             string
	Visibility       domain.MessageVisibility
	// Translation 是对客回复发送给客户的译文，Body 为客服书写的原文；为空时按原文发送。
	Translation *OutgoingTranslation
	// MentionIdentityIDs 是内部备注提醒的企业成员身份，按正文出现顺序排列。
	MentionIdentityIDs []string
}

// OutgoingTranslation 定义翻译发送的译文：Language 为客户语言，SourceLanguage 为客服书写语言。
type OutgoingTranslation struct {
	Language       string
	SourceLanguage string
	Body           string
}

// ServiceAttachmentMessageInput 定义成员发送的服务会话附件消息。
type ServiceAttachmentMessageInput struct {
	ConversationID   string
	ClientMessageID  string
	FileID           string
	Body             string
	ReplyToMessageID string
	ImageWidth       int
	ImageHeight      int
}
