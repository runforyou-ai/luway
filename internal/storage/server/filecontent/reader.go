//go:build server

package filecontent

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// Reader 按原件记录中的存储类型流式读取文件。
type Reader struct {
	local *LocalStore
	s3    S3Settings
}

// NewReader 创建后台原件读取器，s3 返回读取时的对象存储配置。
func NewReader(local *LocalStore, s3 S3Settings) *Reader {
	return &Reader{local: local, s3: s3}
}

// Open 打开本地原件或对象存储响应体，由调用方关闭。
func (r *Reader) Open(ctx context.Context, file *servermodels.File) (io.ReadCloser, error) {
	switch domain.FileStorageBackend(file.StorageBackend) {
	case domain.FileStorageBackendLocal:
		content, _, err := r.local.Open(ctx, file.StorageKey)
		return content, err
	case domain.FileStorageBackendS3:
		config := r.s3()
		output, err := newS3Client(config).GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(config.Bucket), Key: aws.String(file.StorageKey)})
		if err != nil {
			return nil, err
		}
		return output.Body, nil
	default:
		return nil, fmt.Errorf("invalid file storage backend %q", file.StorageBackend)
	}
}

// sniffLength 是识别内容类型读取的开头字节数。
const sniffLength = 512

// UploadedObject 是存储中上传内容的 ETag、实际字节数与用于识别内容类型的开头字节。
type UploadedObject struct {
	ETag     string
	ByteSize int64
	Head     []byte
}

// Inspect 读取上传内容的元数据与开头至多 512 字节；本地存储没有 ETag。
func (r *Reader) Inspect(ctx context.Context, file *servermodels.File) (UploadedObject, error) {
	var object UploadedObject
	var content io.ReadCloser
	switch domain.FileStorageBackend(file.StorageBackend) {
	case domain.FileStorageBackendLocal:
		opened, info, err := r.local.Open(ctx, file.StorageKey)
		if err != nil {
			return UploadedObject{}, fmt.Errorf("open local file: %w", err)
		}
		object.ByteSize, content = info.Size(), opened
	case domain.FileStorageBackendS3:
		config := r.s3()
		info, err := Stat(ctx, config, file.StorageKey)
		if err != nil {
			return UploadedObject{}, fmt.Errorf("stat S3 file: %w", err)
		}
		object.ETag, object.ByteSize = info.ETag, info.ByteSize
		if object.ByteSize == 0 {
			return object, nil
		}
		output, err := newS3Client(config).GetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(config.Bucket), Key: aws.String(file.StorageKey), Range: aws.String(fmt.Sprintf("bytes=0-%d", sniffLength-1)),
		})
		if err != nil {
			return UploadedObject{}, fmt.Errorf("read S3 file head: %w", err)
		}
		content = output.Body
	default:
		return UploadedObject{}, fmt.Errorf("invalid file storage backend %q", file.StorageBackend)
	}
	defer content.Close()
	head, err := io.ReadAll(io.LimitReader(content, sniffLength))
	if err != nil {
		return UploadedObject{}, fmt.Errorf("read file head: %w", err)
	}
	object.Head = head
	return object, nil
}
