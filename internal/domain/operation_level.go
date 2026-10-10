package domain

import (
	"slices"

	"github.com/runforyou-ai/support"
)

// OperationLevel 定义工具的操作级别。
type OperationLevel string

const (
	// OperationLevelL0 是公开信息查询。
	OperationLevelL0 OperationLevel = "l0"
	// OperationLevelL1 是按已核验身份查询本人数据。
	OperationLevelL1 OperationLevel = "l1"
	// OperationLevelL2 是可撤销的写操作。
	OperationLevelL2 OperationLevel = "l2"
	// OperationLevelL3 是不可逆或资金操作。
	OperationLevelL3 OperationLevel = "l3"
)

// operationLevels 按由低到高排列操作级别。
var operationLevels = []OperationLevel{OperationLevelL0, OperationLevelL1, OperationLevelL2, OperationLevelL3}

// Valid 判断操作级别是否为已定义的值。
func (l OperationLevel) Valid() bool { return slices.Contains(operationLevels, l) }

// AtMost 判断操作级别不高于 limit。
func (l OperationLevel) AtMost(limit OperationLevel) bool {
	return slices.Index(operationLevels, l) <= slices.Index(operationLevels, limit)
}

// ToolFacts 定义工具的只读、可撤销与对外发信事实，操作级别由事实推导。
type ToolFacts struct {
	ReadOnly   bool `json:"readOnly"`
	Reversible bool `json:"reversible"`
	Outbound   bool `json:"outbound"`
}

// HintedToolFacts 按服务方声明的只读与不可撤销提示给出事实：未声明只读视为写操作，未声明不可撤销提示的写操作视为不可撤销，对外发信为否。
func HintedToolFacts(readOnlyHint, destructiveHint *bool) ToolFacts {
	facts := ToolFacts{ReadOnly: support.Deref(readOnlyHint)}
	if destructiveHint != nil {
		facts.Reversible = !*destructiveHint
	}
	return facts
}

// ToolLevel 由工具事实与是否绑定身份推导操作级别：只读工具绑定身份为 L1，否则为 L0；写操作可撤销为 L2，否则为 L3。
func ToolLevel(facts ToolFacts, identityBound bool) OperationLevel {
	switch {
	case facts.ReadOnly && identityBound:
		return OperationLevelL1
	case facts.ReadOnly:
		return OperationLevelL0
	case facts.Reversible:
		return OperationLevelL2
	}
	return OperationLevelL3
}

// ToolIntervention 定义工具调用执行前需要的人工介入。
type ToolIntervention string

const (
	// ToolInterventionNone 表示自动执行。
	ToolInterventionNone ToolIntervention = ""
	// ToolInterventionConfirmation 表示由发起人确认后执行：成员会话为发起成员，客服会话为已核验的客户。
	ToolInterventionConfirmation ToolIntervention = "confirmation"
	// ToolInterventionApproval 表示由 AI 员工负责人审批后执行。
	ToolInterventionApproval ToolIntervention = "approval"
)

// ComputerOperationLevels 是工作区电脑授权可选的最高级别：电脑工具不绑定身份，没有 L1。
var ComputerOperationLevels = []OperationLevel{OperationLevelL0, OperationLevelL2, OperationLevelL3}

// ToolGrant 定义 AI 员工对一组工具的授权：可执行的最高级别与 L2 是否需要确认。
type ToolGrant struct {
	MaxLevel  OperationLevel `json:"maxLevel"`
	ConfirmL2 bool           `json:"confirmL2"`
}

// RequiresApproval 判断授权是否允许需要审批的工具：最高级别为 L3。
func (g ToolGrant) RequiresApproval() bool { return g.MaxLevel == OperationLevelL3 }

// Permit 判断授权是否允许指定级别的工具，并给出执行前需要的人工介入：L3 需要审批，L2 按授权需要确认。
func (g ToolGrant) Permit(level OperationLevel) (bool, ToolIntervention) {
	switch {
	case !level.AtMost(g.MaxLevel):
		return false, ToolInterventionNone
	case level == OperationLevelL3:
		return true, ToolInterventionApproval
	case level == OperationLevelL2 && g.ConfirmL2:
		return true, ToolInterventionConfirmation
	}
	return true, ToolInterventionNone
}
