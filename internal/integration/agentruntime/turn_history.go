package agentruntime

import (
	"context"
	"slices"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/luway/internal/domain"
)

// turnHistory 按消费顺序保留当前运行的会话消息和完整工具交互。
type turnHistory struct {
	messages   []*schema.AgenticMessage
	seen       map[string]struct{}
	mediaCount int
	mediaBytes int64
}

// appendInput 按消息编号与修订去重，把新消息追加在上一轮中间结果之后；模型支持的附件由新到旧在预算内随消息直传。
func (h *turnHistory) appendInput(ctx context.Context, messages []Message, media mediaInput) []*schema.AgenticMessage {
	if h.seen == nil {
		h.seen = make(map[string]struct{})
	}
	fresh := make([]Message, 0, len(messages))
	for _, message := range messages {
		key := message.ID + "@" + message.Revision
		if _, exists := h.seen[key]; exists {
			continue
		}
		h.seen[key] = struct{}{}
		fresh = append(fresh, message)
	}
	// 由新到旧读取模型支持格式的用户附件，读取成功才计入数量和字节预算，其余附件只保留正文中的链接。
	inline := make(map[string]*schema.AgenticMessage)
	for i := len(fresh) - 1; i >= 0; i-- {
		attachment := fresh[i].Media
		if attachment == nil || fresh[i].Role != MessageRoleUser || h.mediaCount >= media.maxCount ||
			attachment.ByteSize > domain.AgentMediaMaxBytes || h.mediaBytes+attachment.ByteSize > maxRunMediaBytes {
			continue
		}
		modality, supported := inlineMediaTypes[attachment.MIMEType]
		if !supported || !media.modalities[modality] {
			continue
		}
		direct, read := mediaUserMessage(ctx, fresh[i], modality, media.read)
		if !read {
			continue
		}
		inline[fresh[i].ID] = direct
		h.mediaCount++
		h.mediaBytes += attachment.ByteSize
	}
	for _, message := range fresh {
		direct, inlined := inline[message.ID]
		switch {
		case message.Role == MessageRoleAssistant:
			h.messages = append(h.messages, &schema.AgenticMessage{
				Role:          schema.AgenticRoleTypeAssistant,
				ContentBlocks: []*schema.ContentBlock{schema.NewContentBlock(&schema.AssistantGenText{Text: message.Content})},
			})
		case inlined:
			h.messages = append(h.messages, direct)
		default:
			h.messages = append(h.messages, schema.UserAgenticMessage(message.Content))
		}
	}
	return append([]*schema.AgenticMessage(nil), h.messages...)
}

// appendOutput 保留含正文或工具调用的模型输出与全部工具结果；没有结果的工具调用由修补中间件在调用模型前补上取消说明。
func (h *turnHistory) appendOutput(messages []*schema.AgenticMessage) {
	for _, message := range messages {
		if message.Role != schema.AgenticRoleTypeAssistant || hasToolCalls(message) || assistantText(message) != "" {
			h.messages = append(h.messages, message)
		}
	}
}

// compact 按摘要事件替换历史并返回保留的本轮中间消息：保留点在历史中时把其之前的历史替换为摘要与事件中保留的消息，在中间消息中时历史替换为摘要与事件中保留的消息，并丢弃保留点之前的中间消息；找不到保留点时保持不变。
func (h *turnHistory) compact(compaction *historyCompacted, intermediates []*schema.AgenticMessage) []*schema.AgenticMessage {
	if compaction.keepFromCallID == "" {
		for i, message := range h.messages {
			if adk.GetMessageID(message) == compaction.keepFromID {
				h.messages = slices.Concat([]*schema.AgenticMessage{compaction.summary}, compaction.kept, h.messages[i:])
				break
			}
		}
		return intermediates
	}
	for i, message := range intermediates {
		if message.Role == schema.AgenticRoleTypeAssistant && slices.ContainsFunc(toolCalls(message), func(call *schema.FunctionToolCall) bool { return call.CallID == compaction.keepFromCallID }) {
			h.messages = append([]*schema.AgenticMessage{compaction.summary}, compaction.kept...)
			return intermediates[i:]
		}
	}
	return intermediates
}
