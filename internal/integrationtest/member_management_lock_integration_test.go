//go:build server

package integrationtest

import (
	"context"
	"strings"
	"testing"
	"time"

	roleaction "github.com/runforyou-ai/luway/internal/actions/role"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// TestMemberManagementCrossAccountLockOrder 验证角色调整、资料编辑和状态修改交叉操作两个账号时串行完成。
func TestMemberManagementCrossAccountLockOrder(t *testing.T) {
	t.Parallel()
	for _, pair := range [][2]string{{"role", "role"}, {"profile", "profile"}, {"status", "status"}, {"role", "profile"}, {"profile", "status"}, {"status", "role"}} {
		t.Run(pair[0]+"_"+pair[1], func(t *testing.T) {
			f := newNavigationFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			f.db.AddQueryHook(chatQueryHook{})
			gate := newChatQueryGate(t, true, 1, func(event *bun.QueryEvent) bool {
				return strings.Contains(event.Query, `FROM "roles"`) && strings.Contains(event.Query, "FOR UPDATE")
			})
			defer gate.open()
			// 首个操作持有账号锁后暂停，第二个操作以相反的操作者与目标进入。
			first, second := make(chan error, 1), make(chan error, 1)
			go func() {
				first <- executeMemberManagement(context.WithValue(ctx, chatQueryGateKey{}, gate), f, pair[0], f.owner, f.member)
			}()
			waitChatSignal(t, ctx, gate.reached)
			go func() {
				second <- executeMemberManagement(ctx, f, pair[1], f.member, f.owner)
			}()
			waitChatDatabaseLock(t, ctx, f.db, `FROM "users"`, f.owner.User.ID)
			gate.open()
			if err := waitChatResult(t, ctx, first); err != nil {
				t.Fatalf("首个成员操作失败: %v", err)
			}
			if err := waitChatResult(t, ctx, second); err != nil {
				t.Fatalf("交叉成员操作失败: %v", err)
			}
		})
	}
}

// executeMemberManagement 执行指定类型的成员管理操作，保留目标账号的资料与有效状态。
func executeMemberManagement(ctx context.Context, f navigationFixture, operation string, actor, target *servermodels.Identity) error {
	switch operation {
	case "role":
		return roleaction.NewUpdateAssignmentsAction(f.db).Execute(ctx, actor, []roleaction.AssignmentInput{{
			IdentityID: target.OrganizationIdentity.ID, RoleID: target.User.RoleID,
		}})
	case "profile":
		_, err := useraction.NewUpdateUserAction(f.db, testServiceSessionReturner(f.db), newTestTasks(f.db)).Execute(ctx, actor, target.User.ID, useraction.UpdateInput{
			DisplayName: target.OrganizationIdentity.DisplayName, RoleID: target.User.RoleID,
			HandlesServiceRequests: target.OrganizationIdentity.HandlesServiceRequests, MaxServiceSessions: 10,
		})
		return err
	default:
		_, err := testUserStatusAction(f.db).Execute(ctx, actor, target.User.ID, domain.IdentityStatusActive)
		return err
	}
}
