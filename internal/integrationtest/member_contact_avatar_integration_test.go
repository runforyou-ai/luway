//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	contactaction "github.com/runforyou-ai/luway/internal/actions/contact"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// TestMemberAndContactAvatars 验证企业成员读取身份头像，联系人取最近更新且带头像的渠道身份头像。
func TestMemberAndContactAvatars(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	avatarID := uuid.NewV7().String()
	if _, err := f.db.NewUpdate().Model((*servermodels.OrganizationIdentity)(nil)).Set("avatar_file_id = ?", avatarID).Where("id = ?", f.owner.OrganizationIdentity.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	users, err := useraction.NewListUsersQuery(f.db).Execute(ctx, f.member, useraction.ListInput{})
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range users.Users {
		want := user.ID == f.owner.User.ID
		if got := user.AvatarFileID != nil && *user.AvatarFileID == avatarID; got != want {
			t.Fatalf("user %s avatar=%v", user.DisplayName, user.AvatarFileID)
		}
	}
	user, err := useraction.NewGetUserQuery(f.db).Execute(ctx, f.member, f.owner.User.ID)
	if err != nil || user.AvatarFileID == nil || *user.AvatarFileID != avatarID {
		t.Fatalf("user=%+v err=%v", user, err)
	}

	// 为同一联系人补一个更早更新、带头像的渠道身份，原渠道身份暂无头像。
	existing := servermodels.ContactChannelIdentity{}
	if err := f.db.NewSelect().Model(&existing).
		Join("JOIN channel_conversations AS cc ON cc.contact_channel_identity_id = cci.id").
		Where("cc.conversation_id = ?", f.conversationID).
		Scan(ctx); err != nil {
		t.Fatal(err)
	}
	olderAvatarID, newerAvatarID := uuid.NewV7().String(), uuid.NewV7().String()
	older := servermodels.ContactChannelIdentity{
		ID: uuid.NewV7().String(), OrganizationID: existing.OrganizationID, ContactID: existing.ContactID, ChannelID: existing.ChannelID,
		ExternalID: uuid.NewV7().String(), AvatarFileID: &olderAvatarID, CreatedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now().Add(-time.Hour),
	}
	if _, err := f.db.NewInsert().Model(&older).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	assertContactAvatar(t, f.db, f.owner, existing.ContactID, olderAvatarID)
	if _, err := f.db.NewUpdate().Model((*servermodels.ContactChannelIdentity)(nil)).Set("avatar_file_id = ?, updated_at = now()", newerAvatarID).Where("id = ?", existing.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	assertContactAvatar(t, f.db, f.owner, existing.ContactID, newerAvatarID)
}

// TestCreateMemberWithAvatar 验证新增企业成员时激活并关联已上传的头像，不可用的头像拒绝创建。
func TestCreateMemberWithAvatar(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	avatar, err := fileaction.NewCreateUploadAction(f.db).Execute(ctx, f.owner, domain.FileStorageBackendLocal, fileaction.UploadInput{
		Purpose: domain.FilePurposeUserAvatar, FileName: "member.png", ContentType: "image/png", ByteSize: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := markFileUploaded(ctx, f.db, f.owner, avatar.ID, ""); err != nil {
		t.Fatal(err)
	}
	create := newTestMemberCreator(f.db, newTestTasks(f.db))
	input := memberSpec{HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: "带头像成员", Email: uniqueEmail("avatar-member"), Password: "password123", RoleID: f.owner.User.RoleID, AvatarFileID: avatar.ID}
	created, err := create.Execute(ctx, f.owner, input)
	if err != nil || created.AvatarFileID == nil || *created.AvatarFileID != avatar.ID {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	var status string
	if err := f.db.NewSelect().TableExpr("files").Column("status").Where("id = ?", avatar.ID).Scan(ctx, &status); err != nil || status != string(domain.FileStatusActive) {
		t.Fatalf("avatar status=%q err=%v", status, err)
	}
	// 已被关联的头像不能再用于新成员。
	input.Email, input.DisplayName = uniqueEmail("avatar-member-2"), "第二个成员"
	if _, err := create.Execute(ctx, f.owner, input); !errors.Is(err, fileaction.ErrLinkedImageNotFound) {
		t.Fatalf("reuse avatar err=%v", err)
	}
}

// assertContactAvatar 检查联系人列表与详情返回同一个头像。
func assertContactAvatar(t *testing.T, db *bun.DB, identity *servermodels.Identity, contactID, avatarID string) {
	t.Helper()
	ctx := context.Background()
	list, err := contactaction.NewListContactsQuery(db).Execute(ctx, identity, contactaction.ListInput{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, contact := range list.Contacts {
		if contact.ID == contactID {
			found = true
			if contact.AvatarFileID == nil || *contact.AvatarFileID != avatarID {
				t.Fatalf("list avatar=%v, want %s", contact.AvatarFileID, avatarID)
			}
		}
	}
	if !found {
		t.Fatal("联系人列表缺少联系人")
	}
	detail, err := contactaction.NewGetContactQuery(db).Execute(ctx, identity, contactID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.AvatarFileID == nil || *detail.AvatarFileID != avatarID {
		t.Fatalf("detail avatar=%v, want %s", detail.AvatarFileID, avatarID)
	}
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
		if err != nil {
			t.Fatal(err)
		}
		if _, err := markFileUploaded(ctx, f.db, f.owner, avatar.ID, ""); err != nil {
			t.Fatal(err)
		}
		avatarIDs = append(avatarIDs, avatar.ID)
	}
	created, err := newTestMemberCreator(f.db, newTestTasks(f.db)).Execute(ctx, f.owner, memberSpec{
		DisplayName: "换头像成员", Email: uniqueEmail("avatar-update"), Password: "password123", RoleID: f.owner.User.RoleID, AvatarFileID: avatarIDs[0],
	})
	if err != nil {
		t.Fatal(err)
	}
	update := useraction.NewUpdateUserAction(f.db, testServiceSessionReturner(f.db), newTestTasks(f.db))
	input := useraction.UpdateInput{DisplayName: created.DisplayName, RoleID: created.RoleID}
	kept, err := update.Execute(ctx, f.owner, created.ID, input)
	if err != nil || kept.AvatarFileID == nil || *kept.AvatarFileID != avatarIDs[0] {
		t.Fatalf("kept=%+v err=%v", kept, err)
	}
	input.AvatarFileID = avatarIDs[1]
	replaced, err := update.Execute(ctx, f.owner, created.ID, input)
	if err != nil || replaced.AvatarFileID == nil || *replaced.AvatarFileID != avatarIDs[1] {
		t.Fatalf("replaced=%+v err=%v", replaced, err)
	}
	for fileID, want := range map[string]domain.FileStatus{avatarIDs[0]: domain.FileStatusDeleting, avatarIDs[1]: domain.FileStatusActive} {
		var status string
		if err := f.db.NewSelect().TableExpr("files").Column("status").Where("id = ?", fileID).Scan(ctx, &status); err != nil || status != string(want) {
			t.Fatalf("file %s status=%q err=%v, want %s", fileID, status, err, want)
		}
	}
}
