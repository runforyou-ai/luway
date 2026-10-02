//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"uuid"

	deviceaction "github.com/runforyou-ai/luway/internal/actions/device"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// deviceFixture 是设备注册测试的企业、设备所属成员和另一名成员。
type deviceFixture struct {
	db     *bun.DB
	owner  *servermodels.Identity
	member *servermodels.Identity
}

// newDeviceFixture 初始化企业并创建一名普通成员。
func newDeviceFixture(t *testing.T) deviceFixture {
	t.Helper()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	suffix := uuid.NewV7().String()
	installed := installWorkspace(t, db, workspaceSpec{
		Name: "设备测试", DisplayName: "设备所属成员",
		Email: "owner@" + suffix + ".device.test", Password: "password123", Locale: domain.LocaleEnglishUnitedStates, TimeZone: "UTC",
	})
	owner := installed.Identity
	memberEmail := "member@" + suffix + ".device.test"
	if _, err := newTestMemberCreator(db, newTestTasks(db)).Execute(ctx, owner, memberSpec{
		DisplayName: "另一名成员", Email: memberEmail, Password: "password123", RoleID: owner.User.RoleID,
	}); err != nil {
		t.Fatal(err)
	}
	login := loginMember(t, db, owner.Organization.ID, memberEmail, "password123")
	return deviceFixture{db: db, owner: owner, member: login.Identity}
}

// TestDeviceRegistrationIsIdempotentPerInstall 验证同一安装重复注册指向同一台设备并更新上报信息。
func TestDeviceRegistrationIsIdempotentPerInstall(t *testing.T) {
	t.Parallel()
	f := newDeviceFixture(t)
	ctx := context.Background()
	register := deviceaction.NewRegisterDeviceAction(f.db)
	installID := uuid.NewV7().String()

	first, err := register.Execute(ctx, f.owner, deviceaction.RegisterInput{
		InstallID: installID, Name: "工作本", Platform: domain.DevicePlatformMacOS,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := register.Execute(ctx, f.owner, deviceaction.RegisterInput{
		InstallID: installID, Name: "改名后的工作本", Platform: domain.DevicePlatformMacOS,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("同一安装注册出两台设备：%s 与 %s", first.ID, second.ID)
	}
	if second.Name != "改名后的工作本" {
		t.Fatalf("重复注册未更新上报的名称：%q", second.Name)
	}
	if devices := listDevices(t, f.db, f.owner); len(devices) != 1 {
		t.Fatalf("设备列表数量 = %d", len(devices))
	}
}

// TestDeviceRegistrationSeparatesUsers 验证设备属于注册它的成员，其他成员既看不到也撤销不了。
func TestDeviceRegistrationSeparatesUsers(t *testing.T) {
	t.Parallel()
	f := newDeviceFixture(t)
	ctx := context.Background()
	register := deviceaction.NewRegisterDeviceAction(f.db)
	ownerDevice, err := register.Execute(ctx, f.owner, deviceaction.RegisterInput{
		InstallID: uuid.NewV7().String(), Name: "负责人的电脑", Platform: domain.DevicePlatformMacOS,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := register.Execute(ctx, f.member, deviceaction.RegisterInput{
		InstallID: uuid.NewV7().String(), Name: "成员的电脑", Platform: domain.DevicePlatformWindows,
	}); err != nil {
		t.Fatal(err)
	}

	devices := listDevices(t, f.db, f.member)
	if len(devices) != 1 || devices[0].Name != "成员的电脑" {
		t.Fatalf("成员看到的设备 = %+v", devices)
	}
	err = deviceaction.NewRevokeDeviceAction(f.db).Execute(ctx, f.member, ownerDevice.ID)
	if !errors.Is(err, deviceaction.ErrNotFound) {
		t.Fatalf("成员撤销他人设备的结果 = %v", err)
	}
}

// TestDeviceRevocationHidesDeviceUntilRegisteredAgain 验证撤销后设备离开列表，同一安装重新注册即恢复原设备。
func TestDeviceRevocationHidesDeviceUntilRegisteredAgain(t *testing.T) {
	t.Parallel()
	f := newDeviceFixture(t)
	ctx := context.Background()
	register := deviceaction.NewRegisterDeviceAction(f.db)
	revoke := deviceaction.NewRevokeDeviceAction(f.db)
	installID := uuid.NewV7().String()
	registered, err := register.Execute(ctx, f.owner, deviceaction.RegisterInput{
		InstallID: installID, Name: "工作本", Platform: domain.DevicePlatformLinux,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := revoke.Execute(ctx, f.owner, registered.ID); err != nil {
		t.Fatal(err)
	}
	if devices := listDevices(t, f.db, f.owner); len(devices) != 0 {
		t.Fatalf("撤销后设备列表数量 = %d", len(devices))
	}
	if err := revoke.Execute(ctx, f.owner, registered.ID); !errors.Is(err, deviceaction.ErrNotFound) {
		t.Fatalf("重复撤销的结果 = %v", err)
	}

	again, err := register.Execute(ctx, f.owner, deviceaction.RegisterInput{
		InstallID: installID, Name: "工作本", Platform: domain.DevicePlatformLinux,
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != registered.ID {
		t.Fatalf("重新注册产生新设备：%s", again.ID)
	}
	if devices := listDevices(t, f.db, f.owner); len(devices) != 1 {
		t.Fatalf("重新注册后设备列表数量 = %d", len(devices))
	}
}

// TestDeviceRegistrationRejectsUnknownPlatform 验证未知平台的注册按字段校验失败。
func TestDeviceRegistrationRejectsUnknownPlatform(t *testing.T) {
	t.Parallel()
	f := newDeviceFixture(t)
	_, err := deviceaction.NewRegisterDeviceAction(f.db).Execute(context.Background(), f.owner, deviceaction.RegisterInput{
		InstallID: uuid.NewV7().String(), Name: "手机", Platform: "ios",
	})
	var validationError *deviceaction.ValidationError
	if !errors.As(err, &validationError) || validationError.Fields["platform"] != deviceaction.ValidationPlatformInvalid {
		t.Fatalf("未知平台注册的结果 = %v", err)
	}
}

// listDevices 读取指定身份名下未撤销的设备。
func listDevices(t *testing.T, db *bun.DB, identity *servermodels.Identity) []deviceaction.Record {
	t.Helper()
	records, err := deviceaction.NewListDevicesQuery(db).Execute(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	return records
}
