//go:build server

package filecontent

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
	"uuid"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/runforyou-ai/luway/internal/domain"
)

// CheckError 标识配置校验失败的步骤，底层错误只写入服务端日志。
type CheckError struct {
	Stage string
	Err   error
}

func (e *CheckError) Error() string {
	return fmt.Sprintf("check object storage %s: %v", e.Stage, e.Err)
}
func (e *CheckError) Unwrap() error { return e.Err }

// CheckStorage 用一次性对象验证凭据读写、访问地址和删除能力，并探测源站匿名读取。
func CheckStorage(ctx context.Context, config S3Config) (access domain.StorageOriginAccess, err error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	access = domain.StorageOriginAccessUnknown
	key := "workspaces/" + uuid.NewV7().String() + "/files/" + uuid.NewV7().String() + ".txt"
	payload := make([]byte, 32)
	if _, err := rand.Read(payload); err != nil {
		return access, err
	}
	client := newS3Client(config)
	// 即使上传响应丢失，也尝试删除本次随机对象；独立超时保证取消请求后仍能清理。
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cleanupCancel()
		if cleanupErr := Delete(cleanupCtx, config, key); cleanupErr != nil {
			err = errors.Join(err, &CheckError{Stage: "delete", Err: cleanupErr})
		}
	}()
	if _, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(config.Bucket), Key: aws.String(key), Body: bytes.NewReader(payload),
		ContentLength: aws.Int64(int64(len(payload))), ContentType: aws.String("application/octet-stream"), CacheControl: aws.String("no-store"),
	}); err != nil {
		return access, &CheckError{Stage: "write", Err: err}
	}
	object, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(config.Bucket), Key: aws.String(key)})
	if err != nil {
		return access, &CheckError{Stage: "read", Err: err}
	}
	data, readErr := io.ReadAll(io.LimitReader(object.Body, int64(len(payload)+1)))
	_ = object.Body.Close()
	if readErr != nil || !bytes.Equal(data, payload) {
		return access, &CheckError{Stage: "read", Err: errors.New("object content mismatch")}
	}
	publicURL, err := PublicURL(config.PublicBaseURL, key)
	if err != nil {
		return access, &CheckError{Stage: "public", Err: err}
	}
	if _, err := checkAnonymousObject(ctx, publicURL, payload); err != nil {
		return access, &CheckError{Stage: "public", Err: err}
	}
	// 由 SDK 确定虚拟主机或路径形式地址；移除全部签名后独立探测源站。
	signed, err := s3.NewPresignClient(client).PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(config.Bucket), Key: aws.String(key)})
	if err != nil {
		return access, nil
	}
	origin, err := url.Parse(signed.URL)
	if err != nil {
		return access, nil
	}
	origin.RawQuery = ""
	probeCtx, probeCancel := context.WithTimeout(ctx, 3*time.Second)
	defer probeCancel()
	status, probeErr := checkAnonymousObject(probeCtx, origin.String(), payload)
	switch {
	case probeErr == nil:
		access = domain.StorageOriginAccessPublic
	case status == http.StatusForbidden || status == http.StatusUnauthorized:
		access = domain.StorageOriginAccessRestricted
	}
	return access, nil
}

// checkAnonymousObject 不携带 Cookie 或认证，校验实际响应内容，避免把登录页、错误页判为成功。
func checkAnonymousObject(ctx context.Context, address string, payload []byte) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return 0, err
	}
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return errors.New("invalid redirect scheme")
		}
		return nil
	}}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(len(payload)+1)))
	if err != nil {
		return response.StatusCode, err
	}
	if response.StatusCode != http.StatusOK || !bytes.Equal(data, payload) {
		return response.StatusCode, errors.New("anonymous object content unavailable")
	}
	return response.StatusCode, nil
}
