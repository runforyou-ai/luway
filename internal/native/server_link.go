//go:build !server

package native

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
	"sync"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// serverLinkHost 是连接链接的主机部分，完整链接为 `<品牌标识>://connect?server=<部署地址>`。
const serverLinkHost = "connect"

// ServerLinks 是原生端接收连接链接的能力；Open 交来唤起应用的链接，OnOpen 登记收到连接链接后把主窗口带到前台的处理。
type ServerLinks interface {
	// TakeOpenedServerLink 返回并清除最近一次唤起应用的连接链接携带的部署地址，没有时返回空串。
	TakeOpenedServerLink(context.Context, appservice.RequestMeta) (string, error)
	Open(string)
	OnOpen(func())
}

// openedServerLink 记录最近一次连接链接携带的部署地址，直到主界面取走；应用刚被链接唤起、界面尚未就绪时由界面启动后读取。
type openedServerLink struct {
	mu        sync.Mutex
	serverURL string
	onOpen    func()
}

// OnOpen 登记收到连接链接后的处理。
func (o *openedServerLink) OnOpen(handler func()) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.onOpen = handler
}

// Open 记录连接链接携带的部署地址并通知主界面；协议不是本应用品牌标识或不是连接链接时忽略。
func (o *openedServerLink) Open(rawURL string) {
	serverURL := parseServerLink(rawURL)
	if serverURL == "" {
		slog.WarnContext(context.Background(), "忽略无法识别的唤起链接")
		return
	}
	o.mu.Lock()
	o.serverURL = serverURL
	handler := o.onOpen
	o.mu.Unlock()
	slog.InfoContext(context.Background(), "收到连接链接", "server_url", serverURL)
	if handler != nil {
		handler()
	}
	if app := application.Get(); app != nil {
		app.Event.Emit(ServerLinkOpenedEventName)
	}
}

// TakeOpenedServerLink 返回并清除待处理的部署地址。
func (o *openedServerLink) TakeOpenedServerLink(context.Context, appservice.RequestMeta) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	serverURL := o.serverURL
	o.serverURL = ""
	return serverURL, nil
}

// parseServerLink 返回连接链接携带的部署地址，不是本应用的连接链接时返回空串；地址本身由连接页检测校验。
func parseServerLink(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != brand.Build().Slug || parsed.Host != serverLinkHost {
		return ""
	}
	return strings.TrimSpace(parsed.Query().Get("server"))
}
