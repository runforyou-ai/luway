//go:build server

package team

import (
	"strings"
	"unicode/utf8"

	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
)

const (
	ValidationNameRequired       common.FieldCode = "TEAM_NAME_REQUIRED"
	ValidationNameTooLong        common.FieldCode = "TEAM_NAME_TOO_LONG"
	ValidationNameDuplicate      common.FieldCode = "TEAM_NAME_DUPLICATE"
	ValidationDescriptionTooLong common.FieldCode = "TEAM_DESCRIPTION_TOO_LONG"
	ValidationQueryInvalid       common.FieldCode = "TEAM_QUERY_INVALID"
	ValidationWorkStatusInvalid  common.FieldCode = "WORK_STATUS_INVALID"
)

// normalizeInput 规范化并校验团队字段。
func normalizeInput(input Input) (Input, map[string]common.FieldCode) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	fields := make(map[string]common.FieldCode)
	if input.Name == "" {
		fields["name"] = ValidationNameRequired
	} else if utf8.RuneCountInString(input.Name) > domain.TeamNameMaxLength {
		fields["name"] = ValidationNameTooLong
	}
	if utf8.RuneCountInString(input.Description) > domain.TeamDescriptionMaxLength {
		fields["description"] = ValidationDescriptionTooLong
	}
	return input, fields
}
