//go:build server

package businesssystem

import "errors"

var (
	// ErrNotFound 表示当前工作区中不存在指定业务系统。
	ErrNotFound = errors.New("business system not found")
	// ErrToolNotFound 表示业务系统的当前工具目录中不存在指定工具。
	ErrToolNotFound = errors.New("business tool not found")
	// ErrConnectTimeout 表示建立业务系统 MCP 会话超过连接时限。
	ErrConnectTimeout = errors.New("business system connection timed out")
)
