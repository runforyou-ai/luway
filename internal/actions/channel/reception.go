//go:build server

package channel

import identityaction "github.com/runforyou-ai/luway/internal/actions/identity"

// AcceptsCustomersCondition 返回渠道接待客户的 SQL 条件：渠道已启用且所属工作区处于正常状态，alias 是渠道表别名。
func AcceptsCustomersCondition(alias string) string {
	return alias + ".enabled AND " + identityaction.ActiveWorkspaceCondition(alias+".workspace_id")
}
