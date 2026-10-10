//go:build server

package filecontent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// CreateMultipart 创建对象存储分片上传会话。
func CreateMultipart(ctx context.Context, config S3Config, key, contentType string) (string, error) {
	output, err := newS3Client(config).CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(config.Bucket), Key: aws.String(key), ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", err
	}
	return aws.ToString(output.UploadId), nil
}

// PresignPart 创建指定分片的客户端直传请求。
func PresignPart(ctx context.Context, config S3Config, key, uploadID string, number int32, size int64) (SignedRequest, error) {
	output, err := s3.NewPresignClient(newS3Client(config)).PresignUploadPart(ctx, &s3.UploadPartInput{
		Bucket: aws.String(config.Bucket), Key: aws.String(key), UploadId: aws.String(uploadID),
		PartNumber: aws.Int32(number), ContentLength: aws.Int64(size),
	}, func(options *s3.PresignOptions) { options.Expires = signedRequestLifetime })
	if err != nil {
		return SignedRequest{}, err
	}
	// 浏览器自行生成 Host 和 Content-Length，其余签名头随直传请求发送。
	headers := make(map[string]string)
	for name, values := range output.SignedHeader {
		if http.CanonicalHeaderKey(name) != "Host" && http.CanonicalHeaderKey(name) != "Content-Length" && len(values) > 0 {
			headers[name] = values[0]
		}
	}
	return SignedRequest{Method: output.Method, URL: output.URL, Headers: headers}, nil
}

// CompleteMultipart 按分片序号合并对象存储文件。
func CompleteMultipart(ctx context.Context, config S3Config, key, uploadID string, size, partSize int64) error {
	client := newS3Client(config)
	paginator := s3.NewListPartsPaginator(client, &s3.ListPartsInput{
		Bucket: aws.String(config.Bucket), Key: aws.String(key), UploadId: aws.String(uploadID),
	})
	completed := make([]types.CompletedPart, 0)
	var total int64
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, part := range page.Parts {
			expectedSize := min(partSize, size-total)
			if total >= size || aws.ToInt32(part.PartNumber) != int32(len(completed)+1) || aws.ToInt64(part.Size) != expectedSize {
				return errors.New("invalid uploaded part sequence or size")
			}
			completed = append(completed, types.CompletedPart{PartNumber: part.PartNumber, ETag: part.ETag})
			total += expectedSize
		}
	}
	if total != size {
		return errors.New("multipart upload is incomplete")
	}
	_, err := client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket: aws.String(config.Bucket), Key: aws.String(key), UploadId: aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: completed},
	})
	return err
}

// AbortMultipart 清除未完成的对象存储分片，会话不存在时直接成功。
func AbortMultipart(ctx context.Context, config S3Config, key, uploadID string) error {
	_, err := newS3Client(config).AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket: aws.String(config.Bucket), Key: aws.String(key), UploadId: aws.String(uploadID),
	})
	var missing *types.NoSuchUpload
	if errors.As(err, &missing) {
		return nil
	}
	return err
}

// SavePart 把单个分片原子保存到文件专属的临时目录。
func (s *LocalStore) SavePart(ctx context.Context, key string, number int32, source io.Reader, size int64) error {
	if _, err := s.path(key); err != nil {
		return err
	}
	directory := filepath.Join(s.temporary, filepath.FromSlash(key))
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".part-*")
	if err != nil {
		return err
	}
	defer func() { _ = temporary.Close(); _ = os.Remove(temporary.Name()) }()
	written, err := io.Copy(temporary, io.LimitReader(source, size+1))
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if written != size {
		return fmt.Errorf("part size = %d, want %d", written, size)
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), filepath.Join(directory, strconv.Itoa(int(number))))
}

// CompleteMultipart 顺序读取本地分片并原子生成最终文件。
func (s *LocalStore) CompleteMultipart(ctx context.Context, key string, size, partSize int64) error {
	if _, err := s.path(key); err != nil {
		return err
	}
	directory := filepath.Join(s.temporary, filepath.FromSlash(key))
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		var result error
		defer func() { _ = writer.CloseWithError(result); done <- result }()
		for offset, number := int64(0), 1; offset < size; offset, number = offset+partSize, number+1 {
			if result = ctx.Err(); result != nil {
				return
			}
			part, err := os.Open(filepath.Join(directory, strconv.Itoa(number)))
			if err != nil {
				result = err
				return
			}
			expected := min(partSize, size-offset)
			info, err := part.Stat()
			if err != nil || info.Size() != expected {
				_ = part.Close()
				result = fmt.Errorf("invalid local part %d", number)
				return
			}
			_, result = io.Copy(writer, part)
			_ = part.Close()
			if result != nil {
				return
			}
		}
	}()
	err := s.Save(ctx, key, reader, size)
	_ = reader.CloseWithError(err)
	partErr := <-done
	if err != nil {
		return err
	}
	return partErr
}

// DeleteParts 清除文件的本地分片目录。
func (s *LocalStore) DeleteParts(key string) error {
	if _, err := s.path(key); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(s.temporary, filepath.FromSlash(key)))
}

// PresignDownload 创建携带原始文件名的对象存储下载请求。
func PresignDownload(ctx context.Context, config S3Config, key, disposition string) (SignedRequest, error) {
	output, err := s3.NewPresignClient(newS3Client(config)).PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(config.Bucket), Key: aws.String(key), ResponseContentDisposition: aws.String(disposition),
	}, func(options *s3.PresignOptions) { options.Expires = signedRequestLifetime })
	if err != nil {
		return SignedRequest{}, err
	}
	return SignedRequest{Method: output.Method, URL: output.URL, Headers: map[string]string{}}, nil
}
