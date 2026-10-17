//go:build server

package integrationtest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	"github.com/stretchr/testify/require"
)

// TestS3Storage 验证真实 S3 的配置保存、匿名访问、签名直传和客户端分片合并。
func TestS3Storage(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	files := servertest.NewFiles(t)
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	deployment := servertest.NewDeployment(t, db)
	service := direct.New(db, direct.DeploymentConfig{Deployment: deployment}, nil, nil, testEnqueuer, nil, nil, nil)
	adminEmail := servertest.UniqueEmail("storage")
	admin := servertest.InstallPlatform(t, service, appservice.InstallPlatformInput{
		PublicURL: servertest.PublicURL, WorkspaceName: "S3 验证", DisplayName: "管理员", Email: adminEmail, Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	meta := servertest.AdminMeta(admin.AdminToken)
	userAuth, err := service.Login(ctx, appservice.RequestMeta{Locale: domain.LocaleChineseSimplified}, appservice.LoginInput{Email: adminEmail, Password: "password123"})
	require.NoError(t, err)
	workspaces, err := service.ListWorkspaces(ctx, appservice.RequestMeta{Token: userAuth.Token, Locale: domain.LocaleChineseSimplified})
	require.NoError(t, err)
	require.Len(t, workspaces.Items, 1)
	workspace := workspaces.Items[0]
	config := files.Config
	input := appservice.PlatformStorageSettings{Endpoint: config.Endpoint, PublicBaseURL: config.PublicBaseURL, Region: config.Region, Bucket: config.Bucket, AccessKeyID: config.AccessKeyID, SecretAccessKey: config.SecretAccessKey, ForcePathStyle: true}
	result, err := service.UpdatePlatformStorage(ctx, meta, input)
	require.NoError(t, err)
	require.Equal(t, domain.StorageOriginAccessPublic, result.OriginAccess)
	require.Equal(t, config, deployment.S3())
	assertEmpty := func() {
		objects, err := files.Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(config.Bucket)})
		require.NoError(t, err)
		require.Empty(t, objects.Contents, "连接校验不得留下临时对象")
	}
	assertEmpty()

	// 访问域名返回 200 错误页时拒绝保存，旧配置保持有效。
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("login page")) }))
	defer broken.Close()
	invalid := input
	invalid.PublicBaseURL = broken.URL
	_, err = service.UpdatePlatformStorage(ctx, meta, invalid)
	servertest.RequireLocalizedError(t, err, i18n.ErrorStoragePublicReadFailed)
	require.Equal(t, config, deployment.S3())
	assertEmpty()

	// 私有源站配合有权限回源的 CDN，可以通过匿名访问地址读取。
	_, err = files.Client.DeleteBucketPolicy(ctx, &s3.DeleteBucketPolicyInput{Bucket: aws.String(config.Bucket)})
	require.NoError(t, err)
	_, err = service.UpdatePlatformStorage(ctx, meta, input)
	servertest.RequireLocalizedError(t, err, i18n.ErrorStoragePublicReadFailed)
	assertEmpty()
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := files.Read(r.Context(), strings.TrimPrefix(r.URL.Path, "/assets/"))
		if err != nil {
			http.Error(w, "missing", http.StatusNotFound)
			return
		}
		_, _ = w.Write(body)
	}))
	defer cdn.Close()
	input.PublicBaseURL = cdn.URL + "/assets"
	result, err = service.UpdatePlatformStorage(ctx, meta, input)
	require.NoError(t, err)
	require.Equal(t, domain.StorageOriginAccessRestricted, result.OriginAccess)
	assertEmpty()

	// 成员直传普通文件，完成后地址不含任何过期签名。
	meta = appservice.RequestMeta{Token: userAuth.Token, WorkspaceID: workspace.ID, Locale: domain.LocaleChineseSimplified}
	small, err := service.CreateFileUpload(ctx, meta, appservice.FileUploadInput{Purpose: appservice.FilePurposeMessageAttachment, FileName: "hello.txt", ContentType: "text/plain", ByteSize: 5})
	require.NoError(t, err)
	performS3Request(t, small.Request, []byte("hello"))
	completed, err := service.CompleteFileUpload(ctx, meta, small.File.ID)
	require.NoError(t, err)
	require.Equal(t, input.PublicBaseURL+"/"+small.Key, completed.ContentURL)
	response, err := http.Get(completed.ContentURL)
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	require.NoError(t, err)
	require.Equal(t, []byte("hello"), body)
	_, err = service.SignFileUpload(ctx, meta, small.File.ID, appservice.FileUploadSignInput{Method: "PUT"})
	require.Error(t, err, "完成后不能继续签发覆盖写入")

	// 真实上传 101 MiB，合并由持有受限签名的客户端直接发起。
	total := int64(101 << 20)
	upload, err := service.CreateFileUpload(ctx, meta, appservice.FileUploadInput{Purpose: appservice.FilePurposeMessageAttachment, FileName: "multipart.txt", ContentType: "text/plain", ByteSize: total})
	require.NoError(t, err)
	require.NotEmpty(t, upload.UploadID)
	sign := func(method string, part int32) appservice.FileUploadRequest {
		signed, err := service.SignFileUpload(ctx, meta, upload.File.ID, appservice.FileUploadSignInput{Method: method, UploadID: upload.UploadID, PartNumber: part})
		require.NoError(t, err)
		return signed
	}
	_, err = service.SignFileUpload(ctx, meta, upload.File.ID, appservice.FileUploadSignInput{Method: "GET", UploadID: "another-session"})
	require.Error(t, err)
	_, err = service.CompleteFileUpload(ctx, meta, upload.File.ID)
	require.Error(t, err, "合并前不得完成业务上传")
	var parts strings.Builder
	parts.WriteString("<CompleteMultipartUpload>")
	for offset, number := int64(0), int32(1); offset < total; number++ {
		size := min(upload.PartSize, total-offset)
		headers := performS3Request(t, sign("PUT", number), bytes.Repeat([]byte("x"), int(size)))
		_, err := fmt.Fprintf(&parts, "<Part><PartNumber>%d</PartNumber><ETag>%s</ETag></Part>", number, headers.Get("ETag"))
		require.NoError(t, err)
		offset += size
	}
	performS3Request(t, sign("GET", 0), nil)
	parts.WriteString("</CompleteMultipartUpload>")
	performS3Request(t, sign("POST", 0), []byte(parts.String()))
	completed, err = service.CompleteFileUpload(ctx, meta, upload.File.ID)
	require.NoError(t, err)
	require.Equal(t, total, completed.ByteSize)
	repeated, err := service.CompleteFileUpload(ctx, meta, upload.File.ID)
	require.NoError(t, err)
	require.Equal(t, completed, repeated)

	// 中止签名直接清理 S3 分片，业务取消后拒绝新的签名。
	cancelled, err := service.CreateFileUpload(ctx, meta, appservice.FileUploadInput{Purpose: appservice.FilePurposeMessageAttachment, FileName: "cancel.txt", ContentType: "text/plain", ByteSize: total})
	require.NoError(t, err)
	signed, err := service.SignFileUpload(ctx, meta, cancelled.File.ID, appservice.FileUploadSignInput{Method: "DELETE", UploadID: cancelled.UploadID})
	require.NoError(t, err)
	performS3Request(t, signed, nil)
	require.NoError(t, service.CancelFileUpload(ctx, meta, cancelled.File.ID))
	_, err = service.SignFileUpload(ctx, meta, cancelled.File.ID, appservice.FileUploadSignInput{Method: "GET", UploadID: cancelled.UploadID})
	require.Error(t, err)
}

func performS3Request(t *testing.T, signed appservice.FileUploadRequest, payload []byte) http.Header {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), signed.Method, signed.URL, bytes.NewReader(payload))
	require.NoError(t, err)
	for key, value := range signed.Headers {
		request.Header.Set(key, value)
	}
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Less(t, response.StatusCode, 300, "%s", body)
	require.NotContains(t, string(body), "<Error>", "S3 合并可能通过 200 响应返回错误")
	return response.Header
}

// TestS3StorageProbeFailure 验证凭据写入失败时报告具体阶段。
func TestS3StorageProbeFailure(t *testing.T) {
	t.Parallel()
	files := servertest.NewFiles(t)
	config := files.Config
	config.SecretAccessKey = "invalid-secret"
	_, err := filecontent.CheckStorage(context.Background(), config)
	var check *filecontent.CheckError
	require.ErrorAs(t, err, &check)
	require.Equal(t, "write", check.Stage)
}

// TestS3StorageReadDeleteValidation 验证写入成功也不能掩盖读取或删除权限缺失。
func TestS3StorageReadDeleteValidation(t *testing.T) {
	t.Parallel()
	for _, denied := range []struct{ method, stage string }{{"GET", "read"}, {"DELETE", "delete"}} {
		t.Run(denied.stage, func(t *testing.T) {
			files := servertest.NewFiles(t)
			endpoint, err := url.Parse(files.Config.Endpoint)
			require.NoError(t, err)
			proxy := httputil.NewSingleHostReverseProxy(endpoint)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == denied.method && r.Header.Get("Authorization") != "" {
					w.Header().Set("Content-Type", "application/xml")
					w.WriteHeader(http.StatusForbidden)
					_, _ = io.WriteString(w, "<Error><Code>AccessDenied</Code></Error>")
					return
				}
				proxy.ServeHTTP(w, r)
			}))
			defer server.Close()
			config := files.Config
			config.Endpoint = server.URL
			_, err = filecontent.CheckStorage(t.Context(), config)
			var check *filecontent.CheckError
			require.ErrorAs(t, err, &check)
			require.Equal(t, denied.stage, check.Stage)
		})
	}
}
