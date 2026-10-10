package appservice

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/runforyou-ai/luway/internal/i18n"
)

// TraceparentHeader 是请求方传入串联编号的 W3C Trace Context 请求头。
const TraceparentHeader = "traceparent"

// TraceHeader 是服务端返回本次请求串联编号的响应头。
const TraceHeader = "X-Trace-ID"

// RequestMetaFromHTTP 从请求头提取 Bearer 令牌、目标工作区和语言，构造应用服务请求元数据。
func RequestMetaFromHTTP(header http.Header) RequestMeta {
	return RequestMeta{
		Token:           BearerToken(header.Get("Authorization")),
		WorkspaceID:     strings.TrimSpace(header.Get(WorkspaceHeader)),
		Locale:          Locale(header.Get("Accept-Language")),
		ExecutorVersion: strings.TrimSpace(header.Get(ExecutorVersionHeader)),
	}
}

// BearerToken 从 Authorization 头解析 Bearer 令牌，格式不符时返回空串。
func BearerToken(authorization string) string {
	scheme, token, found := strings.Cut(strings.TrimSpace(authorization), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

// WriteHTTPError 以 {"error": ...} 结构写入业务错误响应，并按错误文案语言设置 Content-Language；错误未记录语言时按请求的 Accept-Language 取语言。
func WriteHTTPError(writer http.ResponseWriter, request *http.Request, applicationError *Error) {
	language := applicationError.Language()
	if language == "" {
		_, language = i18n.Localize(request.Header.Get("Accept-Language"), i18n.ErrorInternal)
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Content-Language", language)
	writer.Header().Set("Vary", "Accept-Language")
	if applicationError.RetryAfter > 0 {
		writer.Header().Set("Retry-After", strconv.Itoa(applicationError.RetryAfter))
	}
	writer.WriteHeader(applicationError.HTTPStatus())
	if err := json.NewEncoder(writer).Encode(struct {
		Error *Error `json:"error"`
	}{applicationError}); err != nil {
		slog.WarnContext(request.Context(), "写入业务错误响应失败", "error", err)
	}
}
