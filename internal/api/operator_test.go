//go:build server

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/appservice/direct"
)

const testOperatorCredential = "operator-credential-operator-credential"

// newTestOperatorService 创建用于测试的运营接口适配器。
func newTestOperatorService() *OperatorService {
	return NewOperatorService(direct.NewOperatorBackend(nil, direct.OperatorConfig{
		Deployment: appservice.OperatorDeployment{
			Mode:      appservice.DeploymentModeManaged,
			PublicURL: "https://app.example.com",
		},
		Credential: testOperatorCredential,
	}))
}

// TestOperatorDeploymentRequiresCredential 验证运营接口只接受配置的运营凭据。
func TestOperatorDeploymentRequiresCredential(t *testing.T) {
	service := newTestOperatorService()
	for name, authorization := range map[string]string{
		"缺少凭据":  "",
		"凭据不匹配": "Bearer member-token",
		"缺少前缀":  testOperatorCredential,
	} {
		request := httptest.NewRequest(http.MethodGet, "/deployment", nil)
		if authorization != "" {
			request.Header.Set("Authorization", authorization)
		}
		recorder := httptest.NewRecorder()
		service.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s 返回了 %d", name, recorder.Code)
		}
		var body operatorErrorBody
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Error.Code != appservice.OperatorErrorCodeInvalidCredential || body.Error.RequestID == "" || body.Error.Message == "" {
			t.Fatalf("%s 的错误体不完整: %#v", name, body.Error)
		}
	}
}

// TestOperatorDeploymentReturnsDeployment 验证凭据有效时返回部署形态与域名后缀。
func TestOperatorDeploymentReturnsDeployment(t *testing.T) {
	service := newTestOperatorService()
	request := httptest.NewRequest(http.MethodGet, "/deployment", nil)
	request.Header.Set("Authorization", "Bearer "+testOperatorCredential)
	request.Header.Set(requestIDHeader, "provisioning-request")
	recorder := httptest.NewRecorder()
	service.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("返回了 %d", recorder.Code)
	}
	var deployment appservice.OperatorDeployment
	if err := json.Unmarshal(recorder.Body.Bytes(), &deployment); err != nil {
		t.Fatal(err)
	}
	if deployment.Mode != appservice.DeploymentModeManaged || deployment.PublicURL != "https://app.example.com" {
		t.Fatalf("部署信息不正确: %#v", deployment)
	}
}

// TestOperatorErrorCarriesRequestID 验证错误响应回传调用方提交的请求关联标识。
func TestOperatorErrorCarriesRequestID(t *testing.T) {
	service := newTestOperatorService()
	request := httptest.NewRequest(http.MethodGet, "/deployment", nil)
	request.Header.Set(requestIDHeader, "provisioning-request")
	recorder := httptest.NewRecorder()
	service.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("返回了 %d", recorder.Code)
	}
	var body operatorErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != appservice.OperatorErrorCodeInvalidCredential || body.Error.RequestID != "provisioning-request" {
		t.Fatalf("错误体不正确: %#v", body.Error)
	}
}

// TestOperatorBindJSONWritesOperatorError 验证请求体绑定失败返回运营错误契约。
func TestOperatorBindJSONWritesOperatorError(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/organizations", strings.NewReader("{"))
	c.Request.Header.Set(requestIDHeader, "provisioning-request")
	var input struct {
		Name string `json:"name"`
	}
	if bindOperatorJSON(c, &input) {
		t.Fatal("非法请求体被接受")
	}
	assertInvalidOperatorRequest(t, recorder)
}

// TestOperatorQueryIntegerWritesOperatorError 验证查询参数非法时返回运营错误契约。
func TestOperatorQueryIntegerWritesOperatorError(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/organizations?page=bad", nil)
	c.Request.Header.Set(requestIDHeader, "provisioning-request")
	if _, ok := positiveOperatorQueryInteger(c, "page", 1); ok {
		t.Fatal("非法分页参数被接受")
	}
	assertInvalidOperatorRequest(t, recorder)
}

// assertInvalidOperatorRequest 断言响应是带稳定错误码和请求标识的参数无效错误。
func assertInvalidOperatorRequest(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("返回了 %d", recorder.Code)
	}
	var body operatorErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != appservice.OperatorErrorCodeInvalidRequest || body.Error.RequestID != "provisioning-request" || body.Error.Message == "" {
		t.Fatalf("错误体不正确: %#v", body.Error)
	}
}
