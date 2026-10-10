//go:build server

// Package httpcodec 提供服务端 HTTP 接口共用的请求绑定与响应写入：解析请求元数据、JSON 请求体与查询参数，把结果和应用服务错误写成 JSON 响应。
package httpcodec

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
)

// OptionalEnum 把非空查询值转换成对应枚举指针，空值返回 nil。
func OptionalEnum[T ~string](value string) *T {
	return support.NilIfZero(T(value))
}

// EnumList 把重复查询参数转换成枚举切片，缺省时返回空切片。
func EnumList[T ~string](values []string) []T {
	return arr.OrEmpty(arr.Map(values, func(value string) T { return T(value) }))
}

// QueryOrDefault 返回查询参数的值，参数缺失时返回默认值。
func QueryOrDefault(query url.Values, name, defaultValue string) string {
	if values, ok := query[name]; ok && len(values) > 0 {
		return values[0]
	}
	return defaultValue
}

// RequestMeta 从请求头提取令牌、目标工作区和语言，构造应用服务请求元数据。
func RequestMeta(request *http.Request) appservice.RequestMeta {
	return appservice.RequestMetaFromHTTP(request.Header)
}

// BindJSON 解析 JSON 请求体，失败时写入校验错误响应并返回 false。
func BindJSON(writer http.ResponseWriter, request *http.Request, output any) bool {
	if err := json.NewDecoder(request.Body).Decode(output); err != nil {
		WriteApplicationError(writer, request, appservice.InvalidError(RequestMeta(request), i18n.ErrorValidationFailed, nil))
		return false
	}
	return true
}

// PositiveQueryInteger 解析正整数查询参数，缺省时返回默认值，非法时写入校验错误响应。
func PositiveQueryInteger(writer http.ResponseWriter, request *http.Request, query url.Values, name string, defaultValue int) (int, bool) {
	value := query.Get(name)
	if value == "" {
		return defaultValue, true
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		WriteApplicationError(writer, request, appservice.InvalidError(RequestMeta(request), i18n.ErrorValidationFailed, map[string]i18n.Key{name: i18n.FieldQueryPositiveInteger}))
		return 0, false
	}
	return parsed, true
}

// WriteResult 无错误时把结果中的 nil 切片与映射补为空值后按状态码写入 JSON，否则写入错误响应。
func WriteResult[T any](writer http.ResponseWriter, request *http.Request, status int, result T, err error) {
	if WriteApplicationError(writer, request, err) {
		return
	}
	appservice.NormalizeEmpty(&result)
	WriteJSON(writer, request, status, result)
}

// WriteEmpty 无错误时返回 204 空响应，否则写入错误响应。
func WriteEmpty(writer http.ResponseWriter, request *http.Request, err error) {
	if WriteApplicationError(writer, request, err) {
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

// WriteJSON 按状态码写入 JSON 响应。
func WriteJSON(writer http.ResponseWriter, request *http.Request, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		slog.WarnContext(request.Context(), "写入接口响应失败", "error", err)
	}
}

// WriteApplicationError 把应用服务错误写成结构化错误响应，返回是否已处理错误；请求已取消时不写响应。
func WriteApplicationError(writer http.ResponseWriter, request *http.Request, err error) bool {
	if err == nil {
		return false
	}
	if request.Context().Err() != nil {
		return true
	}
	if applicationError, ok := errors.AsType[*appservice.Error](err); ok {
		appservice.WriteHTTPError(writer, request, applicationError)
		return true
	}
	slog.ErrorContext(request.Context(), "接口返回非业务错误", "path", request.URL.Path, "error", err)
	appservice.WriteHTTPError(writer, request, appservice.FailedError(RequestMeta(request), i18n.ErrorInternal, err))
	return true
}
