package domain

// ContactFieldType 定义联系人字段类型。
type ContactFieldType string

const (
	ContactFieldTypeText   ContactFieldType = "text"
	ContactFieldTypeNumber ContactFieldType = "number"
	ContactFieldTypeDate   ContactFieldType = "date"
	ContactFieldTypeSelect ContactFieldType = "select"
)

// ContactFieldOption 定义单选字段的一个选项；取值保存选项编号。
type ContactFieldOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ContactProfileSource 定义联系人字段取值和标签的来源。
type ContactProfileSource string

const (
	ContactProfileSourceMember         ContactProfileSource = "member"
	ContactProfileSourceAI             ContactProfileSource = "ai"
	ContactProfileSourceSignedIdentity ContactProfileSource = "signed_identity"
)

// SignedContactProfile 定义客户签名身份带入的联系人档案：Attributes 以字段名称为键，取值为空表示清除签名身份写入的取值；Tags 为 nil 表示载荷未提供标签。
type SignedContactProfile struct {
	Attributes map[string]string
	Tags       []string
}

const (
	// ContactFieldOptionNameMaxLength 是单选选项名称的最大字符数。
	ContactFieldOptionNameMaxLength = 50
	// ContactFieldValueMaxLength 是联系人字段取值的最大字符数。
	ContactFieldValueMaxLength = 500
)
