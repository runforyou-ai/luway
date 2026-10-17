//go:build server

package team

import (
	"time"

	"github.com/runforyou-ai/luway/internal/apptypes"
	"github.com/runforyou-ai/luway/internal/domain"
)

// Input 是团队可编辑字段，与契约共用形状。
type Input = apptypes.TeamInput

// ListInput 是团队列表查询条件，与契约共用形状。
type ListInput = apptypes.TeamListInput

// MemberCandidateInput 是可加入团队的成员查询条件，与契约共用形状。
type MemberCandidateInput = apptypes.TeamMemberCandidateInput

// MemberIdentity 是团队成员身份引用，与契约共用形状。
type MemberIdentity = apptypes.TeamMemberIdentityInput

// Record 是 Action 内部团队视图；含 bun 扫描标签，经 ToTeam() 输出 API 形状。
type Record struct {
	ID          string    `bun:"id"`
	Name        string    `bun:"name"`
	Description string    `bun:"description"`
	MemberCount int       `bun:"member_count"`
	CreatedAt   time.Time `bun:"created_at"`
	UpdatedAt   time.Time `bun:"updated_at"`
}

// PageInfo 定义分页信息。
type PageInfo = apptypes.PageInfo

// ListOutput 定义团队分页结果。
type ListOutput struct {
	Teams []Record
	Page  PageInfo
}

// MemberCandidate 定义可加入团队的成员字段。
type MemberCandidate struct {
	IdentityType domain.WorkspaceIdentityType `bun:"identity_type"`
	IdentityID   string                       `bun:"identity_id"`
	DisplayName  string                       `bun:"display_name"`
	AvatarFileID *string                      `bun:"avatar_file_id"`
}

// MemberCandidateOutput 定义可加入团队的成员分页结果。
type MemberCandidateOutput struct {
	Members []MemberCandidate
	Page    PageInfo
}

// ToTeam 转为对外 API 形状。
func (r Record) ToTeam() apptypes.Team {
	return apptypes.Team{
		ID: r.ID, Name: r.Name, Description: r.Description, MemberCount: r.MemberCount,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}
