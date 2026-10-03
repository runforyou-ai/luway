//go:build server

package identity

import "github.com/runforyou-ai/luway/internal/domain"

// ActiveWorkspaceCondition 返回给定工作区编号列所属工作区处于正常状态的 SQL 条件，column 是带表别名的列名。
func ActiveWorkspaceCondition(column string) string {
	return "EXISTS (SELECT 1 FROM organizations AS active_o WHERE active_o.id = " + column +
		" AND active_o.lifecycle_status = '" + string(domain.OrganizationLifecycleActive) + "')"
}
