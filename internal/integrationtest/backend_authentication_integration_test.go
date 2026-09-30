//go:build server

package integrationtest

import (
	"context"
	"reflect"
	"testing"

	"github.com/runforyou-ai/luway/internal/appservice"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
)

// publicBackendMethods 是不解析登录会话的方法，与 backend.go 中标记 auth=public 的路由一一对应。
var publicBackendMethods = map[string]bool{
	"InstallationStatus":    true,
	"Login":                 true,
	"Register":              true,
	"StartOfficialLogin":    true,
	"CompleteOfficialLogin": true,
	"PreviewInvitation":     true,
}

// accountBackendMethods 是只需要登录账号、不要求目标工作区的方法，与 backend.go 中标记 auth=account 的路由一一对应。
var accountBackendMethods = map[string]bool{
	"Logout":                 true,
	"LoadAccount":            true,
	"ListWorkspaces":         true,
	"ListWorkspaceAttention": true,
	"CreateWorkspace":        true,
	"ChangePassword":         true,
	"AcceptInvitation":       true,
}

// TestBackendMethodsRequireAuthentication 验证非公开方法在无会话时都被挡回登录入口，
// 工作区级方法在会话有效但未指定目标工作区时进入工作区选择，且方法名单与接口闭合。
func TestBackendMethodsRequireAuthentication(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()

	// 部署已有账号，未登录请求停在会话校验上。
	member := installWorkspace(t, db, workspaceSpec{Name: "认证测试", DisplayName: "管理员", Email: uniqueEmail("admin"), Password: "password123"})
	backend := newAccountTestBackend(db)
	backendValue := reflect.ValueOf(backend)
	backendInterface := reflect.TypeFor[appservice.Backend]()

	// call 以零值参数调用方法，认证发生在业务实现之前，零值参数即可到达会话校验。
	call := func(name string, meta appservice.RequestMeta) error {
		method := backendValue.MethodByName(name)
		arguments := make([]reflect.Value, method.Type().NumIn())
		arguments[0] = reflect.ValueOf(ctx)
		arguments[1] = reflect.ValueOf(meta)
		for argument := 2; argument < len(arguments); argument++ {
			arguments[argument] = reflect.New(method.Type().In(argument)).Elem()
		}
		results := method.Call(arguments)
		err, _ := results[len(results)-1].Interface().(error)
		return err
	}

	checked := 0
	for index := range backendInterface.NumMethod() {
		name := backendInterface.Method(index).Name
		if publicBackendMethods[name] {
			continue
		}
		if state := appservice.SessionStateOf(call(name, appservice.RequestMeta{})); state != appservice.SessionStateLogin {
			t.Errorf("%s 未登录调用的会话入口 = %q, want %q", name, state, appservice.SessionStateLogin)
		}
		if !accountBackendMethods[name] {
			err := call(name, appservice.RequestMeta{Token: member.Token})
			if state := appservice.SessionStateOf(err); state != appservice.SessionStateWorkspace {
				t.Errorf("%s 未指定工作区调用的会话入口 = %q, want %q（错误：%v）", name, state, appservice.SessionStateWorkspace, err)
			}
		}
		checked++
	}
	for _, methods := range []map[string]bool{publicBackendMethods, accountBackendMethods} {
		for name := range methods {
			if _, exists := backendInterface.MethodByName(name); !exists {
				t.Errorf("方法名单中的 %s 已不在 Backend 接口上", name)
			}
		}
	}
	if total := checked + len(publicBackendMethods); total != backendInterface.NumMethod() {
		t.Fatalf("已校验 %d 个方法加 %d 个公开方法 = %d，Backend 接口共 %d 个方法",
			checked, len(publicBackendMethods), total, backendInterface.NumMethod())
	}
}
