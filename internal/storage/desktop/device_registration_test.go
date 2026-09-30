//go:build !server && !ios && !android

package desktop

import (
	"context"
	"maps"
	"path/filepath"
	"testing"
)

// TestDeviceInstallIDStaysStable 验证本机安装标识生成一次后跨连接保持不变。
func TestDeviceInstallIDStaysStable(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "app.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	installID, err := store.DeviceInstallID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if installID == "" {
		t.Fatal("install ID is empty")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	reopened, err := store.DeviceInstallID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reopened != installID {
		t.Fatalf("install ID = %q, want %q", reopened, installID)
	}
}

// TestDeviceRegistrationPersistsPerAccountAndWorkspace 验证设备注册结果按服务器、账号与工作区保存，可覆盖和删除，互不影响。
func TestDeviceRegistrationPersistsPerAccountAndWorkspace(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const serverURL = "https://app.example.com"
	for _, registration := range []struct{ account, organization, device string }{
		{"account-1", "org-1", "device-1"},
		{"account-1", "org-2", "device-2"},
		{"account-2", "org-1", "device-3"},
		{"account-1", "org-1", "device-4"},
	} {
		if err := store.SaveDeviceRegistration(ctx, serverURL, registration.account, registration.organization, registration.device); err != nil {
			t.Fatal(err)
		}
	}
	devices, err := store.LoadDeviceRegistrations(ctx, serverURL, "account-1")
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(devices, map[string]string{"org-1": "device-4", "org-2": "device-2"}) {
		t.Fatalf("account-1 devices = %v", devices)
	}
	if err := store.DeleteDeviceRegistration(ctx, serverURL, "account-1", "org-2"); err != nil {
		t.Fatal(err)
	}
	devices, err = store.LoadDeviceRegistrations(ctx, serverURL, "account-1")
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(devices, map[string]string{"org-1": "device-4"}) {
		t.Fatalf("account-1 devices after delete = %v", devices)
	}
	// 同一台机器上另一个账号和另一台服务器的注册结果互不影响。
	devices, err = store.LoadDeviceRegistrations(ctx, serverURL, "account-2")
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(devices, map[string]string{"org-1": "device-3"}) {
		t.Fatalf("account-2 devices = %v", devices)
	}
	devices, err = store.LoadDeviceRegistrations(ctx, "https://other.example.com", "account-1")
	if err != nil || len(devices) != 0 {
		t.Fatalf("other server devices = %v, err = %v", devices, err)
	}
}
