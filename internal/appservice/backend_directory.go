package appservice

import "context"

// DirectoryBackend 定义成员资料、成员、团队、角色与工作区资料的业务调用。
type DirectoryBackend interface {
	// UpdateProfile 修改当前成员的头像和姓名，以及所属账号的邮箱。
	//appservice:route PATCH /profile perm=none
	UpdateProfile(context.Context, RequestMeta, ProfileInput) (CurrentUser, error)
	// UpdateUserPreferences 保存当前用户的偏好设置。
	//appservice:route PATCH /preferences perm=none
	UpdateUserPreferences(context.Context, RequestMeta, UserPreferencesInput) (CurrentUser, error)
	// UpdateUserWorkStatus 保存当前用户主动设置的工作状态。
	//appservice:route PATCH /work-status perm=none
	UpdateUserWorkStatus(context.Context, RequestMeta, UserWorkStatusInput) (CurrentUser, error)
	// ListMemberOptions 返回可分配的企业成员和 AI 员工。
	//appservice:route GET /members/options perm=none
	ListMemberOptions(context.Context, RequestMeta, MemberOptionListInput) (MemberOptionList, error)
	// ListColleagues 返回通讯录同事目录，服务台排在成员之前。
	//appservice:route GET /colleagues perm=none
	ListColleagues(context.Context, RequestMeta, ColleagueListInput) (ColleagueList, error)
	// ListUsers 返回企业成员列表。
	//appservice:route GET /users perm=workspace.manage
	ListUsers(context.Context, RequestMeta, UserListInput) (UserList, error)
	// GetUser 返回企业成员详情。
	//appservice:route GET /users/{userID:uuid} perm=none
	GetUser(context.Context, RequestMeta, string) (User, error)
	// UpdateUser 修改企业成员头像、资料、角色和所属团队。
	//appservice:route PUT /users/{userID:uuid} perm=workspace.manage
	UpdateUser(context.Context, RequestMeta, string, UpdateUserInput) (User, error)
	// UpdateRoleAssignments 在一个事务中批量调整成员角色。
	//appservice:route PATCH /roles/assignments perm=workspace.manage
	UpdateRoleAssignments(context.Context, RequestMeta, RoleAssignmentsInput) error
	// DeactivateUser 禁用企业成员账号。
	//appservice:route POST /users/{userID:uuid}/deactivate perm=workspace.manage
	DeactivateUser(context.Context, RequestMeta, string) (User, error)
	// ReactivateUser 恢复企业成员账号。
	//appservice:route POST /users/{userID:uuid}/reactivate perm=workspace.manage
	ReactivateUser(context.Context, RequestMeta, string) (User, error)
	// ListTeams 返回企业团队列表。
	//appservice:route GET /teams perm=none
	ListTeams(context.Context, RequestMeta, TeamListInput) (TeamList, error)
	// GetTeam 返回团队详情。
	//appservice:route GET /teams/{teamID:uuid} perm=none
	GetTeam(context.Context, RequestMeta, string) (Team, error)
	// CreateTeam 创建企业团队。
	//appservice:route POST /teams status=201 perm=workspace.manage
	CreateTeam(context.Context, RequestMeta, TeamInput) (Team, error)
	// UpdateTeam 修改企业团队。
	//appservice:route PUT /teams/{teamID:uuid} perm=workspace.manage
	UpdateTeam(context.Context, RequestMeta, string, TeamInput) (Team, error)
	// DeleteTeam 删除企业团队及其成员关系。
	//appservice:route DELETE /teams/{teamID:uuid} perm=workspace.manage
	DeleteTeam(context.Context, RequestMeta, string) error
	// ListTeamMembers 返回团队成员列表。
	//appservice:route GET /teams/{teamID:uuid}/members perm=none
	ListTeamMembers(context.Context, RequestMeta, string, TeamMemberListInput) (TeamMemberList, error)
	// ListTeamMemberCandidates 返回尚未加入团队的企业身份。
	//appservice:route GET /teams/{teamID:uuid}/member-candidates perm=workspace.manage
	ListTeamMemberCandidates(context.Context, RequestMeta, string, TeamMemberCandidateInput) (TeamMemberCandidateList, error)
	// AddTeamMembers 将企业身份批量加入团队。
	//appservice:route POST /teams/{teamID:uuid}/members perm=workspace.manage
	AddTeamMembers(context.Context, RequestMeta, string, TeamMemberInput) (Team, error)
	// RemoveTeamMembers 将企业身份批量移出团队。
	//appservice:route POST /teams/{teamID:uuid}/members/remove perm=workspace.manage
	RemoveTeamMembers(context.Context, RequestMeta, string, TeamMemberInput) (Team, error)
	// ListRoleOptions 返回成员表单与筛选使用的角色选项，并标出当前成员可以分配的角色。
	//appservice:route GET /role-options perm=none
	ListRoleOptions(context.Context, RequestMeta) (RoleOptionList, error)
	// ListRoles 返回当前企业的角色和预定义权限目录。
	//appservice:route GET /settings/roles perm=workspace.manage
	ListRoles(context.Context, RequestMeta) (RoleList, error)
	// GetRole 返回当前企业的角色详情。
	//appservice:route GET /settings/roles/{roleID:uuid} perm=workspace.manage
	GetRole(context.Context, RequestMeta, string) (Role, error)
	// CreateRole 创建自定义角色。
	//appservice:route POST /settings/roles status=201 perm=workspace.manage
	CreateRole(context.Context, RequestMeta, RoleInput) (Role, error)
	// UpdateRole 修改角色信息和权限。
	//appservice:route PUT /settings/roles/{roleID:uuid} perm=workspace.manage
	UpdateRole(context.Context, RequestMeta, string, RoleInput) (Role, error)
	// DeleteRole 删除自定义角色。
	//appservice:route DELETE /settings/roles/{roleID:uuid} perm=workspace.manage
	DeleteRole(context.Context, RequestMeta, string) error
	// UpdateWorkspace 修改当前工作区的名称。
	//appservice:route PUT /settings/workspace perm=workspace.manage
	UpdateWorkspace(context.Context, RequestMeta, WorkspaceSettingsInput) (CurrentWorkspace, error)
}
