//go:build server

package publicweb

import "embed"

// markdownAssets 是由前端 Task 构建的共享消息正文资源。
//
//go:embed dist/markdown.js dist/markdown.css
var markdownAssets embed.FS

// composerEmojisJSON 是访客 Messenger 的固定表情候选。
//
//go:embed composer-emojis.json
var composerEmojisJSON string

// widgetScript 是网站渠道嵌入脚本。
//
//go:embed widget.js
var widgetScript []byte

// messengerCSS 是访客 Messenger 页面样式。
//
//go:embed messenger.css
var messengerCSS string

// pageHTML 是访客聊天页模板。
//
//go:embed page.html
var pageHTML string

// previewHTML 是管理端挂件预览宿主页模板。
//
//go:embed preview.html
var previewHTML string

// chatJS 是访客聊天页交互脚本。
//
//go:embed chat.js
var chatJS string
