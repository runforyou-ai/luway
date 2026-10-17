//go:build server

package filecontent

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// CreateMultipart 创建对象存储分片上传会话。
func CreateMultipart(ctx context.Context, config S3Config, key, contentType string) (string, error) {
	output, err := newS3Client(config).CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(config.Bucket), Key: aws.String(key), ContentType: aws.String(contentType), CacheControl: aws.String(ImmutableCacheControl),
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

// PresignMultipartControl 为列举、完成和中止分片签名；对象键和会话已由业务层验证。
func PresignMultipartControl(ctx context.Context, config S3Config, key, uploadID, method string) (SignedRequest, error) {
	endpoint, err := s3.NewDefaultEndpointResolverV2().ResolveEndpoint(ctx, s3.EndpointParameters{
		Region: aws.String(config.Region), Bucket: aws.String(config.Bucket), Endpoint: aws.String(config.Endpoint), ForcePathStyle: aws.Bool(config.ForcePathStyle),
	})
	if err != nil {
		return SignedRequest{}, err
	}
	address := endpoint.URI
	address.Path = strings.TrimRight(address.Path, "/") + "/" + key
	query := address.Query()
	query.Set("uploadId", uploadID)
	query.Set("X-Amz-Expires", strconv.FormatInt(int64(signedRequestLifetime/time.Second), 10))
	address.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, method, address.String(), nil)
	if err != nil {
		return SignedRequest{}, err
	}
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/xml")
	}
	//clock:local 外部 S3 SigV4 签名使用当前机器时钟。
	signed, headers, err := v4.NewSigner().PresignHTTP(ctx, aws.Credentials{AccessKeyID: config.AccessKeyID, SecretAccessKey: config.SecretAccessKey}, request, "UNSIGNED-PAYLOAD", "s3", config.Region, time.Now(), func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true })
	if err != nil {
		return SignedRequest{}, err
	}
	result := SignedRequest{Method: method, URL: signed, Headers: map[string]string{}}
	for name, values := range headers {
		if !strings.EqualFold(name, "Host") && len(values) > 0 {
			result.Headers[name] = values[0]
		}
	}
	return result, nil
}
