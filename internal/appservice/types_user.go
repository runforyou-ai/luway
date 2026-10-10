package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// UserStatus 表示用户账号或 AI 员工的账号状态。
type UserStatus string

const (
	UserStatusActive   UserStatus = UserStatus(domain.IdentityStatusActive)
	UserStatusInactive UserStatus = UserStatus(domain.IdentityStatusInactive)
)

// WorkStatus 表示企业身份主动设置的工作状态。
type WorkStatus = domain.WorkStatus

// CurrentUser 定义当前登录用户信息。
type CurrentUser struct {
	ID          string `json:"id"`
	IdentityID  string `json:"identityId"`
	WorkspaceID string `json:"workspaceId"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	RoleID      string `json:"roleId"`
	// RoleKind 表示所属角色的类型。
	RoleKind RoleKind `json:"roleKind"`
	// Permissions 表示所属角色实际拥有的权限，管理员拥有全部权限。
	Permissions                 []PermissionCode `json:"permissions"`
	Status                      UserStatus       `json:"status"`
	Locale                      Locale           `json:"locale"`
	TranslationLanguage         string           `json:"translationLanguage"`
	TimeZone                    string           `json:"timeZone"`
	MessageNotificationsEnabled bool             `json:"messageNotificationsEnabled"`
	HandlesServiceRequests      bool             `json:"handlesServiceRequests"`
	WorkStatus                  WorkStatus       `json:"workStatus"`
	AvatarURL                   string           `json:"avatarUrl"`
}

// ProfileInput 定义当前用户可编辑的个人资料字段。
type ProfileInput struct {
	DisplayName  string `json:"displayName"`
	Email        string `json:"email"`
	AvatarFileID string `json:"avatarFileId"`
}

// ChangePasswordInput 定义当前用户修改密码所需字段。
type ChangePasswordInput struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

// PushPlatform 定义离线推送的设备平台。
type PushPlatform = domain.PushPlatform

// PushDeviceInput 定义本设备的离线推送目标：App 是注册设备的客户端应用标识，DeviceID 是推送服务分配的设备编号。
type PushDeviceInput struct {
	App      string       `json:"app"`
	Platform PushPlatform `json:"platform"`
	DeviceID string       `json:"deviceId"`
}

// UserPreferencesInput 定义当前用户的偏好设置。
type UserPreferencesInput struct {
	Locale                      Locale `json:"locale"`
	TranslationLanguage         string `json:"translationLanguage"`
	TimeZone                    string `json:"timeZone"`
	MessageNotificationsEnabled bool   `json:"messageNotificationsEnabled"`
}

// UserWorkStatusInput 定义当前用户主动设置的工作状态。
type UserWorkStatusInput struct {
	WorkStatus WorkStatus `json:"workStatus" validate:"oneof=working away off_duty" msg:"field.work_status_invalid"`
}

// UserListInput 定义企业成员列表查询条件。
type UserListInput struct {
	Query    string      `json:"query" query:"query"`
	Status   *UserStatus `json:"status,omitempty" query:"status" validate:"omitnil,oneof=active inactive"`
	RoleID   string      `json:"roleId" query:"roleId" validate:"omitempty,uuid"`
	TeamID   string      `json:"teamId" query:"teamId" validate:"omitempty,uuid"`
	Page     int         `json:"page" query:"page,default=1"`
	PageSize int         `json:"pageSize" query:"pageSize,default=50"`
}

// UpdateUserInput 定义工作区成员可编辑字段，邮箱属于账号不在此修改，最大接待量只在开启接待时生效，AvatarFileID 为空时保留原头像。
type UpdateUserInput struct {
	DisplayName            string   `json:"displayName"`
	RoleID                 string   `json:"roleId" validate:"uuid" msg:"field.member_role_invalid"`
	TeamIDs                []string `json:"teamIds" validate:"dive,uuid" msg:"field.team_invalid"`
	HandlesServiceRequests bool     `json:"handlesServiceRequests"`
	MaxServiceSessions     int      `json:"maxServiceSessions"`
	AvatarFileID           string   `json:"avatarFileId"`
}

// User 定义企业成员信息。
type User struct {
	ID                     string      `json:"id"`
	IdentityID             string      `json:"identityId"`
	Email                  string      `json:"email"`
	DisplayName            string      `json:"displayName"`
	AvatarURL              string      `json:"avatarUrl"`
	Role                   RoleSummary `json:"role"`
	HandlesServiceRequests bool        `json:"handlesServiceRequests"`
	// MaxServiceSessions 是自动分配时本人可负责的开放客服处理周期上限。
	MaxServiceSessions int           `json:"maxServiceSessions"`
	Status             UserStatus    `json:"status"`
	WorkStatus         WorkStatus    `json:"workStatus"`
	Teams              []TeamSummary `json:"teams"`
	CreatedAt          time.Time     `json:"createdAt"`
}

// UserList 定义企业成员分页结果。
type UserList struct {
	Users []User   `json:"users"`
	Page  PageInfo `json:"page"`
}
