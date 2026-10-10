//go:build server

package agent

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/runforyou-ai/luway/internal/actions/localagent"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// NormalizeLocalAgents 去掉本机 Agent 名称两端空白、空名称与重复名称，按名称排序。
func NormalizeLocalAgents(names []string) []string {
	normalized := make([]string, 0, len(names))
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" && !slices.Contains(normalized, name) {
			normalized = append(normalized, name)
		}
	}
	slices.Sort(normalized)
	return normalized
}

// localAgentsJSON 把启用的本机 Agent 名称编码为 jsonb 写入值。
func localAgentsJSON(names []string) string {
	encoded, _ := json.Marshal(names)
	return string(encoded)
}

// releaseStaleLocalAgentSessions 释放 AI 员工已不在所用电脑上或已停用的本机 Agent 的会话，computerID 为空表示不再使用电脑。
func releaseStaleLocalAgentSessions(ctx context.Context, tx bun.Tx, workspaceID, agentID string, computerID *string, localAgents []string) error {
	return localagent.ReleaseWhere(ctx, tx, workspaceID,
		"las.agent_id = ? AND (las.computer_id IS DISTINCT FROM ? OR NOT (las.local_agent = ANY(?)))", agentID, computerID, pgdialect.Array(localAgents))
}
