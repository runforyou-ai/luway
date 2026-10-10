//go:build server

package reportpage

import (
	"fmt"
	"strings"
)

// AgentScope 定义按 AI 员工筛选的范围：AgentID 指定单个 AI 员工，ResponsibleUserID 限定为该成员负责的 AI 员工，都为空表示不筛选。
type AgentScope struct {
	AgentID           string
	ResponsibleUserID string
}

// Condition 返回限定 column 中 AI 员工企业身份编号属于该范围的 SQL 条件与参数，不筛选时返回空条件。
func (s AgentScope) Condition(column, workspaceID string) (string, []any) {
	if s.AgentID == "" && s.ResponsibleUserID == "" {
		return "", nil
	}
	conditions, args := []string{"a.workspace_id = ?"}, []any{workspaceID}
	if s.AgentID != "" {
		conditions, args = append(conditions, "a.id = ?"), append(args, s.AgentID)
	}
	if s.ResponsibleUserID != "" {
		conditions, args = append(conditions, "a.responsible_user_id = ?"), append(args, s.ResponsibleUserID)
	}
	return fmt.Sprintf("%s IN (SELECT a.identity_id FROM agents AS a WHERE %s)", column, strings.Join(conditions, " AND ")), args
}
