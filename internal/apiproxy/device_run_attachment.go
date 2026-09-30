//go:build !server

package apiproxy

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
)

// ReadDeviceRunAttachment 读取指定运行所属会话中附件消息的文件内容，超过随消息直传上限时返回错误；meta 必须携带设备编号。
func (b *Backend) ReadDeviceRunAttachment(ctx context.Context, meta appservice.RequestMeta, runID, messageID string) ([]byte, error) {
	path := "/agent-runs/" + url.PathEscape(runID) + "/attachments/" + url.PathEscape(messageID)
	response, err := b.send(ctx, meta, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, domain.AgentMediaMaxBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		slog.Warn("读取设备运行附件失败", "agent_run_id", runID, "message_id", messageID, "error", err)
		return nil, appservice.UnavailableError(meta, i18n.ErrorServerConnectionFailed, nil)
	}
	if len(content) > domain.AgentMediaMaxBytes {
		return nil, fmt.Errorf("device run attachment exceeds %d bytes", domain.AgentMediaMaxBytes)
	}
	return content, nil
}
