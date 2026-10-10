package domain

// LocalAgentSessionStatus 定义本机 Agent 会话的状态。
type LocalAgentSessionStatus string

const (
	// LocalAgentSessionActive 表示执行器持有会话，新一轮委派在同一会话中续接。
	LocalAgentSessionActive LocalAgentSessionStatus = "active"
	// LocalAgentSessionReleased 表示会话已释放，执行器中止仍在执行的一轮并关闭会话。
	LocalAgentSessionReleased LocalAgentSessionStatus = "released"
)

// ToolCallUpdateKind 定义工具调用过程更新的类型。
type ToolCallUpdateKind string

const (
	// ToolCallUpdateMessage 是回复正文的一段。
	ToolCallUpdateMessage ToolCallUpdateKind = "message"
	// ToolCallUpdateThought 是思考摘要的一段。
	ToolCallUpdateThought ToolCallUpdateKind = "thought"
	// ToolCallUpdateStep 是一个执行步骤的当前状态，同一步骤的后续更新整体替换此前的状态。
	ToolCallUpdateStep ToolCallUpdateKind = "step"
	// ToolCallUpdatePlan 是任务清单的当前状态，整体替换此前的清单。
	ToolCallUpdatePlan ToolCallUpdateKind = "plan"
)

// ToolCallUpdate 是电脑执行工具调用期间上报的一条过程更新。
type ToolCallUpdate struct {
	Kind ToolCallUpdateKind `json:"kind"`
	// Text 是回复或思考的文本片段，按序号拼接得到完整内容。
	Text string        `json:"text,omitempty"`
	Step *ToolCallStep `json:"step,omitempty"`
	Plan []PlanStep    `json:"plan,omitempty"`
}

// ToolCallStep 是本机 Agent 执行的一个步骤：读取、编辑、运行命令等。
type ToolCallStep struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Kind 是步骤类型，取 ACP 工具类型：read、edit、delete、move、search、execute、think、fetch、other。
	Kind string `json:"kind,omitempty"`
	// Status 是步骤状态，取 ACP 工具调用状态：pending、in_progress、completed、failed。
	Status string `json:"status,omitempty"`
	// Locations 是步骤涉及的文件路径。
	Locations []string              `json:"locations,omitempty"`
	Content   []ToolCallStepContent `json:"content,omitempty"`
}

// ToolCallStepContent 是步骤产出的一段内容：文本或文件差异。
type ToolCallStepContent struct {
	Text string    `json:"text,omitempty"`
	Diff *FileDiff `json:"diff,omitempty"`
}

// FileDiff 是本机 Agent 对一个文件的改动，OldText 为空表示新建文件。
type FileDiff struct {
	Path    string  `json:"path"`
	OldText *string `json:"oldText,omitempty"`
	NewText string  `json:"newText"`
}

// PlanStep 是任务清单中的一项。
type PlanStep struct {
	Content string `json:"content"`
	// Status 取 ACP 任务状态：pending、in_progress、completed。
	Status string `json:"status"`
}

// LocalAgentPermission 是本机 Agent 执行一个步骤前请求的权限：步骤与可选的处理方式。
type LocalAgentPermission struct {
	Step    ToolCallStep                 `json:"step"`
	Options []LocalAgentPermissionOption `json:"options"`
}

// LocalAgentPermissionOption 是权限请求的一个处理方式。
type LocalAgentPermissionOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Kind 取 ACP 处理方式类型：allow_once、allow_always、reject_once、reject_always。
	Kind string `json:"kind"`
}

// Option 返回确认或拒绝时选用的处理方式编号：确认优先允许一次，拒绝优先拒绝一次，没有对应类别的处理方式时返回空，表示取消请求。
func (p LocalAgentPermission) Option(allow bool) string {
	preferred, fallback := "reject_once", "reject_always"
	if allow {
		preferred, fallback = "allow_once", "allow_always"
	}
	for _, kind := range []string{preferred, fallback} {
		for _, option := range p.Options {
			if option.Kind == kind {
				return option.ID
			}
		}
	}
	return ""
}
