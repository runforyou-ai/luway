//go:build server

// 分发层的声明式请求校验：路径参数按路由规则、输入结构体按 validate 标签校验。

package dispatch

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/go-playground/validator/v10/non-standard/validators"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
)

// requestValidator 以 json 字段名报告校验失败的字段，notblank 要求去掉首尾空白后非空。
var requestValidator = func() *validator.Validate {
	validate := validator.New(validator.WithRequiredStructEnabled())
	if err := validate.RegisterValidation("notblank", validators.NotBlank); err != nil {
		panic(err)
	}
	validate.RegisterTagNameFunc(func(field reflect.StructField) string {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" {
			return ""
		}
		return name
	})
	return validate
}()

// ruleMessages 是各校验规则的通用字段文案，字段的 msg 标签优先。
var ruleMessages = map[string]i18n.Key{
	"required": i18n.FieldRequired,
	"notblank": i18n.FieldRequired,
	"uuid":     i18n.FieldIDInvalid,
}

// PathRule 是路由为一个路径参数声明的校验规则：Value 是参数值，Rule 是 validator 规则。
type PathRule struct {
	Value string
	Rule  string
}

// ValidateRequest 校验路径参数与输入结构体：路径参数不符合规则时按资源不存在返回，输入结构体存在无效字段时返回带字段文案的输入无效错误；input 为 nil 时只校验路径参数。
func ValidateRequest(meta appservice.RequestMeta, input any, rules ...PathRule) error {
	for _, rule := range rules {
		if requestValidator.Var(rule.Value, rule.Rule) != nil {
			return appservice.NotFoundError(meta, i18n.ErrorNotFound)
		}
	}
	fields := map[string]i18n.Key{}
	if input != nil {
		var invalid validator.ValidationErrors
		if err := requestValidator.Struct(input); errors.As(err, &invalid) {
			for _, fieldError := range invalid {
				path, tag := resolveField(reflect.ValueOf(input), fieldError.StructNamespace())
				fields[path] = fieldMessage(tag, fieldError.Tag(), fieldError.Kind())
			}
		} else if err != nil {
			return err
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return appservice.InvalidError(meta, i18n.ErrorValidationFailed, fields)
}

// fieldMessage 返回字段文案键：msg 标签为单个键时用于全部规则，为「规则=键」列表时按失败规则取键，未声明的规则使用通用文案，字符串的 max 为内容过长。
func fieldMessage(tag, rule string, kind reflect.Kind) i18n.Key {
	if tag != "" && !strings.Contains(tag, "=") {
		return i18n.Key(strings.TrimSpace(tag))
	}
	for _, pair := range strings.Split(tag, ",") {
		if name, key, found := strings.Cut(pair, "="); found && strings.TrimSpace(name) == rule {
			return i18n.Key(strings.TrimSpace(key))
		}
	}
	if rule == "max" && kind == reflect.String {
		return i18n.FieldTooLong
	}
	if key, ok := ruleMessages[rule]; ok {
		return key
	}
	return i18n.FieldInvalid
}

// resolveField 沿输入值逐段解析 validator 的 Go 字段路径，返回以 json 字段名连接的错误路径与失败字段的 msg 标签；无法解析时返回去掉根结构体名的原始路径。
func resolveField(root reflect.Value, namespace string) (string, string) {
	_, rest, _ := strings.Cut(namespace, ".")
	if segments, tag, ok := resolveFrom(root, rest); ok {
		return strings.Join(segments, "."), tag
	}
	return rest, ""
}

// resolveFrom 从字段名开始解析剩余路径；没有 json 名的嵌入字段在失败落在其内部字段时不出现在路径中。
func resolveFrom(current reflect.Value, rest string) ([]string, string, bool) {
	end := strings.IndexAny(rest, ".[")
	if end < 0 {
		end = len(rest)
	}
	current = indirectValue(current)
	if current.Kind() != reflect.Struct {
		return nil, "", false
	}
	field, ok := current.Type().FieldByName(rest[:end])
	if !ok {
		return nil, "", false
	}
	value, err := current.FieldByIndexErr(field.Index)
	if err != nil {
		return nil, "", false
	}
	segment, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	skip := field.Anonymous && segment == "" && rest[end:] != ""
	if segment == "" || segment == "-" {
		segment = field.Name
	}
	return resolveIndexes(value, segment, skip, field.Tag.Get("msg"), rest[end:])
}

// resolveIndexes 解析字段名之后的切片下标与映射键，再解析其后的字段；映射键按输入中实际存在的键从长到短尝试，取第一个能解析完剩余路径的键。
func resolveIndexes(current reflect.Value, segment string, skip bool, tag, rest string) ([]string, string, bool) {
	if !strings.HasPrefix(rest, "[") {
		var head []string
		if !skip {
			head = []string{segment}
		}
		if rest == "" {
			return head, tag, true
		}
		if !strings.HasPrefix(rest, ".") {
			return nil, "", false
		}
		tail, tailTag, ok := resolveFrom(current, rest[1:])
		if !ok {
			return nil, "", false
		}
		return append(head, tail...), tailTag, true
	}
	current = indirectValue(current)
	switch current.Kind() {
	case reflect.Slice, reflect.Array:
		index, _, found := strings.Cut(rest[1:], "]")
		position, err := strconv.Atoi(index)
		if !found || err != nil || position < 0 || position >= current.Len() {
			return nil, "", false
		}
		return resolveIndexes(current.Index(position), segment+"["+index+"]", skip, tag, rest[len(index)+2:])
	case reflect.Map:
		keys := current.MapKeys()
		texts := make([]string, len(keys))
		order := make([]int, len(keys))
		for index, key := range keys {
			texts[index], order[index] = fmt.Sprintf("%v", key), index
		}
		sort.Slice(order, func(left, right int) bool { return len(texts[order[left]]) > len(texts[order[right]]) })
		for _, index := range order {
			if !strings.HasPrefix(rest[1:], texts[index]+"]") {
				continue
			}
			if segments, resolvedTag, ok := resolveIndexes(current.MapIndex(keys[index]), segment+"["+texts[index]+"]", skip, tag, rest[len(texts[index])+2:]); ok {
				return segments, resolvedTag, true
			}
		}
	}
	return nil, "", false
}

// indirectValue 解开非空的指针与接口。
func indirectValue(value reflect.Value) reflect.Value {
	for (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) && !value.IsNil() {
		value = value.Elem()
	}
	return value
}
