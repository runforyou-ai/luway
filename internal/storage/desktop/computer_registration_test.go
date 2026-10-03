//go:build !server && !ios && !android

package desktop

import (
	"context"
	"maps"
	"path/filepath"
	"testing"

	desktopmodels "github.com/runforyou-ai/luway/internal/storage/desktop/models"
)

// TestComputerInstallIDStaysStable 验证本机执行器安装标识生成一次后跨连接保持不变。
func TestComputerInstallIDStaysStable(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "app.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	installID, err := store.ComputerInstallID(ctx)
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
	reopened, err := store.ComputerInstallID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reopened != installID {
		t.Fatalf("install ID = %q, want %q", reopened, installID)
	}
}

// TestComputerRegistrationPersistsPerAccountAndWorkspace 验证电脑注册结果与凭据按服务器、账号与工作区保存，可覆盖和删除，互不影响。
func TestComputerRegistrationPersistsPerAccountAndWorkspace(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const serverURL = "https://app.example.com"
	for _, registration := range []struct{ account, organization, computer string }{
		{"account-1", "org-1", "computer-1"},
		{"account-1", "org-2", "computer-2"},
		{"account-2", "org-1", "computer-3"},
		{"account-1", "org-1", "computer-4"},
	} {
		if err := store.SaveComputerRegistration(ctx, desktopmodels.ComputerRegistration{
			ServerURL: serverURL, AccountID: registration.account, OrganizationID: registration.organization,
			ComputerID: registration.computer, Credential: "secret-" + registration.computer,
		}); err != nil {
			t.Fatal(err)
		}
	}
	computers := func(serverURL, accountID string) map[string]string {
		registrations, err := store.LoadComputerRegistrations(ctx, serverURL, accountID)
		if err != nil {
			t.Fatal(err)
		}
		ids := make(map[string]string, len(registrations))
		for organizationID, registration := range registrations {
			if registration.Credential != "secret-"+registration.ComputerID {
				t.Fatalf("credential = %q", registration.Credential)
			}
			ids[organizationID] = registration.ComputerID
		}
		return ids
	}
	if got := computers(serverURL, "account-1"); !maps.Equal(got, map[string]string{"org-1": "computer-4", "org-2": "computer-2"}) {
		t.Fatalf("account-1 computers = %v", got)
	}
	// 只删除指定电脑的注册，电脑编号不符时保留。
	if err := store.DeleteComputerRegistration(ctx, serverURL, "account-1", "org-1", "computer-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteComputerRegistration(ctx, serverURL, "account-1", "org-2", ""); err != nil {
		t.Fatal(err)
	}
	if got := computers(serverURL, "account-1"); !maps.Equal(got, map[string]string{"org-1": "computer-4"}) {
		t.Fatalf("account-1 computers after delete = %v", got)
	}
	// 同一台机器上另一个账号和另一台服务器的注册结果互不影响。
	if got := computers(serverURL, "account-2"); !maps.Equal(got, map[string]string{"org-1": "computer-3"}) {
		t.Fatalf("account-2 computers = %v", got)
	}
	if got := computers("https://other.example.com", "account-1"); len(got) != 0 {
		t.Fatalf("other server computers = %v", got)
	}
	all, err := store.ListComputerRegistrations(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("all registrations = %v, err = %v", all, err)
	}
}
