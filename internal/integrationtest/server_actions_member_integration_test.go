//go:build server

package integrationtest

import (
	"context"
	"strings"
	"testing"
	"time"

	accountaction "github.com/runforyou-ai/luway/internal/actions/account"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/actions/filemaintenance"
	roleaction "github.com/runforyou-ai/luway/internal/actions/role"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// testTeamsAndMembers 覆盖团队创建、成员账号管理、角色变更保护与团队成员增删流程。
func (s *serverActionsFixture) testTeamsAndMembers(t *testing.T) {
	db, loggedIn := s.db, s.loggedIn
	var err error
	s.team, err = teamaction.NewCreateTeamAction(db).Execute(context.Background(), loggedIn.Identity, teamaction.Input{Name: "客户成功", Description: "服务客户"})
	require.NoError(t, err)
	s.memberRole = &servermodels.Role{}
	require.NoError(t, db.NewSelect().Model(s.memberRole).
		Where("workspace_id = ?", loggedIn.Identity.Workspace.ID).
		Where("kind = ?", domain.RoleKindMember).
		Scan(context.Background()))
	s.createdMember, err = newTestMemberCreator(db, testEnqueuer).Execute(context.Background(), loggedIn.Identity, memberSpec{
		HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: "团队成员", Email: servertest.UniqueEmail("member"), Password: "password123", RoleID: s.memberRole.ID, TeamIDs: []string{s.team.ID},
	})
	require.NoError(t, err)
	require.Len(t, s.createdMember.Teams, 1)
	require.Equal(t, s.team.ID, s.createdMember.Teams[0].ID)
	require.NotEmpty(t, s.createdMember.IdentityID)
	require.NotEqual(t, s.createdMember.ID, s.createdMember.IdentityID)
	updateRoles := roleaction.NewUpdateAssignmentsAction(db)
	require.ErrorIs(t, updateRoles.Execute(context.Background(), loggedIn.Identity, []roleaction.AssignmentInput{{IdentityID: loggedIn.Identity.WorkspaceIdentity.ID, RoleID: s.memberRole.ID}}), roleaction.ErrLastActiveAdministrator)
	administratorAfterRollback, err := useraction.NewGetUserQuery(db).Execute(context.Background(), loggedIn.Identity, loggedIn.Identity.User.ID)
	require.NoError(t, err)
	require.Equal(t, loggedIn.Identity.User.RoleID, administratorAfterRollback.RoleID)
	require.NoError(t, updateRoles.Execute(context.Background(), loggedIn.Identity, []roleaction.AssignmentInput{
		{IdentityID: loggedIn.Identity.WorkspaceIdentity.ID, RoleID: s.memberRole.ID},
		{IdentityID: s.createdMember.IdentityID, RoleID: loggedIn.Identity.User.RoleID},
	}))
	// 降为成员后按当前角色判断，不能停用管理员，也不能调整回管理员。
	_, err = testUserStatusAction(db).Execute(context.Background(), loggedIn.Identity, s.createdMember.ID, domain.IdentityStatusInactive)
	require.ErrorIs(t, err, roleaction.ErrAdministratorOnly)
	require.ErrorIs(t, updateRoles.Execute(context.Background(), loggedIn.Identity, []roleaction.AssignmentInput{
		{IdentityID: loggedIn.Identity.WorkspaceIdentity.ID, RoleID: loggedIn.Identity.User.RoleID},
	}), roleaction.ErrAdministratorOnly)
	_, err = s.db.NewUpdate().Model((*servermodels.User)(nil)).Set("role_id = ?", loggedIn.Identity.User.RoleID).
		Where("id = ?", loggedIn.Identity.User.ID).Exec(context.Background())
	require.NoError(t, err)
	require.NoError(t, updateRoles.Execute(context.Background(), loggedIn.Identity, []roleaction.AssignmentInput{
		{IdentityID: s.createdMember.IdentityID, RoleID: s.memberRole.ID},
	}))
	// 唯一的管理员不能停用自己。
	_, err = testUserStatusAction(db).Execute(context.Background(), loggedIn.Identity, loggedIn.Identity.User.ID, domain.IdentityStatusInactive)
	require.ErrorIs(t, err, useraction.ErrLastActiveAdministrator)
	teamUsers, err := useraction.NewListUsersQuery(db).Execute(context.Background(), loggedIn.Identity, useraction.ListInput{TeamID: s.team.ID, Page: 1, PageSize: 50})
	require.NoError(t, err)
	require.Equal(t, 1, teamUsers.Page.Total)
	require.Len(t, teamUsers.Users, 1)
	inactiveMember, err := testUserStatusAction(db).Execute(context.Background(), loggedIn.Identity, s.createdMember.ID, domain.IdentityStatusInactive)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStatusOffDuty, inactiveMember.WorkStatus)
	inactiveTeamMembers, err := teamaction.NewListMembersQuery(db).Execute(context.Background(), loggedIn.Identity, s.team.ID, teamaction.MemberListInput{Page: 1, PageSize: 50})
	require.NoError(t, err)
	require.Equal(t, 0, inactiveTeamMembers.Page.Total)
	require.Empty(t, inactiveTeamMembers.Members)
	teamAfterUserDeactivation, err := teamaction.NewListTeamsQuery(db).Execute(context.Background(), loggedIn.Identity, teamaction.ListInput{Page: 1, PageSize: 50})
	require.NoError(t, err)
	require.Len(t, teamAfterUserDeactivation.Teams, 1)
	require.Equal(t, 0, teamAfterUserDeactivation.Teams[0].MemberCount)
	reactivatedMember, err := testUserStatusAction(db).Execute(context.Background(), loggedIn.Identity, s.createdMember.ID, domain.IdentityStatusActive)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStatusOffDuty, reactivatedMember.WorkStatus)
	reactivatedTeamMembers, err := teamaction.NewListMembersQuery(db).Execute(context.Background(), loggedIn.Identity, s.team.ID, teamaction.MemberListInput{Page: 1, PageSize: 50})
	require.NoError(t, err)
	require.Equal(t, 1, reactivatedTeamMembers.Page.Total)
	require.Len(t, reactivatedTeamMembers.Members, 1)
	require.Equal(t, s.createdMember.IdentityID, reactivatedTeamMembers.Members[0].IdentityID)
	teamAfterUserReactivation, err := teamaction.NewListTeamsQuery(db).Execute(context.Background(), loggedIn.Identity, teamaction.ListInput{Page: 1, PageSize: 50})
	require.NoError(t, err)
	require.Len(t, teamAfterUserReactivation.Teams, 1)
	require.Equal(t, 1, teamAfterUserReactivation.Teams[0].MemberCount)
	_, err = teamaction.NewRemoveMembersAction(db).Execute(context.Background(), loggedIn.Identity, s.team.ID, []teamaction.MemberIdentity{{IdentityType: domain.WorkspaceIdentityTypeUser, IdentityID: s.createdMember.IdentityID}})
	require.NoError(t, err)
	candidates, err := teamaction.NewListMemberCandidatesQuery(db).Execute(context.Background(), loggedIn.Identity, s.team.ID, teamaction.MemberCandidateInput{Query: s.createdMember.DisplayName, Page: 1, PageSize: 50})
	require.NoError(t, err)
	require.Equal(t, 1, candidates.Page.Total)
	require.Len(t, candidates.Members, 1)
	require.Equal(t, s.createdMember.IdentityID, candidates.Members[0].IdentityID)
	s.team, err = teamaction.NewAddMembersAction(db, testEnqueuer).Execute(context.Background(), loggedIn.Identity, s.team.ID, []teamaction.MemberIdentity{{IdentityType: domain.WorkspaceIdentityTypeUser, IdentityID: s.createdMember.IdentityID}})
	require.NoError(t, err)
	require.Equal(t, 1, s.team.MemberCount)
}

// testFilesAndProfile 覆盖头像文件上传激活、个人资料更新、头像替换与过期清理流程。
func (s *serverActionsFixture) testFilesAndProfile(t *testing.T) {
	db, resolveIdentity, loggedIn, profileEmail, updateProfile := s.db, s.resolveIdentity, s.loggedIn, s.profileEmail, s.updateProfile
	avatar, err := fileaction.NewCreateUploadAction(db).Execute(context.Background(), loggedIn.Identity, domain.FileStorageBackendLocal, fileaction.UploadInput{
		Purpose: domain.FilePurposeUserAvatar, FileName: "avatar.png", ContentType: "image/png", ByteSize: 1024,
	})
	require.NoError(t, err)
	require.Equal(t, string(domain.FileStatusPending), avatar.Status)
	require.NotNil(t, avatar.ExpiresAt)
	avatar, err = markFileUploaded(context.Background(), db, loggedIn.Identity, avatar.ID, "")
	require.NoError(t, err)
	require.Equal(t, string(domain.FileStatusUploaded), avatar.Status)
	require.NotNil(t, avatar.ExpiresAt)

	updatedIdentity, err := updateProfile.Execute(context.Background(), loggedIn.Identity, useraction.ProfileInput{
		DisplayName:  "  新姓名  ",
		Email:        " " + strings.ToUpper(profileEmail) + " ",
		AvatarFileID: avatar.ID,
	})
	require.NoError(t, err)
	require.Equal(t, "新姓名", updatedIdentity.WorkspaceIdentity.DisplayName)
	require.Equal(t, profileEmail, updatedIdentity.Account.Email)
	require.Equal(t, &avatar.ID, updatedIdentity.WorkspaceIdentity.AvatarFileID)
	activeAvatar := &servermodels.File{}
	require.NoError(t, db.NewSelect().Model(activeAvatar).Where("f.id = ?", avatar.ID).Scan(context.Background()))
	require.Equal(t, string(domain.FileStatusActive), activeAvatar.Status)
	require.Nil(t, activeAvatar.ExpiresAt)
	s.resolvedAfterUpdate, err = resolveIdentity.Execute(context.Background(), loggedIn.Identity.Workspace.ID, loggedIn.Token)
	require.NoError(t, err)
	require.NotNil(t, s.resolvedAfterUpdate)
	require.Equal(t, profileEmail, s.resolvedAfterUpdate.Account.Email)
	require.Equal(t, "新姓名", s.resolvedAfterUpdate.WorkspaceIdentity.DisplayName)
	replacement, err := fileaction.NewCreateUploadAction(db).Execute(context.Background(), s.resolvedAfterUpdate, domain.FileStorageBackendLocal, fileaction.UploadInput{
		Purpose: domain.FilePurposeUserAvatar, FileName: "replacement.webp", ContentType: "image/webp", ByteSize: 2048,
	})
	require.NoError(t, err)
	replacement, err = markFileUploaded(context.Background(), db, s.resolvedAfterUpdate, replacement.ID, "")
	require.NoError(t, err)
	updatedIdentity, err = updateProfile.Execute(context.Background(), s.resolvedAfterUpdate, useraction.ProfileInput{
		DisplayName: "新姓名", Email: profileEmail, AvatarFileID: replacement.ID,
	})
	require.NoError(t, err)
	require.Equal(t, &replacement.ID, updatedIdentity.WorkspaceIdentity.AvatarFileID)
	require.NoError(t, db.NewSelect().Model(activeAvatar).Where("f.id = ?", avatar.ID).Scan(context.Background()))
	require.Equal(t, string(domain.FileStatusDeleting), activeAvatar.Status)
	require.NotNil(t, activeAvatar.ExpiresAt)
	_, err = db.NewUpdate().Model((*servermodels.File)(nil)).
		Set("expires_at = ?", time.Now().UTC().Add(-time.Second)).
		Where("id = ?", avatar.ID).
		Exec(context.Background())
	require.NoError(t, err)
	localFiles, err := serverfilecontent.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	cleanup := filemaintenance.NewDeleteExpiredAction(db, serverfilecontent.NewDeleter(localFiles, func() serverfilecontent.S3Config { return serverfilecontent.S3Config{} }))
	require.NoError(t, cleanup.Execute(context.Background(), filemaintenance.DeleteExpiredInput{FileID: avatar.ID}))
	s.resolvedAfterUpdate, err = resolveIdentity.Execute(context.Background(), loggedIn.Identity.Workspace.ID, loggedIn.Token)
	require.NoError(t, err)
	_, err = updateProfile.Execute(context.Background(), s.resolvedAfterUpdate, useraction.ProfileInput{
		DisplayName: "不应保存的姓名", Email: servertest.UniqueEmail("discarded"), AvatarFileID: "00000000-0000-0000-0000-000000000099",
	})
	require.ErrorIs(t, err, fileaction.ErrLinkedImageNotFound)
	s.resolvedAfterUpdate, err = resolveIdentity.Execute(context.Background(), loggedIn.Identity.Workspace.ID, loggedIn.Token)
	require.NoError(t, err)
	require.Equal(t, profileEmail, s.resolvedAfterUpdate.Account.Email)
	require.Equal(t, "新姓名", s.resolvedAfterUpdate.WorkspaceIdentity.DisplayName)
}

// testEmailConflictAndAvatarRetry 覆盖个人资料邮箱与其他账号重复时校验失败、头像文件保留并可重试的流程。
func (s *serverActionsFixture) testEmailConflictAndAvatarRetry(t *testing.T) {
	db, loggedIn, profileEmail, updateProfile := s.db, s.loggedIn, s.profileEmail, s.updateProfile
	otherEmail := servertest.UniqueEmail("other")
	otherAccount, err := accountaction.CreateAccount(context.Background(), db, accountaction.NewAccount{
		Email: otherEmail, PasswordHash: "unused", DisplayName: "其他成员", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	require.NoError(t, err)
	otherIdentity := &servermodels.WorkspaceIdentity{
		WorkspaceID: loggedIn.Identity.Workspace.ID,
		Type:        string(domain.WorkspaceIdentityTypeUser), DisplayName: "其他成员", WorkStatus: string(domain.WorkStatusWorking),
	}
	_, err = db.NewInsert().Model(otherIdentity).
		Column("workspace_id", "type", "display_name", "work_status").Returning("id").Exec(context.Background())
	require.NoError(t, err)
	otherUser := &servermodels.User{
		IdentityID:  otherIdentity.ID,
		WorkspaceID: loggedIn.Identity.Workspace.ID,
		AccountID:   otherAccount.ID,
		RoleID:      s.memberRole.ID,
		Status:      string(domain.IdentityStatusActive),
	}
	_, err = db.NewInsert().Model(otherUser).
		Column("identity_id", "workspace_id", "account_id", "role_id", "status").
		Exec(context.Background())
	require.NoError(t, err)
	retryAvatar, err := fileaction.NewCreateUploadAction(db).Execute(context.Background(), s.resolvedAfterUpdate, domain.FileStorageBackendLocal, fileaction.UploadInput{
		Purpose: domain.FilePurposeUserAvatar, FileName: "retry.png", ContentType: "image/png", ByteSize: 4096,
	})
	require.NoError(t, err)
	retryAvatar, err = markFileUploaded(context.Background(), db, s.resolvedAfterUpdate, retryAvatar.ID, "")
	require.NoError(t, err)
	_, err = updateProfile.Execute(context.Background(), s.resolvedAfterUpdate, useraction.ProfileInput{
		DisplayName:  "新姓名",
		Email:        strings.ToUpper(otherEmail),
		AvatarFileID: retryAvatar.ID,
	})
	var profileValidation *useraction.ValidationError
	require.ErrorAs(t, err, &profileValidation)
	require.Equal(t, useraction.ValidationEmailDuplicate, profileValidation.Fields["email"])
	require.NoError(t, db.NewSelect().Model(retryAvatar).Where("f.id = ?", retryAvatar.ID).Scan(context.Background()))
	require.Equal(t, string(domain.FileStatusUploaded), retryAvatar.Status)
	require.NotNil(t, retryAvatar.ExpiresAt)
	updatedIdentity, err := updateProfile.Execute(context.Background(), s.resolvedAfterUpdate, useraction.ProfileInput{
		DisplayName: "新姓名", Email: profileEmail, AvatarFileID: retryAvatar.ID,
	})
	require.NoError(t, err)
	require.Equal(t, &retryAvatar.ID, updatedIdentity.WorkspaceIdentity.AvatarFileID)
}
