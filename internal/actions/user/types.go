//go:build server

package user

import (
	"time"

	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
)

// ListInput 定义企业成员目录查询条件。
type ListInput struct {
	Query    string
	Status   domain.IdentityStatus
	RoleID   string
	TeamID   string
	Page     int
	PageSize int
}

// UpdateInput 定义工作区成员可编辑字段，最大接待量只在开启接待时生效，AvatarFileID 为空时保留原头像；邮箱属于账号，不在此修改。
type UpdateInput struct {
	DisplayName            string
	RoleID                 string
	TeamIDs                []string
	HandlesServiceRequests bool
	MaxServiceSessions     int
	AvatarFileID           string
}

// ProfileInput 定义当前成员可编辑的个人资料字段，邮箱写入所属账号。
type ProfileInput struct {
	DisplayName  string
	Email        string
	AvatarFileID string
}

// PreferencesInput 定义当前成员的偏好设置，界面语言和时区写入所属账号。
type PreferencesInput struct {
	Locale domain.Locale
	// TranslationLanguage 是本人的翻译语言，为空时使用界面语言。
	TranslationLanguage         string
	TimeZone                    string
	MessageNotificationsEnabled bool
}

// WorkStatusInput 定义当前用户主动设置的工作状态。
type WorkStatusInput struct {
	WorkStatus domain.WorkStatus
}

// User 定义企业成员信息。
type User struct {
	ID                     string `bun:"id"`
	IdentityID             string `bun:"identity_id"`
	Email                  string
	DisplayName            string
	AvatarFileID           *string         `bun:"avatar_file_id"`
	RoleID                 string          `bun:"role_id"`
	RoleKind               domain.RoleKind `bun:"role_kind"`
	RoleName               string          `bun:"role_name"`
	HandlesServiceRequests bool            `bun:"handles_service_requests"`
	MaxServiceSessions     int             `bun:"max_service_sessions"`
	Status                 domain.IdentityStatus
	WorkStatus             domain.WorkStatus
	Teams                  []TeamSummary
	CreatedAt              time.Time
}

// TeamSummary 定义成员所属团队的精简字段。
type TeamSummary = teamaction.Summary

// ListOutput 定义企业成员分页结果。
type ListOutput struct {
	Users []User
	Page  common.PageInfo
}
