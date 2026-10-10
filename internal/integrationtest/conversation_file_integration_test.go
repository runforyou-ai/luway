//go:build server

package integrationtest

import (
	"bytes"
	"context"
	"io"
	"testing"
	"uuid"

	"github.com/google/go-cmp/cmp"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/actions/conversationfile"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common/textfile"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// conversationFileFixture 是会话文件区测试的群聊、成员、本地文件存储与文件区读写。
type conversationFileFixture struct {
	navigationFixture
	local   *serverfilecontent.LocalStore
	store   *conversationfile.Store
	members *conversationfile.MemberFiles
}

// newConversationFileFixture 以临时目录作为本地文件存储创建会话文件区读写。
func newConversationFileFixture(t *testing.T) conversationFileFixture {
	t.Helper()
	local, err := serverfilecontent.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	settings := func() serverfilecontent.S3Config { return serverfilecontent.S3Config{} }
	f := newNavigationFixture(t)
	store := conversationfile.NewStore(f.db, func() domain.FileStorageBackend { return domain.FileStorageBackendLocal },
		serverfilecontent.NewWriter(local, settings), serverfilecontent.NewReader(local, settings))
	return conversationFileFixture{navigationFixture: f, local: local, store: store, members: conversationfile.NewMemberFiles(store)}
}

// upload 以成员身份上传文件内容并完成上传，返回文件编号。
func (f conversationFileFixture) upload(t *testing.T, identity *servermodels.Identity, purpose domain.FilePurpose, name string, content []byte) string {
	t.Helper()
	ctx := context.Background()
	file, err := fileaction.NewCreateUploadAction(f.db).Execute(ctx, identity, domain.FileStorageBackendLocal, fileaction.UploadInput{
		Purpose: purpose, FileName: name, ContentType: "text/plain", ByteSize: int64(len(content)),
	})
	require.NoError(t, err)
	require.NoError(t, f.local.Save(ctx, file.StorageKey, bytes.NewReader(content), int64(len(content))))
	_, err = markFileUploaded(ctx, f.db, identity, file.ID, "")
	require.NoError(t, err)
	return file.ID
}

// content 读取会话文件当前版本的内容。
func (f conversationFileFixture) content(t *testing.T, identity *servermodels.Identity, id string) []byte {
	t.Helper()
	_, record, err := f.members.File(context.Background(), identity, f.groupID, id)
	require.NoError(t, err)
	reader, err := serverfilecontent.NewReader(f.local, func() serverfilecontent.S3Config { return serverfilecontent.S3Config{} }).Open(context.Background(), record)
	require.NoError(t, err)
	defer reader.Close()
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	return data
}

// TestConversationFilesMemberAccess 验证成员上传、同名追加序号、保存附件、下载与删除，非会话成员不能访问文件区。
func TestConversationFilesMemberAccess(t *testing.T) {
	t.Parallel()
	f := newConversationFileFixture(t)
	ctx := context.Background()

	first, err := f.members.AddUpload(ctx, f.owner, f.groupID, f.upload(t, f.owner, domain.FilePurposeConversationFile, "方案.md", []byte("v1")))
	require.NoError(t, err)
	require.Equal(t, "方案.md", first.Path)
	require.Equal(t, textfile.Hash([]byte("v1")), first.ContentHash)
	require.NotNil(t, first.UpdatedByName)
	require.Equal(t, "群主", *first.UpdatedByName)
	second, err := f.members.AddUpload(ctx, f.member, f.groupID, f.upload(t, f.member, domain.FilePurposeConversationFile, "方案.md", []byte("v2")))
	require.NoError(t, err)
	require.Equal(t, "方案 (2).md", second.Path)
	// 其他成员上传的文件、消息附件用途的文件与已加入的文件都不能再次加入。
	_, err = f.members.AddUpload(ctx, f.owner, f.groupID, f.upload(t, f.member, domain.FilePurposeConversationFile, "其他.md", []byte("x")))
	require.ErrorIs(t, err, conversationfile.ErrNotFound)
	_, err = f.members.AddUpload(ctx, f.owner, f.groupID, f.upload(t, f.owner, domain.FilePurposeMessageAttachment, "附件.md", []byte("x")))
	require.ErrorIs(t, err, conversationfile.ErrNotFound)
	_, err = f.members.AddUpload(ctx, f.owner, f.groupID, first.FileID)
	require.ErrorIs(t, err, conversationfile.ErrNotFound)

	// 消息附件保存为新文件，同名时追加序号。
	message, err := directchataction.NewSendAttachmentMessageAction(f.db, testEnqueuer, nil).Execute(ctx, f.owner, directchataction.AttachmentMessageInput{
		ConversationID: f.groupID, ClientMessageID: uuid.NewV7().String(), FileID: f.upload(t, f.owner, domain.FilePurposeMessageAttachment, "方案.md", []byte("attachment")),
	})
	require.NoError(t, err)
	saved, err := f.members.SaveAttachment(ctx, f.member, f.groupID, message.Message.ID)
	require.NoError(t, err)
	require.Equal(t, "方案 (3).md", saved.Path)
	require.Equal(t, []byte("attachment"), f.content(t, f.member, saved.ID))

	entries, err := f.members.List(ctx, f.member, f.groupID)
	require.NoError(t, err)
	paths := make([]string, len(entries))
	for index, entry := range entries {
		paths[index] = entry.Path
	}
	require.Empty(t, cmp.Diff([]string{"方案 (2).md", "方案 (3).md", "方案.md"}, paths))

	// 在会话锁内复核修改人失败时放弃写入，文件区中没有该文件。
	_, err = f.store.Write(ctx, conversationfile.WriteInput{
		WorkspaceID: f.owner.Workspace.ID, ConversationID: f.groupID, Path: "revoked.txt", Data: []byte("x"),
		Mode: conversationfile.WriteNew, SubjectID: f.subjectID, UserID: f.member.User.ID,
		Authorize: func(context.Context, bun.Tx) error { return conversationaction.ErrConversationNotFound },
	})
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound)
	_, _, err = f.store.Lookup(ctx, f.owner.Workspace.ID, f.groupID, "", "revoked.txt")
	require.ErrorIs(t, err, conversationfile.ErrNotFound)

	// 不在群内的成员看不到文件区。
	outsiderEmail := servertest.UniqueEmail("outsider")
	_, err = newTestMemberCreator(f.db, testEnqueuer).Execute(ctx, f.owner, memberSpec{DisplayName: "局外人", Email: outsiderEmail, Password: "password123", RoleID: f.owner.User.RoleID})
	require.NoError(t, err)
	outsider := servertest.LoginMember(t, f.db, f.owner.Workspace.ID, outsiderEmail, "password123").Identity
	_, err = f.members.List(ctx, outsider, f.groupID)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound)
	require.ErrorIs(t, f.members.Delete(ctx, outsider, f.groupID, first.ID), conversationaction.ErrConversationNotFound)

	// 文件区变化推进会话版本，断线重连的同步探针据此发现变化。
	versionOf := func() int64 {
		var version int64
		require.NoError(t, f.db.NewSelect().Model((*servermodels.Conversation)(nil)).Column("version").Where("id = ?", f.groupID).Scan(ctx, &version))
		return version
	}
	before := versionOf()
	require.NoError(t, f.members.Delete(ctx, f.member, f.groupID, second.ID))
	require.Greater(t, versionOf(), before)

	// 删除后文件区不再列出该文件，内容文件交给过期清理。
	require.NoError(t, f.members.Delete(ctx, f.member, f.groupID, first.ID))
	_, _, err = f.members.File(ctx, f.owner, f.groupID, first.ID)
	require.ErrorIs(t, err, conversationfile.ErrNotFound)
	retired := &servermodels.File{ID: first.FileID}
	require.NoError(t, f.db.NewSelect().Model(retired).WherePK().Scan(ctx))
	require.Equal(t, string(domain.FileStatusDeleting), retired.Status)
	require.ErrorIs(t, f.members.Delete(ctx, f.member, f.groupID, first.ID), conversationfile.ErrNotFound)

	// 上传完成后成员被停用时提交被拒绝。
	pending := f.upload(t, f.member, domain.FilePurposeConversationFile, "disabled.txt", []byte("x"))
	_, err = f.db.NewUpdate().Model((*servermodels.User)(nil)).Set("status = ?", domain.IdentityStatusInactive).Where("id = ?", f.member.User.ID).Exec(ctx)
	require.NoError(t, err)
	_, err = f.members.AddUpload(ctx, f.member, f.groupID, pending)
	require.ErrorIs(t, err, identityaction.ErrInvalid)
}

// TestConversationFilesWrite 验证覆盖写入要求读取时的摘要、重复写入幂等、只新建的写入拒绝同名文件，被替换的版本交给过期清理。
func TestConversationFilesWrite(t *testing.T) {
	t.Parallel()
	f := newConversationFileFixture(t)
	ctx := context.Background()
	subject := f.subjectID
	write := func(path, content, base string, mode conversationfile.WriteMode) (conversationfile.Entry, error) {
		return f.store.Write(ctx, conversationfile.WriteInput{
			WorkspaceID: f.owner.Workspace.ID, ConversationID: f.groupID, Path: path, Data: []byte(content),
			Mode: mode, BaseHash: base, SubjectID: subject, UserID: f.member.User.ID,
		})
	}

	created, err := write("docs/../报告.md", "v1", "", conversationfile.WriteReplace)
	require.NoError(t, err)
	require.Equal(t, "报告.md", created.Path)
	require.Equal(t, "text/markdown", created.ContentType)
	_, err = write("../报告.md", "v1", "", conversationfile.WriteReplace)
	require.ErrorIs(t, err, conversationfile.ErrInvalidPath)
	_, err = write("报告.md", "v2", "", conversationfile.WriteReplace)
	require.ErrorIs(t, err, conversationfile.ErrNotRead)
	_, err = write("报告.md", "v2", textfile.Hash([]byte("other")), conversationfile.WriteReplace)
	require.ErrorIs(t, err, conversationfile.ErrModified)
	repeated, err := write("报告.md", "v1", "", conversationfile.WriteReplace)
	require.NoError(t, err)
	require.Equal(t, created.FileID, repeated.FileID)
	updated, err := write("报告.md", "v2", created.ContentHash, conversationfile.WriteReplace)
	require.NoError(t, err)
	require.Equal(t, created.ID, updated.ID)
	require.NotEqual(t, created.FileID, updated.FileID)
	require.Equal(t, []byte("v2"), f.content(t, f.owner, updated.ID))
	_, err = write("报告.md", "v3", "", conversationfile.WriteNew)
	require.ErrorIs(t, err, conversationfile.ErrExists)

	// 被替换与未采用的内容文件都交给过期清理。
	var statuses []string
	require.NoError(t, f.db.NewSelect().Model((*servermodels.File)(nil)).Column("status").
		Where("workspace_id = ? AND purpose = ? AND id <> ?", f.owner.Workspace.ID, domain.FilePurposeConversationFile, updated.FileID).
		Scan(ctx, &statuses))
	require.NotEmpty(t, statuses)
	for _, status := range statuses {
		require.Equal(t, string(domain.FileStatusDeleting), status)
	}

	read, err := f.store.Read(ctx, f.owner.Workspace.ID, f.groupID, "报告.md", 1)
	require.NoError(t, err)
	require.Nil(t, read.Data, "content over the read limit returned")
	read, err = f.store.Read(ctx, f.owner.Workspace.ID, f.groupID, "/报告.md", textfile.MaxReadBytes)
	require.NoError(t, err)
	require.Equal(t, []byte("v2"), read.Data)
}
