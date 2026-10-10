//go:build server

package integrationtest

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// loadSyncHeads 读取当前用户的同步探针值。
func loadSyncHeads(t *testing.T, db *bun.DB, identity *servermodels.Identity) inboxaction.SyncHeads {
	t.Helper()
	heads, err := inboxaction.NewLoadInboxQuery(db).SyncHeads(context.Background(), identity)
	require.NoError(t, err)
	return heads
}

// loadConversationVersion 读取会话当前版本。
func loadConversationVersion(t *testing.T, db *bun.DB, conversationID string) int64 {
	t.Helper()
	var version int64
	require.NoError(t, db.NewSelect().Table("conversations").Column("version").Where("id = ?", conversationID).Scan(context.Background(), &version))
	return version
}

// TestConversationVersionAppend 验证并发追加逐次推进会话版本，回滚和幂等重放不推进。
func TestConversationVersionAppend(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	send := newGroupSendAction(f.db)
	before := loadConversationVersion(t, f.db, f.groupID)
	const writers = 8
	start := make(chan struct{})
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for index := range writers {
		identity := f.owner
		if index%2 == 1 {
			identity = f.member
		}
		wg.Go(func() {
			<-start
			_, err := send.Execute(ctx, identity, groupchataction.GroupTextMessageInput{ConversationID: f.groupID, ClientMessageID: uuid.NewV7().String(), Body: "并发消息"})
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	current := loadConversationVersion(t, f.db, f.groupID)
	require.Equal(t, before+writers, current, "concurrent version")

	errRollback := errors.New("rollback")
	cv := &servermodels.Conversation{ID: f.groupID}
	key := uuid.NewV7().String()
	// 同一幂等键依次验证回滚、首次提交和重放。
	for _, step := range []struct {
		name    string
		fail    bool
		advance int64
	}{{"rollback", true, 0}, {"commit", false, 1}, {"replay", false, 0}} {
		err := realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
			if err := tx.NewSelect().Model(cv).WherePK().For("UPDATE").Scan(ctx); err != nil {
				return err
			}
			message := &servermodels.Message{ID: uuid.NewV7().String(), WorkspaceID: cv.WorkspaceID, ConversationID: cv.ID, Type: "text", Body: "版本回滚", OriginatedAt: time.Now().UTC(), IdempotencyKey: &key}
			if _, _, err := chatstate.AppendMessage(ctx, tx, testEnqueuer, cv, message); err != nil {
				return err
			}
			if step.fail {
				return errRollback
			}
			return nil
		})
		if step.fail {
			require.ErrorIs(t, err, errRollback, step.name)
		} else {
			require.NoError(t, err, step.name)
		}
		require.Equal(t, current+step.advance, loadConversationVersion(t, f.db, f.groupID), "%s version", step.name)
		current += step.advance
	}
}

// TestSyncHeadsConversationChanges 验证探针覆盖消息、个人状态、群资料、成员变化与企业隔离。
func TestSyncHeadsConversationChanges(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	// expectHeads 执行变化后核对校验和是否变化及可见会话数量，身份资料版本保持不变。
	expectHeads := func(name string, changed bool, count int, change func()) {
		t.Helper()
		before := loadSyncHeads(t, f.db, f.member)
		change()
		after := loadSyncHeads(t, f.db, f.member)
		require.Equal(t, changed, after.ConversationChecksum != before.ConversationChecksum, "%s: before=%+v after=%+v", name, before, after)
		require.Equal(t, count, after.ConversationCount, "%s: before=%+v after=%+v", name, before, after)
		require.Equal(t, before.IdentityProfileVersion, after.IdentityProfileVersion, "%s: before=%+v after=%+v", name, before, after)
	}
	stateRows, err := f.db.NewSelect().Table("conversation_user_states").Where("conversation_id = ? AND user_id = ?", f.groupID, f.member.User.ID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, stateRows, "member state rows")
	expectHeads("无个人状态行时追加消息", true, 1, func() { f.send(t, f.owner, "尚无个人状态", false) })

	var second groupchataction.GroupConversationSummary
	expectHeads("加入新群", true, 2, func() {
		second, err = groupchataction.NewCreateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: "低版本群", MemberIdentityIDs: []string{f.member.WorkspaceIdentity.ID}})
		require.NoError(t, err)
	})
	for range 3 {
		f.send(t, f.owner, "推高第一个群版本", false)
	}
	expectHeads("低版本会话追加消息", true, 2, func() {
		_, err := newGroupSendAction(f.db).Execute(ctx, f.owner, groupchataction.GroupTextMessageInput{ConversationID: second.ID, ClientMessageID: uuid.NewV7().String(), Body: "低版本群消息"})
		require.NoError(t, err)
	})

	last := f.send(t, f.owner, "待读", false)
	mention := f.send(t, f.owner, "提及", false, f.subjectID)
	groupVersion := loadConversationVersion(t, f.db, f.groupID)
	read := conversationaction.NewMarkConversationReadAction(f.db)
	mute := conversationaction.NewUpdateConversationNotificationSettingsAction(f.db)
	mark := conversationaction.NewUpdateConversationUnreadMarkAction(f.db)
	review := conversationaction.NewMarkConversationMentionReviewedAction(f.db)
	for _, step := range []struct {
		name   string
		change func() error
	}{
		{"已读", func() error { _, err := read.Execute(ctx, f.member, f.groupID, last.ID, false); return err }},
		{"静音", func() error { _, err := mute.Execute(ctx, f.member, f.groupID, true); return err }},
		{"手动未读", func() error { return mark.Execute(ctx, f.member, f.groupID, true) }},
		{"确认提及", func() error { _, err := review.Execute(ctx, f.member, f.groupID, mention.ID); return err }},
	} {
		// 首次操作改变个人状态，重复同一操作不产生变化。
		for attempt, changed := range []bool{true, false} {
			expectHeads(step.name+strconv.Itoa(attempt), changed, 2, func() {
				require.NoError(t, step.change())
			})
		}
	}
	require.Equal(t, groupVersion, loadConversationVersion(t, f.db, f.groupID), "personal states moved conversation version")

	expectHeads("修改群描述", true, 2, func() {
		_, err := groupchataction.NewUpdateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationProfileInput{ConversationID: f.groupID, Title: "导航测试群", Description: "新的描述"})
		require.NoError(t, err)
	})
	expectHeads("移出群", true, 1, func() {
		_, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, testEnqueuer, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{ConversationID: second.ID, MemberIdentityID: f.member.WorkspaceIdentity.ID})
		require.NoError(t, err)
	})

	// 两个群的会话版本与个人状态版本对齐后交换可见性，数量不变而校验和变化。
	groupIDs := bun.List([]string{f.groupID, second.ID})
	_, err = f.db.NewRaw("UPDATE conversations SET version = 100 WHERE id IN (?)", groupIDs).Exec(ctx)
	require.NoError(t, err)
	_, err = f.db.NewRaw("DELETE FROM conversation_user_states WHERE user_id = ? AND conversation_id IN (?)", f.member.User.ID, groupIDs).Exec(ctx)
	require.NoError(t, err)
	expectHeads("同版本会话互换", true, 1, func() {
		_, err := f.db.NewRaw("UPDATE conversation_participants SET left_at = CASE WHEN conversation_id = ? THEN now() END WHERE subject_id = ? AND conversation_id IN (?)", f.groupID, f.subjectID, groupIDs).Exec(ctx)
		require.NoError(t, err)
	})

	other := newNavigationFixture(t)
	expectHeads("其他企业追加消息", false, 1, func() { other.send(t, other.owner, "其他企业", false) })
}

// TestSyncHeadsIdentityProfile 验证身份资料与账户偏好实际变化时推进版本，重复保存不推进。
func TestSyncHeadsIdentityProfile(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	lonelyEmail, renamedEmail := servertest.UniqueEmail("lonely"), servertest.UniqueEmail("renamed")
	_, err := newTestMemberCreator(f.db, testEnqueuer).Execute(ctx, f.owner, memberSpec{HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: "无会话成员", Email: lonelyEmail, Password: "password123", RoleID: f.owner.User.RoleID})
	require.NoError(t, err)
	login := servertest.LoginMember(t, f.db, f.owner.Workspace.ID, lonelyEmail, "password123")
	lonely := login.Identity
	workStatus := useraction.NewUpdateWorkStatusAction(f.db, testEnqueuer)
	// renamed 记录名称是否在本步实际变化，名称变化推进所在会话版本并改变会话校验和。
	renamed := false
	// expectProfile 执行变化后核对身份资料版本是否推进，会话数量不变，校验和只随名称变化改变。
	expectProfile := func(name string, changed bool, change func() error) {
		t.Helper()
		before := loadSyncHeads(t, f.db, lonely)
		require.NoError(t, change(), name)
		after := loadSyncHeads(t, f.db, lonely)
		require.Equal(t, changed, after.IdentityProfileVersion != before.IdentityProfileVersion, "%s: before=%+v after=%+v", name, before, after)
		require.Equal(t, before.ConversationCount, after.ConversationCount, "%s: before=%+v after=%+v", name, before, after)
		require.Equal(t, renamed, after.ConversationChecksum != before.ConversationChecksum, "%s: before=%+v after=%+v", name, before, after)
	}
	empty := loadSyncHeads(t, f.db, lonely)
	require.Equal(t, 0, empty.ConversationCount, "empty heads")
	require.Equal(t, "0", empty.ConversationChecksum, "empty heads")
	expectProfile("无会话时修改工作状态", true, func() error {
		_, err := workStatus.Execute(ctx, lonely, useraction.WorkStatusInput{WorkStatus: domain.WorkStatusOffDuty})
		return err
	})
	// 加入群后在已有会话上验证资料变化：名称变化推进所在会话版本并改变会话校验和，其余资料变化不改变。
	_, err = groupchataction.NewAddGroupConversationMembersAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationMembersInput{ConversationID: f.groupID, MemberIdentityIDs: []string{lonely.WorkspaceIdentity.ID}})
	require.NoError(t, err)
	preferences := useraction.NewUpdatePreferencesAction(f.db)
	profile := useraction.NewUpdateProfileAction(f.db)
	shanghai := useraction.PreferencesInput{Locale: domain.Locale(lonely.Account.Locale), TimeZone: "Asia/Shanghai", MessageNotificationsEnabled: lonely.User.MessageNotificationsEnabled}
	for _, step := range []struct {
		name   string
		change func() error
	}{
		{"工作状态", func() error {
			_, err := workStatus.Execute(ctx, lonely, useraction.WorkStatusInput{WorkStatus: domain.WorkStatusAway})
			return err
		}},
		{"恢复工作中", func() error {
			_, err := workStatus.Execute(ctx, lonely, useraction.WorkStatusInput{WorkStatus: domain.WorkStatusWorking})
			return err
		}},
		{"账户偏好", func() error { _, err := preferences.Execute(ctx, lonely, shanghai); return err }},
		{"个人资料", func() error {
			_, err := profile.Execute(ctx, lonely, useraction.ProfileInput{DisplayName: "改名成员", Email: lonelyEmail})
			return err
		}},
		{"修改账号邮箱", func() error {
			_, err := profile.Execute(ctx, lonely, useraction.ProfileInput{DisplayName: "改名成员", Email: renamedEmail})
			return err
		}},
	} {
		// 首次保存改变资料，重复保存同一值不推进版本；只有个人资料一步修改名称。
		for attempt, changed := range []bool{true, false} {
			renamed = step.name == "个人资料" && changed
			expectProfile(step.name+strconv.Itoa(attempt), changed, step.change)
		}
		renamed = false
	}
	// 登录保持身份资料版本。
	expectProfile("登录", false, func() error {
		login = servertest.LoginMember(t, f.db, f.owner.Workspace.ID, renamedEmail, "password123")
		return nil
	})

	backend := direct.New(f.db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, f.db)}, nil, nil, nil, testEnqueuer, nil, nil, nil)
	heads, err := backend.GetSyncHeads(ctx, appservice.RequestMeta{Token: login.Token, WorkspaceID: f.owner.Workspace.ID})
	stored := loadSyncHeads(t, f.db, lonely)
	require.NoError(t, err)
	require.Equal(t, 1, heads.ConversationCount, "backend heads=%+v stored=%+v", heads, stored)
	require.Equal(t, stored.ConversationChecksum, heads.ConversationChecksum, "backend heads=%+v stored=%+v", heads, stored)
	require.Equal(t, strconv.FormatInt(stored.IdentityProfileVersion, 10), heads.IdentityProfileVersion, "backend heads=%+v stored=%+v", heads, stored)
}
