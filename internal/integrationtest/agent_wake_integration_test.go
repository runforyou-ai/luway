//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/einorun"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/servertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wakeSignalTimeout 是等待唤醒信号的时限，明显短于兜底读取间隔。
const wakeSignalTimeout = 2 * time.Second

// TestAgentInputWakesRunningRun 验证运行中的 AI 聊天收到新消息时，写入持久输入的事务提交后执行侧立即收到新增输入信号并读到新输入：
// 未启动发布器时在进程内送达；启动发布器时经消息总线送达，另一服务端实例订阅同一输入队列同样收到信号。
func TestAgentInputWakesRunningRun(t *testing.T) {
	t.Parallel()
	db, identity, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		DisplayName: "唤醒助手",
		Execution:   agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID, SystemInstruction: "回答问题"}},
	})
	require.NoError(t, err)
	tasks := servertest.NewTasks()
	send := directchataction.NewSendAgentTextMessageAction(db, testEnqueuer, agentrunaction.NewScheduler(tasks))
	for _, withPublisher := range []bool{false, true} {
		name := "进程内"
		if withPublisher {
			name = "消息总线"
		}
		t.Run(name, func(t *testing.T) {
			var remote <-chan struct{}
			first, run := createAgentLockChat(t, ctx, db, identity, agent.IdentityID, tasks)
			if withPublisher {
				channel := servertest.BusChannel()
				startTestPublisherOn(t, openRealtimeDB(t), channel)
				// 另一服务端实例订阅同一输入队列的新增输入信号。
				other := servertest.StartBus(t, openRealtimeDB(t), channel, uuid.NewV7().String())
				signals := make(chan struct{}, 1)
				unsubscribe, err := other.Subscribe(realtime.Topic(identity.Workspace.ID, realtime.AudienceAgentLane, run.LaneID), func([]byte) {
					select {
					case signals <- struct{}{}:
					default:
					}
				})
				require.NoError(t, err)
				t.Cleanup(unsubscribe)
				require.NoError(t, other.Sync(ctx))
				remote = signals
			} else {
				// 独占进程内活动发布器的位置，保证运行期间没有发布器启动。
				realtimePublisherLock.Lock()
				t.Cleanup(realtimePublisherLock.Unlock)
			}
			runtime := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
				wake, unwatch, _ := feed.Watch(ctx)
				defer unwatch()
				triggers, err := pendingTriggers(ctx, feed, 0)
				if !assert.NoError(t, err, "initial peek") || !assert.Len(t, triggers, 1, "initial triggers") {
					return agentruntime.RunResult{}, errors.New("unexpected initial input")
				}
				_, err = send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "补充输入"})
				if !assert.NoError(t, err, "send follow-up") {
					return agentruntime.RunResult{}, err
				}
				select {
				case <-wake:
				case <-time.After(wakeSignalTimeout):
					assert.Fail(t, "running run was not woken by the follow-up input")
					return agentruntime.RunResult{}, errors.New("no wake signal")
				}
				triggers, err = pendingTriggers(ctx, feed, 1)
				if !assert.NoError(t, err, "peek after wake") || !assert.Len(t, triggers, 1, "triggers after wake") {
					return agentruntime.RunResult{}, errors.New("follow-up input not visible")
				}
				assert.Equal(t, int64(2), triggers[0].Seq, "follow-up sequence")
				return completeTestRun(ctx, feed, "都看到了")
			}}
			require.NoError(t, newTestAgentRun(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).
				Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
			assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusSucceeded)
			if remote != nil {
				select {
				case <-remote:
				case <-time.After(wakeSignalTimeout):
					require.Fail(t, "other instance did not receive the input signal")
				}
			}
		})
	}
}
