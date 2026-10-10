package domain

// ContactStage 定义联系人阶段。
type ContactStage string

const (
	ContactStageVisitor  ContactStage = "visitor"
	ContactStageLead     ContactStage = "lead"
	ContactStageCustomer ContactStage = "customer"
)

// ContactMethodType 定义联系人联系方式类型。
type ContactMethodType string

const (
	ContactMethodTypeEmail ContactMethodType = "email"
	ContactMethodTypePhone ContactMethodType = "phone"
)

const (
	// ContactDisplayNameMaxLength 是联系人名称的最大字符数。
	ContactDisplayNameMaxLength = 200
	// ContactMethodsMaxCount 是一个联系人的联系方式最大数量。
	ContactMethodsMaxCount = 20
)

// ContactSort 定义联系人列表排序方式。
type ContactSort string

const (
	ContactSortUpdatedAtDescending  ContactSort = "updatedAt.desc"
	ContactSortCreatedAtDescending  ContactSort = "createdAt.desc"
	ContactSortDisplayNameAscending ContactSort = "displayName.asc"
)
