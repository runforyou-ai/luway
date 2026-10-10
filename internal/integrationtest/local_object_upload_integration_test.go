//go:build server

package integrationtest

import (
	"context"
	"testing"

	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/stretchr/testify/require"
)

// TestLocalObjectUploadAuthorization 验证本地对象直传按凭据认证、校验文件归属与待写入状态，并按分片序号给出期望字节数。
func TestLocalObjectUploadAuthorization(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	authorizer := direct.NewLocalObjectAuthorizer(f.db)
	create := fileaction.NewCreateUploadAction(f.db)
	single, err := create.Execute(ctx, f.owner, domain.FileStorageBackendLocal, fileaction.UploadInput{
		Purpose: domain.FilePurposeMessageAttachment, FileName: "note.txt", ContentType: "text/plain", ByteSize: 10,
	})
	require.NoError(t, err)
	multipart, err := create.Execute(ctx, f.owner, domain.FileStorageBackendLocal, fileaction.UploadInput{
		Purpose: domain.FilePurposeMessageAttachment, FileName: "large.bin", ContentType: "application/octet-stream", ByteSize: domain.FilePartSize + 1,
	})
	require.NoError(t, err)
	owner := direct.LocalObjectCredentials{Bearer: f.ownerToken}
	upload, err := authorizer.AuthorizeUpload(ctx, owner, single.StorageKey, "")
	require.NoError(t, err, "single upload")
	require.Equal(t, int32(0), upload.PartNumber)
	require.Equal(t, int64(10), upload.ExpectedSize)
	upload, err = authorizer.AuthorizeUpload(ctx, owner, multipart.StorageKey, "2")
	require.NoError(t, err, "second part")
	require.Equal(t, int32(2), upload.PartNumber)
	require.Equal(t, int64(1), upload.ExpectedSize)
	for _, test := range []struct {
		name        string
		credentials direct.LocalObjectCredentials
		storageKey  string
		partNumber  string
		want        error
	}{
		{"成员令牌无效", direct.LocalObjectCredentials{Bearer: "invalid"}, single.StorageKey, "", direct.ErrLocalObjectUnauthorized},
		{"其他成员的文件", direct.LocalObjectCredentials{Bearer: f.memberToken}, single.StorageKey, "", direct.ErrLocalObjectNotFound},
		{"访客令牌格式不合法", direct.LocalObjectCredentials{VisitorToken: "not-a-token"}, single.StorageKey, "", direct.ErrLocalObjectUnauthorized},
		{"访客不拥有文件", direct.LocalObjectCredentials{VisitorToken: "0123456789abcdef0123456789abcdef"}, single.StorageKey, "", direct.ErrLocalObjectNotFound},
		{"签名身份无效", direct.LocalObjectCredentials{CustomerToken: "invalid"}, single.StorageKey, "", direct.ErrLocalObjectUnauthorized},
		{"分片序号越界", owner, multipart.StorageKey, "3", direct.ErrLocalObjectInvalid},
		{"分片序号缺失", owner, multipart.StorageKey, "", direct.ErrLocalObjectInvalid},
	} {
		_, err := authorizer.AuthorizeUpload(ctx, test.credentials, test.storageKey, test.partNumber)
		require.ErrorIs(t, err, test.want, test.name)
	}
	// 已写入内容的文件拒绝直传。
	_, err = f.db.ExecContext(ctx, "UPDATE files SET status = ? WHERE id = ?", domain.FileStatusUploaded, single.ID)
	require.NoError(t, err)
	_, err = authorizer.AuthorizeUpload(ctx, owner, single.StorageKey, "")
	require.ErrorIs(t, err, direct.ErrLocalObjectConflict, "uploaded file")
}
