//go:build server

package documentconvert

import (
	"bytes"
	"context"
	"mime"
	"strings"

	"github.com/runforyou-ai/luway/pkg/htmlmarkdown"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
)

// convertHTML 按 BOM、文档中首个 meta 编码声明依次确定编码，两者都没有时按文本规则解码，再转换为 Markdown。
func convertHTML(ctx context.Context, data []byte) (string, error) {
	// 请求内容类型不带编码，DetermineEncoding 只在检测到 BOM 时返回确定结果。
	detected, _, bom := charset.DetermineEncoding(data, "text/html")
	if !bom {
		detected = declaredEncoding(data)
	}
	if detected == nil {
		return htmlmarkdown.Convert(ctx, decodeText(data))
	}
	decoded, err := detected.NewDecoder().Bytes(data)
	if err != nil {
		return "", err
	}
	return htmlmarkdown.Convert(ctx, string(decoded))
}

// declaredEncoding 返回 body 之前首个 meta 标签声明的编码，未声明或名称无法识别时返回 nil。
func declaredEncoding(data []byte) encoding.Encoding {
	tokenizer := html.NewTokenizer(bytes.NewReader(data))
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return nil
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			if token.Data == "body" {
				return nil
			}
			if token.Data != "meta" {
				continue
			}
			name, content, contentType := "", "", false
			for _, attr := range token.Attr {
				switch attr.Key {
				case "charset":
					name = attr.Val
				case "content":
					content = attr.Val
				case "http-equiv":
					contentType = strings.EqualFold(attr.Val, "content-type")
				}
			}
			// http-equiv 形式的声明从 content 的媒体类型参数中读取编码。
			if _, params, err := mime.ParseMediaType(content); name == "" && contentType && err == nil {
				name = params["charset"]
			}
			if name != "" {
				declared, _ := charset.Lookup(name)
				return declared
			}
		}
	}
}
