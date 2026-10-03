//go:build server

package integrationtest

import (
	"context"
	"strings"
	"testing"
	"uuid"

	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	invitationaction "github.com/runforyou-ai/luway/internal/actions/invitation"
	organizationaction "github.com/runforyou-ai/luway/internal/actions/organization"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	commonpassword "github.com/runforyou-ai/luway/pkg/password"
	"github.com/uptrace/bun"
)

// testPublicURL 是集成测试使用的部署地址，服务端生成的对外链接以它为根地址。
const testPublicURL = "https://app.example.test"

// workspaceSpec 定义测试工作区名称前缀与首位管理员账号；Email 在同一测试库内必须唯一，语言和时区为空时取中文与上海时区。
type workspaceSpec struct {
	Name        string
	DisplayName string
	Email       string
	Password    string
	Locale      domain.Locale
	TimeZone    string
}

// installedWorkspace 表示测试工作区中某个成员的身份及其账号登录令牌。
type installedWorkspace struct {
	Identity *servermodels.Identity
	Token    string
}

// installWorkspace 创建名称和标识独立的工作区及管理员账号并签发登录会话。
func installWorkspace(t testing.TB, db *bun.DB, spec workspaceSpec) installedWorkspace {
	t.Helper()
	ctx := context.Background()
	spec.Name = uniqueWorkspaceName(spec.Name)
	ensureTestPlatform(t, db)
	if spec.Locale == "" {
		spec.Locale = domain.LocaleChineseSimplified
	}
	if spec.TimeZone == "" {
		spec.TimeZone = "Asia/Shanghai"
	}
	passwordHash, err := commonpassword.Hash(spec.Password)
	if err != nil {
		t.Fatal(err)
	}
	var token string
	var organizationID string
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		account, err := identityaction.CreateAccount(ctx, tx, identityaction.NewAccount{
			Email: spec.Email, PasswordHash: passwordHash, DisplayName: spec.DisplayName, Locale: spec.Locale, TimeZone: spec.TimeZone,
		})
		if err != nil {
			return err
		}
		created, err := organizationaction.Create(ctx, tx, organizationaction.CreateInput{
			Name: spec.Name, Account: account, AdminDisplayName: spec.DisplayName,
		})
		if err != nil {
			return err
		}
		organizationID = created.Organization.ID
		session, err := authaction.IssueSession(ctx, tx, account)
		token = session.Token
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return resolveMemberSession(t, db, organizationID, token)
}

// ensureTestPlatform 在共享测试库中写入平台行，注册仅限受邀、工作区仅平台管理员可创建；已存在时保留原行。
func ensureTestPlatform(t testing.TB, db *bun.DB) {
	t.Helper()
	if _, err := db.NewInsert().Model(&servermodels.Platform{
		RegistrationPolicy:      string(domain.RegistrationPolicyInvitationOnly),
		WorkspaceCreationPolicy: string(domain.WorkspaceCreationPolicyPlatformAdmin),
	}).Column("registration_policy", "workspace_creation_policy").On("CONFLICT DO NOTHING").Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// uniqueWorkspaceName 为共享测试库中的工作区名称添加随机后缀。
func uniqueWorkspaceName(prefix string) string {
	name := []rune(prefix)
	return string(name[:min(len(name), domain.OrganizationNameMaxLength-13)]) + "-" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")[20:]
}

// addAccountWorkspace 为登录令牌所属账号创建名称和标识独立的工作区，账号成为首位管理员成员。
func addAccountWorkspace(t testing.TB, db *bun.DB, token, name string) *servermodels.Identity {
	t.Helper()
	ctx := context.Background()
	account, err := authaction.NewResolveAccountQuery(db).Execute(ctx, token)
	if err != nil {
		t.Fatalf("resolve account: %v", err)
	}
	var created *servermodels.Identity
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		created, err = organizationaction.Create(ctx, tx, organizationaction.CreateInput{
			Name: uniqueWorkspaceName(name), Account: &account.Account, AdminDisplayName: account.Account.DisplayName,
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

// loginMember 用账号密码登录，并解析该账号在目标工作区中的成员身份。
func loginMember(t testing.TB, db *bun.DB, organizationID, email, password string) installedWorkspace {
	t.Helper()
	session, err := authaction.NewLoginAction(db).Execute(context.Background(), authaction.LoginInput{Email: email, Password: password})
	if err != nil {
		t.Fatalf("login %s: %v", email, err)
	}
	return resolveMemberSession(t, db, organizationID, session.Token)
}

// resolveMemberSession 解析登录令牌在目标工作区中的成员身份。
func resolveMemberSession(t testing.TB, db *bun.DB, organizationID, token string) installedWorkspace {
	t.Helper()
	identity, err := authaction.NewResolveIdentityQuery(db).Execute(context.Background(), organizationID, token)
	if err != nil {
		t.Fatalf("resolve member session: %v", err)
	}
	return installedWorkspace{Identity: identity, Token: token}
}

// uniqueEmail 返回同一测试库内唯一的邮箱，local 用于标识测试中的角色。
func uniqueEmail(local string) string {
	return local + "." + strings.ReplaceAll(uuid.NewV7().String(), "-", "") + "@example.test"
}

// memberSpec 定义测试中新增成员的账号、资料与接待设置；Email 在同一测试库内必须唯一。
type memberSpec struct {
	DisplayName            string
	Email                  string
	Password               string
	RoleID                 string
	TeamIDs                []string
	HandlesServiceRequests bool
	MaxServiceSessions     int
	AvatarFileID           string
}

// testMemberCreator 按正式的加入路径为工作区新增成员：新建账号、发起邀请并接受，再设置接待、团队与头像。
type testMemberCreator struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
}

// newTestMemberCreator 创建测试成员新增器。
func newTestMemberCreator(db *bun.DB, enqueuer servertask.TxEnqueuer) testMemberCreator {
	return testMemberCreator{db: db, enqueuer: enqueuer}
}

// Execute 由 owner 邀请新账号加入其工作区，返回加入后并完成设置的成员。
func (c testMemberCreator) Execute(ctx context.Context, owner *servermodels.Identity, spec memberSpec) (*useraction.User, error) {
	passwordHash, err := commonpassword.Hash(spec.Password)
	if err != nil {
		return nil, err
	}
	account, err := identityaction.CreateAccount(ctx, c.db, identityaction.NewAccount{
		Email: spec.Email, PasswordHash: passwordHash, DisplayName: spec.DisplayName,
		Locale: domain.Locale(owner.Account.Locale), TimeZone: owner.Account.TimeZone,
	})
	if err != nil {
		return nil, err
	}
	created, err := invitationaction.NewCreateAction(c.db, nil, testPublicURL).Execute(ctx, owner, invitationaction.Input{Email: spec.Email, DisplayName: spec.DisplayName, RoleID: spec.RoleID})
	if err != nil {
		return nil, err
	}
	_, token, _ := strings.Cut(created.Link, "#/invitations/")
	if _, err := invitationaction.NewAcceptAction(c.db).Execute(ctx, &servermodels.AccountIdentity{Account: *account}, token); err != nil {
		return nil, err
	}
	var userID string
	if err := c.db.NewSelect().Model((*servermodels.User)(nil)).ColumnExpr("id::text").
		Where("organization_id = ? AND account_id = ?", owner.Organization.ID, account.ID).Scan(ctx, &userID); err != nil {
		return nil, err
	}
	// 设置头像时走成员修改流程激活头像文件；其余设置直接写入，工作区可以没有管理员。
	if spec.AvatarFileID != "" {
		return useraction.NewUpdateUserAction(c.db, testServiceSessionReturner(c.db), c.enqueuer).Execute(ctx, owner, userID, useraction.UpdateInput{
			DisplayName: spec.DisplayName, RoleID: spec.RoleID, TeamIDs: spec.TeamIDs,
			HandlesServiceRequests: spec.HandlesServiceRequests, MaxServiceSessions: spec.MaxServiceSessions, AvatarFileID: spec.AvatarFileID,
		})
	}
	err = realtime.RunInTx(ctx, c.db, func(ctx context.Context, tx bun.Tx) error {
		var identityID string
		if err := tx.NewUpdate().Model((*servermodels.User)(nil)).
			Set("max_service_sessions = ?", max(spec.MaxServiceSessions, 1)).
			Where("id = ?", userID).
			Returning("identity_id::text").
			Scan(ctx, &identityID); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model((*servermodels.OrganizationIdentity)(nil)).
			Set("handles_service_requests = ?", spec.HandlesServiceRequests).
			Where("id = ?", identityID).
			Exec(ctx); err != nil {
			return err
		}
		return teamaction.ReplaceIdentityTeams(ctx, tx, owner, identityID, spec.TeamIDs)
	})
	if err != nil {
		return nil, err
	}
	return useraction.NewGetUserQuery(c.db).Execute(ctx, owner, userID)
}
