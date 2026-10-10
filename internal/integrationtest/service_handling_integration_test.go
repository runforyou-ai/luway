//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/stretchr/testify/require"
)

// TestServiceHandlingAuthorization 验证只有开启接待的成员可以领取、对客回复、关闭与重开客服周期，未开启的成员仍可写内部备注，也不能作为转交目标。
func TestServiceHandlingAuthorization(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	coordinator := newGroupAgentCoordinator(f.db)
	// 群主恢复管理员角色，修改成员需要工作区保留有效管理员。
	_, err := f.db.NewUpdate().Table("users").
		Set("role_id = (SELECT id FROM roles WHERE workspace_id = ? AND kind = ?)", f.owner.Workspace.ID, domain.RoleKindAdmin).
		Where("id = ?", f.owner.User.ID).Exec(ctx)
	require.NoError(t, err)
	setHandlesServiceRequests := func(handlesServiceRequests bool) {
		t.Helper()
		_, err := useraction.NewUpdateUserAction(f.db, testServiceSessionReturner(f.db), testEnqueuer).Execute(ctx, f.owner, f.member.User.ID, useraction.UpdateInput{
			DisplayName: f.member.WorkspaceIdentity.DisplayName, RoleID: f.member.User.RoleID, HandlesServiceRequests: handlesServiceRequests, MaxServiceSessions: 10,
		})
		require.NoError(t, err)
	}
	expectHandlingRequired := func(step string, err error) {
		t.Helper()
		var conflict *conversationaction.ConflictError
		require.ErrorAs(t, err, &conflict, step)
		require.Equal(t, servicesessionaction.ConflictReasonServiceHandlingRequired, conflict.Reason, step)
	}

	setHandlesServiceRequests(false)
	claim := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, testEnqueuer)
	_, err = claim.Execute(ctx, f.member, f.conversationID)
	expectHandlingRequired("未开启接待领取", err)
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer)
	_, err = send.Execute(ctx, f.member, servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "未开启接待的回复",
	})
	expectHandlingRequired("未开启接待对客回复", err)

	// 未开启接待的成员仍可写内部备注。
	_, err = send.Execute(ctx, f.member, servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(),
		Body: "未开启接待的内部备注", Visibility: domain.MessageVisibilityInternal,
	})
	require.NoError(t, err, "未开启接待写内部备注")

	// 未开启接待的成员不能作为转交目标。
	_, err = claim.Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	transfer := servicesessionaction.NewTransferServiceSessionAction(f.db, coordinator, agentrunaction.NewScheduler(testEnqueuer), testEnqueuer)
	_, err = transfer.Execute(ctx, f.owner, servicesessionaction.TransferServiceSessionInput{
		ConversationID: f.conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.member.WorkspaceIdentity.ID,
	})
	var validation *conversationaction.ValidationError
	require.ErrorAs(t, err, &validation, "转交给未开启接待的成员")
	require.Equal(t, conversationaction.ValidationTargetIdentityIDInvalid, validation.Fields["identityId"], "转交给未开启接待的成员")

	// 开启接待后领取、关闭与重开都可用。
	setHandlesServiceRequests(true)
	_, err = transfer.Execute(ctx, f.owner, servicesessionaction.TransferServiceSessionInput{
		ConversationID: f.conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.member.WorkspaceIdentity.ID,
	})
	require.NoError(t, err, "转交给开启接待的成员")
	closeSession := servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, testEnqueuer)
	_, err = closeSession.Execute(ctx, f.member, f.conversationID)
	require.NoError(t, err, "开启接待关闭周期")

	// 关闭开关后连重开也被拒绝。
	setHandlesServiceRequests(false)
	reopen := servicesessionaction.NewReopenServiceSessionAction(f.db, testEnqueuer)
	_, err = reopen.Execute(ctx, f.member, f.conversationID)
	expectHandlingRequired("未开启接待重开", err)
	_, err = closeSession.Execute(ctx, f.member, f.conversationID)
	expectHandlingRequired("未开启接待关闭", err)
}
