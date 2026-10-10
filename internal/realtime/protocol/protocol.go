// Package protocol 定义实时推送的 JSON 事件契约与编解码；go generate ./internal/appservice 据此生成 frontend/src/api/generated/realtime.ts 的 TypeScript 类型与 Zod 解码模式。
package protocol

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
)

// Version 是当前协议主版本，只有破坏性演进才提升。
const Version = 1

var (
	// ErrUnknownFrame 表示未定义的事件种类，接收方忽略该事件。
	ErrUnknownFrame = errors.New("realtime protocol: unknown event type")
	// ErrUnsupportedVersion 表示事件的协议主版本与当前版本不一致。
	ErrUnsupportedVersion = errors.New("realtime protocol: unsupported version")
)

// Type 定义事件种类。
type Type string

// 事件种类。
const (
	TypeServerHello              Type = "server_hello"
	TypeConversationChanged      Type = "conversation_changed"
	TypeConversationRemoved      Type = "conversation_removed"
	TypeConversationStateChanged Type = "conversation_state_changed"
	TypeConversationTyping       Type = "conversation_typing"
	TypeVisitorTyping            Type = "visitor_typing"
	TypeIdentityProfileChanged   Type = "identity_profile_changed"
	TypePinOrderChanged          Type = "pin_order_changed"
	TypeUserNotification         Type = "user_notification"
	TypeRunStreamInvalidated     Type = "run_stream_invalidated"
	TypeRunStreamDelta           Type = "run_stream_delta"
	TypeRunStreamEnded           Type = "run_stream_ended"
	TypeComputerWork             Type = "computer_work"
	TypeAgentMemoryChanged       Type = "agent_memory_changed"
	TypeToolDecisionsChanged     Type = "tool_decisions_changed"
	TypeReceptionChanged         Type = "reception_changed"
	TypeKnowledgeGapsChanged     Type = "knowledge_gaps_changed"
	TypeWorkspaceActivity        Type = "workspace_activity"
	TypeServiceReportsChanged    Type = "service_reports_changed"
	TypeChannelChanged           Type = "channel_changed"
)

// Frame 是可编码的实时事件。
type Frame interface {
	// FrameType 返回事件种类。
	FrameType() Type
}

// ServerHello 返回连接编号和订阅安装后读取的同步探针值；变更通知可能先于本事件到达。
type ServerHello struct {
	ConnectionID string               `json:"connectionId"`
	SyncHeads    appservice.SyncHeads `json:"syncHeads"`
}

// ConversationChanged 表示会话变到了指定版本，成员事件流携带会话类型；Changes 是自上次下发以来的全部变化类别。
type ConversationChanged struct {
	ConversationID   string                     `json:"conversationId"`
	ConversationType domain.ConversationType    `json:"conversationType,omitempty"`
	Version          int64                      `json:"version,string"`
	Changes          domain.ConversationChanges `json:"changes"`
}

// ConversationRemoved 表示当前用户失去指定会话的阅读资格。
type ConversationRemoved struct {
	ConversationID string `json:"conversationId"`
}

// ConversationStateChanged 表示本人对会话的个人状态变到了指定版本。
type ConversationStateChanged struct {
	ConversationID string `json:"conversationId"`
	Version        int64  `json:"version,string"`
}

// ConversationTyping 表示会话中另一参与主体开始或停止输入，只驱动界面临时状态。
type ConversationTyping struct {
	ConversationID  string `json:"conversationId"`
	SenderSubjectID string `json:"senderSubjectId"`
	Active          bool   `json:"active"`
}

// VisitorTyping 表示客户会话中客服或 AI 员工正在准备回复，不携带内部身份编号。
type VisitorTyping struct {
	ConversationID string `json:"conversationId"`
	Active         bool   `json:"active"`
}

// ReceptionChanged 表示企业的网站接待状态可能已变化，访客据此重新读取接待状态。
type ReceptionChanged struct{}

// KnowledgeGapsChanged 表示企业的待补知识或 AI 员工负责人可能已变化，成员据此重新读取待补知识。
type KnowledgeGapsChanged struct{}

// ServiceReportsChanged 表示企业已关闭客服周期的统计口径可能已变化，成员据此重新读取 AI 表现、团队表现报表与问题会话。
type ServiceReportsChanged struct{}

// ChannelChanged 表示渠道的连接状态可能已变化，成员据此重新读取该渠道详情。
type ChannelChanged struct {
	ChannelID string `json:"channelId"`
}

// IdentityProfileChanged 表示本人身份资料变到了指定版本。
type IdentityProfileChanged struct {
	Version int64 `json:"version,string"`
}

// PinOrderChanged 表示本人的个人置顶顺序变到了指定版本，置顶区需要整区重读。
type PinOrderChanged struct {
	Version int64 `json:"version,string"`
}

// UserNotification 是服务端为本人生成的一条通知，只经工作区动态事件流下发：WorkspaceID 与 UserID 是接收成员所在工作区和成员编号，View 是点击后打开会话的视图，标题与正文已按接收人语言生成。
type UserNotification struct {
	WorkspaceID    string                  `json:"workspaceId"`
	UserID         string                  `json:"userId"`
	ID             string                  `json:"id"`
	ConversationID string                  `json:"conversationId"`
	View           domain.NotificationView `json:"view"`
	Title          string                  `json:"title"`
	Body           string                  `json:"body"`
}

// WorkspaceActivity 是工作区动态事件流下发的本人在某个工作区中的变化：Kind 是原成员事件种类（会话变更、移除、个人会话状态或身份资料变化），会话编号与会话变化类别沿用原事件，不携带版本与会话类型；客户端据此刷新各工作区的提醒数量。
type WorkspaceActivity struct {
	WorkspaceID    string                     `json:"workspaceId"`
	Kind           Type                       `json:"kind"`
	ConversationID string                     `json:"conversationId,omitempty"`
	Changes        domain.ConversationChanges `json:"changes"`
}

// AgentMemoryChanged 表示本人负责的个人 AI 员工的记忆已变化，客户端据此重新读取该 AI 员工的记忆。
type AgentMemoryChanged struct {
	AgentID string `json:"agentId"`
}

// ToolDecisionsChanged 表示待本人确认、审批或核对的操作可能已变化，客户端据此重新读取待处理列表。
type ToolDecisionsChanged struct{}

// ComputerWork 表示有派发给本电脑的待执行操作，只发给执行器事件流。
type ComputerWork struct{}

// RunStreamBlock 是运行过程展示块契约。
type RunStreamBlock = appservice.RunStreamBlock

// RunStreamPlanTask 是运行任务清单契约。
type RunStreamPlanTask = appservice.RunStreamPlanTask

// RunStreamToolCall 是运行工具调用契约。
type RunStreamToolCall = appservice.RunStreamToolCall

// RunStreamOperationKind 定义运行过程流增量中的操作类型，与运行流的操作一一对应。
type RunStreamOperationKind string

// 运行过程流增量的操作类型。
const (
	RunStreamUpsertBlock     RunStreamOperationKind = "upsert_block"
	RunStreamAppendBlockText RunStreamOperationKind = "append_block_text"
	RunStreamRemoveBlocks    RunStreamOperationKind = "remove_blocks"
	RunStreamAppendCandidate RunStreamOperationKind = "append_candidate"
	RunStreamClearCandidate  RunStreamOperationKind = "clear_candidate"
	RunStreamSetPlan         RunStreamOperationKind = "set_plan"
)

// RunStreamOperation 是可按顺序应用到运行过程流快照的一条变更。
type RunStreamOperation struct {
	Kind     RunStreamOperationKind `json:"kind"`
	Block    *RunStreamBlock        `json:"block,omitempty"`
	BlockID  string                 `json:"blockId,omitempty"`
	BlockIDs []string               `json:"blockIds,omitempty"`
	Text     string                 `json:"text,omitempty"`
	Plan     []RunStreamPlanTask    `json:"plan,omitempty"`
}

// RunStreamDelta 是运行过程流快照从起始序号到终止序号的增量，起始序号与当前快照序号不一致时接收方重新取快照。
type RunStreamDelta struct {
	RunID        string               `json:"runId"`
	StreamID     string               `json:"streamId"`
	Attempt      int                  `json:"attempt"`
	BaseSequence int64                `json:"baseSequence,string"`
	Sequence     int64                `json:"sequence,string"`
	Operations   []RunStreamOperation `json:"operations"`
}

// RunStreamInvalidated 通知客户端重新读取当前业务快照。
type RunStreamInvalidated struct {
	RunID    string `json:"runId"`
	StreamID string `json:"streamId"`
	Attempt  int    `json:"attempt"`
	Sequence int64  `json:"sequence,string"`
}

// RunStreamEnded 表示本次执行尝试的运行过程流已结束，最终结果以持久查询为准。
type RunStreamEnded struct {
	RunID string `json:"runId"`
}

// FrameType 返回服务端 Hello 事件种类。
func (ServerHello) FrameType() Type { return TypeServerHello }

// FrameType 返回会话变更事件种类。
func (ConversationChanged) FrameType() Type { return TypeConversationChanged }

// FrameType 返回会话失权事件种类。
func (ConversationRemoved) FrameType() Type { return TypeConversationRemoved }

// FrameType 返回本人会话状态变更事件种类。
func (ConversationStateChanged) FrameType() Type { return TypeConversationStateChanged }

// FrameType 返回会话输入状态事件种类。
func (ConversationTyping) FrameType() Type { return TypeConversationTyping }

// FrameType 返回访客可见输入状态事件种类。
func (VisitorTyping) FrameType() Type { return TypeVisitorTyping }

// FrameType 返回接待状态变化事件种类。
func (ReceptionChanged) FrameType() Type { return TypeReceptionChanged }

// FrameType 返回待补知识变化事件种类。
func (KnowledgeGapsChanged) FrameType() Type { return TypeKnowledgeGapsChanged }

// FrameType 返回客服报表变化事件种类。
func (ServiceReportsChanged) FrameType() Type { return TypeServiceReportsChanged }

// FrameType 返回渠道连接状态变化事件种类。
func (ChannelChanged) FrameType() Type { return TypeChannelChanged }

// FrameType 返回身份资料变更事件种类。
func (IdentityProfileChanged) FrameType() Type { return TypeIdentityProfileChanged }

// FrameType 返回个人置顶顺序变更事件种类。
func (PinOrderChanged) FrameType() Type { return TypePinOrderChanged }

// FrameType 返回用户通知事件种类。
func (UserNotification) FrameType() Type { return TypeUserNotification }

// FrameType 返回电脑待执行操作事件种类。
func (ComputerWork) FrameType() Type { return TypeComputerWork }

// FrameType 返回 AI 员工记忆变更事件种类。
func (AgentMemoryChanged) FrameType() Type { return TypeAgentMemoryChanged }

// FrameType 返回待处理操作变更事件种类。
func (ToolDecisionsChanged) FrameType() Type { return TypeToolDecisionsChanged }

// FrameType 返回工作区动态事件种类。
func (WorkspaceActivity) FrameType() Type { return TypeWorkspaceActivity }

// FrameType 返回运行过程流快照失效事件种类。
func (RunStreamInvalidated) FrameType() Type { return TypeRunStreamInvalidated }

// FrameType 返回运行过程流增量事件种类。
func (RunStreamDelta) FrameType() Type { return TypeRunStreamDelta }

// FrameType 返回运行过程流结束事件种类。
func (RunStreamEnded) FrameType() Type { return TypeRunStreamEnded }

// envelope 是事件经 jetcast 传输的外层结构。
type envelope struct {
	V    int             `json:"v"`
	Type Type            `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// decoder 把事件数据解码为具体事件。
type decoder func(json.RawMessage) (Frame, error)

// decoders 是已定义的事件种类。
var decoders = map[Type]decoder{
	TypeServerHello:              decodeAs[ServerHello],
	TypeConversationChanged:      decodeAs[ConversationChanged],
	TypeConversationRemoved:      decodeAs[ConversationRemoved],
	TypeConversationStateChanged: decodeAs[ConversationStateChanged],
	TypeConversationTyping:       decodeAs[ConversationTyping],
	TypeVisitorTyping:            decodeAs[VisitorTyping],
	TypeReceptionChanged:         decodeAs[ReceptionChanged],
	TypeKnowledgeGapsChanged:     decodeAs[KnowledgeGapsChanged],
	TypeServiceReportsChanged:    decodeAs[ServiceReportsChanged],
	TypeChannelChanged:           decodeAs[ChannelChanged],
	TypeIdentityProfileChanged:   decodeAs[IdentityProfileChanged],
	TypePinOrderChanged:          decodeAs[PinOrderChanged],
	TypeUserNotification:         decodeAs[UserNotification],
	TypeRunStreamInvalidated:     decodeAs[RunStreamInvalidated],
	TypeRunStreamDelta:           decodeAs[RunStreamDelta],
	TypeRunStreamEnded:           decodeAs[RunStreamEnded],
	TypeComputerWork:             decodeAs[ComputerWork],
	TypeAgentMemoryChanged:       decodeAs[AgentMemoryChanged],
	TypeToolDecisionsChanged:     decodeAs[ToolDecisionsChanged],
	TypeWorkspaceActivity:        decodeAs[WorkspaceActivity],
}

// Encode 把事件编码为带协议主版本的单行 JSON 文本。
func Encode(frame Frame) ([]byte, error) {
	data, err := json.Marshal(frame)
	if err != nil {
		return nil, fmt.Errorf("encode realtime event %s: %w", frame.FrameType(), err)
	}
	return json.Marshal(envelope{V: Version, Type: frame.FrameType(), Data: data})
}

// Decode 先校验协议主版本再按事件种类解码；只校验结构和类型，业务合法性由接收方判断。
func Decode(data []byte) (Frame, error) {
	var value envelope
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("decode realtime event: %w", err)
	}
	if value.V != Version {
		return nil, ErrUnsupportedVersion
	}
	decodeData, ok := decoders[value.Type]
	if !ok {
		return nil, ErrUnknownFrame
	}
	frame, err := decodeData(value.Data)
	if err != nil {
		return nil, fmt.Errorf("decode realtime event %s: %w", value.Type, err)
	}
	return frame, nil
}

// decodeAs 把事件数据解码为指定事件结构，缺少数据时返回零值事件。
func decodeAs[T Frame](data json.RawMessage) (Frame, error) {
	var frame T
	if len(data) == 0 {
		return frame, nil
	}
	err := json.Unmarshal(data, &frame)
	return frame, err
}
