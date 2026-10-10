package wechat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// ErrMediaTooLarge 表示临时素材内容超过允许的字节上限。
var ErrMediaTooLarge = errors.New("wechat media too large")

// Media 定义下载的临时素材内容；微信随内容给出文件名与内容类型时对应字段有值。
type Media struct {
	Data        []byte
	FileName    string
	ContentType string
}

// DownloadMedia 用接口调用凭据下载临时素材，视频素材按微信返回的下载地址取回内容；内容超过 maxSize 字节时返回 ErrMediaTooLarge。
func (c *Client) DownloadMedia(ctx context.Context, accessToken, mediaID string, maxSize int64) (Media, error) {
	address := c.baseURL + "/cgi-bin/media/get?access_token=" + url.QueryEscape(accessToken) + "&media_id=" + url.QueryEscape(mediaID)
	media, err := c.fetchMedia(ctx, address, maxSize)
	if err != nil {
		return Media{}, err
	}
	if !jsonContent(media.ContentType) {
		return media, nil
	}
	// 业务错误与视频素材以 JSON 返回。
	var result struct {
		ErrCode  int    `json:"errcode"`
		ErrMsg   string `json:"errmsg"`
		VideoURL string `json:"video_url"`
	}
	if err := json.Unmarshal(media.Data, &result); err != nil {
		return Media{}, fmt.Errorf("%w: decode media response: %w", ErrUnavailable, err)
	}
	if result.ErrCode != 0 {
		return Media{}, &APIError{Code: result.ErrCode, Message: result.ErrMsg}
	}
	if result.VideoURL == "" {
		return Media{}, fmt.Errorf("%w: media content missing", ErrUnavailable)
	}
	return c.fetchMedia(ctx, result.VideoURL, maxSize)
}

// fetchMedia 读取 address 返回的内容，内容超过 maxSize 字节时返回 ErrMediaTooLarge。
func (c *Client) fetchMedia(ctx context.Context, address string, maxSize int64) (Media, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return Media{}, fmt.Errorf("create wechat media request: %w", err)
	}
	response, err := c.media.Do(request)
	if err != nil {
		return Media{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Media{}, fmt.Errorf("%w: http status %d", ErrUnavailable, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxSize+1))
	if err != nil {
		return Media{}, fmt.Errorf("%w: read media: %w", ErrUnavailable, err)
	}
	if int64(len(data)) > maxSize {
		return Media{}, ErrMediaTooLarge
	}
	media := Media{Data: data}
	if contentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type")); err == nil {
		media.ContentType = contentType
	}
	if _, params, err := mime.ParseMediaType(response.Header.Get("Content-Disposition")); err == nil {
		media.FileName = params["filename"]
	}
	return media, nil
}

// jsonContent 判断内容类型是否为微信接口返回 JSON 时使用的类型。
func jsonContent(contentType string) bool {
	return contentType == "application/json" || strings.HasPrefix(contentType, "text/")
}
