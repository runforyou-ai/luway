//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"uuid"

	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// computerFixture 是电脑注册测试的企业、电脑所属成员和另一名成员。
type computerFixture struct {
	db     *bun.DB
	owner  *servermodels.Identity
	member *servermodels.Identity
}

// newComputerFixture 初始化企业并创建一名普通成员。
func newComputerFixture(t *testing.T) computerFixture {
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
		Name: "电脑测试", DisplayName: "电脑所属成员",
		Email: "owner@" + suffix + ".computer.test", Password: "password123", Locale: domain.LocaleEnglishUnitedStates, TimeZone: "UTC",
	})
	owner := installed.Identity
	memberEmail := "member@" + suffix + ".computer.test"
	if _, err := newTestMemberCreator(db, newTestTasks(db)).Execute(ctx, owner, memberSpec{
		DisplayName: "另一名成员", Email: memberEmail, Password: "password123", RoleID: owner.User.RoleID,
	}); err != nil {
		t.Fatal(err)
	}
	login := loginMember(t, db, owner.Organization.ID, memberEmail, "password123")
	return computerFixture{db: db, owner: owner, member: login.Identity}
}

// TestComputerRegistrationIsIdempotentPerInstall 验证同一安装重复注册指向同一台电脑并更新上报信息，重新注册后只有新凭据有效。
func TestComputerRegistrationIsIdempotentPerInstall(t *testing.T) {
	t.Parallel()
	f := newComputerFixture(t)
	ctx := context.Background()
	register := computeraction.NewRegisterComputerAction(f.db)
	installID := uuid.NewV7().String()

	first, err := register.Execute(ctx, f.owner, computeraction.RegisterInput{
		InstallID: installID, Name: "工作本", Platform: domain.ComputerPlatformMacOS,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := register.Execute(ctx, f.owner, computeraction.RegisterInput{
		InstallID: installID, Name: "改名后的工作本", Platform: domain.ComputerPlatformMacOS,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Record.ID != first.Record.ID {
		t.Fatalf("同一安装注册出两台电脑：%s 与 %s", first.Record.ID, second.Record.ID)
	}
	authenticate := computeraction.NewAuthenticateComputerQuery(f.db)
	if _, err := authenticate.Execute(ctx, first.Credential); !errors.Is(err, computeraction.ErrCredentialInvalid) {
		t.Fatalf("旧凭据认证的结果 = %v", err)
	}
	if identity, err := authenticate.Execute(ctx, second.Credential); err != nil || identity.ComputerID != first.Record.ID || identity.OrganizationID != f.owner.Organization.ID {
		t.Fatalf("新凭据认证 = %+v, err = %v", identity, err)
	}
	if second.Record.Name != "改名后的工作本" {
		t.Fatalf("重复注册未更新上报的名称：%q", second.Record.Name)
	}
	if computers := listComputers(t, f.db, f.owner); len(computers) != 1 {
		t.Fatalf("电脑列表数量 = %d", len(computers))
	}
}

// TestComputerRegistrationSeparatesUsers 验证电脑属于注册它的成员，其他成员既看不到也撤销不了。
func TestComputerRegistrationSeparatesUsers(t *testing.T) {
	t.Parallel()
	f := newComputerFixture(t)
	ctx := context.Background()
	register := computeraction.NewRegisterComputerAction(f.db)
	ownerComputer, err := register.Execute(ctx, f.owner, computeraction.RegisterInput{
		InstallID: uuid.NewV7().String(), Name: "负责人的电脑", Platform: domain.ComputerPlatformMacOS,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := register.Execute(ctx, f.member, computeraction.RegisterInput{
		InstallID: uuid.NewV7().String(), Name: "成员的电脑", Platform: domain.ComputerPlatformWindows,
	}); err != nil {
		t.Fatal(err)
	}

	computers := listComputers(t, f.db, f.member)
	if len(computers) != 1 || computers[0].Name != "成员的电脑" {
		t.Fatalf("成员看到的电脑 = %+v", computers)
	}
	err = computeraction.NewRevokeComputerAction(f.db, newTestTasks(f.db)).Execute(ctx, f.member, ownerComputer.Record.ID)
	if !errors.Is(err, computeraction.ErrNotFound) {
		t.Fatalf("成员撤销他人电脑的结果 = %v", err)
	}
}

// TestComputerRevocationHidesComputerUntilRegisteredAgain 验证撤销后电脑离开列表且凭据失效，同一安装重新注册即恢复原电脑。
func TestComputerRevocationHidesComputerUntilRegisteredAgain(t *testing.T) {
	t.Parallel()
	f := newComputerFixture(t)
	ctx := context.Background()
	register := computeraction.NewRegisterComputerAction(f.db)
	revoke := computeraction.NewRevokeComputerAction(f.db, newTestTasks(f.db))
	installID := uuid.NewV7().String()
	registered, err := register.Execute(ctx, f.owner, computeraction.RegisterInput{
		InstallID: installID, Name: "工作本", Platform: domain.ComputerPlatformLinux,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := revoke.Execute(ctx, f.owner, registered.Record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := computeraction.NewAuthenticateComputerQuery(f.db).Execute(ctx, registered.Credential); !errors.Is(err, computeraction.ErrCredentialInvalid) {
		t.Fatalf("撤销后凭据认证的结果 = %v", err)
	}
	if computers := listComputers(t, f.db, f.owner); len(computers) != 0 {
		t.Fatalf("撤销后电脑列表数量 = %d", len(computers))
	}
	if err := revoke.Execute(ctx, f.owner, registered.Record.ID); !errors.Is(err, computeraction.ErrNotFound) {
		t.Fatalf("重复撤销的结果 = %v", err)
	}

	again, err := register.Execute(ctx, f.owner, computeraction.RegisterInput{
		InstallID: installID, Name: "工作本", Platform: domain.ComputerPlatformLinux,
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.Record.ID != registered.Record.ID {
		t.Fatalf("重新注册产生新电脑：%s", again.Record.ID)
	}
	if computers := listComputers(t, f.db, f.owner); len(computers) != 1 {
		t.Fatalf("重新注册后电脑列表数量 = %d", len(computers))
	}
}

// TestComputerRegistrationRejectsUnknownPlatform 验证未知平台的注册按字段校验失败。
func TestComputerRegistrationRejectsUnknownPlatform(t *testing.T) {
	t.Parallel()
	f := newComputerFixture(t)
	_, err := computeraction.NewRegisterComputerAction(f.db).Execute(context.Background(), f.owner, computeraction.RegisterInput{
		InstallID: uuid.NewV7().String(), Name: "手机", Platform: "ios",
	})
	var validationError *computeraction.ValidationError
	if !errors.As(err, &validationError) || validationError.Fields["platform"] != computeraction.ValidationPlatformInvalid {
		t.Fatalf("未知平台注册的结果 = %v", err)
	}
}

// listComputers 读取指定身份名下未撤销的电脑。
func listComputers(t *testing.T, db *bun.DB, identity *servermodels.Identity) []computeraction.Record {
	t.Helper()
	records, err := computeraction.NewListComputersQuery(db).Execute(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	return records
}
