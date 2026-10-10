package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// RunStreamToolCall 是运行过程流中的工具调用名称、状态和起止时间，完整参数与结果经过程详情查询读取；description 是委派调用的子任务说明，activity 是子 Agent 正在调用的工具名称。
type RunStreamToolCall struct {
	Name        string                     `json:"name"`
	Status      domain.AgentToolCallStatus `json:"status"`
	StartedAt   *time.Time                 `json:"startedAt,omitempty"`
	CompletedAt *time.Time                 `json:"completedAt,omitempty"`
	Description string                     `json:"description,omitempty"`
	Activity    string                     `json:"activity,omitempty"`
}

// RunStreamPlanTask 是运行过程流任务清单中的一项任务。
type RunStreamPlanTask struct {
	ID         string                     `json:"id"`
	Subject    string                     `json:"subject"`
	ActiveForm string                     `json:"activeForm,omitempty"`
	Status     domain.AgentPlanTaskStatus `json:"status"`
}

// RunStreamBlock 是运行过程流中按位置排列的展示内容块。
type RunStreamBlock struct {
	ID       string                   `json:"id"`
	Position int64                    `json:"position"`
	Kind     domain.AgentRunBlockKind `json:"kind"`
	Text     string                   `json:"text"`
	ToolCall *RunStreamToolCall       `json:"toolCall,omitempty"`
}

// RunStreamSnapshot 是一个业务序号上的完整运行展示状态。
type RunStreamSnapshot struct {
	RunID            string              `json:"runId"`
	StreamID         string              `json:"streamId"`
	Attempt          int                 `json:"attempt"`
	Sequence         string              `json:"sequence"`
	CandidateContent string              `json:"candidateContent"`
	Plan             []RunStreamPlanTask `json:"plan"`
	Blocks           []RunStreamBlock    `json:"blocks"`
}

// AgentRunStreamState 包含持久运行状态、当前尝试与可用的执行快照。
type AgentRunStreamState struct {
	Status   AgentRunStatus     `json:"status"`
	Attempt  int                `json:"attempt"`
	Snapshot *RunStreamSnapshot `json:"snapshot,omitempty"`
}
