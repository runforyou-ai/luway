//go:build server

package mail

import (
	"context"
	"errors"
	"net"
	netmail "net/mail"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/aws/smithy-go"
)

// sendSES 使用显式区域和静态凭据调用 SES v2，单次调用只尝试一次。
func (c *Client) sendSES(ctx context.Context, message Message) error {
	config := c.config.SES
	client := sesv2.New(sesv2.Options{
		Region: config.Region, Credentials: credentials.NewStaticCredentialsProvider(config.AccessKeyID, config.SecretAccessKey, ""),
		HTTPClient: c.http, Retryer: aws.NopRetryer{},
	})
	content := func(value string) *types.Content {
		return &types.Content{Data: aws.String(value), Charset: aws.String("UTF-8")}
	}
	output, err := client.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: aws.String((&netmail.Address{Name: message.FromName, Address: c.config.FromAddress}).String()),
		Destination:      &types.Destination{ToAddresses: []string{message.To}},
		Content:          &types.EmailContent{Simple: &types.Message{Subject: content(message.Subject), Body: &types.Body{Text: content(message.Text), Html: content(message.HTML)}}},
	})
	if err == nil {
		if output == nil || aws.ToString(output.MessageId) == "" {
			return ErrRejected
		}
		return nil
	}
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		switch apiError.ErrorCode() {
		case "AccessDeniedException", "InvalidClientTokenId", "SignatureDoesNotMatch", "UnrecognizedClientException", "InvalidSignatureException":
			return ErrAuthentication
		case "TooManyRequestsException", "Throttling", "ThrottlingException", "LimitExceededException":
			return ErrRateLimited
		case "MessageRejected", "BadRequestException", "MailFromDomainNotVerifiedException", "SendingPausedException", "AccountSuspendedException", "NotFoundException":
			return ErrRejected
		}
	}
	return safeNetworkError(ctx, err)
}

// safeNetworkError 将请求错误归一为可写入任务日志的类别。
func safeNetworkError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return context.DeadlineExceeded
	}
	return ErrUnavailable
}
