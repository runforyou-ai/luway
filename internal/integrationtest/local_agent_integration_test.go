//go:build server

package integrationtest

import (
	"context"
	"strings"
	"testing"
	"uuid"

	"github.com/runforyou-ai/einorun"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess/processquery"
	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/actions/conversationfile"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/common/textfile"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// testLocalAgentDelegation 验证个人 AI 员工委派本机 Agent：一轮交给会话后运行立即结束，电脑领取的轮次不随运行结束中止也不占同时执行上限；
// 过程更新按序号幂等写入，权限请求由发起人确认后随领取交给电脑，完成后本机 Agent 的回复以 AI 员工身份写入会话；
// 续接沿用同一会话与 ACP 会话编号，停止时释放会话、中止执行中的轮次并记为已中断。
func testLocalAgentDelegation(t *testing.T, f *personalAgentFixture, modelID string) {
	ctx, db := f.ctx, f.db
	f.online()
	operations := computeraction.NewOperationsAction(db, newTestToolDecisions(db, f.tasks))
	require.NoError(t, computeraction.NewReportCapabilitiesAction(db).Execute(ctx, f.computerIdentity(), computeraction.CapabilitiesInput{
		Capabilities:    domain.ComputerCapabilities{Shell: "bash", LocalAgents: []domain.ComputerLocalAgent{{Name: "codex", Description: "编程 Agent"}, {Name: "other"}}},
		ExecutorVersion: domain.ExecutorVersion, MaxConcurrency: 1,
	}))
	updated, err := agentaction.NewUpdatePersonalAgentAction(db).Execute(ctx, f.identity, f.personalAgent.ID, agentaction.PersonalAgentInput{
		DisplayName: f.personalAgent.DisplayName, LocalAgents: []string{" codex ", "codex"},
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID}},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"codex"}, updated.LocalAgents)
	require.Len(t, updated.ComputerLocalAgents, 2)

	conversationID := f.personalAgentChat()
	run := f.sendAndLoadRun(conversationID, "把登录页改成深色")
	f.model.use("local_agent", `{"agent":"codex","request":"把登录页改成深色","context":"项目在 web 目录"}`)
	f.run(run.ID)
	f.assertStatus(run.ID, domain.AgentRunStatusSucceeded)
	f.assertReply(run.ID, "结果："+agentcontract.LocalAgentSubmitted("codex"))
	turn := f.computerCall(run.ID)
	require.Equal(t, string(domain.AgentToolCallQueued), turn.Status, "turn stays with the session after the run ends")
	require.NotNil(t, turn.LocalAgentSessionID)
	require.Equal(t, domain.ComputerOperationLocalAgent, turn.Operation.Operation.Kind)
	require.Equal(t, *turn.LocalAgentSessionID, turn.Operation.Operation.Session)
	require.Equal(t, "", turn.Operation.Operation.AgentSession)
	require.Equal(t, conversationID, turn.Operation.Operation.Folder)
	require.Equal(t, agentruntime.LocalAgentPrompt("把登录页改成深色", "项目在 web 目录"), turn.Operation.Operation.Prompt)
	session := &servermodels.LocalAgentSession{}
	require.NoError(t, db.NewSelect().Model(session).Where("las.id = ?", *turn.LocalAgentSessionID).Scan(ctx))
	require.Equal(t, string(domain.LocalAgentSessionActive), session.Status)
	require.Equal(t, conversationID, session.ConversationID)
	require.Equal(t, f.personalAgent.ID, session.AgentID)

	// 运行以失败或取消结束时不结算已交给会话的轮次。
	require.NoError(t, realtime.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		return agentprocess.SettleEndedRuns(ctx, tx, f.identity.Workspace.ID, run.ID)
	}))
	require.Equal(t, string(domain.AgentToolCallQueued), f.computerCall(run.ID).Status)

	// 运行恢复时已交给会话的轮次按已交给处理，不随运行结束结算。
	blocks, _, err := agentprocess.Load(ctx, db, f.identity.Workspace.ID, run.ID)
	require.NoError(t, err)
	found := false
	for _, block := range blocks {
		if block.Payload.ToolCall != nil && block.Payload.ToolCall.ID == turn.ID {
			found = true
			require.Equal(t, "codex", block.Payload.ToolCall.LocalAgent)
		}
	}
	require.True(t, found)

	// 电脑上已有一个执行中的操作占满同时执行上限时，委派的轮次仍被领取且不设时限。
	claimed, err := operations.Claim(ctx, f.computerIdentity(), computeraction.ClaimInput{Limit: 0})
	require.NoError(t, err)
	require.Len(t, claimed.Operations, 1)
	require.Equal(t, turn.ID, claimed.Operations[0].ID)
	require.Zero(t, claimed.Operations[0].Timeout)
	held := computeraction.ClaimInput{Limit: 1, Running: []string{turn.ID}, Sessions: []string{session.ID}}
	claimed, err = operations.Claim(ctx, f.computerIdentity(), held)
	require.NoError(t, err)
	require.Empty(t, claimed.Abort, "run ended but the turn belongs to the session")
	require.Empty(t, claimed.Released)

	testComputerSharedFiles(t, f, turn.ID)

	// 过程更新按序号写入，重复上报保留首次写入，只在调用执行中接受。
	process := processquery.NewToolCallProcessQuery(db)
	require.NoError(t, operations.ReportUpdates(ctx, f.computerIdentity(), turn.ID, []computeraction.UpdateInput{
		{Seq: 1, Update: domain.ToolCallUpdate{Kind: domain.ToolCallUpdateMessage, Text: "我来看看"}},
		{Seq: 2, Update: domain.ToolCallUpdate{Kind: domain.ToolCallUpdateStep, Step: &domain.ToolCallStep{ID: "s1", Title: "编辑 login.css", Status: "in_progress"}}},
	}))
	require.NoError(t, operations.ReportUpdates(ctx, f.computerIdentity(), turn.ID, []computeraction.UpdateInput{
		{Seq: 1, Update: domain.ToolCallUpdate{Kind: domain.ToolCallUpdateMessage, Text: "重复"}},
	}))
	read, err := process.Execute(ctx, f.identity, turn.ID, 1)
	require.NoError(t, err)
	require.Equal(t, domain.AgentToolCallRunning, read.Status)
	require.Equal(t, "codex", *read.LocalAgent)
	require.Len(t, read.Updates, 2)
	require.Equal(t, "我来看看", read.Updates[0].Update.Text)
	later, err := process.Execute(ctx, f.identity, turn.ID, 2)
	require.NoError(t, err)
	require.Len(t, later.Updates, 1)
	other := newChatLockUser(t, db, f.identity)
	_, err = process.Execute(ctx, other, turn.ID, 1)
	require.ErrorIs(t, err, processquery.ErrToolCallProcessUnavailable, "other member cannot read the private chat")

	// 权限请求交给这一轮的发起人确认，确认后在下次领取时取回允许的处理方式。
	requestID := uuid.NewV7().String()
	permission := domain.LocalAgentPermission{Step: domain.ToolCallStep{ID: "s1", Title: "编辑 login.css", Kind: "edit"},
		Options: []domain.LocalAgentPermissionOption{{ID: "allow", Name: "允许", Kind: "allow_once"}, {ID: "reject", Name: "拒绝", Kind: "reject_once"}}}
	input := computeraction.PermissionInput{ID: requestID, Permission: permission}
	require.NoError(t, operations.RequestPermission(ctx, f.computerIdentity(), turn.ID, input))
	require.NoError(t, operations.RequestPermission(ctx, f.computerIdentity(), turn.ID, input), "repeated report is idempotent")
	decisions := newTestToolDecisions(db, f.tasks)
	pending, err := decisions.List(ctx, f.identity)
	require.NoError(t, err)
	var asked *agentprocess.ToolDecision
	for index := range pending {
		if pending[index].ID == requestID {
			asked = &pending[index]
		}
	}
	require.NotNil(t, asked, "permission request is pending for the initiator")
	require.True(t, asked.CanDecide)
	require.Equal(t, "codex", *asked.LocalAgent)
	require.Equal(t, "编辑 login.css", asked.Name)
	waiting := held
	waiting.Permissions = []string{requestID}
	claimed, err = operations.Claim(ctx, f.computerIdentity(), waiting)
	require.NoError(t, err)
	require.Empty(t, claimed.Permissions, "undecided request is not returned")
	require.NoError(t, decisions.Decide(ctx, f.identity, requestID, true))
	claimed, err = operations.Claim(ctx, f.computerIdentity(), waiting)
	require.NoError(t, err)
	require.Equal(t, []computeraction.PermissionResult{{ID: requestID, OptionID: "allow"}}, claimed.Permissions)
	f.assertStatus(run.ID, domain.AgentRunStatusSucceeded)
	// 过期的权限请求按权限请求结算，不当作暂停运行等待确认的调用。
	expiringID := uuid.NewV7().String()
	require.NoError(t, operations.RequestPermission(ctx, f.computerIdentity(), turn.ID, computeraction.PermissionInput{ID: expiringID, Permission: permission}))
	_, err = db.NewUpdate().Model((*servermodels.AgentToolCall)(nil)).Set("expires_at = now() - interval '1 second'").Where("id = ?", expiringID).Exec(ctx)
	require.NoError(t, err)
	require.NoError(t, decisions.Expire(ctx, agentprocess.ToolCallInput{WorkspaceID: f.identity.Workspace.ID, ToolCallID: expiringID}))
	expiring := &servermodels.AgentToolCall{}
	require.NoError(t, db.NewSelect().Model(expiring).Where("atc.id = ?", expiringID).Scan(ctx))
	require.NotEqual(t, string(domain.AgentToolCallAwaitingDecision), expiring.Status, "expired permission request still waiting")
	require.Empty(t, expiring.Decision, "permission request recorded as a paused decision")
	// 运行的过程保存时不删除权限请求。
	var requests int64
	requests, err = db.NewSelect().Model((*servermodels.AgentToolCall)(nil)).Where("atc.id = ? AND atc.parent_id = ?", requestID, turn.ID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), requests)

	// 完成后本机 Agent 的回复以 AI 员工身份写入会话并关联这一轮，记录 ACP 会话编号，不唤醒 AI 员工。
	require.NoError(t, operations.Complete(ctx, f.computerIdentity(), turn.ID, domain.ComputerOutcome{Output: "登录页已改成深色", AgentSession: "acp-1"}))
	// 已结束的调用不能同步共享文件区。
	_, err = computeraction.NewSharedFilesAction(db, nil).List(ctx, f.computerIdentity(), turn.ID)
	require.ErrorIs(t, err, computeraction.ErrSyncUnavailable)
	reply := &servermodels.Message{}
	require.NoError(t, db.NewSelect().Model(reply).Where("msg.agent_tool_call_id = ?", turn.ID).Scan(ctx))
	require.Equal(t, string(domain.MessageTypeText), reply.Type)
	require.Equal(t, "登录页已改成深色", reply.Body)
	require.NoError(t, db.NewSelect().Model(session).WherePK().Scan(ctx))
	require.Equal(t, "acp-1", *session.SessionID)
	queued, err := db.NewSelect().Model((*servermodels.AgentRun)(nil)).
		Where("agr.conversation_id = ? AND agr.status IN (?)", conversationID, []domain.AgentRunStatus{domain.AgentRunStatusQueued, domain.AgentRunStatusRunning}).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, queued, "local agent reply does not wake the AI employee")
	history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, f.identity, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID, AroundMessageID: reply.ID})
	require.NoError(t, err)
	var turnMessage *conversationaction.ConversationMessage
	for index := range history.Messages {
		if history.Messages[index].ID == reply.ID {
			turnMessage = &history.Messages[index]
		}
	}
	require.NotNil(t, turnMessage)
	require.Equal(t, &processquery.LocalAgentTurn{ToolCallID: turn.ID, LocalAgent: "codex"}, turnMessage.LocalAgentReply)
	var answered *conversationaction.ConversationMessage
	for index := range history.Messages {
		if history.Messages[index].AgentProcess != nil && history.Messages[index].AgentProcess.ID == run.ID {
			answered = &history.Messages[index]
		}
	}
	require.NotNil(t, answered, "run reply message")
	require.Equal(t, []processquery.LocalAgentTurn{{ToolCallID: turn.ID, LocalAgent: "codex"}}, answered.LocalAgentTurns)

	// 续接沿用同一会话与 ACP 会话编号。
	next := f.sendAndLoadRun(conversationID, "再把按钮改圆")
	f.model.use("local_agent", `{"agent":"codex","request":"再把按钮改圆"}`)
	f.run(next.ID)
	continued := f.computerCall(next.ID)
	require.Equal(t, session.ID, *continued.LocalAgentSessionID)
	require.Equal(t, "再把按钮改圆", continued.Operation.Operation.Prompt)
	claimed, err = operations.Claim(ctx, f.computerIdentity(), computeraction.ClaimInput{Limit: 1, Sessions: []string{session.ID}})
	require.NoError(t, err)
	require.Len(t, claimed.Operations, 1)
	require.Equal(t, "acp-1", claimed.Operations[0].Operation.AgentSession, "claimed turn carries the latest ACP session id")

	// 停止时释放会话，执行中的轮次在下次领取时中止，中止后记为已中断并写入已中断提示。
	require.NoError(t, conversationaction.NewStopLocalAgentAction(db).Execute(ctx, f.identity, continued.ID))
	claimed, err = operations.Claim(ctx, f.computerIdentity(), computeraction.ClaimInput{Limit: 1, Running: []string{continued.ID}, Sessions: []string{session.ID}})
	require.NoError(t, err)
	require.Equal(t, []string{continued.ID}, claimed.Abort)
	require.Equal(t, []string{session.ID}, claimed.Released)
	require.NoError(t, operations.Complete(ctx, f.computerIdentity(), continued.ID, domain.ComputerOutcome{Error: "context canceled", Aborted: true}))
	stopped := f.computerCall(next.ID)
	require.Equal(t, string(domain.AgentToolCallInterrupted), stopped.Status)
	cancelled := &servermodels.Message{}
	require.NoError(t, db.NewSelect().Model(cancelled).Where("msg.agent_tool_call_id = ?", continued.ID).Scan(ctx))
	require.Equal(t, string(domain.MessageTypeAgentCancelled), cancelled.Type)

	// 新一轮在新会话中执行；停用本机 Agent 时释放会话，委派工具不再提供。
	again := f.sendAndLoadRun(conversationID, "重新开始")
	f.model.use("local_agent", `{"agent":"codex","request":"重新开始"}`)
	f.run(again.ID)
	fresh := f.computerCall(again.ID)
	require.NotEqual(t, session.ID, *fresh.LocalAgentSessionID)
	_, err = agentaction.NewUpdatePersonalAgentAction(db).Execute(ctx, f.identity, f.personalAgent.ID, agentaction.PersonalAgentInput{
		DisplayName: f.personalAgent.DisplayName,
		Execution:   agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID}},
	})
	require.NoError(t, err)
	released := &servermodels.LocalAgentSession{}
	require.NoError(t, db.NewSelect().Model(released).Where("las.id = ?", *fresh.LocalAgentSessionID).Scan(ctx))
	require.Equal(t, string(domain.LocalAgentSessionReleased), released.Status)
	require.Equal(t, string(domain.AgentToolCallCancelled), f.computerCall(again.ID).Status, "queued turn is cancelled with the session")
	disabled := f.sendAndLoadRun(conversationID, "还能交给它吗")
	inspect := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		assert.NotContains(t, request.Assignment.Tools, "local_agent")
		return completeTestRun(ctx, feed, "不能了")
	}}
	f.execute(inspect, disabled.ID)
}

// testComputerSharedFiles 验证电脑为执行中的轮次同步会话共享文件区：给出清单、按电脑创建上传、按同步基准写回并以 AI 员工署名，文件已被改动时另存为冲突副本；
// 其他电脑与已结束的调用不能同步。
func testComputerSharedFiles(t *testing.T, f *personalAgentFixture, callID string) {
	ctx, db := f.ctx, f.db
	local, err := serverfilecontent.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	settings := func() serverfilecontent.S3Config { return serverfilecontent.S3Config{} }
	store := conversationfile.NewStore(db, func() domain.FileStorageBackend { return domain.FileStorageBackendLocal },
		serverfilecontent.NewWriter(local, settings), serverfilecontent.NewReader(local, settings))
	action := computeraction.NewSharedFilesAction(db, store)
	finalize := func(ctx context.Context, record *servermodels.File) (serverfilecontent.UploadedObject, error) {
		info, err := local.Stat(ctx, record.StorageKey)
		if err != nil {
			return serverfilecontent.UploadedObject{}, err
		}
		return serverfilecontent.UploadedObject{ByteSize: info.Size()}, nil
	}
	commit := func(path, content, base string) (conversationfile.Entry, error) {
		record, err := action.CreateUpload(ctx, f.computerIdentity(), callID, domain.FileStorageBackendLocal, computeraction.SharedUploadInput{
			Path: path, ContentType: "text/markdown", ByteSize: int64(len(content)),
		})
		require.NoError(t, err)
		require.Equal(t, f.computer.Record.ID, *record.UploaderComputerID)
		// 本地存储的内容凭电脑凭据写入，电脑凭据只能写入该电脑同步时创建的上传。
		upload, err := direct.NewLocalObjectAuthorizer(db).AuthorizeUpload(ctx, direct.LocalObjectCredentials{Bearer: f.computer.Credential}, record.StorageKey, "")
		require.NoError(t, err)
		require.Equal(t, record.ID, upload.FileID)
		require.NoError(t, local.Save(ctx, record.StorageKey, strings.NewReader(content), int64(len(content))))
		return action.Commit(ctx, f.computerIdentity(), callID, computeraction.SharedCommitInput{Path: path, FileID: record.ID, BaseHash: base}, finalize)
	}

	files, err := action.List(ctx, f.computerIdentity(), callID)
	require.NoError(t, err)
	require.Empty(t, files)
	created, err := commit("docs/报告.md", "v1", "")
	require.NoError(t, err)
	require.Equal(t, "docs/报告.md", created.Path)
	require.Equal(t, textfile.Hash([]byte("v1")), created.ContentHash)
	require.Equal(t, f.personalAgent.DisplayName, *created.UpdatedByName)
	updated, err := commit("docs/报告.md", "v2", created.ContentHash)
	require.NoError(t, err)
	require.Equal(t, "docs/报告.md", updated.Path)
	conflict, err := commit("docs/报告.md", "local", created.ContentHash)
	require.NoError(t, err)
	require.Equal(t, "docs/报告（电脑上的版本）.md", conflict.Path, "stale base is saved as a conflict copy")
	same, err := commit("docs/报告.md", "v2", "")
	require.NoError(t, err)
	require.Equal(t, updated.ID, same.ID, "identical content keeps the file")
	files, err = action.List(ctx, f.computerIdentity(), callID)
	require.NoError(t, err)
	require.Len(t, files, 2)
	require.Equal(t, "docs/报告.md", files[0].Path)
	require.Equal(t, textfile.Hash([]byte("v2")), files[0].ContentHash)
	require.NotNil(t, files[0].Content)

	_, err = action.List(ctx, computeraction.Identity{WorkspaceID: f.identity.Workspace.ID, ComputerID: uuid.NewV7().String()}, callID)
	require.ErrorIs(t, err, computeraction.ErrSyncUnavailable)
	_, err = action.CreateUpload(ctx, f.computerIdentity(), callID, domain.FileStorageBackendLocal, computeraction.SharedUploadInput{Path: "../越界.md", ByteSize: 1})
	require.ErrorIs(t, err, conversationfile.ErrInvalidPath)
}
