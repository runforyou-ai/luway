package appservice

import "time"

// TeamSummary 定义团队选择项和成员所属团队字段。
type TeamSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TeamInput 定义团队可编辑字段。
type TeamInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Team 定义团队详情。
type Team struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	MemberCount int       `json:"memberCount"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// TeamListInput 定义团队列表查询条件。
type TeamListInput struct {
	Query    string `json:"query" query:"query"`
	Page     int    `json:"page" query:"page,default=1"`
	PageSize int    `json:"pageSize" query:"pageSize,default=50"`
}

// TeamList 定义团队分页结果。
type TeamList struct {
	Teams []Team   `json:"teams"`
	Page  PageInfo `json:"page"`
}

// TeamMemberListInput 定义团队成员列表查询条件。
type TeamMemberListInput struct {
	Query      string      `json:"query" query:"query"`
	WorkStatus *WorkStatus `json:"workStatus,omitempty" query:"workStatus"`
	Page       int         `json:"page" query:"page,default=1"`
	PageSize   int         `json:"pageSize" query:"pageSize,default=50"`
}

// TeamMember 定义团队成员信息。UserID 与 AgentID 按身份类型二选一，另一个为空字符串。
type TeamMember struct {
	IdentityID   string                   `json:"identityId"`
	IdentityType OrganizationIdentityType `json:"identityType"`
	UserID       string                   `json:"userId"`
	AgentID      string                   `json:"agentId"`
	DisplayName  string                   `json:"displayName"`
	AvatarURL    string                   `json:"avatarUrl"`
	WorkStatus   WorkStatus               `json:"workStatus"`
	JoinedAt     time.Time                `json:"joinedAt"`
}

// TeamMemberList 定义团队成员分页结果。
type TeamMemberList struct {
	Members []TeamMember `json:"members"`
	Page    PageInfo     `json:"page"`
}

// TeamMemberCandidateInput 定义可加入团队的成员查询条件。
type TeamMemberCandidateInput struct {
	Query    string `json:"query" query:"query"`
	Page     int    `json:"page" query:"page,default=1"`
	PageSize int    `json:"pageSize" query:"pageSize,default=50"`
}

// TeamMemberCandidate 定义可加入团队的成员。
type TeamMemberCandidate struct {
	IdentityType OrganizationIdentityType `json:"identityType"`
	IdentityID   string                   `json:"identityId"`
	DisplayName  string                   `json:"displayName"`
	AvatarURL    string                   `json:"avatarUrl"`
}

// TeamMemberCandidateList 定义可加入团队的成员分页结果。
type TeamMemberCandidateList struct {
	Members []TeamMemberCandidate `json:"members"`
	Page    PageInfo              `json:"page"`
}

// TeamMemberIdentityInput 定义要变更的团队成员身份。
type TeamMemberIdentityInput struct {
	IdentityType OrganizationIdentityType `json:"identityType"`
	IdentityID   string                   `json:"identityId"`
}

// TeamMemberInput 定义批量变更团队的身份列表。
type TeamMemberInput struct {
	Members []TeamMemberIdentityInput `json:"members"`
}
