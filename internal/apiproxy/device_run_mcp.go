//go:build !server

package apiproxy

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
)

// maxMCPToolResultBytes 是企业 MCP 工具调用响应的最大字节数，大结果交给运行时转存。
const maxMCPToolResultBytes = 16 << 20

// CallDeviceRunMCPTool 为本设备持有的运行调用企业 MCP 工具，调用期限由 ctx 控制；meta 必须携带设备编号。
func (b *Backend) CallDeviceRunMCPTool(ctx context.Context, meta appservice.RequestMeta, runID string, input appservice.DeviceRunMCPToolCallInput) (appservice.DeviceRunMCPToolCallResult, error) {
	var output appservice.DeviceRunMCPToolCallResult
	path := "/agent-runs/" + url.PathEscape(runID) + "/mcp/tools/call"
	response, err := b.sendVia(ctx, meta, true, http.MethodPost, path, nil, input)
	if err != nil {
		return output, err
	}
	defer response.Body.Close()
	if err := json.NewDecoder(io.LimitReader(response.Body, maxMCPToolResultBytes)).Decode(&output); err != nil {
		if ctx.Err() != nil {
			return output, ctx.Err()
		}
		slog.Warn("解析企业 MCP 工具调用结果失败", "agent_run_id", runID, "error", err)
		return output, appservice.UnavailableError(meta, i18n.ErrorServerConnectionFailed, nil)
	}
	return output, nil
}
