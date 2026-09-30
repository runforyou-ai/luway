//go:build server

package mcpserver

import "errors"

var (
	// ErrNotFound 表示当前企业中不存在指定 MCP 服务。
	ErrNotFound = errors.New("MCP server not found")
	// ErrToolNotFound 表示 MCP 服务的当前工具目录中不存在指定工具。
	ErrToolNotFound = errors.New("MCP tool not found")
)
