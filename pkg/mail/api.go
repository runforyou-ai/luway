//go:build server

package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	netmail "net/mail"
	"net/url"
)

// sendAPI 向服务商固定接口提交邮件并检查接受结果。
func (c *Client) sendAPI(ctx context.Context, message Message) error {
	from := (&netmail.Address{Name: message.FromName, Address: c.config.FromAddress}).String()
	var body bytes.Buffer
	endpoint, contentType := "", "application/json"
	switch c.config.Provider {
	case "resend":
		if c.config.Resend == nil {
			return ErrConfiguration
		}
		endpoint = "https://api.resend.com/emails"
		_ = json.NewEncoder(&body).Encode(map[string]any{"from": from, "to": []string{message.To}, "subject": message.Subject, "text": message.Text, "html": message.HTML})
	case "postmark":
		if c.config.Postmark == nil {
			return ErrConfiguration
		}
		endpoint = "https://api.postmarkapp.com/email"
		payload := map[string]any{"From": from, "To": message.To, "Subject": message.Subject, "TextBody": message.Text, "HtmlBody": message.HTML}
		if c.config.Postmark.MessageStream != "" {
			payload["MessageStream"] = c.config.Postmark.MessageStream
		}
		_ = json.NewEncoder(&body).Encode(payload)
	case "mailgun":
		if c.config.Mailgun == nil {
			return ErrConfiguration
		}
		host := "api.mailgun.net"
		if c.config.Mailgun.Region == "eu" {
			host = "api.eu.mailgun.net"
		}
		endpoint = "https://" + host + "/v3/" + url.PathEscape(c.config.Mailgun.Domain) + "/messages"
		writer := multipart.NewWriter(&body)
		for key, value := range map[string]string{"from": from, "to": message.To, "subject": message.Subject, "text": message.Text, "html": message.HTML} {
			if err := writer.WriteField(key, value); err != nil {
				return ErrMessage
			}
		}
		if err := writer.Close(); err != nil {
			return ErrMessage
		}
		contentType = writer.FormDataContentType()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return ErrConfiguration
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Accept", "application/json")
	switch c.config.Provider {
	case "resend":
		request.Header.Set("Authorization", "Bearer "+c.config.Resend.APIKey)
	case "postmark":
		request.Header.Set("X-Postmark-Server-Token", c.config.Postmark.ServerToken)
	case "mailgun":
		request.SetBasicAuth("api", c.config.Mailgun.APIKey)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return safeNetworkError(ctx, err)
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == 401 || response.StatusCode == 403:
		return ErrAuthentication
	case response.StatusCode == 429:
		return ErrRateLimited
	case response.StatusCode >= 500:
		return ErrUnavailable
	case response.StatusCode < 200 || response.StatusCode >= 300:
		return ErrRejected
	}
	var result struct {
		ID        string `json:"id"`
		MessageID string `json:"MessageID"`
		ErrorCode int    `json:"ErrorCode"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&result); err != nil {
		return safeNetworkError(ctx, err)
	}
	if c.config.Provider == "postmark" {
		if result.ErrorCode != 0 || result.MessageID == "" {
			return ErrRejected
		}
	} else if result.ID == "" {
		return ErrRejected
	}
	return nil
}
