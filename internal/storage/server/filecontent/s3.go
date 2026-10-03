//go:build server

package filecontent

import (
	"bytes"
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/runforyou-ai/luway/internal/domain"
)

const signedRequestLifetime = 15 * time.Minute

// S3Config 定义访问 S3 兼容对象存储所需配置，关闭时新文件写入本地存储。
type S3Config struct {
	Enabled         bool
	Endpoint        string
	PublicBaseURL   string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	ForcePathStyle  bool
}

// Backend 返回新文件写入的存储类型，开启对象存储时写入对象存储，否则写入本地目录。
func (c S3Config) Backend() domain.FileStorageBackend {
	if c.Enabled {
		return domain.FileStorageBackendS3
	}
	return domain.FileStorageBackendLocal
}

// SignedRequest 定义客户端直传对象存储所需请求。
type SignedRequest struct {
	Method  string
	URL     string
	Headers map[string]string
}

// ObjectInfo 返回对象存储中的文件元数据。
type ObjectInfo struct {
	ByteSize int64
	ETag     string
}

// Put 把服务端内容写入 S3 兼容对象存储并返回 ETag。
func Put(ctx context.Context, config S3Config, key, contentType string, data []byte) (string, error) {
	output, err := newS3Client(config).PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(config.Bucket), Key: aws.String(key), ContentType: aws.String(contentType), CacheControl: aws.String(ImmutableCacheControl),
		ContentLength: aws.Int64(int64(len(data))), Body: bytes.NewReader(data),
	})
	if err != nil {
		return "", err
	}
	return aws.ToString(output.ETag), nil
}

// PresignPut 创建对象直传请求。
func PresignPut(ctx context.Context, config S3Config, key, contentType string) (SignedRequest, error) {
	presigned, err := s3.NewPresignClient(newS3Client(config)).PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(config.Bucket), Key: aws.String(key), ContentType: aws.String(contentType), CacheControl: aws.String(ImmutableCacheControl),
	}, func(options *s3.PresignOptions) {
		options.Expires = signedRequestLifetime
	})
	if err != nil {
		return SignedRequest{}, err
	}
	headers := presigned.SignedHeader.Clone()
	headers.Set("Content-Type", contentType)
	// 将签名请求头转换成前端契约。
	flattenedHeaders := make(map[string]string, len(headers))
	for name, values := range headers {
		if http.CanonicalHeaderKey(name) == "Host" {
			continue
		}
		if len(values) > 0 {
			flattenedHeaders[name] = values[0]
		}
	}
	return SignedRequest{
		Method:  http.MethodPut,
		URL:     presigned.URL,
		Headers: flattenedHeaders,
	}, nil
}

// Stat 读取对象元数据。
func Stat(ctx context.Context, config S3Config, key string) (ObjectInfo, error) {
	output, err := newS3Client(config).HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(config.Bucket), Key: aws.String(key)})
	if err != nil {
		return ObjectInfo{}, err
	}
	return ObjectInfo{ByteSize: aws.ToInt64(output.ContentLength), ETag: aws.ToString(output.ETag)}, nil
}

// CheckBucket 确认存储桶可以用当前凭据访问。
func CheckBucket(ctx context.Context, config S3Config) error {
	_, err := newS3Client(config).HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(config.Bucket)})
	return err
}

// Delete 删除对象存储文件。
func Delete(ctx context.Context, config S3Config, key string) error {
	_, err := newS3Client(config).DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(config.Bucket),
		Key:    aws.String(key),
	})
	return err
}

// s3Clients 按配置缓存 S3 兼容客户端，同一配置复用 HTTP 连接和凭据缓存。
var s3Clients sync.Map

// newS3Client 返回指定配置的 S3 兼容客户端。
func newS3Client(config S3Config) *s3.Client {
	if client, ok := s3Clients.Load(config); ok {
		return client.(*s3.Client)
	}
	client, _ := s3Clients.LoadOrStore(config, s3.New(s3.Options{
		BaseEndpoint: aws.String(config.Endpoint), Region: config.Region,
		Credentials:  aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(config.AccessKeyID, config.SecretAccessKey, "")),
		UsePathStyle: config.ForcePathStyle,
	}))
	return client.(*s3.Client)
}
