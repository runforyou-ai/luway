//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"

	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
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
	if err != nil {
		t.Fatal(err)
	}
	multipart, err := create.Execute(ctx, f.owner, domain.FileStorageBackendLocal, fileaction.UploadInput{
		Purpose: domain.FilePurposeMessageAttachment, FileName: "large.bin", ContentType: "application/octet-stream", ByteSize: domain.FilePartSize + 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	owner := direct.LocalObjectCredentials{Bearer: f.ownerToken}
	if upload, err := authorizer.AuthorizeUpload(ctx, owner, single.StorageKey, ""); err != nil || upload.PartNumber != 0 || upload.ExpectedSize != 10 {
		t.Fatalf("single upload = %+v, %v", upload, err)
	}
	if upload, err := authorizer.AuthorizeUpload(ctx, owner, multipart.StorageKey, "2"); err != nil || upload.PartNumber != 2 || upload.ExpectedSize != 1 {
		t.Fatalf("second part = %+v, %v", upload, err)
	}
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
		if _, err := authorizer.AuthorizeUpload(ctx, test.credentials, test.storageKey, test.partNumber); !errors.Is(err, test.want) {
			t.Fatalf("%s: error = %v, want %v", test.name, err, test.want)
		}
	}
	// 已写入内容的文件拒绝直传。
	if _, err := f.db.ExecContext(ctx, "UPDATE files SET status = ? WHERE id = ?", domain.FileStatusUploaded, single.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := authorizer.AuthorizeUpload(ctx, owner, single.StorageKey, ""); !errors.Is(err, direct.ErrLocalObjectConflict) {
		t.Fatalf("uploaded file error = %v", err)
	}
}
