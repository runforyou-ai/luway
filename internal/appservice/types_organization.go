package appservice

import "github.com/runforyou-ai/cervi/internal/domain"

// OrganizationIdentityType 表示工作区身份类型。
type OrganizationIdentityType string

const (
	OrganizationIdentityTypeUser      OrganizationIdentityType = OrganizationIdentityType(domain.OrganizationIdentityTypeUser)
	OrganizationIdentityTypeAgent     OrganizationIdentityType = OrganizationIdentityType(domain.OrganizationIdentityTypeAgent)
	OrganizationIdentityTypeAssistant OrganizationIdentityType = OrganizationIdentityType(domain.OrganizationIdentityTypeAssistant)
)

// Organization 定义当前工作区及其通用设置。
type Organization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// OrganizationInput 定义工作区通用设置修改输入，工作区标识创建后不修改。
type OrganizationInput struct {
	Name string `json:"name"`
}
