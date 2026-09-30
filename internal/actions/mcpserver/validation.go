//go:build server

package mcpserver

import (
	"strings"
	"unicode/utf8"

	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
)

// ValidationCode 标识 MCP 服务字段校验结果。
type ValidationCode = common.FieldCode

const (
	ValidationServerTypeInvalid  ValidationCode = "MCP_SERVER_TYPE_INVALID"
	ValidationNameRequired       ValidationCode = "MCP_SERVER_NAME_REQUIRED"
	ValidationNameTooLong        ValidationCode = "MCP_SERVER_NAME_TOO_LONG"
	ValidationNameDuplicate      ValidationCode = "MCP_SERVER_NAME_DUPLICATE"
	ValidationURLRequired        ValidationCode = "MCP_SERVER_URL_REQUIRED"
	ValidationURLInvalid         ValidationCode = "MCP_SERVER_URL_INVALID"
	ValidationURLTooLong         ValidationCode = "MCP_SERVER_URL_TOO_LONG"
	ValidationToolPurposeInvalid ValidationCode = "MCP_TOOL_PURPOSE_INVALID"
)

// ValidationError 表示 MCP 服务字段校验失败。
type ValidationError = common.FieldError

const (
	// maxNameLength 是 MCP 服务名称的最大字符数。
	maxNameLength = 100
	// maxURLBytes 是 MCP 服务地址的最大字节数。
	maxURLBytes = 2048
)

// normalizeInput 归一化并校验 MCP 服务输入。
func normalizeInput(input Input) (Input, map[string]ValidationCode) {
	input.Name = strings.TrimSpace(input.Name)
	input.URL = strings.TrimSpace(input.URL)
	connection, fields := normalizeConnectionInput(ConnectionInput{URL: input.URL, ServerType: input.ServerType, AuthorizationToken: input.AuthorizationToken})
	input.URL = connection.URL
	if input.Name == "" {
		fields["name"] = ValidationNameRequired
	} else if utf8.RuneCountInString(input.Name) > maxNameLength {
		fields["name"] = ValidationNameTooLong
	}
	return input, fields
}

// normalizeConnectionInput 校验地址与传输类型。
func normalizeConnectionInput(input ConnectionInput) (ConnectionInput, map[string]ValidationCode) {
	input.URL = strings.TrimSpace(input.URL)
	fields := make(map[string]ValidationCode)
	if input.URL == "" {
		fields["url"] = ValidationURLRequired
	} else if len(input.URL) > maxURLBytes {
		fields["url"] = ValidationURLTooLong
	} else if !common.ValidHTTPURL(input.URL) {
		fields["url"] = ValidationURLInvalid
	}
	if input.ServerType != domain.MCPServerTypeSSE && input.ServerType != domain.MCPServerTypeStreamableHTTP {
		fields["serverType"] = ValidationServerTypeInvalid
	}
	return input, fields
}
