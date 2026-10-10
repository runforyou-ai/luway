package domain

// IdentityStatus 定义成员账号、AI 员工的启用状态。
type IdentityStatus string

const (
	IdentityStatusActive   IdentityStatus = "active"
	IdentityStatusInactive IdentityStatus = "inactive"
)

// WorkStatus 定义企业身份主动设置的工作状态。
type WorkStatus string

const (
	WorkStatusWorking WorkStatus = "working"
	WorkStatusAway    WorkStatus = "away"
	WorkStatusOffDuty WorkStatus = "off_duty"
)

// Valid 判断工作状态是否为受支持的取值。
func (status WorkStatus) Valid() bool {
	return status == WorkStatusWorking || status == WorkStatusAway || status == WorkStatusOffDuty
}
