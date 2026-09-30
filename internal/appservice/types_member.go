package appservice

import "time"

// MemberOption 定义可分配的企业身份选择项。
type MemberOption struct {
	ID          string                   `json:"id"`
	Type        OrganizationIdentityType `json:"type"`
	DisplayName string                   `json:"displayName"`
	AvatarURL   string                   `json:"avatarUrl"`
}

// MemberOptionListInput 定义企业身份选择项查询条件。
type MemberOptionListInput struct {
	Query    string `json:"query" query:"query"`
	Page     int    `json:"page" query:"page,default=1"`
	PageSize int    `json:"pageSize" query:"pageSize,default=50"`
}

// MemberOptionList 定义企业身份选择项分页结果。
type MemberOptionList struct {
	Members []MemberOption `json:"members"`
	Page    PageInfo       `json:"page"`
}

// ColleagueListInput 定义通讯录同事目录查询条件。
type ColleagueListInput struct {
	Query    string `json:"query" query:"query"`
	Page     int    `json:"page" query:"page,default=1"`
	PageSize int    `json:"pageSize" query:"pageSize,default=50"`
}

// Colleague 定义通讯录同事目录项：IdentityType 为 user 时是在职成员并带 UserID 与 Email，为 agent 时是服务台并带 AgentID 与在职负责人姓名 ResponsibleName。
type Colleague struct {
	IdentityID      string                   `json:"identityId"`
	IdentityType    OrganizationIdentityType `json:"identityType"`
	UserID          string                   `json:"userId"`
	AgentID         string                   `json:"agentId"`
	DisplayName     string                   `json:"displayName"`
	AvatarURL       string                   `json:"avatarUrl"`
	WorkStatus      WorkStatus               `json:"workStatus"`
	Email           string                   `json:"email"`
	ResponsibleName string                   `json:"responsibleName"`
	Teams           []TeamSummary            `json:"teams"`
	CreatedAt       time.Time                `json:"createdAt"`
}

// ColleagueList 定义通讯录同事目录分页结果。
type ColleagueList struct {
	Colleagues []Colleague `json:"colleagues"`
	Page       PageInfo    `json:"page"`
}
