//go:build server

// Package groupchat 处理群聊的创建、资料与成员管理、群内助理和群消息发送。
package groupchat

import (
	"time"

	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	"github.com/runforyou-ai/cervi/internal/domain"
)

const (
	ValidationGroupTitleTooLong        conversationaction.ValidationCode = "group_title_too_long"
	ValidationGroupDescriptionTooLong  conversationaction.ValidationCode = "group_description_too_long"
	ValidationGroupImageFileIDInvalid  conversationaction.ValidationCode = "group_image_file_id_invalid"
	ValidationGroupMembersRequired     conversationaction.ValidationCode = "group_members_required"
	ValidationGroupMembersTooMany      conversationaction.ValidationCode = "group_members_too_many"
	ValidationGroupMemberIDsInvalid    conversationaction.ValidationCode = "group_member_ids_invalid"
	ValidationGroupMemberIDInvalid     conversationaction.ValidationCode = "group_member_id_invalid"
	ValidationGroupOwnerIDInvalid      conversationaction.ValidationCode = "group_owner_id_invalid"
	ValidationMentionSubjectIDsInvalid conversationaction.ValidationCode = "mention_subject_ids_invalid"
)

const (
	// ConflictReasonGroupMemberAlreadyActive 表示成员已经在群聊中。
	ConflictReasonGroupMemberAlreadyActive = "group_member_already_active"
	// ConflictReasonGroupMemberNotActive 表示目标群成员资格已失效。
	ConflictReasonGroupMemberNotActive = "group_member_not_active"
	// ConflictReasonGroupOwnerCannotBeRemoved 表示移除操作的目标为群主。
	ConflictReasonGroupOwnerCannotBeRemoved = "group_owner_cannot_be_removed"
	// ConflictReasonGroupOwnerCannotLeave 表示群主必须先转让后退出。
	ConflictReasonGroupOwnerCannotLeave = "group_owner_cannot_leave"
	// ConflictReasonGroupMentionTargetInvalid 表示当前群聊的提醒目标校验失败。
	ConflictReasonGroupMentionTargetInvalid = "group_mention_target_invalid"
	// ConflictReasonAssistantInactive 表示被点名的助理已禁用。
	ConflictReasonAssistantInactive = "assistant_inactive"
)

// GroupConversationInput 定义企业成员创建群聊的资料和初始成员。
type GroupConversationInput struct {
	Title             string
	Description       string
	ImageFileID       string
	MemberIdentityIDs []string
}

// GroupConversationProfileInput 定义群聊资料修改参数。
type GroupConversationProfileInput struct {
	ConversationID string
	Title          string
	Description    string
	ImageFileID    *string
}

// GroupConversationMembersInput 定义群聊批量增员参数。
type GroupConversationMembersInput struct {
	ConversationID    string
	MemberIdentityIDs []string
}

// GroupConversationMemberInput 定义群聊单个成员操作参数。
type GroupConversationMemberInput struct {
	ConversationID   string
	MemberIdentityID string
}

// GroupConversationOwnerInput 定义群主转让参数。
type GroupConversationOwnerInput struct {
	ConversationID  string
	OwnerIdentityID string
}

// GroupConversationSummary 定义企业内部群聊摘要。
type GroupConversationSummary struct {
	ID          string
	Title       string
	Status      domain.ConversationStatus
	MemberCount int
	ImageFileID *string
	// MemberPreviewNames 是除创建人外按入群先后排列的前几名成员名称。
	MemberPreviewNames []string
}

// GroupParticipant 定义群聊中的当前有效成员。
type GroupParticipant struct {
	IdentityType  domain.OrganizationIdentityType
	ChatSubjectID string
	IdentityID    string
	DisplayName   string
	AvatarFileID  *string
	Role          domain.ConversationParticipantRole
	// AssistantOwnerName 是成员为助理时其主人的名称，其他成员为空。
	AssistantOwnerName *string
	// AssistantOwnerIdentityID 是成员为助理时其主人的企业身份编号，其他成员为空。
	AssistantOwnerIdentityID *string
}

// GroupConversation 定义群聊资料和当前有效成员。
type GroupConversation struct {
	ID           string
	Title        string
	Description  string
	ImageFileID  *string
	Status       domain.ConversationStatus
	CreatedAt    time.Time
	Participants []GroupParticipant
	Muted        bool
	// ArchivedAt 是当前成员归档该群聊的时间，未归档时为空。
	ArchivedAt *time.Time
	// MemberPreviewNames 是除查看者外按入群先后排列的前几名在群成员名称。
	MemberPreviewNames []string
}

// GroupTextMessageInput 定义成员发送的群聊文本消息。
type GroupTextMessageInput struct {
	ConversationID    string
	ClientMessageID   string
	Body              string
	ReplyToMessageID  string
	MentionSubjectIDs []string
	MentionAll        bool
}
