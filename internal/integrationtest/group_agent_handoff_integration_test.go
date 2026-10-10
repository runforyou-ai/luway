//go:build server

package integrationtest

import (
	"context"
	"fmt"
	"testing"

	"github.com/runforyou-ai/einorun"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// submitReply 以指定正文结束一次群内运行，并返回执行的 AI 员工身份。
func (f groupAgentFixture) submitReply(t *testing.T, content string, inspect func(agentruntime.RunRequest)) string {
	t.Helper()
	ctx := context.Background()
	run := f.activeRun(t)
	require.NotNil(t, run, "群内没有待执行的运行")
	runtime := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		triggers, err := pendingTriggers(ctx, feed, 0)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		claimed, err := feed.Claim(ctx, triggers[len(triggers)-1].Seq)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		if inspect != nil {
			inspect(request)
		}
		return agentruntime.RunResult{Content: content, EndSeq: claimed.EndSeq}, nil
	}}
	require.NoError(t, newTestAgentRun(f.db, f.tasks, runtime, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	return run.AgentIdentityID
}

// latestInput 读取指定队列最新一条输入。
func (f groupAgentFixture) latestInput(t *testing.T, laneID string) *servermodels.AgentInput {
	t.Helper()
	input := &servermodels.AgentInput{}
	require.NoError(t, f.db.NewSelect().Model(input).
		Where("ai.lane_id = ?", laneID).
		OrderExpr("ai.input_seq DESC").Limit(1).Scan(context.Background()))
	return input
}

// countMessageMentions 统计群内指定正文消息的提醒关系数量。
func (f groupAgentFixture) countMessageMentions(t *testing.T, body string) int {
	t.Helper()
	count, err := f.db.NewSelect().Model((*servermodels.MessageMention)(nil)).
		Join("JOIN messages AS msg ON msg.id = mm.message_id").
		Where("msg.conversation_id = ? AND msg.body = ?", f.groupID, body).Count(context.Background())
	require.NoError(t, err)
	return int(count)
}

// TestGroupAgentHandoff 验证正文点名、按轮次合并的接力与不设深度上限的接力链。
func TestGroupAgentHandoff(t *testing.T) {
	t.Parallel()
	db, identity, providerID, modelID := newAIWorkspace(t)
	t.Helper()
	ctx := context.Background()
	agents := newGroupAgentCollaborators(t, db, identity, providerID, modelID)

	t.Run("正文点名另一位 AI 员工形成接力", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "请第一位看一下", []string{f.agents[0].IdentityID}, "")
		content := fmt.Sprintf("成本我算过，请 @%s 确认排期", f.agents[1].DisplayName)
		first := f.submitReply(t, content, nil)
		next := f.activeRun(t)
		require.NotNil(t, next, "接力未安排到被点名的 AI 员工")
		require.NotEqual(t, first, next.AgentIdentityID)
		input := &servermodels.AgentInput{}
		require.NoError(t, db.NewSelect().Model(input).
			Where("ai.lane_id = ?", next.LaneID).
			OrderExpr("ai.input_seq DESC").Limit(1).Scan(ctx))
		require.Equal(t, string(domain.AgentInputKindHandoff), input.Kind)
		// 接力挂在发起方的公开回复上，被点名方据此看到确认内容。
		var sourceBody string
		require.NoError(t, db.NewSelect().Model((*servermodels.Message)(nil)).
			ColumnExpr("msg.body").Where("msg.id = ?", input.SourceMessageID).Scan(ctx, &sourceBody))
		require.Equal(t, content, sourceBody)
		require.Equal(t, 1, f.countMessageMentions(t, content), "接力回复的提醒关系")
	})

	t.Run("本轮尚未发言的 AI 员工被点名时并入本轮发言", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "两位看下", []string{f.agents[0].IdentityID, f.agents[1].IdentityID}, "")
		require.Equal(t, f.agents[0].IdentityID, f.submitReply(t, fmt.Sprintf("@%s 请补充", f.agents[1].DisplayName), nil), "首位发言者")
		require.Equal(t, f.agents[1].IdentityID, f.submitReply(t, "补充完毕", nil), "第二位发言者")
		require.Nil(t, f.activeRun(t), "本轮结束后不应再安排发言")
		count, err := db.NewSelect().Model((*servermodels.AgentRun)(nil)).
			Where("agr.conversation_id = ? AND agr.agent_identity_id = ?", f.groupID, f.agents[1].IdentityID).Count(ctx)
		require.NoError(t, err)
		require.Equal(t, int64(1), count, "被点名 AI 员工的运行次数")
	})

	t.Run("接力不设深度上限", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "请第一位看一下", []string{f.agents[0].IdentityID}, "")
		// 两位 AI 员工持续互相点名，每一轮都安排被点名方发言。
		for round := range 6 {
			run := f.activeRun(t)
			require.NotNil(t, run, "第 %d 轮接力未安排发言", round+1)
			target := f.agents[0].DisplayName
			if run.AgentIdentityID == f.agents[0].IdentityID {
				target = f.agents[1].DisplayName
			}
			f.submitReply(t, fmt.Sprintf("@%s 继续确认", target), nil)
		}
		require.NotNil(t, f.activeRun(t), "连续接力后应继续安排被点名方发言")
		f.submitReply(t, "确认完毕", nil)
		require.Nil(t, f.activeRun(t), "没有点名时接力应结束")
	})

	t.Run("点名真人只写提醒不安排执行", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "请第一位看一下", []string{f.agents[0].IdentityID}, "")
		content := fmt.Sprintf("请 @%s 确认", identity.WorkspaceIdentity.DisplayName)
		f.submitReply(t, content, nil)
		require.Nil(t, f.activeRun(t), "点名真人不应安排执行")
		require.Equal(t, 1, f.countMessageMentions(t, content), "点名真人的提醒关系")
	})

	t.Run("无法匹配的点名与代码中的 @ 保持普通文字", func(t *testing.T) {
		f := newGroupAgentFixture(t, db, identity, agents)
		f.post(t, "请第一位看一下", []string{f.agents[0].IdentityID}, "")
		content := fmt.Sprintf("@查无此人 请看，示例 `@%s`", f.agents[1].DisplayName)
		f.submitReply(t, content, nil)
		require.Nil(t, f.activeRun(t), "无效点名不应安排执行")
		require.Zero(t, f.countMessageMentions(t, content), "无效点名的提醒关系")
	})
}
