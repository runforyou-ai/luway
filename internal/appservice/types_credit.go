package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// CreditPrice 定义平台模型的积分价格：每百万输入 Token、每百万输出 Token 与每次调用的积分。
type CreditPrice struct {
	Input   int64 `json:"input"`
	Output  int64 `json:"output"`
	Request int64 `json:"request"`
}

// CreditBalance 定义工作区可用积分与今天的每日赠送；今天没有发放每日赠送时 DailyGrantExpiresAt 为空。
type CreditBalance struct {
	Available           int64      `json:"available"`
	DailyGrant          int64      `json:"dailyGrant"`
	DailyGrantRemaining int64      `json:"dailyGrantRemaining"`
	DailyGrantExpiresAt *time.Time `json:"dailyGrantExpiresAt"`
}

// CreditEntryKind 表示积分流水的业务事件类型。
type CreditEntryKind string

const (
	CreditEntryKindDailyGrant CreditEntryKind = CreditEntryKind(domain.CreditEntryKindDailyGrant)
	CreditEntryKindAdjustment CreditEntryKind = CreditEntryKind(domain.CreditEntryKindAdjustment)
	CreditEntryKindModelCall  CreditEntryKind = CreditEntryKind(domain.CreditEntryKindModelCall)
	CreditEntryKindExpiration CreditEntryKind = CreditEntryKind(domain.CreditEntryKindExpiration)
	CreditEntryKindPurchase   CreditEntryKind = CreditEntryKind(domain.CreditEntryKindPurchase)
	CreditEntryKindRefund     CreditEntryKind = CreditEntryKind(domain.CreditEntryKindRefund)
)

// CreditEntry 定义积分流水中的一个业务事件：入账为正数，扣除为负数；模型调用一次一条，进行中时为当前预占积分，模型字段只对模型调用有值，备注只对平台管理员调整有值。
type CreditEntry struct {
	ID           string            `json:"id"`
	Kind         CreditEntryKind   `json:"kind"`
	OccurredAt   time.Time         `json:"occurredAt"`
	Amount       int64             `json:"amount"`
	Note         string            `json:"note"`
	ModelName    string            `json:"modelName"`
	ModelUsage   AIModelUsage      `json:"modelUsage"`
	CallStatus   AIModelCallStatus `json:"callStatus"`
	InputTokens  int64             `json:"inputTokens"`
	OutputTokens int64             `json:"outputTokens"`
}

// CreditEntryListInput 定义积分流水的分页条件。
type CreditEntryListInput struct {
	Page     int `json:"page" query:"page,default=1"`
	PageSize int `json:"pageSize" query:"pageSize,default=50"`
}

// CreditEntryList 定义积分流水分页结果。
type CreditEntryList struct {
	Entries []CreditEntry `json:"entries"`
	Page    PageInfo      `json:"page"`
}

// PlatformDailyCreditGrantInput 定义每日赠送积分的修改值，0 表示不赠送。
type PlatformDailyCreditGrantInput struct {
	DailyCreditGrant int64 `json:"dailyCreditGrant"`
}

// PlatformCreditAdjustmentInput 定义平台管理员对工作区积分的调整：正数为增加，负数为扣减。
type PlatformCreditAdjustmentInput struct {
	Amount int64  `json:"amount"`
	Note   string `json:"note"`
}

// PlatformCreditAdjustment 定义积分调整结果：实际变动积分与调整后的余额，扣减最多扣到余额为 0。
type PlatformCreditAdjustment struct {
	Amount  int64         `json:"amount"`
	Balance CreditBalance `json:"balance"`
}
