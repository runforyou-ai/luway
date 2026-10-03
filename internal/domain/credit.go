package domain

// MaxCreditPrice 是平台模型单项积分价格的上限。
const MaxCreditPrice int64 = 1_000_000_000

// CreditPrice 定义平台模型的积分价格：每百万输入 Token、每百万输出 Token 与每次调用的积分。
type CreditPrice struct {
	Input   int64
	Output  int64
	Request int64
}

// Valid 校验各项价格不为负且不超过上限。
func (p CreditPrice) Valid() bool {
	for _, value := range []int64{p.Input, p.Output, p.Request} {
		if value < 0 || value > MaxCreditPrice {
			return false
		}
	}
	return true
}

// Cost 返回一次调用的积分费用：每次调用积分加上按 Token 计算的积分，Token 部分向上取整。
func (p CreditPrice) Cost(inputTokens, outputTokens int64) int64 {
	tokens := inputTokens*p.Input + outputTokens*p.Output
	return p.Request + (tokens+999_999)/1_000_000
}

// CreditLotSource 定义积分批次的入账来源。
type CreditLotSource string

const (
	CreditLotSourceDailyGrant CreditLotSource = "daily_grant"
	CreditLotSourceAdjustment CreditLotSource = "adjustment"
)

// CreditMovementSource 定义积分批次变动的来源类型。
type CreditMovementSource string

const (
	CreditMovementSourceModelCall  CreditMovementSource = "model_call"
	CreditMovementSourceAdjustment CreditMovementSource = "adjustment"
)

// CreditEntryKind 定义工作区积分流水的业务事件类型。
type CreditEntryKind string

const (
	CreditEntryKindDailyGrant CreditEntryKind = "daily_grant"
	CreditEntryKindAdjustment CreditEntryKind = "adjustment"
	CreditEntryKindModelCall  CreditEntryKind = "model_call"
	CreditEntryKindExpiration CreditEntryKind = "expiration"
)

// MaxCreditAmount 是每日赠送积分与单次调整积分的上限。
const MaxCreditAmount int64 = 1_000_000_000_000
