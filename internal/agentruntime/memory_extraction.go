package agentruntime

import (
	"context"
	"fmt"
	"strings"

	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/einorun/llm"
	"github.com/runforyou-ai/einorun/memory"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/arr"
)

// memoryKeySuffix 是记忆文件名的后缀，提取时去掉后缀作为条目键，结果中补回。
const memoryKeySuffix = ".md"

// memoryExtractionCriteria 是记忆提取中应该记住与不要记住的内容，对方是个人 AI 员工的负责人。
const memoryExtractionCriteria = `你负责整理个人 AI 员工的长期记忆。个人 AI 员工替负责人工作，你根据负责人与个人 AI 员工最近的对话更新个人 AI 员工对负责人的记忆，让个人 AI 员工以后更懂负责人。对话中 user 是负责人，assistant 是个人 AI 员工。

## 应该记住
- 负责人是谁：角色、职责、所在团队与常用的工作资料。
- 负责人的偏好和对个人 AI 员工工作方式的要求，包括纠正过和认可过的做法，并写明原因。
- 进行中的长期工作、目标与约束；相对日期换算为具体日期。
- 常用的外部资源：网址、文档位置、系统名称。

## 不要记住
- 只与这次对话有关的临时状态和任务细节。
- 密码、密钥、验证码等凭据。
- 猜测或负责人没有确认的结论，包括个人 AI 员工自己说过而负责人没有认可的内容。`

// MemoryMessage 是记忆提取资料中的一条对话消息，Sender 为 owner 表示负责人、assistant 表示个人 AI 员工。
type MemoryMessage struct {
	Sender  string `json:"sender"`
	Content string `json:"content"`
}

// MemoryExtractionRequest 定义一次记忆提取：Earlier 是已提取过的最近消息，只用于理解上下文；Recent 是本次需要分析的新消息。
type MemoryExtractionRequest struct {
	Model   ModelConfig
	Entries []MemoryEntry
	Earlier []MemoryMessage
	Recent  []MemoryMessage
}

// MemoryExtractionResult 定义记忆提取的变更：Saved 是新建或改写的条目，Deleted 是删除的条目路径。
type MemoryExtractionResult struct {
	Saved   []MemoryEntry
	Deleted []string
	Usage   agentcontract.Usage
}

// MemoryExtractor 根据新消息更新个人 AI 员工记忆。
type MemoryExtractor interface {
	ExtractMemory(context.Context, MemoryExtractionRequest) (MemoryExtractionResult, error)
}

// ExtractMemory 以一次关闭思考的结构化调用根据新消息给出记忆变更，名称、说明与正文按记忆的长度上限校验，不合规的条目不保存。
func (r *EinoRuntime) ExtractMemory(ctx context.Context, request MemoryExtractionRequest) (MemoryExtractionResult, error) {
	entries := arr.Map(request.Entries, func(entry MemoryEntry) memory.Entry {
		converted := memoryEntry(entry)
		converted.Key = strings.TrimSuffix(entry.Path, memoryKeySuffix)
		return converted
	})
	changes, used, err := memory.Extract(ctx, request.Model.New, memory.ExtractRequest{
		Instruction: memoryExtractionCriteria, Entries: entries,
		Earlier: arr.Map(request.Earlier, memoryMessage), Recent: arr.Map(request.Recent, memoryMessage),
		Language: llm.Chinese,
		Limits:   memory.ExtractLimits{Name: domain.AgentMemoryNameMaxLength, Description: domain.AgentMemoryDescriptionMaxLength, Body: domain.AgentMemoryBodyMaxLength},
	})
	result := MemoryExtractionResult{Usage: agentcontract.UsageFrom(used)}
	if err != nil {
		return result, fmt.Errorf("extract agent memory: %w", err)
	}
	for _, entry := range changes.Saved {
		path := entry.Key + memoryKeySuffix
		if !domain.ValidAgentMemoryPath(path) {
			continue
		}
		result.Saved = append(result.Saved, MemoryEntry{Path: path, Name: entry.Name, Description: entry.Description, Body: entry.Body})
	}
	for _, key := range changes.Deleted {
		result.Deleted = append(result.Deleted, key+memoryKeySuffix)
	}
	return result, nil
}

// memoryMessage 把记忆提取资料中的消息转换为提取使用的对话消息。
func memoryMessage(message MemoryMessage) memory.Message {
	role := einorun.RoleUser
	if message.Sender == "assistant" {
		role = einorun.RoleAssistant
	}
	return memory.Message{Role: role, Content: message.Content}
}
