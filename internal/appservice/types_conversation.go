package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// ChatSubjectKind 表示聊天主体类型。
type ChatSubjectKind = domain.ChatSubjectKind

// MessageType 表示会话消息类型。
type MessageType = domain.MessageType

// MessageVisibility 表示消息在服务会话中的可见范围：shared 会话各方可见，internal 仅处理方可见，requester 仅企业成员发起人可见。
type MessageVisibility = domain.MessageVisibility

// ConversationStatus 表示会话生命周期状态。
type ConversationStatus = domain.ConversationStatus

// ConversationSystemEventType 表示会话系统事件类型。
type ConversationSystemEventType = domain.ConversationSystemEventType

// ServiceSessionReturnReason 表示服务周期退回队列的原因。
type ServiceSessionReturnReason = domain.ServiceSessionReturnReason

// ServiceSessionCloseReason 表示服务周期的结束方式。
type ServiceSessionCloseReason = domain.ServiceSessionCloseReason

// ServiceSessionTargetKind 表示服务周期流转去向的类型。
type ServiceSessionTargetKind = domain.ServiceSessionTargetKind

// ServiceSessionTarget 定义服务周期流转的去向及名称快照。
type ServiceSessionTarget struct {
	Kind        ServiceSessionTargetKind `json:"kind"`
	TeamID      *string                  `json:"teamId"`
	TeamName    *string                  `json:"teamName"`
	IdentityID  *string                  `json:"identityId"`
	DisplayName *string                  `json:"displayName"`
}

// GroupParticipantRole 表示群聊成员角色。
type GroupParticipantRole string

const (
	GroupParticipantRoleOwner  GroupParticipantRole = GroupParticipantRole(domain.ConversationParticipantRoleOwner)
	GroupParticipantRoleMember GroupParticipantRole = GroupParticipantRole(domain.ConversationParticipantRoleMember)
)

// ConversationMessageListInput 定义成员消息查询方向。
type ConversationMessageListInput struct {
	Before string `json:"before" query:"before"`
	After  string `json:"after" query:"after"`
}

// ConversationMessageWindowInput 定义已加载消息窗口的首尾游标，读取包含两端的连续范围。
type ConversationMessageWindowInput struct {
	Start string `json:"start" query:"start"`
	End   string `json:"end" query:"end"`
}

// ServiceTextMessageInput 定义成员发送的服务会话文本消息。
type ServiceTextMessageInput struct {
	ReplyToMessageID string `json:"replyToMessageId" validate:"omitempty,uuid" msg:"field.reply_to_message_id_invalid"`
	ClientMessageID  string `json:"clientMessageId" validate:"uuid" msg:"field.client_message_id_invalid"`
	Body             string `json:"body" validate:"notblank,max=4000" msg:"notblank=field.message_body_required,max=field.message_body_too_long"`
	// Visibility 为空时按对客消息处理。
	Visibility MessageVisibility `json:"visibility" validate:"omitempty,oneof=shared internal" msg:"field.message_visibility_invalid"`
	// MentionIdentityIDs 是内部备注提醒的企业成员身份，按正文出现顺序排列；对客消息不得携带。
	MentionIdentityIDs []string `json:"mentionIdentityIds" validate:"unique,dive,uuid" msg:"field.mention_identity_ids_invalid"`
	// Translate 表示对客回复译为客户语言后发送；Translation 为发送前预览得到的译文，提供时直接发送该译文。
	Translate   bool                      `json:"translate"`
	Translation *CustomerReplyTranslation `json:"translation"`
}

// ServiceAttachmentMessageInput 定义成员发送的服务会话附件消息。
type ServiceAttachmentMessageInput struct {
	ReplyToMessageID string `json:"replyToMessageId" validate:"omitempty,uuid" msg:"field.reply_to_message_id_invalid"`
	ClientMessageID  string `json:"clientMessageId" validate:"uuid" msg:"field.client_message_id_invalid"`
	FileID           string `json:"fileId" validate:"uuid" msg:"error.file_not_found"`
	Body             string `json:"body" validate:"max=4000" msg:"field.message_body_too_long"`
	ImageWidth       int    `json:"imageWidth" validate:"min=0"`
	ImageHeight      int    `json:"imageHeight" validate:"min=0"`
}

// ServiceReplyMode 表示 AI 写回复的生成方式。
type ServiceReplyMode = domain.ServiceReplyMode

// ServiceReplyTone 表示 AI 写回复的语气。
type ServiceReplyTone = domain.ServiceReplyTone

// ServiceReplySuggestionsInput 定义 AI 写回复的生成条件，草稿仅在改写模式使用。
type ServiceReplySuggestionsInput struct {
	AgentIdentityID  string           `json:"agentIdentityId" validate:"uuid" msg:"field.agent_identity_id_invalid"`
	Mode             ServiceReplyMode `json:"mode" validate:"oneof=reply rewrite" msg:"field.customer_reply_mode_invalid"`
	Tone             ServiceReplyTone `json:"tone" validate:"oneof=keep professional friendly concise" msg:"field.customer_reply_tone_invalid"`
	Draft            string           `json:"draft"`
	ReplyToMessageID string           `json:"replyToMessageId" validate:"omitempty,uuid" msg:"field.reply_to_message_id_invalid"`
	// Language 是候选回复的书写语言，为空时与客户最近消息的语言一致。
	Language string `json:"language"`
}

// ServiceReplyAgent 定义可用于 AI 写回复的 AI 员工。
type ServiceReplyAgent struct {
	IdentityID  string `json:"identityId"`
	DisplayName string `json:"displayName"`
}

// ServiceReplyAgentList 定义可用于 AI 写回复的 AI 员工列表。
type ServiceReplyAgentList struct {
	Agents []ServiceReplyAgent `json:"agents"`
}

// ServiceReplySuggestions 定义可直接填入对客草稿的回复候选。
type ServiceReplySuggestions struct {
	Candidates []string `json:"candidates"`
}

// TransferServiceSessionInput 定义服务周期的转交去向；成员去向填 identityId，团队去向填 teamId，公共队列两者都不填。
type TransferServiceSessionInput struct {
	Kind       ServiceSessionTargetKind `json:"kind"`
	TeamID     string                   `json:"teamId"`
	IdentityID string                   `json:"identityId"`
}

// ServiceSession 定义服务会话最新服务周期。
type ServiceSession struct {
	ID       string               `json:"id"`
	Status   ServiceSessionStatus `json:"status"`
	Assignee *InboxAssignee       `json:"assignee"`
	ClosedAt *time.Time           `json:"closedAt"`
}

// ConversationMessageSender 定义消息发送主体。
type ConversationMessageSender struct {
	ChatSubjectID string                 `json:"chatSubjectId"`
	Kind          ChatSubjectKind        `json:"kind"`
	SourceID      string                 `json:"sourceId"`
	DisplayName   *string                `json:"displayName"`
	ContactNumber *int64                 `json:"contactNumber"`
	AvatarURL     string                 `json:"avatarUrl"`
	IdentityType  *WorkspaceIdentityType `json:"identityType"`
	// PersonalResponsibleName 是发送者为个人 AI 员工时其负责人的名称，其他发送者为空。
	PersonalResponsibleName *string `json:"personalResponsibleName"`
}

// ConversationMessageReference 定义引用消息的一层摘要。
type ConversationMessageReference struct {
	// ExternalSenderName 仅未关联本地原消息的引用返回平台名称，此时 ID 为空。
	ExternalSenderName string                     `json:"externalSenderName,omitempty"`
	Type               MessageType                `json:"type"`
	Visibility         MessageVisibility          `json:"visibility"`
	Deleted            bool                       `json:"deleted"`
	ID                 string                     `json:"id"`
	Body               string                     `json:"body"`
	Sender             *ConversationMessageSender `json:"sender"`
}

// ConversationMessageMention 定义消息提醒的聊天主体。
type ConversationMessageMention struct {
	ChatSubjectID string          `json:"chatSubjectId"`
	Kind          ChatSubjectKind `json:"kind"`
	SourceID      string          `json:"sourceId"`
	DisplayName   *string         `json:"displayName"`
}

// ConversationMessageSessionStart 定义客服处理周期开始标记。
type ConversationMessageSessionStart struct {
	Sequence  int64                `json:"sequence"`
	StartedAt time.Time            `json:"startedAt"`
	Status    ServiceSessionStatus `json:"status"`
}

// ConversationSystemEventParticipant 定义系统事件中的成员快照。
type ConversationSystemEventParticipant struct {
	IdentityID  string `json:"identityId"`
	DisplayName string `json:"displayName"`
	// PersonalResponsibleName 是成员为个人 AI 员工时事件写入时其负责人的名称，其他成员为空。
	PersonalResponsibleName *string `json:"personalResponsibleName"`
}

// ConversationSystemEvent 定义成员可见的系统事件。
type ConversationSystemEvent struct {
	Type          ConversationSystemEventType          `json:"type"`
	Actor         ConversationSystemEventParticipant   `json:"actor"`
	Targets       []ConversationSystemEventParticipant `json:"targets"`
	PreviousTitle *string                              `json:"previousTitle"`
	Title         *string                              `json:"title"`
	// 以下字段只由客服处理周期事件携带：原负责人、去向，转人工或退回队列的原因与成员可见的原因说明，转人工时的咨询分类，关闭事件的结束方式，访客评价的是否解决与评语，以及访客接收回复的邮箱；操作人写入 Actor。
	ServiceSessionID *string                     `json:"serviceSessionId"`
	FromIdentityID   *string                     `json:"fromIdentityId"`
	FromDisplayName  *string                     `json:"fromDisplayName"`
	SessionTarget    *ServiceSessionTarget       `json:"sessionTarget"`
	HandoffReason    *AgentHandoffReason         `json:"handoffReason"`
	ReturnReason     *ServiceSessionReturnReason `json:"returnReason"`
	CloseReason      *ServiceSessionCloseReason  `json:"closeReason"`
	ReasonText       *string                     `json:"reasonText"`
	CategoryName     *string                     `json:"categoryName"`
	AgentRunID       *string                     `json:"agentRunId"`
	RatingResolved   *bool                       `json:"ratingResolved"`
	RatingComment    *string                     `json:"ratingComment"`
	Email            *string                     `json:"email"`
	// ServiceStatus 只由服务进度事件携带，转交与处理中的去向写入 SessionTarget，结束方式写入 CloseReason。
	ServiceStatus *ServiceRequestStatus `json:"serviceStatus"`
	// ToolCall 只由 AI 员工操作事件携带，是该操作的当前内容；提交它的 AI 员工写入 Actor。
	ToolCall *AgentToolDecision `json:"toolCall"`
}

// ServiceRequestStatus 表示企业成员发起人看到的服务进度：handed_off 已转交队列或团队，processing 由成员处理中，closed 本次服务已结束。
type ServiceRequestStatus = domain.ServiceRequestStatus

// ConversationMessage 定义成员可见的会话消息。
type ConversationMessage struct {
	// CanReply 表示可以在对客回复中引用该消息，CanNoteReply 表示可以在内部备注中引用该消息。
	CanReply     bool `json:"canReply"`
	CanNoteReply bool `json:"canNoteReply"`
	// ClientMessageID 仅向原发送身份返回。
	ClientMessageID *string                   `json:"clientMessageId"`
	Attachment      *MessageAttachment        `json:"attachment"`
	AgentProcess    *ConversationAgentProcess `json:"agentProcess"`
	AgentErrorCode  *string                   `json:"agentErrorCode"` // AI 回复失败消息对应运行的稳定失败原因，没有时为空。
	// LocalAgentReply 是产生该消息的委派本机 Agent 的一轮，其他消息为空；LocalAgentTurns 是以该消息作答的运行交给本机 Agent 的轮次，按提交顺序排列。
	LocalAgentReply *ConversationLocalAgentTurn      `json:"localAgentReply"`
	LocalAgentTurns []ConversationLocalAgentTurn     `json:"localAgentTurns"`
	MessageSeq      string                           `json:"messageSeq"`
	ID              string                           `json:"id"`
	Type            MessageType                      `json:"type"`
	Visibility      MessageVisibility                `json:"visibility"`
	Body            string                           `json:"body"`
	Language        string                           `json:"language"`
	Translation     *ConversationMessageTranslation  `json:"translation"`
	OriginatedAt    time.Time                        `json:"originatedAt"`
	CreatedAt       time.Time                        `json:"createdAt"`
	Sender          *ConversationMessageSender       `json:"sender"`
	SessionStart    *ConversationMessageSessionStart `json:"sessionStart"`
	SystemEvent     *ConversationSystemEvent         `json:"systemEvent"`
	ReplyTo         *ConversationMessageReference    `json:"replyTo"`
	Mentions        []ConversationMessageMention     `json:"mentions"`
	MentionAll      bool                             `json:"mentionAll"`
	// Delivery 是客户消息的外部投递状态，没有外部投递记录时为空。
	Delivery *ChannelMessageDelivery `json:"delivery"`
}

// ConversationLocalAgentTurn 定义委派本机 Agent 的一轮：工具调用编号与本机 Agent 名称。
type ConversationLocalAgentTurn struct {
	ToolCallID string `json:"toolCallId"`
	LocalAgent string `json:"localAgent"`
}

// ConversationMessageList 定义成员消息页。
type ConversationMessageList struct {
	AgentRuns     []ConversationAgentRun     `json:"agentRuns"`
	PendingAgents []ConversationPendingAgent `json:"pendingAgents"`
	HasEarlier    bool                       `json:"hasEarlier"`
	HasLater      bool                       `json:"hasLater"`
	Messages      []ConversationMessage      `json:"messages"`
	Before        *string                    `json:"before"`
	After         *string                    `json:"after"`
}

// ConversationPendingAgent 定义已收到输入、等待轮转执行的 AI 员工。
type ConversationPendingAgent struct {
	IdentityID  string `json:"identityId"`
	DisplayName string `json:"displayName"`
	AvatarURL   string `json:"avatarUrl"`
	// PersonalResponsibleName 是等待者为个人 AI 员工时其负责人的名称，其他 AI 员工为空。
	PersonalResponsibleName *string `json:"personalResponsibleName"`
}

// MarkConversationReadInput 定义用户确认已读的消息水位。
type MarkConversationReadInput struct {
	LastReadMessageID string `json:"lastReadMessageId" validate:"uuid" msg:"field.client_message_id_invalid"`
	ClearUnreadMark   bool   `json:"clearUnreadMark"`
}

// ConversationReadState 定义用户会话的已读水位。
type ConversationReadState struct {
	ReadSeq           string    `json:"readSeq"`
	LastReadMessageID string    `json:"lastReadMessageId"`
	LastReadAt        time.Time `json:"lastReadAt"`
}

// FirstDirectTextMessageInput 定义成员向目标身份发送的首条单聊消息。
type FirstDirectTextMessageInput struct {
	TargetIdentityID string `json:"targetIdentityId" validate:"uuid" msg:"field.target_identity_id_invalid"`
	ClientMessageID  string `json:"clientMessageId" validate:"uuid" msg:"field.client_message_id_invalid"`
	Body             string `json:"body" validate:"notblank,max=4000" msg:"notblank=field.message_body_required,max=field.message_body_too_long"`
}

// FirstDirectTextMessageResult 定义首条单聊消息及其确定的长期会话。
type FirstDirectTextMessageResult struct {
	Conversation InboxConversation   `json:"conversation"`
	Message      ConversationMessage `json:"message"`
}

// DirectConversationLookup 定义按目标身份查找单聊的结果。
type DirectConversationLookup struct {
	Conversation *InboxConversation `json:"conversation"`
}

// DirectTextMessageInput 定义成员发送的内部单聊文本消息。
type DirectTextMessageInput struct {
	ClientMessageID  string `json:"clientMessageId" validate:"uuid" msg:"field.client_message_id_invalid"`
	Body             string `json:"body" validate:"notblank,max=4000" msg:"notblank=field.message_body_required,max=field.message_body_too_long"`
	ReplyToMessageID string `json:"replyToMessageId" validate:"omitempty,uuid" msg:"field.reply_to_message_id_invalid"`
}

// GroupConversationInput 定义群聊资料和创建时加入的成员。
type GroupConversationInput struct {
	// Title 为空时创建未命名的群，按成员名称显示。
	Title             string   `json:"title" validate:"max=100" msg:"field.group_title_too_long"`
	Description       string   `json:"description" validate:"max=500" msg:"field.group_description_too_long"`
	ImageFileID       string   `json:"imageFileId" validate:"omitempty,uuid" msg:"field.group_image_file_id_invalid"`
	MemberIdentityIDs []string `json:"memberIdentityIds" validate:"min=1,max=99,unique,dive,uuid" msg:"min=field.group_members_required,max=field.group_members_too_many,unique=field.group_member_ids_invalid,uuid=field.group_member_ids_invalid"`
}

// GroupConversationProfileInput 定义群聊资料修改参数。
type GroupConversationProfileInput struct {
	Title       string `json:"title" validate:"max=100" msg:"field.group_title_too_long"`
	Description string `json:"description" validate:"max=500" msg:"field.group_description_too_long"`
	// ImageFileID 为 nil 时保留当前图片，非 nil 时关联新图片。
	ImageFileID *string `json:"imageFileId" validate:"omitnil,uuid" msg:"field.group_image_file_id_invalid"`
}

// GroupConversationMembersInput 定义群聊批量增员参数。
type GroupConversationMembersInput struct {
	MemberIdentityIDs []string `json:"memberIdentityIds" validate:"min=1,max=99,unique,dive,uuid" msg:"min=field.group_members_required,max=field.group_members_too_many,unique=field.group_member_ids_invalid,uuid=field.group_member_ids_invalid"`
}

// GroupConversationMemberInput 定义群聊单个成员操作参数。
type GroupConversationMemberInput struct {
	MemberIdentityID string `json:"memberIdentityId" validate:"uuid" msg:"field.group_member_id_invalid"`
}

// GroupConversationOwnerInput 定义群主转让参数。
type GroupConversationOwnerInput struct {
	OwnerIdentityID string `json:"ownerIdentityId" validate:"uuid" msg:"field.group_owner_id_invalid"`
}

// GroupParticipant 定义群聊当前有效成员。
type GroupParticipant struct {
	IdentityType  WorkspaceIdentityType `json:"identityType"`
	ChatSubjectID string                `json:"chatSubjectId"`
	IdentityID    string                `json:"identityId"`
	DisplayName   string                `json:"displayName"`
	AvatarURL     string                `json:"avatarUrl"`
	Role          GroupParticipantRole  `json:"role"`
	// PersonalResponsibleName 是成员为个人 AI 员工时其负责人的名称，其他成员为空。
	PersonalResponsibleName *string `json:"personalResponsibleName"`
	// PersonalResponsibleIdentityID 是成员为个人 AI 员工时其负责人的企业身份编号，其他成员为空。
	PersonalResponsibleIdentityID *string `json:"personalResponsibleIdentityId"`
}

// GroupConversation 定义群聊资料和当前有效成员。
type GroupConversation struct {
	ID           string             `json:"id"`
	Title        string             `json:"title"`
	Description  string             `json:"description"`
	ImageURL     string             `json:"imageUrl"`
	Status       ConversationStatus `json:"status"`
	CreatedAt    time.Time          `json:"createdAt"`
	Participants []GroupParticipant `json:"participants"`
	Muted        bool               `json:"muted"`
	// ArchivedAt 是当前用户归档该群聊的时间，未归档时为空。
	ArchivedAt *time.Time `json:"archivedAt"`
	// MemberPreviewNames 是除查看者外按入群先后排列的前几名在群成员名称，用于显示未命名的群。
	MemberPreviewNames []string `json:"memberPreviewNames"`
}

// GroupTextMessageInput 定义成员发送的群聊文本消息。
type GroupTextMessageInput struct {
	ClientMessageID   string   `json:"clientMessageId" validate:"uuid" msg:"field.client_message_id_invalid"`
	Body              string   `json:"body" validate:"notblank,max=4000" msg:"notblank=field.message_body_required,max=field.message_body_too_long"`
	ReplyToMessageID  string   `json:"replyToMessageId" validate:"omitempty,uuid" msg:"field.reply_to_message_id_invalid"`
	MentionSubjectIDs []string `json:"mentionSubjectIds" validate:"max=99,unique,dive,uuid" msg:"field.mention_subject_ids_invalid"`
	MentionAll        bool     `json:"mentionAll"`
}

// ConversationUnreadMarkInput 定义当前用户的独立未读标记。
type ConversationUnreadMarkInput struct {
	MarkedUnread bool `json:"markedUnread"`
}

// ConversationTypingInput 定义当前用户在会话中的输入状态。
type ConversationTypingInput struct {
	Active bool `json:"active"`
}

// ConversationArchiveInput 定义当前用户的会话归档状态。
type ConversationArchiveInput struct {
	Archived bool `json:"archived"`
}

// ConversationNotificationSettingsInput 定义当前用户的会话提醒设置。
type ConversationNotificationSettingsInput struct {
	Muted bool `json:"muted"`
}

// ConversationNotificationSettings 定义当前用户保存后的会话提醒设置。
type ConversationNotificationSettings struct {
	Muted bool `json:"muted"`
}

// ConversationNavigationState 定义群聊可见尾端和提及查看进度。
type ConversationNavigationState struct {
	PendingMentionCount      int     `json:"pendingMentionCount"`
	ReviewedThroughMessageID *string `json:"reviewedThroughMessageId"`
	ReviewedThroughSequence  string  `json:"reviewedThroughSequence"`
	LatestMessageID          *string `json:"latestMessageId"`
	LatestSequence           string  `json:"latestSequence"`
}

// PendingConversationMentions 定义本轮固定提及目标及序号上界。
type PendingConversationMentions struct {
	MessageIDs         []string `json:"messageIds"`
	LastTargetSequence *string  `json:"lastTargetSequence"`
}

// MarkConversationMentionReviewedInput 定义待确认的提及目标。
type MarkConversationMentionReviewedInput struct {
	MessageID string `json:"messageId" validate:"uuid" msg:"field.client_message_id_invalid"`
}

// ConversationMentionReviewOutcome 定义提及确认结果。
type ConversationMentionReviewOutcome string

const (
	ConversationMentionReviewed        ConversationMentionReviewOutcome = "reviewed"
	ConversationMentionAlreadyReviewed ConversationMentionReviewOutcome = "alreadyReviewed"
	ConversationMentionUnavailable     ConversationMentionReviewOutcome = "unavailable"
)

// ConversationMentionReview 定义连续确认后的服务端水位。
type ConversationMentionReview struct {
	ReviewedThroughMessageID *string                          `json:"reviewedThroughMessageId"`
	ReviewedThroughSequence  string                           `json:"reviewedThroughSequence"`
	Outcome                  ConversationMentionReviewOutcome `json:"outcome"`
}

// FirstAgentTextMessageInput 定义 AI 草稿的稳定编号、目标和首条消息。
type FirstAgentTextMessageInput struct {
	ConversationID  string `json:"conversationId" validate:"uuid" msg:"field.conversation_id_invalid"`
	AgentIdentityID string `json:"agentIdentityId" validate:"uuid" msg:"field.target_identity_id_invalid"`
	ClientMessageID string `json:"clientMessageId" validate:"uuid" msg:"field.client_message_id_invalid"`
	Body            string `json:"body" validate:"notblank,max=4000" msg:"notblank=field.message_body_required,max=field.message_body_too_long"`
}

// FirstAgentTextMessageResult 定义首次发送确认的 AI 会话和消息。
type FirstAgentTextMessageResult struct {
	Conversation InboxConversation   `json:"conversation"`
	Message      ConversationMessage `json:"message"`
}

// AgentTextMessageInput 定义发给 AI 会话的成员消息。
type AgentTextMessageInput struct {
	ClientMessageID  string `json:"clientMessageId" validate:"uuid" msg:"field.client_message_id_invalid"`
	Body             string `json:"body" validate:"notblank,max=4000" msg:"notblank=field.message_body_required,max=field.message_body_too_long"`
	ReplyToMessageID string `json:"replyToMessageId" validate:"omitempty,uuid" msg:"field.reply_to_message_id_invalid"`
}

// AttachmentMessageInput 定义已上传附件的发送意图，agentIdentityId 非空表示按 conversationId 草稿编号首发 AI 聊天，同时指定 servedConversationId 表示首发该服务会话的 Copilot 线程。
type AttachmentMessageInput struct {
	ConversationID       string `json:"conversationId"`
	TargetIdentityID     string `json:"targetIdentityId"`
	AgentIdentityID      string `json:"agentIdentityId"`
	ServedConversationID string `json:"servedConversationId"`
	ClientMessageID      string `json:"clientMessageId"`
	FileID               string `json:"fileId"`
	Body                 string `json:"body"`
	ImageWidth           int    `json:"imageWidth"`
	ImageHeight          int    `json:"imageHeight"`
}

// AttachmentMessageResult 定义附件消息及首发时创建的单聊或 AI 聊天。
type AttachmentMessageResult struct {
	ConversationID string              `json:"conversationId"`
	Conversation   *InboxConversation  `json:"conversation"`
	Message        ConversationMessage `json:"message"`
}

// MessageAttachmentTransferStatus 表示附件内容的取回状态。
type MessageAttachmentTransferStatus = domain.MessageAttachmentTransferStatus

// MessageAttachment 定义消息的文件信息、图片尺寸与内容取回状态。
type MessageAttachment struct {
	File
	TransferStatus MessageAttachmentTransferStatus `json:"transferStatus"`
	ImageWidth     int                             `json:"imageWidth"`
	ImageHeight    int                             `json:"imageHeight"`
}

// ConversationMessageTranslation 定义消息的一份译文。
type ConversationMessageTranslation struct {
	Language string `json:"language"`
	Body     string `json:"body"`
}

// ConversationTranslation 定义当前成员在客户会话中的翻译状态。
type ConversationTranslation struct {
	// Enabled 表示企业已设置翻译模型。
	Enabled bool `json:"enabled"`
	// ViewerLanguage 是当前成员阅读译文与书写回复的语言。
	ViewerLanguage string `json:"viewerLanguage"`
	// CustomerLanguage 是对客回复使用的客户语言，尚无法确定时为空字符串。
	CustomerLanguage string `json:"customerLanguage"`
	// ReplyLanguageLocked 表示客户语言由客服手动锁定。
	ReplyLanguageLocked bool `json:"replyLanguageLocked"`
}

// TranslateConversationMessagesInput 定义待翻译的消息编号。
type TranslateConversationMessagesInput struct {
	MessageIDs []string `json:"messageIds" validate:"min=1,max=50,unique,dive,uuid" msg:"field.translation_messages_invalid"`
}

// ConversationMessageTranslationResult 定义一条消息的翻译结果；成员可直接阅读正文或翻译失败时 Body 为空字符串。
type ConversationMessageTranslationResult struct {
	MessageID string `json:"messageId"`
	Language  string `json:"language"`
	Body      string `json:"body"`
}

// ConversationMessageTranslationList 定义一批消息的翻译结果，不可翻译的消息不出现在结果中。
type ConversationMessageTranslationList struct {
	Translations []ConversationMessageTranslationResult `json:"translations"`
}

// CustomerReplyLanguageInput 定义锁定的对客回复语言，为空字符串时恢复按客户最近消息的语言回复。
type CustomerReplyLanguageInput struct {
	Language string `json:"language"`
}

// CustomerReplyTranslationInput 定义待预览翻译的客服回复。
type CustomerReplyTranslationInput struct {
	Body string `json:"body" validate:"notblank,max=4000" msg:"notblank=field.message_body_required,max=field.message_body_too_long"`
}

// CustomerReplyTranslation 定义客服回复发给客户的译文。
type CustomerReplyTranslation struct {
	Language string `json:"language"`
	Body     string `json:"body"`
}

// CustomerReplyTranslationPreview 定义发送前预览的译文与回译；客户语言与客服语言相同时 Language 为空字符串，按原文发送。
type CustomerReplyTranslationPreview struct {
	Language        string `json:"language"`
	Body            string `json:"body"`
	BackTranslation string `json:"backTranslation"`
}
