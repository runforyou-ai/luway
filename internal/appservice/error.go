package appservice

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
)

// Error 定义跨 Wails 和 HTTP 传输的业务错误。
type Error struct {
	Kind    ErrorKind         `json:"kind,omitempty"`
	State   SessionState      `json:"state,omitempty"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
	Reason  string            `json:"reason,omitempty"`
	status  int               `json:"-"`
	// language 是错误文案实际使用的语言标签。
	language string `json:"-"`
	// cause 是操作失败的原始错误，只在服务端记录，不随错误传输。
	cause error `json:"-"`
}

// Unwrap 返回操作失败的原始错误。
func (e *Error) Unwrap() error {
	return e.cause
}

// Language 返回错误文案实际使用的语言标签，未记录时为空。
func (e *Error) Language() string {
	return e.language
}

// displayMessage 返回错误种类或会话状态，以及用户文案。
func (e *Error) displayMessage() string {
	if e.Kind != "" {
		return string(e.Kind) + ": " + e.Message
	}
	if e.State != "" {
		return string(e.State) + ": " + e.Message
	}
	return e.Message
}

// HTTPStatus 返回对应的 HTTP 状态码。
func (e *Error) HTTPStatus() int {
	if e.status != 0 {
		return e.status
	}
	switch e.State {
	case SessionStateLogin:
		return http.StatusUnauthorized
	case SessionStateSetup:
		return http.StatusConflict
	case SessionStateConnect:
		return http.StatusPreconditionRequired
	case SessionStateWorkspace:
		return http.StatusForbidden
	case SessionStateUpgrade:
		return http.StatusPreconditionFailed
	}
	switch e.Kind {
	case ErrorKindInvalid:
		return http.StatusBadRequest
	case ErrorKindNotFound:
		return http.StatusNotFound
	case ErrorKindForbidden:
		return http.StatusForbidden
	case ErrorKindConflict:
		return http.StatusConflict
	case ErrorKindUnavailable:
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

// WithStatus 指定 HTTP 状态码。
func (e *Error) WithStatus(status int) *Error {
	e.status = status
	return e
}

// WithReason 附加可向调用方展示的具体错误原因。
func (e *Error) WithReason(reason string) *Error {
	e.Reason = reason
	return e
}

// MarshalError 将业务错误写入 Wails RuntimeError 的 cause。
func MarshalError(err error) []byte {
	var apiError *Error
	if !errors.As(err, &apiError) {
		return nil
	}
	payload, err := json.Marshal(apiError)
	if err != nil {
		slog.Warn("序列化业务错误失败", "error", err)
		return nil
	}
	return payload
}

// InvalidError 返回输入无效的业务错误。
func InvalidError(meta RequestMeta, messageKey i18n.Key, fieldKeys map[string]i18n.Key) *Error {
	return newError(meta, ErrorKindInvalid, "", messageKey, fieldKeys)
}

// NotFoundError 返回资源不存在的业务错误。
func NotFoundError(meta RequestMeta, messageKey i18n.Key) *Error {
	return newError(meta, ErrorKindNotFound, "", messageKey, nil)
}

// ForbiddenError 返回当前账号无权执行操作的业务错误。
func ForbiddenError(meta RequestMeta, messageKey i18n.Key) *Error {
	return newError(meta, ErrorKindForbidden, "", messageKey, nil)
}

// ConflictError 返回带稳定原因码的业务冲突。
func ConflictError(meta RequestMeta, messageKey i18n.Key, reason string) *Error {
	conflictError := newError(meta, ErrorKindConflict, "", messageKey, nil)
	conflictError.Reason = reason
	return conflictError
}

// UnavailableError 返回依赖服务不可用的业务错误。
func UnavailableError(meta RequestMeta, messageKey i18n.Key, fieldKeys map[string]i18n.Key) *Error {
	return newError(meta, ErrorKindUnavailable, "", messageKey, fieldKeys)
}

// FailedError 返回操作失败的业务错误，cause 是导致失败的原始错误。
func FailedError(meta RequestMeta, messageKey i18n.Key, cause error) *Error {
	failedError := newError(meta, ErrorKindFailed, "", messageKey, nil)
	failedError.cause = cause
	return failedError
}

// SessionError 返回应回到会话入口的业务错误。
func SessionError(meta RequestMeta, state SessionState, messageKey i18n.Key) *Error {
	return newError(meta, "", state, messageKey, nil)
}

// SessionStateOf 读取错误中的会话入口。
func SessionStateOf(err error) SessionState {
	if apiError, ok := errors.AsType[*Error](err); ok {
		return apiError.State
	}
	return ""
}

// methodNotAllowedError 返回当前平台不支持该操作的业务错误。
func methodNotAllowedError(meta RequestMeta, operation string) *Error {
	slog.Warn("当前平台不支持此操作", "operation", operation)
	return newError(meta, ErrorKindFailed, "", i18n.ErrorMethodNotAllowed, nil).WithStatus(http.StatusMethodNotAllowed)
}

// newError 构造本地化业务错误。
func newError(meta RequestMeta, kind ErrorKind, state SessionState, messageKey i18n.Key, fieldKeys map[string]i18n.Key) *Error {
	message, language := i18n.Localize(string(meta.Locale), messageKey)
	return &Error{Kind: kind, State: state, Message: message, Fields: i18n.LocalizeMap(string(meta.Locale), fieldKeys), language: language}
}

// WebsiteVisitorFailedError 按对客语言构造返回给网站访客的操作失败错误，cause 是导致失败的原始错误。
func WebsiteVisitorFailedError(locale CustomerLocale, messageKey i18n.Key, cause error) *Error {
	failedError := WebsiteVisitorError(locale, ErrorKindFailed, messageKey, nil)
	failedError.cause = cause
	return failedError
}

// WebsiteVisitorError 按对客语言构造返回给网站访客的业务错误。
func WebsiteVisitorError(locale CustomerLocale, kind ErrorKind, messageKey i18n.Key, fieldKeys map[string]i18n.Key) *Error {
	visitorError := &Error{Kind: kind, Message: i18n.LocalizeCustomerTemplate(domain.CustomerLocale(locale), messageKey, nil), language: string(locale)}
	if len(fieldKeys) > 0 {
		visitorError.Fields = i18n.LocalizeCustomerMap(domain.CustomerLocale(locale), fieldKeys)
	}
	return visitorError
}
