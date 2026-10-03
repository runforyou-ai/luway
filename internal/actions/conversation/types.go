//go:build server

// Package conversation 提供各类会话共用的消息模型、历史读取、会话状态与消息写入能力。
package conversation

import (
	"time"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime/runstream"
)

// ValidationCode 标识会话业务输入的校验结果。
type ValidationCode = common.FieldCode

const (
	ValidationConversationIDInvalid    ValidationCode = "conversation_id_invalid"
	ValidationTargetIdentityIDInvalid  ValidationCode = "target_identity_id_invalid"
	ValidationClientMessageIDInvalid   ValidationCode = "client_message_id_invalid"
	ValidationLastReadMessageIDInvalid ValidationCode = "last_read_message_id_invalid"
	ValidationReplyToMessageIDInvalid  ValidationCode = "reply_to_message_id_invalid"
	ValidationBodyRequired             ValidationCode = "body_required"
	ValidationBodyTooLong              ValidationCode = "body_too_long"
	ValidationCursorInvalid            ValidationCode = "cursor_invalid"
	ValidationFileIDInvalid            ValidationCode = "file_id_invalid"
	ValidationNeighborIDInvalid        ValidationCode = "neighbor_id_invalid"
	ValidationPinPositionInvalid       ValidationCode = "pin_position_invalid"
	ValidationPinOrderVersionInvalid   ValidationCode = "pin_order_version_invalid"
)

const (
	// ConflictReasonIdempotencyMismatch 表示同一消息编号对应了不同写入意图。
	ConflictReasonIdempotencyMismatch = "idempotency_mismatch"
	// ConflictReasonServiceSessionOwned 表示服务周期已由其他主体负责。
	ConflictReasonServiceSessionOwned = "service_session_owned"
	// ConflictReasonServiceSessionNotReplyable 表示服务周期当前不可回复。
	ConflictReasonServiceSessionNotReplyable = "service_session_not_replyable"
	// ConflictReasonChannelOutboundUnavailable 表示来源渠道已停用或尚未配置。
	ConflictReasonChannelOutboundUnavailable = "channel_outbound_unavailable"
	// ConflictReasonChannelOutboundUnsupported 表示来源渠道尚不支持外发。
	ConflictReasonChannelOutboundUnsupported = "channel_outbound_unsupported"
	// ConflictReasonReplyTargetInvalid 表示当前会话的引用消息校验失败。
	ConflictReasonReplyTargetInvalid = "reply_target_invalid"
	// ConflictReasonAttachmentTooLarge 表示附件超过来源渠道的字节上限。
	ConflictReasonAttachmentTooLarge = "attachment_too_large"
	// ConflictReasonPersonalAgentPaused 表示个人 AI 员工已被负责人暂停，不接收新请求。
	ConflictReasonPersonalAgentPaused = "personal_agent_paused"
	// ConflictReasonPersonalAgentUnbound 表示个人 AI 员工使用的电脑已撤销，负责人换到新电脑前不接收新请求。
	ConflictReasonPersonalAgentUnbound = "personal_agent_unbound"
	// ConflictReasonPinOrderVersionStale 表示提交的置顶顺序版本不是当前版本。
	ConflictReasonPinOrderVersionStale = "pin_order_version_stale"
	// ConflictReasonPinNeighborNotPinned 表示置顶顺序的邻居会话当前不在置顶区。
	ConflictReasonPinNeighborNotPinned = "pin_neighbor_not_pinned"
)

// MessageCursorPoint 定义消息分页稳定边界。
type MessageCursorPoint struct {
	MessageSeq int64
	ID         string
}

// ConversationMessageSender 定义成员可见的消息发送主体。
type ConversationMessageSender struct {
	ChatSubjectID string
	Kind          domain.ChatSubjectKind
	SourceID      string
	DisplayName   *string
	ContactNumber *int64
	AvatarFileID  *string
	IdentityType  *domain.OrganizationIdentityType
	// PersonalResponsibleName 是发送者为个人 AI 员工时其负责人的名称，其他发送者为空。
	PersonalResponsibleName *string
}

// ConversationMessageReference 定义引用消息的一层摘要。
type ConversationMessageReference struct {
	ExternalSenderName string
	Type               domain.MessageType
	Visibility         domain.MessageVisibility
	Deleted            bool
	ID                 string
	Body               string
	Sender             *ConversationMessageSender
}

// ConversationMessageMention 定义消息提醒的聊天主体。
type ConversationMessageMention struct {
	ChatSubjectID string
	Kind          domain.ChatSubjectKind
	SourceID      string
	DisplayName   *string
	IdentityType  domain.OrganizationIdentityType
}

// ConversationMessageSessionStart 定义客服处理周期开始标记。
type ConversationMessageSessionStart struct {
	Sequence  int64
	StartedAt time.Time
	Status    domain.ServiceSessionStatus
}

// ConversationSystemEventParticipant 定义系统事件中的成员快照。
type ConversationSystemEventParticipant struct {
	IdentityID  string `json:"identityId"`
	DisplayName string `json:"displayName"`
	// PersonalResponsibleName 是成员为个人 AI 员工时事件写入时其负责人的名称，其他成员为空。
	PersonalResponsibleName *string `json:"personalResponsibleName,omitempty"`
}

// ConversationSystemEvent 定义会话系统事件及其审计载荷。
type ConversationSystemEvent struct {
	Type          domain.ConversationSystemEventType   `json:"-"`
	Actor         ConversationSystemEventParticipant   `json:"actor"`
	Targets       []ConversationSystemEventParticipant `json:"targets"`
	PreviousTitle *string                              `json:"previousTitle,omitempty"`
	Title         *string                              `json:"title,omitempty"`
	// 以下字段只由 service_session_* 与 service_status_changed 事件携带，结构与 domain.ServiceSessionHandedOffEvent、domain.ServiceSessionOperatedEvent、domain.ServiceSessionReturnedEvent、domain.ServiceSessionAssignedEvent、domain.ServiceSessionRatedEvent、domain.ServiceSessionEmailEvent、domain.ServiceStatusChangedEvent 一致。
	ServiceSessionID *string                            `json:"serviceSessionId,omitempty"`
	ActorIdentityID  *string                            `json:"actorIdentityId,omitempty"`
	ActorDisplayName *string                            `json:"actorDisplayName,omitempty"`
	FromIdentityID   *string                            `json:"fromIdentityId,omitempty"`
	FromDisplayName  *string                            `json:"fromDisplayName,omitempty"`
	Target           *domain.ServiceSessionTarget       `json:"target,omitempty"`
	Reason           *domain.AgentHandoffReason         `json:"reason,omitempty"`
	ReturnReason     *domain.ServiceSessionReturnReason `json:"returnReason,omitempty"`
	CloseReason      *domain.ServiceSessionCloseReason  `json:"closeReason,omitempty"`
	ReasonText       *string                            `json:"reasonText,omitempty"`
	CategoryName     *string                            `json:"categoryName,omitempty"`
	AgentRunID       *string                            `json:"agentRunId,omitempty"`
	Resolved         *bool                              `json:"resolved,omitempty"`
	Comment          *string                            `json:"comment,omitempty"`
	Email            *string                            `json:"email,omitempty"`
	Status           *domain.ServiceRequestStatus       `json:"status,omitempty"`
}

// ConversationMessage 定义成员可见的会话消息。
type ConversationMessage struct {
	ReplyUnavailable bool
	ClientMessageID  *string
	Attachment       *MessageAttachment
	AgentProcess     *ConversationAgentProcess
	AgentErrorCode   *domain.AgentRunErrorCode // AI 回复失败消息对应运行的稳定失败原因，没有时为空。
	MessageSeq       int64
	ID               string
	Type             domain.MessageType
	Visibility       domain.MessageVisibility
	Body             string
	// Language 是正文语言，尚未识别时为空。
	Language *string
	// Translation 是当前成员语言的译文，没有译文时为空。
	Translation  *MessageTranslation
	OriginatedAt time.Time
	SourceOrder  int64
	CreatedAt    time.Time
	Sender       *ConversationMessageSender
	SessionStart *ConversationMessageSessionStart
	SystemEvent  *ConversationSystemEvent
	ReplyTo      *ConversationMessageReference
	Mentions     []ConversationMessageMention
	MentionAll   bool
	// Delivery 是客户消息的外部投递状态，没有外部投递记录时为空。
	Delivery *MessageDelivery
}

// MessageDelivery 定义客户消息的外部投递状态与成员当前可执行的处理。
type MessageDelivery struct {
	ID        string
	Status    domain.CustomerDeliveryStatus
	LastError string
	CanRetry  bool
	Paused    bool
}

// MessageTranslation 定义消息的一份译文。
type MessageTranslation struct {
	Language string
	Body     string
}

// ConversationMessageHistoryInput 定义成员消息历史查询方向；Start 与 End 同时提供时读取包含两端的连续范围。
type ConversationMessageHistoryInput struct {
	ConversationID  string
	Before          *MessageCursorPoint
	After           *MessageCursorPoint
	AroundMessageID string
	Start           *MessageCursorPoint
	End             *MessageCursorPoint
}

// ConversationMessageHistory 定义成员消息历史和下一页边界。
type ConversationMessageHistory struct {
	AgentRuns     []ConversationAgentRun
	PendingAgents []ConversationPendingAgent
	HasEarlier    bool
	HasLater      bool
	Messages      []ConversationMessage
	Before        *MessageCursorPoint
	After         *MessageCursorPoint
}

// ConversationPendingAgent 定义已收到输入、等待轮转执行的 AI 员工。
type ConversationPendingAgent struct {
	IdentityID   string
	DisplayName  string
	AvatarFileID *string
	// PersonalResponsibleName 是等待者为个人 AI 员工时其负责人的名称，其他 AI 员工为空。
	PersonalResponsibleName *string
}

// ConversationAgentProcess 定义已完成运行的过程引用和模型用量。
type ConversationAgentProcess struct {
	ID                   string
	DurationMilliseconds int64
	Usage                agentruntime.Usage
	Outcome              *domain.AgentRunOutcome
	OutcomeReason        *domain.AgentHandoffReason
}

// AgentRunProcess 定义一次已完成运行的有序过程内容、任务清单和模型用量。
type AgentRunProcess struct {
	ID                   string
	DurationMilliseconds int64
	Usage                agentruntime.Usage
	Outcome              *domain.AgentRunOutcome
	OutcomeReason        *domain.AgentHandoffReason
	Blocks               []agentruntime.Block
	Plan                 []runstream.PlanTask
}

// ConversationAgentRun 定义尚未由结果消息表达的运行状态，取消运行携带自身过程引用。
type ConversationAgentRun struct {
	AgentAvatarFileID *string
	AgentName         string
	// AgentPersonalResponsibleName 是执行者为个人 AI 员工时其负责人的名称，其他 AI 员工为空。
	AgentPersonalResponsibleName *string
	ID                           string
	AgentIdentityID              string
	Status                       domain.AgentRunStatus
	ErrorCode                    *string
	LastError                    *string
	Process                      *ConversationAgentProcess
}

// ConversationNotificationSettings 定义当前用户的会话提醒设置。
type ConversationNotificationSettings struct {
	Muted bool
}

// MessageAttachment 定义消息文件的元数据。
type MessageAttachment struct {
	ImageWidth     int                                    `bun:"image_width"`
	ImageHeight    int                                    `bun:"image_height"`
	ID             string                                 `bun:"id"`
	Name           string                                 `bun:"name"`
	ContentType    string                                 `bun:"content_type"`
	ByteSize       int64                                  `bun:"byte_size"`
	TransferStatus domain.MessageAttachmentTransferStatus `bun:"transfer_status"`
}
