//go:build server

package messenger

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/runforyou-ai/luway/internal/web/static"
)

// markdownAssetsByName 与 markdownAssetVersion 在启动时由内嵌资源生成，聊天页按版本引用资源地址。
var markdownAssetsByName, markdownAssetVersion = loadMarkdownAssets()

// loadMarkdownAssets 读取内嵌正文资源并计算整体内容版本。
func loadMarkdownAssets() (map[string]static.Asset, string) {
	files := []struct{ name, contentType string }{
		{"markdown.js", "text/javascript; charset=utf-8"},
		{"markdown.css", "text/css; charset=utf-8"},
		{"upload.js", "text/javascript; charset=utf-8"},
		{"realtime.js", "text/javascript; charset=utf-8"},
	}
	assets := make(map[string]static.Asset, len(files))
	version := sha256.New()
	for _, file := range files {
		raw, err := markdownAssets.ReadFile("dist/" + file.name)
		if err != nil {
			panic("读取内嵌正文资源失败: " + err.Error())
		}
		asset := static.New(file.contentType, raw)
		digest := asset.Digest()
		version.Write(digest[:])
		assets[file.name] = asset
	}
	return assets, hex.EncodeToString(version.Sum(nil)[:6])
}
