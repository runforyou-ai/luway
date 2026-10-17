package agentcontract

// CallDecision 是宿主对暂停待确认调用的裁决：Approved 为 true 时执行调用，否则（含零值）拒绝并由模型看到 Reason。
type CallDecision struct {
	Approved bool   `json:"approved"`
	Reason   string `json:"reason,omitempty"`
	// Arguments 在批准时若非空，替换模型给出的参数；规格的 Policy 不再复核。
	Arguments string `json:"arguments,omitempty"`
}
