//go:build server

package team

import (
	"strings"

	"github.com/runforyou-ai/luway/internal/common"
)

// 团队与团队成员查询的字段校验码。
const (
	ValidationNameDuplicate common.FieldCode = "TEAM_NAME_DUPLICATE"
	ValidationQueryInvalid  common.FieldCode = "TEAM_QUERY_INVALID"
)

// normalizeInput 去除团队字段的首尾空白。
func normalizeInput(input Input) Input {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	return input
}
