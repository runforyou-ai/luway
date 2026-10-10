//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	memberaction "github.com/runforyou-ai/luway/internal/actions/member"
	roleaction "github.com/runforyou-ai/luway/internal/actions/role"
	seataction "github.com/runforyou-ai/luway/internal/actions/seat"
	servicehandoffaction "github.com/runforyou-ai/luway/internal/actions/servicehandoff"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	workspaceaction "github.com/runforyou-ai/luway/internal/actions/workspace"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// directoryOps 持有企业成员、团队、角色与工作区的 Action 和 Query。
type directoryOps struct {
	listMemberOptions        *memberaction.ListOptionsQuery
	listColleagues           *memberaction.ListColleaguesQuery
	listUsers                *useraction.ListUsersQuery
	getUser                  *useraction.GetUserQuery
	updateUser               *useraction.UpdateUserAction
	updateRoleAssignments    *roleaction.UpdateAssignmentsAction
	updateUserStatus         *useraction.UpdateStatusAction
	listTeams                *teamaction.ListTeamsQuery
	getTeam                  *teamaction.GetTeamQuery
	createTeam               *teamaction.CreateTeamAction
	updateTeam               *teamaction.UpdateTeamAction
	deleteTeam               *teamaction.DeleteTeamAction
	listTeamMembers          *teamaction.ListMembersQuery
	listTeamMemberCandidates *teamaction.ListMemberCandidatesQuery
	addTeamMembers           *teamaction.AddMembersAction
	removeTeamMembers        *teamaction.RemoveMembersAction
	updateProfile            *useraction.UpdateProfileAction
	updateUserPreferences    *useraction.UpdatePreferencesAction
	updateUserWorkStatus     *useraction.UpdateWorkStatusAction
	listRoles                *roleaction.ListRolesQuery
	listRoleOptions          *roleaction.ListOptionsQuery
	getRole                  *roleaction.GetRoleQuery
	createRole               *roleaction.CreateRoleAction
	updateRole               *roleaction.UpdateRoleAction
	deleteRole               *roleaction.DeleteRoleAction
	updateWorkspace          *workspaceaction.UpdateWorkspaceAction
	// files 解析文件地址与当前成员资料。
	files *fileOps
}

// newDirectoryOps 创建企业成员、团队、角色与工作区的业务实现依赖。
func newDirectoryOps(db *bun.DB, returner *servicehandoffaction.Returner, runCancellation *agentrunaction.RunCancellation, taskEnqueuer servertask.TxEnqueuer, seats seataction.Seats, files *fileOps) *directoryOps {
	return &directoryOps{
		listMemberOptions:        memberaction.NewListOptionsQuery(db),
		listColleagues:           memberaction.NewListColleaguesQuery(db),
		listUsers:                useraction.NewListUsersQuery(db),
		getUser:                  useraction.NewGetUserQuery(db),
		updateUser:               useraction.NewUpdateUserAction(db, returner, taskEnqueuer),
		updateRoleAssignments:    roleaction.NewUpdateAssignmentsAction(db),
		updateUserStatus:         useraction.NewUpdateStatusAction(db, taskEnqueuer, seats, returner, groupchataction.NewPersonalAgentRetirer(taskEnqueuer, runCancellation)),
		listTeams:                teamaction.NewListTeamsQuery(db),
		getTeam:                  teamaction.NewGetTeamQuery(db),
		createTeam:               teamaction.NewCreateTeamAction(db),
		updateTeam:               teamaction.NewUpdateTeamAction(db),
		deleteTeam:               teamaction.NewDeleteTeamAction(db, taskEnqueuer),
		listTeamMembers:          teamaction.NewListMembersQuery(db),
		listTeamMemberCandidates: teamaction.NewListMemberCandidatesQuery(db),
		addTeamMembers:           teamaction.NewAddMembersAction(db, taskEnqueuer),
		removeTeamMembers:        teamaction.NewRemoveMembersAction(db),
		updateProfile:            useraction.NewUpdateProfileAction(db),
		updateUserPreferences:    useraction.NewUpdatePreferencesAction(db),
		updateUserWorkStatus:     useraction.NewUpdateWorkStatusAction(db, taskEnqueuer),
		listRoles:                roleaction.NewListRolesQuery(db),
		listRoleOptions:          roleaction.NewListOptionsQuery(db),
		getRole:                  roleaction.NewGetRoleQuery(db),
		createRole:               roleaction.NewCreateRoleAction(db),
		updateRole:               roleaction.NewUpdateRoleAction(db),
		deleteRole:               roleaction.NewDeleteRoleAction(db),
		updateWorkspace:          workspaceaction.NewUpdateWorkspaceAction(db),
		files:                    files,
	}
}

// UpdateProfile 修改当前用户的头像、姓名和邮箱。
func (o *directoryOps) UpdateProfile(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ProfileInput) (appservice.CurrentUser, error) {
	updatedIdentity, err := o.updateProfile.Execute(ctx, identity, useraction.ProfileInput{
		DisplayName:  input.DisplayName,
		Email:        input.Email,
		AvatarFileID: input.AvatarFileID,
	})
	if err != nil {
		return appservice.CurrentUser{}, currentUserError(meta, err, i18n.ErrorProfileUpdateFailed, profileFieldKeys)
	}
	slog.InfoContext(ctx, "个人资料保存成功", "identity_id", identity.User.IdentityID, "user_id", identity.User.ID)
	user, err := o.files.currentUserFromIdentity(ctx, updatedIdentity)
	if err != nil {
		return appservice.CurrentUser{}, currentUserError(meta, err, i18n.ErrorProfileUpdateFailed, profileFieldKeys)
	}
	return user, nil
}

// UpdateUserPreferences 保存当前用户的偏好设置。
func (o *directoryOps) UpdateUserPreferences(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.UserPreferencesInput) (appservice.CurrentUser, error) {
	updatedIdentity, err := o.updateUserPreferences.Execute(ctx, identity, useraction.PreferencesInput{
		Locale:                      input.Locale,
		TranslationLanguage:         input.TranslationLanguage,
		TimeZone:                    input.TimeZone,
		MessageNotificationsEnabled: input.MessageNotificationsEnabled,
	})
	if err != nil {
		return appservice.CurrentUser{}, currentUserError(meta, err, i18n.ErrorPreferencesUpdateFailed, preferencesFieldKeys)
	}
	slog.InfoContext(ctx, "用户偏好保存成功",
		"user_id", identity.User.ID,
		"locale", input.Locale,
		"time_zone", input.TimeZone,
		"message_notifications_enabled", input.MessageNotificationsEnabled,
	)
	user, err := o.files.currentUserFromIdentity(ctx, updatedIdentity)
	if err != nil {
		return appservice.CurrentUser{}, currentUserError(meta, err, i18n.ErrorPreferencesUpdateFailed, preferencesFieldKeys)
	}
	return user, nil
}

// UpdateUserWorkStatus 保存当前用户主动设置的工作状态。
func (o *directoryOps) UpdateUserWorkStatus(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.UserWorkStatusInput) (appservice.CurrentUser, error) {
	updatedIdentity, err := o.updateUserWorkStatus.Execute(ctx, identity, useraction.WorkStatusInput{
		WorkStatus: input.WorkStatus,
	})
	if err != nil {
		return appservice.CurrentUser{}, currentUserError(meta, err, i18n.ErrorWorkStatusUpdateFailed, nil)
	}
	slog.InfoContext(ctx, "工作状态保存成功", "identity_id", identity.User.IdentityID, "user_id", identity.User.ID, "work_status", input.WorkStatus)
	user, err := o.files.currentUserFromIdentity(ctx, updatedIdentity)
	if err != nil {
		return appservice.CurrentUser{}, currentUserError(meta, err, i18n.ErrorWorkStatusUpdateFailed, nil)
	}
	return user, nil
}

// ListUsers 返回企业成员列表。
func (o *directoryOps) ListUsers(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.UserListInput) (appservice.UserList, error) {
	output, err := o.listUsers.Execute(ctx, identity, useraction.ListInput{
		Query: input.Query, Status: domain.IdentityStatus(support.Deref(input.Status)), RoleID: input.RoleID, TeamID: input.TeamID, Page: input.Page, PageSize: input.PageSize,
	})
	if err != nil {
		if errors.Is(err, useraction.ErrQueryInvalid) {
			return appservice.UserList{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
		}
		return appservice.UserList{}, appservice.FailedError(meta, i18n.ErrorUserListFailed, err)
	}
	avatarFileIDs := arr.Map(output.Users, func(user useraction.User) *string { return user.AvatarFileID })
	avatarURLs, err := o.files.optionalFileURLs(ctx, identity, avatarFileIDs...)
	if err != nil {
		return appservice.UserList{}, appservice.FailedError(meta, i18n.ErrorUserListFailed, err)
	}
	users := arr.Map(output.Users, func(user useraction.User) appservice.User { return userFromAction(user, avatarURLs) })
	return appservice.UserList{Users: users, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total}}, nil
}

// GetUser 返回企业成员详情。
func (o *directoryOps) GetUser(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, userID string) (appservice.User, error) {
	user, err := o.getUser.Execute(ctx, identity, userID)
	if err != nil {
		if errors.Is(err, useraction.ErrNotFound) {
			return appservice.User{}, appservice.NotFoundError(meta, i18n.ErrorUserNotFound)
		}
		return appservice.User{}, appservice.FailedError(meta, i18n.ErrorUserReadFailed, err)
	}
	output, err := o.userWithAvatar(ctx, identity, *user)
	if err != nil {
		return appservice.User{}, appservice.FailedError(meta, i18n.ErrorUserReadFailed, err)
	}
	return output, nil
}

// UpdateUser 修改企业成员头像、资料、角色、接待开关和所属团队。
func (o *directoryOps) UpdateUser(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, userID string, input appservice.UpdateUserInput) (appservice.User, error) {
	user, err := o.updateUser.Execute(ctx, identity, userID, useraction.UpdateInput{DisplayName: input.DisplayName, RoleID: input.RoleID, TeamIDs: input.TeamIDs, HandlesServiceRequests: input.HandlesServiceRequests, MaxServiceSessions: input.MaxServiceSessions, AvatarFileID: input.AvatarFileID})
	if err != nil {
		return appservice.User{}, userMutationError(meta, err, i18n.ErrorUserUpdateFailed)
	}
	slog.InfoContext(ctx, "企业成员更新成功", "identity_id", user.IdentityID, "user_id", userID, "role_id", user.RoleID)
	return o.userMutationResult(ctx, meta, identity, *user, i18n.ErrorUserUpdateFailed)
}

// DeactivateUser 禁用企业成员账号。
func (o *directoryOps) DeactivateUser(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, userID string) (appservice.User, error) {
	return o.changeUserStatus(ctx, meta, identity, userID, domain.IdentityStatusInactive)
}

// ReactivateUser 恢复企业成员账号。
func (o *directoryOps) ReactivateUser(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, userID string) (appservice.User, error) {
	return o.changeUserStatus(ctx, meta, identity, userID, domain.IdentityStatusActive)
}

// changeUserStatus 修改企业成员账号状态。
func (o *directoryOps) changeUserStatus(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, userID string, status domain.IdentityStatus) (appservice.User, error) {
	user, err := o.updateUserStatus.Execute(ctx, identity, userID, status)
	if err != nil {
		return appservice.User{}, userMutationError(meta, err, i18n.ErrorUserStatusUpdateFailed)
	}
	slog.InfoContext(ctx, "企业成员账号状态已修改", "identity_id", user.IdentityID, "user_id", userID, "status", status)
	return o.userMutationResult(ctx, meta, identity, *user, i18n.ErrorUserStatusUpdateFailed)
}

// currentUserErrors 是当前用户资料、密码、偏好和工作状态操作在字段校验之后匹配的错误。
var currentUserErrors = dispatch.Catalog{
	dispatch.Is(fileaction.ErrLinkedImageNotFound, dispatch.NotFound(i18n.ErrorFileNotFound)),
	dispatch.SessionRule,
}

// currentUserError 转换当前用户资料、密码、偏好和工作状态操作错误，fieldKeys 是该操作的校验错误码文案。
func currentUserError(meta appservice.RequestMeta, err error, failureKey i18n.Key, fieldKeys map[common.FieldCode]i18n.Key) error {
	return dispatch.Catalog{dispatch.FieldRule(fieldKeys), currentUserErrors.Find}.Translate(meta, err, failureKey)
}

// userMutationErrors 是企业成员写入的错误转换规则。
var userMutationErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{
	dispatch.FieldRule(userFieldKeys),
	dispatch.Is(useraction.ErrNotFound, dispatch.NotFound(i18n.ErrorUserNotFound)),
	dispatch.Is(fileaction.ErrLinkedImageNotFound, dispatch.InvalidField("avatarFileId", i18n.ErrorFileNotFound)),
	dispatch.Is(useraction.ErrLastActiveAdministrator, dispatch.Invalid(i18n.ErrorUserLastActiveAdministrator)),
	dispatch.Is(useraction.ErrPlatformAdmin, dispatch.Invalid(i18n.ErrorUserPlatformAdmin)),
	dispatch.Is(seataction.ErrLimitReached, dispatch.Conflict(i18n.ErrorSeatLimitReached, "seat_limit_reached")),
})

// userMutationError 转换企业成员写入错误。
func userMutationError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return userMutationErrors.Translate(meta, err, failureKey)
}

// userMutationResult 补齐写入后企业成员的头像地址。
func (o *directoryOps) userMutationResult(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, user useraction.User, failureKey i18n.Key) (appservice.User, error) {
	output, err := o.userWithAvatar(ctx, identity, user)
	if err != nil {
		return appservice.User{}, userMutationError(meta, err, failureKey)
	}
	return output, nil
}

// userWithAvatar 解析单个企业成员的头像地址并转换契约。
func (o *directoryOps) userWithAvatar(ctx context.Context, identity *servermodels.Identity, user useraction.User) (appservice.User, error) {
	avatarURLs, err := o.files.optionalFileURLs(ctx, identity, user.AvatarFileID)
	if err != nil {
		return appservice.User{}, err
	}
	return userFromAction(user, avatarURLs), nil
}

// userFromAction 转换企业成员契约。
func userFromAction(user useraction.User, avatarURLs map[string]string) appservice.User {
	teams := arr.Map(user.Teams, func(team useraction.TeamSummary) appservice.TeamSummary {
		return appservice.TeamSummary{ID: team.ID, Name: team.Name}
	})
	return appservice.User{ID: user.ID, IdentityID: user.IdentityID, Email: user.Email, DisplayName: user.DisplayName, AvatarURL: optionalFileURL(avatarURLs, user.AvatarFileID), Role: appservice.RoleSummary{ID: user.RoleID, Kind: user.RoleKind, Name: user.RoleName}, HandlesServiceRequests: user.HandlesServiceRequests, MaxServiceSessions: user.MaxServiceSessions, Status: appservice.UserStatus(user.Status), WorkStatus: user.WorkStatus, Teams: teams, CreatedAt: user.CreatedAt}
}

// userFieldKeys 把企业成员校验错误码映射为本地化文案键。
var userFieldKeys = map[common.FieldCode]i18n.Key{
	useraction.ValidationDisplayNameRequired:       i18n.FieldDisplayNameRequired,
	useraction.ValidationDisplayNameInvalid:        i18n.FieldDisplayNameInvalid,
	useraction.ValidationEmailInvalid:              i18n.FieldEmailInvalid,
	useraction.ValidationEmailDuplicate:            i18n.FieldEmailDuplicate,
	useraction.ValidationRoleInvalid:               i18n.FieldMemberRoleInvalid,
	useraction.ValidationTeamInvalid:               i18n.FieldTeamInvalid,
	useraction.ValidationStatusInvalid:             i18n.FieldUserStatusInvalid,
	useraction.ValidationMaxServiceSessionsInvalid: i18n.FieldMaxServiceSessionsInvalid,
}

// profileFieldKeys 把个人资料校验错误码映射为本地化文案键。
var profileFieldKeys = map[common.FieldCode]i18n.Key{
	useraction.ValidationDisplayNameRequired: i18n.FieldDisplayNameRequired,
	useraction.ValidationDisplayNameInvalid:  i18n.FieldDisplayNameInvalid,
	useraction.ValidationEmailInvalid:        i18n.FieldEmailInvalid,
	useraction.ValidationEmailDuplicate:      i18n.FieldEmailDuplicate,
}

// preferencesFieldKeys 把语言和时区校验错误码映射为本地化文案键。
var preferencesFieldKeys = map[common.FieldCode]i18n.Key{
	useraction.ValidationLocaleInvalid:              i18n.FieldLocaleInvalid,
	useraction.ValidationTimeZoneInvalid:            i18n.FieldTimeZoneInvalid,
	useraction.ValidationTranslationLanguageInvalid: i18n.FieldLocaleInvalid,
}
