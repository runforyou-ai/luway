//go:build server

// Package contact 实现外部联系人领域的应用操作。
package contact

import (
	"strings"
	"unicode/utf8"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/set"
	"github.com/runforyou-ai/support/str"
	"github.com/runforyou-ai/support/validate"
)

// ValidationCode 标识联系人字段校验结果。
type ValidationCode = common.FieldCode

const (
	ValidationIdentityRequired ValidationCode = "CONTACT_IDENTITY_REQUIRED"
	ValidationChannelInvalid   ValidationCode = "CONTACT_CHANNEL_INVALID"
	ValidationChannelImmutable ValidationCode = "CONTACT_CHANNEL_IMMUTABLE"
	ValidationMethodInvalid    ValidationCode = "CONTACT_METHOD_INVALID"
	ValidationMethodDuplicate  ValidationCode = "CONTACT_METHOD_DUPLICATE"
	ValidationPrimaryDuplicate ValidationCode = "CONTACT_PRIMARY_DUPLICATE"
)

const (
	// maxMethodLabelLength 是联系方式标签的最大字符数。
	maxMethodLabelLength = 100
)

// ValidationError 表示联系人字段校验失败。
type ValidationError = common.FieldError

// normalizeContactInput 规范化联系人写入字段，并校验联系方式的取值、重复与首选项。
func normalizeContactInput(input ContactInput) (ContactInput, map[string]ValidationCode) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.ChannelID, _ = str.NormalizeUUID(input.ChannelID)
	input.Stage = domain.ContactStage(strings.TrimSpace(string(input.Stage)))
	input.Notes = strings.TrimSpace(input.Notes)

	fields := make(map[string]ValidationCode)
	var methodCode ValidationCode
	input.Methods, methodCode = normalizeMethods(input.Methods)
	if methodCode != "" {
		fields["methods"] = methodCode
	}
	return input, fields
}

// normalizeNewContactInput 规范化手动新建的联系人输入；手动新建的联系人没有渠道身份，至少需要姓名或一项联系方式。
func normalizeNewContactInput(input ContactInput) (ContactInput, map[string]ValidationCode) {
	input, fields := normalizeContactInput(input)
	if input.DisplayName == "" && len(input.Methods) == 0 {
		fields["displayName"] = ValidationIdentityRequired
	}
	return input, fields
}

// normalizeMethods 规范化联系方式并返回优先级最高的错误码。
func normalizeMethods(methods []MethodInput) ([]MethodInput, ValidationCode) {
	type methodKey struct {
		typeName string
		value    string
	}
	var seen set.Set[methodKey]
	var primarySeen set.Set[string]
	firstByType := make(map[string]int)
	var code ValidationCode
	for index := range methods {
		method := &methods[index]
		method.Type = domain.ContactMethodType(strings.TrimSpace(string(method.Type)))
		method.Value = strings.TrimSpace(method.Value)
		method.Label = strings.TrimSpace(method.Label)
		if utf8.RuneCountInString(method.Label) > maxMethodLabelLength && code == "" {
			code = ValidationMethodInvalid
		}

		normalized, ok := normalizeMethodValue(method.Type, method.Value)
		if !ok {
			if code == "" {
				code = ValidationMethodInvalid
			}
			continue
		}
		method.Value = normalized
		key := methodKey{typeName: string(method.Type), value: normalized}
		if !seen.Add(key) && code != ValidationPrimaryDuplicate {
			code = ValidationMethodDuplicate
		}
		if _, exists := firstByType[string(method.Type)]; !exists {
			firstByType[string(method.Type)] = index
		}
		if method.IsPrimary && !primarySeen.Add(string(method.Type)) {
			code = ValidationPrimaryDuplicate
		}
	}
	for methodType, index := range firstByType {
		if !primarySeen.Has(methodType) {
			methods[index].IsPrimary = true
		}
	}
	return methods, code
}

// normalizeListInput 规范化联系人列表参数，补齐分页与排序默认值。
func normalizeListInput(input ListInput) ListInput {
	input.Query = strings.TrimSpace(input.Query)
	input.ChannelID, _ = str.NormalizeUUID(input.ChannelID)
	input.TagID, _ = str.NormalizeUUID(input.TagID)
	input.Page, input.PageSize, _ = common.NormalizePagination(input.Page, input.PageSize)
	if input.Sort == "" {
		input.Sort = domain.ContactSortCreatedAtDescending
	}
	return input
}

// normalizeMethodValue 规范化邮箱或国际电话号码。
func normalizeMethodValue(methodType domain.ContactMethodType, value string) (string, bool) {
	switch methodType {
	case domain.ContactMethodTypeEmail:
		normalized := strings.ToLower(strings.TrimSpace(value))
		return normalized, str.IsEmail(normalized)
	case domain.ContactMethodTypePhone:
		return validate.E164(value)
	default:
		return "", false
	}
}
