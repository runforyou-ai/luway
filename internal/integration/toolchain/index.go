package toolchain

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"golang.org/x/net/html"

	"github.com/runforyou-ai/cervi/internal/common/brand"
)

// indexLink 是简单索引项目页中的一个文件，sha256 取自链接片段，索引未给出时为空。
type indexLink struct {
	file   string
	url    string
	sha256 string
}

// indexLinks 读取 PyPI 简单索引（PEP 503）中项目页列出的全部文件，相对链接按项目页解析；索引不可用时返回下载失败。
func indexLinks(ctx context.Context, client *http.Client, index, project string) ([]indexLink, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, downloadStallTimeout, errDownloadStalled)
	defer cancel()
	page, err := url.Parse(index + "/" + project + "/")
	if err != nil {
		return nil, fmt.Errorf("parse package index: %w", err)
	}
	response, err := get(ctx, client, page.String(), "text/html")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	links := make([]indexLink, 0)
	tokens := html.NewTokenizer(response.Body)
	for {
		switch tokens.Next() {
		case html.ErrorToken:
			if err := tokens.Err(); !errors.Is(err, io.EOF) {
				return nil, &stepError{failure: FailureDownload, err: fmt.Errorf("read package index %s: %w", page, cmp.Or(context.Cause(ctx), err))}
			}
			return links, nil
		case html.StartTagToken:
			name, hasAttributes := tokens.TagName()
			if string(name) != "a" || !hasAttributes {
				continue
			}
			for {
				key, value, more := tokens.TagAttr()
				if string(key) == "href" {
					if link, err := page.Parse(string(value)); err == nil {
						digest, _ := strings.CutPrefix(link.Fragment, "sha256=")
						link.Fragment = ""
						links = append(links, indexLink{file: path.Base(link.Path), url: link.String(), sha256: digest})
					}
				}
				if !more {
					break
				}
			}
		}
	}
}

// get 以品牌标识作为 User-Agent 发起 GET 请求，连接失败或状态码不是 200 时返回下载失败。
func get(ctx context.Context, client *http.Client, target, accept string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Accept", accept)
	request.Header.Set("User-Agent", brand.Build().Slug)
	response, err := client.Do(request)
	if err != nil {
		return nil, &stepError{failure: FailureDownload, err: fmt.Errorf("read %s: %w", target, cmp.Or(context.Cause(ctx), err))}
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, &stepError{failure: FailureDownload, err: fmt.Errorf("read %s: HTTP %d", target, response.StatusCode)}
	}
	return response, nil
}
