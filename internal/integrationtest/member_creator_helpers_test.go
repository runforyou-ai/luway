//go:build server

package integrationtest

import (
	"context"
	"strings"

	accountaction "github.com/runforyou-ai/luway/internal/actions/account"
	invitationaction "github.com/runforyou-ai/luway/internal/actions/invitation"
	seataction "github.com/runforyou-ai/luway/internal/actions/seat"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	commonpassword "github.com/runforyou-ai/luway/pkg/password"
	"github.com/uptrace/bun"
)

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
	account, err := accountaction.CreateAccount(ctx, c.db, accountaction.NewAccount{
		Email: spec.Email, PasswordHash: passwordHash, DisplayName: spec.DisplayName,
		Locale: domain.Locale(owner.Account.Locale), TimeZone: owner.Account.TimeZone,
	})
	if err != nil {
		return nil, err
	}
	created, err := invitationaction.NewCreateAction(c.db, servertest.NewTasks(), seataction.Seats{}, servertest.DisabledMail{}.Enabled, func() string { return servertest.PublicURL }).Execute(ctx, owner, invitationaction.Input{Email: spec.Email, DisplayName: spec.DisplayName, RoleID: spec.RoleID})
	if err != nil {
		return nil, err
	}
	_, token, _ := strings.Cut(created.Link, "#/invitations/")
	if _, err := invitationaction.NewAcceptAction(c.db, seataction.Seats{}).Execute(ctx, &servermodels.AccountIdentity{Account: *account}, token); err != nil {
		return nil, err
	}
	var userID string
	if err := c.db.NewSelect().Model((*servermodels.User)(nil)).ColumnExpr("id::text").
		Where("workspace_id = ? AND account_id = ?", owner.Workspace.ID, account.ID).Scan(ctx, &userID); err != nil {
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
		if _, err := tx.NewUpdate().Model((*servermodels.WorkspaceIdentity)(nil)).
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
