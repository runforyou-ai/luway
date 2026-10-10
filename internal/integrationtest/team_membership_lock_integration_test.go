//go:build server

package integrationtest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// TestTeamMembershipWritesSerializeWithDeletion 验证团队成员添加和成员资料写入与团队删除互斥，两个提交顺序均保持关联完整。
func TestTeamMembershipWritesSerializeWithDeletion(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"add", "update_user"} {
		for _, deleteFirst := range []bool{false, true} {
			name := operation + "/write_first"
			if deleteFirst {
				name = operation + "/delete_first"
			}
			t.Run(name, func(t *testing.T) {
				f := newNavigationFixture(t)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				team, err := teamaction.NewCreateTeamAction(f.db).Execute(ctx, f.owner, teamaction.Input{Name: "并发团队"})
				require.NoError(t, err)
				f.db.AddQueryHook(chatQueryHook{})
				gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
					if deleteFirst {
						return strings.Contains(event.Query, "FROM teams AS t") && strings.Contains(event.Query, "FOR UPDATE")
					}
					return strings.HasPrefix(event.Query, `INSERT INTO "team_members"`)
				})
				defer gate.open()
				written, deleted := make(chan error, 1), make(chan error, 1)
				gated := context.WithValue(ctx, chatQueryGateKey{}, gate)
				if deleteFirst {
					go func() {
						deleted <- teamaction.NewDeleteTeamAction(f.db, testEnqueuer).Execute(gated, f.member, team.ID)
					}()
					waitChatSignal(t, ctx, gate.reached)
					go func() { written <- writeTeamMembership(ctx, f, operation, team.ID) }()
				} else {
					go func() { written <- writeTeamMembership(gated, f, operation, team.ID) }()
					waitChatSignal(t, ctx, gate.reached)
					go func() {
						deleted <- teamaction.NewDeleteTeamAction(f.db, testEnqueuer).Execute(ctx, f.member, team.ID)
					}()
				}
				waitChatDatabaseLock(t, ctx, f.db, "FROM teams AS t", team.ID)
				gate.open()
				writeErr := waitChatResult(t, ctx, written)
				if deleteFirst {
					var fields *common.FieldError
					require.True(t, errors.Is(writeErr, teamaction.ErrNotFound) || (errors.As(writeErr, &fields) && fields.Fields["teamIds"] == useraction.ValidationTeamInvalid), "团队删除后写入关联 = %v", writeErr)
				} else {
					require.NoError(t, writeErr, "先写入团队关联失败")
				}
				require.NoError(t, waitChatResult(t, ctx, deleted))
				count, err := f.db.NewSelect().Table("team_members").Where("team_id = ?", team.ID).Count(ctx)
				require.NoError(t, err)
				require.Zero(t, count, "团队删除后残留关联")
			})
		}
	}
}

// writeTeamMembership 从团队页面或成员编辑入口写入指定团队关系。
func writeTeamMembership(ctx context.Context, f navigationFixture, operation, teamID string) error {
	switch operation {
	case "add":
		_, err := teamaction.NewAddMembersAction(f.db, testEnqueuer).Execute(ctx, f.owner, teamID, []teamaction.MemberIdentity{{
			IdentityType: domain.WorkspaceIdentityTypeUser, IdentityID: f.owner.WorkspaceIdentity.ID,
		}})
		return err
	default:
		_, err := useraction.NewUpdateUserAction(f.db, testServiceSessionReturner(f.db), testEnqueuer).Execute(ctx, f.owner, f.owner.User.ID, useraction.UpdateInput{
			DisplayName: f.owner.WorkspaceIdentity.DisplayName, RoleID: f.owner.User.RoleID,
			HandlesServiceRequests: f.owner.WorkspaceIdentity.HandlesServiceRequests, MaxServiceSessions: 10, TeamIDs: []string{teamID},
		})
		return err
	}
}
