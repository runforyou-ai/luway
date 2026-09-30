// Package telegram 提供 Telegram Bot API 的最小安全适配器。
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/runforyou-ai/luway/pkg/connectiontest"
)

const (
	defaultBaseURL  = "https://api.telegram.org"
	maxResponseSize = 1 << 20
	maxAvatarSize   = 5 << 20
)

var botTokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)

// Bot 描述 getMe 返回的机器人身份。
type Bot struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

// ProfilePhoto 定义 Telegram 用户当前头像的可下载引用和唯一标识。
type ProfilePhoto struct {
	FileID   string
	UniqueID string
}

// DownloadedPhoto 返回经过内容校验的头像字节。
type DownloadedPhoto struct {
	ContentType string
	Data        []byte
}

// ProfilePhotoAPI 定义用户头像同步和读取依赖的 Telegram 能力。
type ProfilePhotoAPI interface {
	GetUserProfilePhoto(context.Context, string, int64) (*ProfilePhoto, error)
	DownloadPhoto(context.Context, string, string) (DownloadedPhoto, error)
}

// MediaDownloader 定义客户入站媒体取回依赖的 Telegram 能力。
type MediaDownloader interface {
	DownloadMedia(context.Context, string, string, int64) ([]byte, error)
}

// Webhook 定义 setWebhook 所需字段。
type Webhook struct {
	URL    string
	Secret string
}

// BotAPI 定义渠道操作依赖的 Telegram API 能力。
type BotAPI interface {
	GetMe(context.Context, string) (Bot, error)
	SetWebhook(context.Context, string, Webhook) error
	DeleteWebhook(context.Context, string) error
}

// Option 配置 Telegram 客户端。
type Option func(*Client)

// WithBaseURL 覆盖 Bot API 基础地址，供受控环境和测试使用。
func WithBaseURL(baseURL string) Option {
	return func(client *Client) {
		client.baseURL = strings.TrimRight(baseURL, "/")
	}
}

// Client 调用 Telegram Bot API 并将传输错误转换为业务错误。
type Client struct {
	httpClient     connectiontest.HTTPDoer
	downloadClient connectiontest.HTTPDoer
	baseURL        string
}

// NewClient 创建 Telegram Bot API 客户端。
func NewClient(httpClient connectiontest.HTTPDoer, options ...Option) *Client {
	if httpClient == nil {
		httpClient = connectiontest.NewHTTPClient()
	}
	downloadClient := httpClient
	if standardClient, ok := httpClient.(*http.Client); ok {
		clone := *standardClient
		clone.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}
		downloadClient = &clone
	}
	client := &Client{httpClient: httpClient, downloadClient: downloadClient, baseURL: defaultBaseURL}
	for _, option := range options {
		option(client)
	}
	return client
}

// GetMe 校验 Token 并返回机器人身份。
func (c *Client) GetMe(ctx context.Context, token string) (Bot, error) {
	bot := Bot{}
	if err := c.call(ctx, token, "getMe", struct{}{}, &bot); err != nil {
		return Bot{}, err
	}
	if bot.ID <= 0 || !bot.IsBot || strings.TrimSpace(bot.FirstName) == "" {
		return Bot{}, protocolError()
	}
	return bot, nil
}

// GetUserProfilePhoto 返回用户最新头像的最大静态尺寸，无头像时返回 nil。
func (c *Client) GetUserProfilePhoto(ctx context.Context, token string, userID int64) (*ProfilePhoto, error) {
	if userID <= 0 {
		return nil, protocolError()
	}
	result := struct {
		Photos [][]struct {
			FileID       string `json:"file_id"`
			FileUniqueID string `json:"file_unique_id"`
			Width        int64  `json:"width"`
			Height       int64  `json:"height"`
		} `json:"photos"`
	}{}
	if err := c.call(ctx, token, "getUserProfilePhotos", struct {
		UserID int64 `json:"user_id"`
		Offset int   `json:"offset"`
		Limit  int   `json:"limit"`
	}{UserID: userID, Limit: 1}, &result); err != nil {
		return nil, err
	}
	if len(result.Photos) == 0 {
		return nil, nil
	}
	var selected *ProfilePhoto
	var selectedArea int64
	for _, size := range result.Photos[0] {
		fileID := strings.TrimSpace(size.FileID)
		uniqueID := strings.TrimSpace(size.FileUniqueID)
		if fileID == "" || uniqueID == "" || size.Width <= 0 || size.Height <= 0 || size.Width > 10000 || size.Height > 10000 {
			continue
		}
		area := size.Width * size.Height
		if selected == nil || area > selectedArea {
			selected = &ProfilePhoto{FileID: fileID, UniqueID: uniqueID}
			selectedArea = area
		}
	}
	if selected == nil {
		return nil, protocolError()
	}
	return selected, nil
}

// DownloadPhoto 通过 getFile 下载并校验 Telegram 头像内容。
func (c *Client) DownloadPhoto(ctx context.Context, token, fileID string) (DownloadedPhoto, error) {
	data, err := c.downloadFile(ctx, token, fileID, maxAvatarSize)
	if err != nil {
		return DownloadedPhoto{}, err
	}
	// 按文件魔数识别允许的静态头像格式。
	contentType := ""
	switch {
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		contentType = "image/jpeg"
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}):
		contentType = "image/png"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		contentType = "image/webp"
	}
	if contentType == "" {
		return DownloadedPhoto{}, protocolError()
	}
	return DownloadedPhoto{ContentType: contentType, Data: data}, nil
}

// DownloadMedia 通过 getFile 取回客户发送的媒体内容，超过 maxSize 字节返回协议错误。
func (c *Client) DownloadMedia(ctx context.Context, token, fileID string, maxSize int64) ([]byte, error) {
	return c.downloadFile(ctx, token, fileID, maxSize)
}

// downloadFile 每次调用重新执行 getFile 取得下载路径，再按大小上限读取文件内容。
func (c *Client) downloadFile(ctx context.Context, token, fileID string, maxSize int64) ([]byte, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" || len(fileID) > 512 || maxSize <= 0 {
		return nil, protocolError()
	}
	file := struct {
		FileSize *int64 `json:"file_size"`
		FilePath string `json:"file_path"`
	}{}
	if err := c.call(ctx, token, "getFile", struct {
		FileID string `json:"file_id"`
	}{FileID: fileID}, &file); err != nil {
		return nil, err
	}
	if file.FileSize != nil && (*file.FileSize <= 0 || *file.FileSize > maxSize) {
		return nil, protocolError()
	}
	endpoint, err := c.fileURL(token, file.FilePath)
	if err != nil {
		return nil, protocolError()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, protocolError()
	}
	response, err := c.downloadClient.Do(request)
	if err != nil {
		return nil, safeTransportError(err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, connectiontest.HTTPStatusError(response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxSize+1))
	if err != nil {
		return nil, safeTransportError(err)
	}
	if len(data) == 0 || int64(len(data)) > maxSize {
		return nil, protocolError()
	}
	return data, nil
}

// SetWebhook 注册当前渠道的 Webhook。
func (c *Client) SetWebhook(ctx context.Context, token string, webhook Webhook) error {
	var accepted bool
	err := c.call(ctx, token, "setWebhook", struct {
		URL                string   `json:"url"`
		SecretToken        string   `json:"secret_token"`
		AllowedUpdates     []string `json:"allowed_updates"`
		DropPendingUpdates bool     `json:"drop_pending_updates"`
	}{
		URL: webhook.URL, SecretToken: webhook.Secret,
		AllowedUpdates: []string{"message", "my_chat_member"}, DropPendingUpdates: true,
	}, &accepted)
	if err != nil {
		return err
	}
	if !accepted {
		return protocolError()
	}
	return nil
}

// DeleteWebhook 删除机器人当前 Webhook 并丢弃积压 Update。
func (c *Client) DeleteWebhook(ctx context.Context, token string) error {
	var deleted bool
	err := c.call(ctx, token, "deleteWebhook", struct {
		DropPendingUpdates bool `json:"drop_pending_updates"`
	}{DropPendingUpdates: true}, &deleted)
	if err != nil {
		return err
	}
	if !deleted {
		return protocolError()
	}
	return nil
}

// call 调用单个 Bot API 方法并校验 Telegram 响应信封。
func (c *Client) call(ctx context.Context, token, method string, input, output any) error {
	endpoint, err := c.methodURL(token, method)
	if err != nil {
		return connectiontest.InvalidConfigError(errors.New("invalid Telegram bot token"))
	}
	body, err := json.Marshal(input)
	if err != nil {
		return protocolError()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return connectiontest.InvalidConfigError(errors.New("invalid Telegram request"))
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return safeTransportError(err)
	}
	defer response.Body.Close()

	envelope := struct {
		OK        bool            `json:"ok"`
		Result    json.RawMessage `json:"result"`
		ErrorCode int             `json:"error_code"`
	}{}
	decodeErr := json.NewDecoder(io.LimitReader(response.Body, maxResponseSize)).Decode(&envelope)
	succeeded := response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
	if decodeErr != nil && succeeded {
		return protocolError()
	}
	// 平台失败按信封中的 error_code 分类，信封缺少错误码或无法解析时按 HTTP 状态码分类。
	if decodeErr != nil || !envelope.OK || !succeeded {
		code := response.StatusCode
		if decodeErr == nil && envelope.ErrorCode != 0 {
			code = envelope.ErrorCode
		}
		return connectiontest.HTTPStatusError(code)
	}
	if len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return protocolError()
	}
	if err := json.Unmarshal(envelope.Result, output); err != nil {
		return protocolError()
	}
	return nil
}

// methodURL 构造包含转义 Token 的 Bot API 方法地址。
func (c *Client) methodURL(token, method string) (string, error) {
	token = strings.TrimSpace(token)
	if !botTokenPattern.MatchString(token) {
		return "", errors.New("invalid token")
	}
	base, err := url.Parse(c.baseURL)
	if err != nil || !base.IsAbs() || base.Host == "" {
		return "", errors.New("invalid base URL")
	}
	base.RawPath = ""
	base.Path = strings.TrimRight(base.Path, "/") + "/bot" + token + "/" + method
	return base.String(), nil
}

// fileURL 构造受限于 Telegram 基础地址的文件下载地址。
func (c *Client) fileURL(token, filePath string) (string, error) {
	token = strings.TrimSpace(token)
	filePath = strings.TrimSpace(filePath)
	if !botTokenPattern.MatchString(token) || filePath == "" || strings.HasPrefix(filePath, "/") || strings.Contains(filePath, "\\") {
		return "", errors.New("invalid Telegram file path")
	}
	reference, err := url.Parse(filePath)
	if err != nil || reference.IsAbs() || reference.Host != "" || reference.RawQuery != "" || reference.Fragment != "" {
		return "", errors.New("invalid Telegram file path")
	}
	cleanedPath := path.Clean(reference.Path)
	if cleanedPath != reference.Path || cleanedPath == "." || cleanedPath == ".." || strings.HasPrefix(cleanedPath, "../") {
		return "", errors.New("invalid Telegram file path")
	}
	base, err := url.Parse(c.baseURL)
	if err != nil || !base.IsAbs() || base.Host == "" {
		return "", errors.New("invalid base URL")
	}
	base.RawPath = ""
	base.RawQuery = ""
	base.Fragment = ""
	base.Path = strings.TrimRight(base.Path, "/") + "/file/bot" + token + "/" + reference.Path
	return base.String(), nil
}

// safeTransportError 保留错误分类但移除可能包含 Token URL 的原始错误。
func safeTransportError(err error) error {
	classified := connectiontest.ClassifyTransportError(connectiontest.StageConnect, err)
	stage, kind, ok := connectiontest.Details(classified)
	if !ok {
		return connectiontest.NewError(connectiontest.StageConnect, connectiontest.FailureUnavailable, nil)
	}
	return connectiontest.NewError(stage, kind, nil)
}

// protocolError 返回不包含 Telegram 原始响应的协议错误。
func protocolError() error {
	return connectiontest.NewError(connectiontest.StageCapability, connectiontest.FailureProtocol, nil)
}
