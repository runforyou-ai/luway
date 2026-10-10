package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/str"
)

const (
	// askCustomerToolName、handoffToolName 与 resolveToolName 是客服场景的追问、转人工与确认解决工具名。
	askCustomerToolName = "ask_customer"
	handoffToolName     = "handoff_to_human"
	resolveToolName     = "resolve_conversation"
	// correctionLimit 是一次执行尝试内允许纠正无效终止输出、同批违规或无依据正文的次数。
	correctionLimit = 1
	// handoffReasonTextMaxRunes 限制转交说明写入系统事件的长度。
	handoffReasonTextMaxRunes = 500
)

// askCustomerInput 是 ask_customer 的参数。
type askCustomerInput struct {
	Purpose domain.AgentAskCustomerPurpose `json:"purpose"`
	Message string                         `json:"message"`
}

// handoffInput 是 handoff_to_human 的参数。
type handoffInput struct {
	Reason   domain.AgentHandoffReason `json:"reason"`
	Category *string                   `json:"category"`
	Note     string                    `json:"note"`
}

// resolveInput 是 resolve_conversation 的参数。
type resolveInput struct {
	Message string `json:"message"`
}

// terminalGuidance 是终止工具参数无效时附在错误后的引导。
const terminalGuidance = "。需要客户补充信息或只是问候请单独调用 ask_customer；无法解答请单独调用 handoff_to_human；客户确认问题已解决请单独调用 resolve_conversation"

// serviceFinalNotice 是服务场景迭代预算用尽时追加的收尾提示，此时只保留终止工具。
const serviceFinalNotice = "工具调用次数已达本轮上限，不能再查询资料。已取得依据时直接给出最终回答；需要客户补充信息请调用 ask_customer；无法解答请调用 handoff_to_human。"

// isTerminalToolName 判断工具是否为客服场景的终止工具。
func isTerminalToolName(name string) bool {
	return name == askCustomerToolName || name == handoffToolName || name == resolveToolName
}

// terminalCompletion 是终止工具与兜底转人工结束运行时给出的完成载荷。
type terminalCompletion struct {
	Kind       domain.AgentRunOutcome         `json:"kind"`
	Purpose    domain.AgentAskCustomerPurpose `json:"purpose,omitempty"`
	Message    string                         `json:"message,omitempty"`
	Reason     domain.AgentHandoffReason      `json:"reason,omitempty"`
	ReasonText string                         `json:"reasonText,omitempty"`
	CategoryID string                         `json:"categoryId,omitempty"`
}

// fallbackReasons 把运行时兜底完成的原因对应到转人工原因。
var fallbackReasons = map[string]domain.AgentHandoffReason{
	einorun.ReasonCorrectionsExhausted: domain.AgentHandoffReasonInvalidOutput,
	einorun.ReasonBudgetExhausted:      domain.AgentHandoffReasonBudgetExhausted,
	einorun.ReasonGuardRejected:        domain.AgentHandoffReasonInsufficientEvidence,
}

// terminalPolicy 返回服务场景的完成策略：输出无法纠正时由运行时构造转人工，迭代预算末端追加服务场景的收尾提示。
func terminalPolicy() *einorun.CompletionPolicy {
	return &einorun.CompletionPolicy{
		Fallback: func(reason string) json.RawMessage {
			handoff := fallbackReasons[reason]
			if handoff == "" {
				handoff = domain.AgentHandoffReasonInvalidOutput
			}
			value, _ := json.Marshal(terminalCompletion{Kind: domain.AgentRunOutcomeHandoff, Reason: handoff})
			return value
		},
		FinalNotice: serviceFinalNotice,
	}
}

// decodeCompletion 把运行的完成转换为结束方式与发给对方的正文。
func decodeCompletion(completion *einorun.Completion) (agentcontract.TerminalDecision, string, error) {
	var value terminalCompletion
	if err := json.Unmarshal(completion.Value, &value); err != nil {
		return agentcontract.TerminalDecision{}, "", fmt.Errorf("decode agent run completion: %w", err)
	}
	decision := agentcontract.TerminalDecision{Kind: value.Kind, Purpose: value.Purpose, Reason: value.Reason, ReasonText: value.ReasonText, CategoryID: value.CategoryID}
	return decision, value.Message, nil
}

// terminalTools 是客服场景的追问、转人工与确认解决工具：参数校验通过后以完成结束运行，转人工的完成不被新输入取代；同批与参数错误由运行时的完成协议计入纠正额度。
type terminalTools struct {
	categories []HandoffCategory
}

// newTerminalTools 创建终止工具，categories 是转人工时可选的咨询分类。
func newTerminalTools(categories []HandoffCategory) *terminalTools {
	return &terminalTools{categories: categories}
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
	reasons := arr.Map(domain.AgentHandoffBusinessReasons, func(reason domain.AgentHandoffReason) string { return string(reason) })
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

// tool 返回指定名称的终止工具。
func (t *terminalTools) tool(name string) tool.BaseTool {
	for _, item := range t.tools() {
		if item.(*terminalTool).info.Name == name {
			return item
		}
	}
	return nil
}

// askCustomer 校验追问参数并给出 ask_customer 完成。
func (t *terminalTools) askCustomer(arguments string) (terminalCompletion, error) {
	input := askCustomerInput{}
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return terminalCompletion{}, fmt.Errorf("参数不是有效的 JSON：%w", err)
	}
	input.Message = strings.TrimSpace(input.Message)
	switch input.Purpose {
	case domain.AgentAskCustomerPurposeGreeting, domain.AgentAskCustomerPurposeClarify, domain.AgentAskCustomerPurposeConfirm,
		domain.AgentAskCustomerPurposeConfirmResolution:
	default:
		return terminalCompletion{}, errors.New("purpose 只能是 greeting、clarify、confirm 或 confirm_resolution")
	}
	if input.Message == "" {
		return terminalCompletion{}, errors.New("message 不能为空")
	}
	return terminalCompletion{Kind: domain.AgentRunOutcomeAskCustomer, Purpose: input.Purpose, Message: input.Message}, nil
}

// handoffToHuman 校验转交参数并给出转人工完成。
func (t *terminalTools) handoffToHuman(arguments string) (terminalCompletion, error) {
	input := handoffInput{}
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return terminalCompletion{}, fmt.Errorf("参数不是有效的 JSON：%w", err)
	}
	if !slices.Contains(domain.AgentHandoffBusinessReasons, input.Reason) {
		return terminalCompletion{}, errors.New("reason 只能是 knowledge_gap、customer_requested、needs_human_judgment 或 complaint")
	}
	// 咨询分类只接受本次提供的分类名称并换成分类编号，null 或空值表示没有匹配的分类。
	categoryID := ""
	if input.Category != nil && strings.TrimSpace(*input.Category) != "" {
		name := strings.TrimSpace(*input.Category)
		index := slices.IndexFunc(t.categories, func(category HandoffCategory) bool { return category.Name == name })
		if index < 0 {
			return terminalCompletion{}, errors.New("category 只能是参数说明中列出的分类名称，没有匹配的分类时不填")
		}
		categoryID = t.categories[index].ID
	}
	note := str.Substr(strings.TrimSpace(input.Note), 0, handoffReasonTextMaxRunes)
	if note == "" {
		return terminalCompletion{}, errors.New("note 不能为空")
	}
	return terminalCompletion{Kind: domain.AgentRunOutcomeHandoff, Reason: input.Reason, ReasonText: note, CategoryID: categoryID}, nil
}

// resolve 校验结束语并给出确认解决完成。
func (t *terminalTools) resolve(arguments string) (terminalCompletion, error) {
	input := resolveInput{}
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return terminalCompletion{}, fmt.Errorf("参数不是有效的 JSON：%w", err)
	}
	input.Message = strings.TrimSpace(input.Message)
	if input.Message == "" {
		return terminalCompletion{}, errors.New("message 不能为空")
	}
	return terminalCompletion{Kind: domain.AgentRunOutcomeResolve, Message: input.Message}, nil
}

// terminalTool 以原始参数执行终止工具：参数无效时返回带引导的错误，有效时以完成结束运行。
type terminalTool struct {
	info *schema.ToolInfo
	run  func(string) (terminalCompletion, error)
}

// Info 返回工具定义。
func (t *terminalTool) Info(context.Context) (*schema.ToolInfo, error) {
	return t.info, nil
}

// InvokableRun 校验参数并以完成结束运行，转人工的完成固定，不被新输入取代。
func (t *terminalTool) InvokableRun(_ context.Context, arguments string, _ ...tool.Option) (string, error) {
	completion, err := t.run(arguments)
	if err != nil {
		return "", errors.New(err.Error() + terminalGuidance)
	}
	value, err := json.Marshal(completion)
	if err != nil {
		return "", err
	}
	return "", einorun.Complete(value, completion.Kind == domain.AgentRunOutcomeHandoff)
}
