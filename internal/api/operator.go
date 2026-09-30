//go:build server

package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/runforyou-ai/luway/internal/appservice"
	"uuid"
)

// requestIDHeader 是运营调用方传入的请求关联标识。
const requestIDHeader = "X-Request-Id"

// operatorRequestMetaKey 在请求上下文中保存本次运营调用的请求元数据。
const operatorRequestMetaKey = "operatorRequestMeta"

type operatorErrorBody struct {
	Error *appservice.OperatorError `json:"error"`
}

// OperatorService 是官方托管运营接口的 Gin 适配器，仅在托管部署注册。
type OperatorService struct {
	application appservice.OperatorBackend
	router      *gin.Engine
}

// NewOperatorService 创建运营接口适配器。
func NewOperatorService(application appservice.OperatorBackend) *OperatorService {
	service := &OperatorService{application: application}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	service.registerGeneratedOperatorRoutes(router)
	service.router = router
	return service
}

// ServeHTTP 将运营请求交给 Gin 路由处理。
func (s *OperatorService) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	s.router.ServeHTTP(writer, request)
}

// operatorRequestMeta 从请求头提取运营凭据、请求关联标识和语言，缺少标识时生成一个。
//
// 同一次请求的业务调用、日志和错误响应使用同一个请求标识。
func operatorRequestMeta(c *gin.Context) appservice.OperatorRequestMeta {
	if cached, ok := c.Get(operatorRequestMetaKey); ok {
		return cached.(appservice.OperatorRequestMeta)
	}
	requestID := strings.TrimSpace(c.GetHeader(requestIDHeader))
	if requestID == "" {
		requestID = uuid.New().String()
	}
	meta := appservice.OperatorRequestMeta{
		Credential: appservice.BearerToken(c.GetHeader("Authorization")),
		RequestID:  requestID,
		Locale:     appservice.Locale(c.GetHeader("Accept-Language")),
	}
	c.Set(operatorRequestMetaKey, meta)
	return meta
}

// bindOperatorJSON 绑定运营请求体，失败时写入运营错误响应并返回 false。
func bindOperatorJSON(c *gin.Context, output any) bool {
	if err := c.ShouldBindJSON(output); err != nil {
		writeOperatorError(c, appservice.NewOperatorInvalidRequestError(operatorRequestMeta(c)))
		return false
	}
	return true
}

// positiveOperatorQueryInteger 解析运营请求的正整数查询参数，缺省时返回默认值，非法时写入运营错误响应。
func positiveOperatorQueryInteger(c *gin.Context, name string, defaultValue int) (int, bool) {
	value := c.Query(name)
	if value == "" {
		return defaultValue, true
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		writeOperatorError(c, appservice.NewOperatorInvalidRequestError(operatorRequestMeta(c)))
		return 0, false
	}
	return parsed, true
}

// writeOperatorResult 无错误时按状态码写入 JSON 结果，否则写入运营错误响应。
func writeOperatorResult(c *gin.Context, status int, result any, err error) {
	if writeOperatorError(c, err) {
		return
	}
	c.JSON(status, result)
}

// writeOperatorEmpty 无错误时返回 204 空响应，否则写入运营错误响应。
func writeOperatorEmpty(c *gin.Context, err error) {
	if writeOperatorError(c, err) {
		return
	}
	c.Status(http.StatusNoContent)
}

// writeOperatorError 把运营调用错误写成带稳定错误码的响应体，返回是否已处理错误。
func writeOperatorError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	if c.Request.Context().Err() != nil {
		return true
	}
	operatorError, ok := appservice.OperatorErrorOf(err)
	if !ok {
		operatorError = appservice.NewOperatorInternalError(operatorRequestMeta(c))
	}
	slog.Warn("运营调用失败", "request_id", operatorError.RequestID, "path", c.Request.URL.Path,
		"code", operatorError.Code, "error", err)
	c.JSON(operatorError.HTTPStatus(), operatorErrorBody{Error: operatorError})
	return true
}
