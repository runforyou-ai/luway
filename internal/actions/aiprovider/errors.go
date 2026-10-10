//go:build server

package aiprovider

import "errors"

var (
	// ErrNotFound 表示当前企业中不存在指定模型服务供应商。
	ErrNotFound = errors.New("AI provider not found")
	// ErrInUse 表示模型服务供应商仍被 AI 员工、知识库或客服设置引用。
	ErrInUse = errors.New("AI provider is in use")
)
