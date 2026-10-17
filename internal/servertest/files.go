//go:build server

package servertest

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"
	"uuid"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// Files 为每项测试创建独立的真实 S3 桶，退出时只清理该桶。
type Files struct {
	*filecontent.Store
	Config filecontent.S3Config
	Client *s3.Client
}

// NewFiles 创建带公开文件读取策略的隔离测试桶。
func NewFiles(t testing.TB) *Files {
	t.Helper()
	config := filecontent.S3Config{Endpoint: os.Getenv("TEST_S3_ENDPOINT"), Region: "us-east-1", Bucket: "luway-test-" + uuid.NewV7().String(), AccessKeyID: os.Getenv("TEST_S3_ACCESS_KEY_ID"), SecretAccessKey: os.Getenv("TEST_S3_SECRET_ACCESS_KEY"), ForcePathStyle: true}
	require.NotEmpty(t, config.Endpoint, "请配置 TEST_S3_ENDPOINT，并在主工作区运行 wails3 task dev:up")
	config.PublicBaseURL = config.Endpoint + "/" + config.Bucket
	client := s3.New(s3.Options{BaseEndpoint: aws.String(config.Endpoint), Region: config.Region, UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider(config.AccessKeyID, config.SecretAccessKey, "")})
	ctx := context.Background()
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(config.Bucket)})
	require.NoError(t, err)
	f := &Files{Config: config, Client: client, Store: filecontent.NewStore(func() filecontent.S3Config { return config })}
	t.Cleanup(func() {
		uploads := s3.NewListMultipartUploadsPaginator(client, &s3.ListMultipartUploadsInput{Bucket: aws.String(config.Bucket)})
		for uploads.HasMorePages() {
			page, e := uploads.NextPage(ctx)
			if e != nil {
				t.Errorf("list test uploads: %v", e)
				break
			}
			for _, u := range page.Uploads {
				_, e = client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: aws.String(config.Bucket), Key: u.Key, UploadId: u.UploadId})
				require.NoError(t, e)
			}
		}
		objects := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{Bucket: aws.String(config.Bucket)})
		for objects.HasMorePages() {
			page, e := objects.NextPage(ctx)
			if e != nil {
				t.Errorf("list test objects: %v", e)
				break
			}
			for _, o := range page.Contents {
				_, e = client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(config.Bucket), Key: o.Key})
				require.NoError(t, e)
			}
		}
		_, e := client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(config.Bucket)})
		require.NoError(t, e)
	})
	// 普通业务文件与知识文档原件均可经公开访问地址匿名读取。
	policy, _ := json.Marshal(map[string]any{"Version": "2012-10-17", "Statement": []any{
		map[string]any{"Effect": "Allow", "Principal": "*", "Action": "s3:GetObject", "Resource": []string{
			"arn:aws:s3:::" + config.Bucket + "/workspaces/*/files/*",
			"arn:aws:s3:::" + config.Bucket + "/workspaces/*/knowledge-documents/*",
		}},
	}})
	_, err = client.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{Bucket: aws.String(config.Bucket), Policy: aws.String(string(policy))})
	require.NoError(t, err)
	return f
}

// Put 写入测试文件内容。
func (f *Files) Put(ctx context.Context, key string, data []byte) error {
	_, err := f.Store.Save(ctx, &servermodels.File{StorageKey: key, ContentType: "application/octet-stream", ByteSize: int64(len(data))}, data)
	return err
}

// Read 读取测试对象的实际内容。
func (f *Files) Read(ctx context.Context, key string) ([]byte, error) {
	body, err := f.Store.Open(ctx, &servermodels.File{StorageKey: key})
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return io.ReadAll(body)
}

// Configure 将测试桶写入当前测试数据库，仅在不会并发修改部署配置的测试中使用。
func (f *Files) Configure(t testing.TB, db *bun.DB) {
	t.Helper()
	var previous servermodels.Platform
	require.NoError(t, db.NewSelect().Model(&previous).Limit(1).Scan(t.Context()))
	t.Cleanup(func() {
		_, err := db.NewUpdate().Model(&previous).Column("s3_endpoint", "s3_public_base_url", "s3_region", "s3_bucket", "s3_access_key_id", "s3_secret_access_key", "s3_force_path_style").WherePK().Exec(context.Background())
		require.NoError(t, err)
	})
	_, err := db.NewUpdate().Model((*servermodels.Platform)(nil)).
		Set("s3_endpoint = ?", f.Config.Endpoint).Set("s3_public_base_url = ?", f.Config.PublicBaseURL).
		Set("s3_region = ?", f.Config.Region).Set("s3_bucket = ?", f.Config.Bucket).
		Set("s3_access_key_id = ?", f.Config.AccessKeyID).Set("s3_secret_access_key = ?", f.Config.SecretAccessKey).
		Set("s3_force_path_style = ?", true).Where("TRUE").Exec(t.Context())
	require.NoError(t, err)
}
