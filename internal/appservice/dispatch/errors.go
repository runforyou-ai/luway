//go:build server

package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	roleaction "github.com/runforyou-ai/luway/internal/actions/role"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/i18n"
)

// Rule 把命中的动作错误转换为业务错误，未命中时返回 nil。
type Rule func(meta appservice.RequestMeta, err error) error

// Builder 按请求语言构造业务错误。
type Builder func(meta appservice.RequestMeta) *appservice.Error

// Catalog 是按顺序匹配的动作错误转换规则表。
type Catalog []Rule

// Find 返回第一条命中规则转换出的业务错误，没有规则命中时返回 nil。
func (c Catalog) Find(meta appservice.RequestMeta, err error) error {
	for _, rule := range c {
		if mapped := rule(meta, err); mapped != nil {
			return mapped
		}
	}
	return nil
}

// Translate 依次应用规则转换动作错误，没有规则命中时返回携带原始错误的 failureKey 操作失败；err 为 nil 时返回 nil。
func (c Catalog) Translate(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	if err == nil {
		return nil
	}
	if mapped := c.Find(meta, err); mapped != nil {
		return mapped
	}
	return appservice.FailedError(meta, failureKey, err)
}

// Catalogs 按顺序拼接多张规则表。
func Catalogs(catalogs ...Catalog) Catalog {
	return slices.Concat(catalogs...)
}

// Is 在错误链包含 target 时用 build 构造业务错误。
func Is(target error, build Builder) Rule {
	return func(meta appservice.RequestMeta, err error) error {
		if errors.Is(err, target) {
			return build(meta)
		}
		return nil
	}
}

// NotFound 构造资源不存在错误。
func NotFound(key i18n.Key) Builder {
	return func(meta appservice.RequestMeta) *appservice.Error { return appservice.NotFoundError(meta, key) }
}

// Forbidden 构造无权操作错误。
func Forbidden(key i18n.Key) Builder {
	return func(meta appservice.RequestMeta) *appservice.Error { return appservice.ForbiddenError(meta, key) }
}

// Conflict 构造带原因码的业务冲突。
func Conflict(key i18n.Key, reason string) Builder {
	return func(meta appservice.RequestMeta) *appservice.Error {
		return appservice.ConflictError(meta, key, reason)
	}
}

// Invalid 构造不带字段的输入无效错误。
func Invalid(key i18n.Key) Builder {
	return func(meta appservice.RequestMeta) *appservice.Error { return appservice.InvalidError(meta, key, nil) }
}

// InvalidField 构造单个字段校验失败的输入无效错误。
func InvalidField(field string, key i18n.Key) Builder {
	return func(meta appservice.RequestMeta) *appservice.Error {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, map[string]i18n.Key{field: key})
	}
}

// Unavailable 构造依赖服务不可用错误。
func Unavailable(key i18n.Key) Builder {
	return func(meta appservice.RequestMeta) *appservice.Error {
		return appservice.UnavailableError(meta, key, nil)
	}
}

// Session 构造回到指定会话入口的错误。
func Session(state appservice.SessionState, key i18n.Key) Builder {
	return func(meta appservice.RequestMeta) *appservice.Error { return appservice.SessionError(meta, state, key) }
}

// WithReason 为构造出的业务错误附加稳定原因码。
func WithReason(build Builder, reason string) Builder {
	return func(meta appservice.RequestMeta) *appservice.Error { return build(meta).WithReason(reason) }
}

// FieldRule 把字段校验错误按 keys 转换为输入无效错误。
func FieldRule(keys map[common.FieldCode]i18n.Key) Rule {
	return func(meta appservice.RequestMeta, err error) error {
		if validationError, ok := errors.AsType[*common.FieldError](err); ok {
			return appservice.InvalidError(meta, i18n.ErrorValidationFailed, TranslateFields(validationError.Fields, keys))
		}
		return nil
	}
}

// SessionRule 在操作者身份失效时要求重新登录。
var SessionRule = Is(identityaction.ErrInvalid, Session(appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired))

// CommonErrors 是各业务域共用的动作错误：操作者身份失效时要求重新登录，角色超出操作者权限时禁止，调用被拒绝时返回带原因码的冲突。
var CommonErrors = Catalog{
	SessionRule,
	Is(roleaction.ErrAdministratorOnly, Forbidden(i18n.ErrorAdministratorOnly)),
	func(meta appservice.RequestMeta, err error) error {
		if rejection, ok := errors.AsType[*modelcall.Rejection](err); ok {
			return appservice.ConflictError(meta, i18n.Key(rejection.MessageKey), rejection.Reason)
		}
		return nil
	},
}

// TranslateFields 把校验错误码映射为本地化文案键。
func TranslateFields[Code comparable](fields map[string]Code, keys map[Code]i18n.Key) map[string]i18n.Key {
	result := make(map[string]i18n.Key, len(fields))
	for field, code := range fields {
		key, exists := keys[code]
		if !exists {
			slog.WarnContext(context.Background(), "未映射的校验错误码", "field", field, "code", fmt.Sprint(code))
			continue
		}
		result[field] = key
	}
	return result
}
