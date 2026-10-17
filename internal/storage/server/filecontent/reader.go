//go:build server

package filecontent

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// Open 打开 S3 对象响应体，由调用方关闭。
func (s *Store) Open(ctx context.Context, file *servermodels.File) (io.ReadCloser, error) {
	config := s.s3()
	output, err := newS3Client(config).GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(config.Bucket), Key: aws.String(file.StorageKey)})
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}

// sniffLength 是识别内容类型读取的开头字节数。
const sniffLength = 512

// UploadedObject 是存储中上传内容的 ETag、实际字节数与用于识别内容类型的开头字节。
type UploadedObject struct {
	ETag     string
	ByteSize int64
	Head     []byte
}

// Inspect 读取上传内容的元数据与开头至多 512 字节。
func (s *Store) Inspect(ctx context.Context, file *servermodels.File) (UploadedObject, error) {
	var object UploadedObject
	var content io.ReadCloser
	config := s.s3()
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
	defer content.Close()
	head, err := io.ReadAll(io.LimitReader(content, sniffLength))
	if err != nil {
		return UploadedObject{}, fmt.Errorf("read file head: %w", err)
	}
	object.Head = head
	return object, nil
}
