//go:build server

package direct

import (
	"context"
	"errors"
	"testing"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
)

// TestBackendPreservesCancellation 验证请求取消原因的透传。
func TestBackendPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (&directOperations{}).contactError(ctx, appservice.RequestMeta{}, errors.New("query failed"), i18n.ErrorContactReadFailed)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

// TestManagedDeploymentClosesLocalAccountEntrances 验证托管部署关闭首次安装、本地密码登录和本地注册。
func TestManagedDeploymentClosesLocalAccountEntrances(t *testing.T) {
	ops := &directOperations{sessionGuard: sessionGuard{deploymentMode: domain.DeploymentModeManaged}}
	if _, err := ops.InstallWorkspace(context.Background(), appservice.RequestMeta{}, appservice.InstallWorkspaceInput{}); !isInvalidError(err) {
		t.Fatalf("install error = %v, want invalid", err)
	}
	if _, err := ops.Login(context.Background(), appservice.RequestMeta{}, appservice.LoginInput{Email: "admin@example.com", Password: "password123"}); !isInvalidError(err) {
		t.Fatalf("login error = %v, want invalid", err)
	}
	if _, err := ops.Register(context.Background(), appservice.RequestMeta{}, appservice.RegisterInput{}); !isInvalidError(err) {
		t.Fatalf("register error = %v, want invalid", err)
	}
}

// isInvalidError 判断错误是否为业务输入无效。
func isInvalidError(err error) bool {
	appErr, ok := errors.AsType[*appservice.Error](err)
	return ok && appErr.Kind == appservice.ErrorKindInvalid
}
