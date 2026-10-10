//go:build server

package integrationtest

import (
	"context"
	"testing"

	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	"github.com/stretchr/testify/require"
)

// TestCreateMemberWithAvatar 验证新增企业成员时激活并关联已上传的头像，不可用的头像拒绝创建。
func TestCreateMemberWithAvatar(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	avatar, err := fileaction.NewCreateUploadAction(f.db).Execute(ctx, f.owner, domain.FileStorageBackendLocal, fileaction.UploadInput{
		Purpose: domain.FilePurposeUserAvatar, FileName: "member.png", ContentType: "image/png", ByteSize: 1024,
	})
	require.NoError(t, err)
	_, err = markFileUploaded(ctx, f.db, f.owner, avatar.ID, "")
	require.NoError(t, err)
	create := newTestMemberCreator(f.db, testEnqueuer)
	input := memberSpec{HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: "带头像成员", Email: servertest.UniqueEmail("avatar-member"), Password: "password123", RoleID: f.owner.User.RoleID, AvatarFileID: avatar.ID}
	created, err := create.Execute(ctx, f.owner, input)
	require.NoError(t, err)
	require.Equal(t, &avatar.ID, created.AvatarFileID)
	var status string
	require.NoError(t, f.db.NewSelect().TableExpr("files").Column("status").Where("id = ?", avatar.ID).Scan(ctx, &status))
	require.Equal(t, string(domain.FileStatusActive), status, "avatar status")
	// 已被关联的头像不能再用于新成员。
	input.Email, input.DisplayName = servertest.UniqueEmail("avatar-member-2"), "第二个成员"
	_, err = create.Execute(ctx, f.owner, input)
	require.ErrorIs(t, err, fileaction.ErrLinkedImageNotFound, "reuse avatar")
}

// TestUpdateMemberAvatar 验证修改企业成员时激活新头像并把替换下来的旧头像交给清理任务，不传头像时保留原头像。
func TestUpdateMemberAvatar(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	// 上传两张成员头像，分别用于创建和替换。
	avatarIDs := make([]string, 0, 2)
	for _, name := range []string{"first.png", "second.png"} {
		avatar, err := fileaction.NewCreateUploadAction(f.db).Execute(ctx, f.owner, domain.FileStorageBackendLocal, fileaction.UploadInput{
			Purpose: domain.FilePurposeUserAvatar, FileName: name, ContentType: "image/png", ByteSize: 1024,
		})
		require.NoError(t, err)
		_, err = markFileUploaded(ctx, f.db, f.owner, avatar.ID, "")
		require.NoError(t, err)
		avatarIDs = append(avatarIDs, avatar.ID)
	}
	created, err := newTestMemberCreator(f.db, testEnqueuer).Execute(ctx, f.owner, memberSpec{
		DisplayName: "换头像成员", Email: servertest.UniqueEmail("avatar-update"), Password: "password123", RoleID: f.owner.User.RoleID, AvatarFileID: avatarIDs[0],
	})
	require.NoError(t, err)
	update := useraction.NewUpdateUserAction(f.db, testServiceSessionReturner(f.db), testEnqueuer)
	input := useraction.UpdateInput{DisplayName: created.DisplayName, RoleID: created.RoleID}
	kept, err := update.Execute(ctx, f.owner, created.ID, input)
	require.NoError(t, err)
	require.Equal(t, &avatarIDs[0], kept.AvatarFileID)
	input.AvatarFileID = avatarIDs[1]
	replaced, err := update.Execute(ctx, f.owner, created.ID, input)
	require.NoError(t, err)
	require.Equal(t, &avatarIDs[1], replaced.AvatarFileID)
	for fileID, want := range map[string]domain.FileStatus{avatarIDs[0]: domain.FileStatusDeleting, avatarIDs[1]: domain.FileStatusActive} {
		var status string
		require.NoError(t, f.db.NewSelect().TableExpr("files").Column("status").Where("id = ?", fileID).Scan(ctx, &status), "file %s", fileID)
		require.Equal(t, string(want), status, "file %s status", fileID)
	}
}
