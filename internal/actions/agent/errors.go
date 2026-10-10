//go:build server

package agent

import "errors"

var (
	// ErrNotFound 表示当前企业中不存在指定 AI 员工。
	ErrNotFound = errors.New("agent not found")
	// ErrQueryInvalid 表示 AI 员工目录查询条件无效。
	ErrQueryInvalid = errors.New("agent query invalid")
)
