package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/luway/internal/domain"
)

const (
	askCustomerToolName = "ask_customer"
	handoffToolName     = "handoff_to_human"
	resolveToolName     = "resolve_conversation"
	// correctionLimit 是一次执行尝试内允许纠正无效终止输出或无依据正文的次数。
	correctionLimit = 1
	// handoffReasonTextMaxRunes 限制转交说明写入系统事件的长度。
	handoffReasonTextMaxRunes = 500
)

// TerminalDecision 定义一次执行的结束方式；Kind 为空表示直接输出正文作为回答。
type TerminalDecision struct {
	Kind       domain.AgentRunOutcome
	Purpose    domain.AgentAskCustomerPurpose // ask_customer 的发问用途。
	Reason     domain.AgentHandoffReason      // handoff 的原因。
	ReasonText string                         // handoff 时模型写明的转交说明，仅成员可见。
	CategoryID string                         // handoff 时模型选择的咨询分类编号，未选择时为空。
}

// Outcome 返回决定对应的运行结果类型。
func (d TerminalDecision) Outcome() domain.AgentRunOutcome {
	if d.Kind == "" {
		return domain.AgentRunOutcomeReply
	}
	return d.Kind
}

type askCustomerInput struct {
	Purpose domain.AgentAskCustomerPurpose `json:"purpose"`
	Message string                         `json:"message"`
}

type handoffInput struct {
	Reason   domain.AgentHandoffReason `json:"reason"`
	Category *string                   `json:"category"`
	Note     string                    `json:"note"`
}

type resolveInput struct {
	Message string `json:"message"`
}

// isTerminalToolName 判断工具是否为客服场景的终止工具。
func isTerminalToolName(name string) bool {
	return name == askCustomerToolName || name == handoffToolName || name == resolveToolName
}

// terminalIntent 记录一次校验通过的终止工具调用。
type terminalIntent struct {
	decision TerminalDecision
	message  string
}

// terminalTools 注册客服场景的终止工具，校验同批调用与参数，并登记终止意图；工具执行只记录意图，不产生外部副作用。
type terminalTools struct {
	adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
	mu          sync.Mutex
	batchIssue  string // 最近一次模型输出的同批违规说明，空表示合法。
	batchSeen   bool   // 当前批次的违规已计入纠正额度。
	corrections int
	intents     map[string]terminalIntent
	forced      *terminalIntent // 纠正额度或迭代预算用尽后由 Runtime 构造的转人工。
	handoff     bool            // 本次执行已固定转人工决定。
	budgetSpent func() bool     // 返回 true 表示当前规划已在迭代预算末端，无效输出直接转人工。
	categories  []HandoffCategory
}

// newTerminalTools 创建一次执行尝试共用的终止工具状态，categories 是转人工时可选的咨询分类。
func newTerminalTools(categories []HandoffCategory) *terminalTools {
	return &terminalTools{intents: make(map[string]terminalIntent), categories: categories}
}

// handoffDescription 生成 handoff_to_human 的工具说明；企业没有咨询分类时不提及分类。
func (t *terminalTools) handoffDescription() string {
	if len(t.categories) == 0 {
		return "把当前客户会话交给人工客服。reason 与 note 仅企业成员可见；系统会按承接结果通知客户。"
	}
	return "把当前客户会话交给人工客服。reason、category 与 note 仅企业成员可见；填写 category 时系统交给该分类的团队，并按承接结果通知客户。"
}

// handoffParams 生成 handoff_to_human 的参数定义；企业没有咨询分类时不提供 category。
func (t *terminalTools) handoffParams() map[string]*schema.ParameterInfo {
	reasons := make([]string, 0, len(domain.AgentHandoffBusinessReasons))
	for _, reason := range domain.AgentHandoffBusinessReasons {
		reasons = append(reasons, string(reason))
	}
	params := map[string]*schema.ParameterInfo{
		"reason": {Type: schema.String, Required: true, Enum: reasons,
			Desc: "knowledge_gap 资料中查不到答案，customer_requested 客户明确要求真人，needs_human_judgment 需要人工判断或决定（如退款赔偿、特殊处理），complaint 客户投诉"},
		"note": {Type: schema.String, Required: true, Desc: "写给人工客服的转交说明：客户要什么、你已做了什么、卡在哪里"},
	}
	if len(t.categories) == 0 {
		return params
	}
	names := make([]string, 0, len(t.categories))
	lines := make([]string, 0, len(t.categories))
	for _, category := range t.categories {
		names = append(names, category.Name)
		line := category.Name
		if category.Description != "" {
			line += "：" + category.Description
		}
		lines = append(lines, line)
	}
	params["category"] = &schema.ParameterInfo{Type: schema.String, Enum: names,
		Desc: "客户咨询明确属于的分类，系统会交给该分类的团队；没有明确匹配的分类时不填。可选分类：\n" + strings.Join(lines, "\n")}
	return params
}

// tools 返回 ask_customer、handoff_to_human 与 resolve_conversation 三个终止工具。
func (t *terminalTools) tools() []tool.BaseTool {
	return []tool.BaseTool{
		&terminalTool{info: &schema.ToolInfo{
			Name: askCustomerToolName,
			Desc: "向客户发送追问、确认或问候并等待客户回复。message 是发给客户的完整内容。",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"purpose": {Type: schema.String, Required: true, Desc: "greeting 问候，clarify 请客户补充信息，confirm 请客户确认，confirm_resolution 请客户确认问题是否已解决", Enum: []string{
					string(domain.AgentAskCustomerPurposeGreeting), string(domain.AgentAskCustomerPurposeClarify), string(domain.AgentAskCustomerPurposeConfirm),
					string(domain.AgentAskCustomerPurposeConfirmResolution),
				}},
				"message": {Type: schema.String, Required: true, Desc: "发给客户的内容"},
			}),
		}, run: t.askCustomer},
		&terminalTool{info: &schema.ToolInfo{
			Name:        handoffToolName,
			Desc:        t.handoffDescription(),
			ParamsOneOf: schema.NewParamsOneOfByParams(t.handoffParams()),
		}, run: t.handoffToHuman},
		&terminalTool{info: &schema.ToolInfo{
			Name: resolveToolName,
			Desc: "客户确认问题已解决时发送结束语并结束本次服务。message 是发给客户的简短结束语。",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"message": {Type: schema.String, Required: true, Desc: "发给客户的结束语"},
			}),
		}, run: t.resolve},
	}
}

// askCustomer 校验追问参数并登记 ask_customer 意图。
func (t *terminalTools) askCustomer(ctx context.Context, arguments string) error {
	input := askCustomerInput{}
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return fmt.Errorf("参数不是有效的 JSON：%w", err)
	}
	input.Message = strings.TrimSpace(input.Message)
	switch input.Purpose {
	case domain.AgentAskCustomerPurposeGreeting, domain.AgentAskCustomerPurposeClarify, domain.AgentAskCustomerPurposeConfirm,
		domain.AgentAskCustomerPurposeConfirmResolution:
	default:
		return errors.New("purpose 只能是 greeting、clarify、confirm 或 confirm_resolution")
	}
	if input.Message == "" {
		return errors.New("message 不能为空")
	}
	t.record(ctx, terminalIntent{decision: TerminalDecision{Kind: domain.AgentRunOutcomeAskCustomer, Purpose: input.Purpose}, message: input.Message})
	return nil
}

// handoffToHuman 校验转交参数并登记不可降级的 handoff 意图。
func (t *terminalTools) handoffToHuman(ctx context.Context, arguments string) error {
	input := handoffInput{}
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return fmt.Errorf("参数不是有效的 JSON：%w", err)
	}
	if !slices.Contains(domain.AgentHandoffBusinessReasons, input.Reason) {
		return errors.New("reason 只能是 knowledge_gap、customer_requested、needs_human_judgment 或 complaint")
	}
	// 咨询分类只接受本次提供的分类名称并换成分类编号，null 或空值表示没有匹配的分类。
	categoryID := ""
	if input.Category != nil && strings.TrimSpace(*input.Category) != "" {
		name := strings.TrimSpace(*input.Category)
		index := slices.IndexFunc(t.categories, func(category HandoffCategory) bool { return category.Name == name })
		if index < 0 {
			return errors.New("category 只能是参数说明中列出的分类名称，没有匹配的分类时不填")
		}
		categoryID = t.categories[index].ID
	}
	note := []rune(strings.TrimSpace(input.Note))
	if len(note) == 0 {
		return errors.New("note 不能为空")
	}
	if len(note) > handoffReasonTextMaxRunes {
		note = note[:handoffReasonTextMaxRunes]
	}
	t.record(ctx, terminalIntent{decision: TerminalDecision{
		Kind: domain.AgentRunOutcomeHandoff, Reason: input.Reason, ReasonText: string(note), CategoryID: categoryID,
	}})
	return nil
}

// resolve 校验结束语并登记 resolve 意图。
func (t *terminalTools) resolve(ctx context.Context, arguments string) error {
	input := resolveInput{}
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return fmt.Errorf("参数不是有效的 JSON：%w", err)
	}
	input.Message = strings.TrimSpace(input.Message)
	if input.Message == "" {
		return errors.New("message 不能为空")
	}
	t.record(ctx, terminalIntent{decision: TerminalDecision{Kind: domain.AgentRunOutcomeResolve}, message: input.Message})
	return nil
}

// record 按工具调用编号登记终止意图，并请求本次模型规划在工具结果后直接结束。
func (t *terminalTools) record(ctx context.Context, intent terminalIntent) {
	t.mu.Lock()
	t.intents[compose.GetToolCallID(ctx)] = intent
	if intent.decision.Kind == domain.AgentRunOutcomeHandoff {
		t.handoff = true
	}
	t.mu.Unlock()
	if err := adk.SetToolReturnDirectly(ctx); err != nil {
		slog.Warn("终止工具请求直接返回失败", "agent_run_id", runIDFromContext(ctx), "error", err)
	}
}

// AfterModelRewriteState 在工具执行前检查本次模型输出：终止工具至多一个且不与其他工具同批调用。
func (t *terminalTools) AfterModelRewriteState(ctx context.Context, state *adk.TypedChatModelAgentState[*schema.AgenticMessage], _ *adk.TypedModelContext[*schema.AgenticMessage]) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	message := state.Messages[len(state.Messages)-1]
	names := make([]string, 0)
	terminal := 0
	for _, block := range message.ContentBlocks {
		if block.Type != schema.ContentBlockTypeFunctionToolCall {
			continue
		}
		names = append(names, block.FunctionToolCall.Name)
		if isTerminalToolName(block.FunctionToolCall.Name) {
			terminal++
		}
	}
	issue := ""
	switch {
	case terminal > 1:
		issue = "ask_customer、handoff_to_human 与 resolve_conversation 一次只能调用其中一个"
	case terminal == 1 && len(names) > 1:
		issue = "ask_customer、handoff_to_human 或 resolve_conversation 必须单独调用，不能与其他工具同时调用"
	}
	t.mu.Lock()
	t.batchIssue, t.batchSeen = issue, false
	t.mu.Unlock()
	return ctx, state, nil
}

// middleware 拦截同批违规的工具调用并处理终止工具参数错误：纠正额度内把错误交回模型，额度用尽或已在预算末端时构造转人工并直接结束。
func (t *terminalTools) middleware() compose.ToolMiddleware {
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				t.mu.Lock()
				issue, counted := t.batchIssue, t.batchSeen
				t.batchSeen = t.batchSeen || issue != ""
				t.mu.Unlock()
				if issue != "" {
					// 同一批次只计一次纠正，其余调用直接返回同样的说明。
					if counted {
						return nil, errors.New(issue)
					}
					return nil, t.reject(ctx, issue)
				}
				output, err := next(ctx, input)
				if err == nil || !isTerminalToolName(input.Name) || ctx.Err() != nil {
					return output, err
				}
				return nil, t.reject(ctx, err.Error())
			}
		},
	}
}

// reject 消耗一次纠正额度并返回交给模型的错误；额度用尽或已在迭代预算末端时登记 Runtime 构造的转人工并请求直接结束。
func (t *terminalTools) reject(ctx context.Context, issue string) error {
	exhausted := t.budgetSpent != nil && t.budgetSpent()
	t.mu.Lock()
	if !exhausted && t.corrections < correctionLimit {
		t.corrections++
		t.mu.Unlock()
		slog.Warn("Agent 终止输出无效，要求模型纠正", "agent_run_id", runIDFromContext(ctx), "issue", issue)
		return fmt.Errorf("%s。需要客户补充信息或只是问候请单独调用 ask_customer；无法解答请单独调用 handoff_to_human；客户确认问题已解决请单独调用 resolve_conversation", issue)
	}
	reason := domain.AgentHandoffReasonInvalidOutput
	if exhausted {
		reason = domain.AgentHandoffReasonBudgetExhausted
	}
	if t.forced == nil {
		t.forced = &terminalIntent{decision: TerminalDecision{Kind: domain.AgentRunOutcomeHandoff, Reason: reason}}
		t.handoff = true
	}
	t.mu.Unlock()
	slog.Warn("Agent 终止调用无效且无法纠正，转交人工", "agent_run_id", runIDFromContext(ctx), "issue", issue, "reason", reason)
	if err := adk.SetToolReturnDirectly(ctx); err != nil {
		slog.Warn("终止工具请求直接返回失败", "agent_run_id", runIDFromContext(ctx), "error", err)
	}
	return errors.New(issue)
}

// takeCorrection 消耗一次与无效终止输出共用的纠正额度，额度已用尽时返回 false。
func (t *terminalTools) takeCorrection() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.corrections >= correctionLimit {
		return false
	}
	t.corrections++
	return true
}

// beginTurn 在认领新输入时清空上一轮的非转人工意图。
func (t *terminalTools) beginTurn() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.handoff {
		t.intents = make(map[string]terminalIntent)
	}
}

// handoffFixed 判断本次执行是否已固定转人工决定。
func (t *terminalTools) handoffFixed() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.handoff
}

// decision 按本轮成功的终止工具调用编号取得终止意图；已固定的转人工优先。
func (t *terminalTools) decision(resultCallIDs []string) (terminalIntent, bool) {
	if t == nil {
		return terminalIntent{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.forced != nil {
		return *t.forced, true
	}
	var found *terminalIntent
	for _, intent := range t.intents {
		if intent.decision.Kind == domain.AgentRunOutcomeHandoff {
			return intent, true
		}
	}
	for _, callID := range resultCallIDs {
		if intent, ok := t.intents[callID]; ok {
			found = &intent
		}
	}
	if found == nil {
		return terminalIntent{}, false
	}
	return *found, true
}

// terminalTool 以原始参数执行终止工具，参数错误交由中间件计入纠正额度。
type terminalTool struct {
	info *schema.ToolInfo
	run  func(context.Context, string) error
}

// Info 返回工具定义。
func (t *terminalTool) Info(context.Context) (*schema.ToolInfo, error) {
	return t.info, nil
}

// InvokableRun 校验参数并登记意图，返回给过程记录的受理结果。
func (t *terminalTool) InvokableRun(ctx context.Context, arguments string, _ ...tool.Option) (string, error) {
	if err := t.run(ctx, arguments); err != nil {
		return "", err
	}
	return `{"accepted":true}`, nil
}
