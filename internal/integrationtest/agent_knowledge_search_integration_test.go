//go:build server

package integrationtest

import (
	"context"
	"strings"
	"testing"
	"uuid"

	"github.com/runforyou-ai/einorun"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	aiprovideraction "github.com/runforyou-ai/luway/internal/actions/aiprovider"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/knowledgeretrieval"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// testKnowledgeRuntime 把知识库检索工具交给检查函数验证的测试运行时。
type testKnowledgeRuntime struct {
	t     *testing.T
	check func(agentruntime.KnowledgeSearch)
}

// Run 认领全部输入后交给当前阶段的检查函数验证知识库检索工具。
func (r *testKnowledgeRuntime) Run(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
	triggers, err := pendingTriggers(ctx, feed, 0)
	if err != nil {
		return agentruntime.RunResult{}, err
	}
	claimed, err := feed.Claim(ctx, triggers[len(triggers)-1].Seq)
	if err != nil {
		return agentruntime.RunResult{}, err
	}
	// 未设置检查函数时要求本次运行没有知识库检索工具。
	if r.check == nil {
		require.Nil(r.t, request.KnowledgeSearch, "unbound agent received knowledge search")
	} else {
		require.NotNil(r.t, request.KnowledgeSearch, "bound agent did not receive knowledge search")
		r.check(request.KnowledgeSearch)
	}
	return agentruntime.RunResult{Content: "已查阅资料", EndSeq: claimed.EndSeq}, nil
}

// newKnowledgeAgent 创建绑定指定知识库的托管 AI 员工，返回员工与对话模型供应商编号。
func newKnowledgeAgent(t *testing.T, db *bun.DB, identity *servermodels.Identity, knowledgeBaseIDs []string) (*agentaction.Agent, string) {
	t.Helper()
	ctx := context.Background()
	provider, err := aiprovideraction.NewCreateAIProviderAction(db).Execute(ctx, identity, aiprovideraction.Input{
		CredentialType: domain.AIProviderCredentialTypeAPIKey,
		Brand:          domain.AIProviderBrandOpenAI, Name: uuid.NewV7().String(), APIKey: "test-key", APIURL: "https://models.test/v1",
		Models: []aiprovideraction.Model{{Identifier: "chat", Name: "对话模型", Type: domain.AIModelTypeChat, InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText}, ContextWindow: 32000, MaxOutputTokens: 4096}},
	})
	require.NoError(t, err)
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, DisplayName: "资料助手",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ModelID: aiModelID(t, db, provider.ID, "chat"), SystemInstruction: "依据知识库回答", KnowledgeBaseIDs: knowledgeBaseIDs,
		}},
	})
	require.NoError(t, err)
	return agent, provider.ID
}

// newKnowledgeAgentScheduler 创建只登记运行任务的 Agent 调度器。
func newKnowledgeAgentScheduler(t *testing.T, db *bun.DB) (*servertest.Tasks, *agentrunaction.Scheduler) {
	t.Helper()
	tasks := servertest.NewTasks()
	return tasks, agentrunaction.NewScheduler(tasks)
}

// runQueuedAgentRun 同步执行会话中排队的运行并确认成功收尾。
func runQueuedAgentRun(t *testing.T, db *bun.DB, execute *testAgentRun, conversationID string) {
	t.Helper()
	ctx := context.Background()
	run := &servermodels.AgentRun{}
	require.NoError(t, db.NewSelect().Model(run).Where("agr.conversation_id = ? AND agr.status = ?", conversationID, domain.AgentRunStatusQueued).Scan(ctx))
	require.NoError(t, execute.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	require.NoError(t, db.NewSelect().Model(run).WherePK().Scan(ctx))
	require.Equal(t, domain.AgentRunStatusSucceeded, domain.AgentRunStatus(run.Status))
}

// TestAgentKnowledgeSearch 验证运行期按配置版本绑定的知识库注入检索、跨库融合、游标阅读、范围隔离以及知识库删除后的行为。
func TestAgentKnowledgeSearch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	defer store.Close()
	db := store.DB()
	installed, refundBase := newDocumentFixture(t, db)
	identity := installed.Identity
	invoiceBase, err := knowledgeaction.NewCreateKnowledgeBaseAction(db).Execute(ctx, identity, newKnowledgeBaseInput(t, db, identity, "发票", domain.KnowledgeBaseCategoryStandard))
	require.NoError(t, err)
	unboundBase, err := knowledgeaction.NewCreateKnowledgeBaseAction(db).Execute(ctx, identity, newKnowledgeBaseInput(t, db, identity, "配送", domain.KnowledgeBaseCategoryStandard))
	require.NoError(t, err)
	probe := &retrievalProbe{}
	refundID := publishRetrievalDocument(t, db, probe, identity, refundBase, "退款政策.txt", strings.Repeat("签收后七天内可以申请退款，退款金额原路返回。", 30))
	invoiceID := publishRetrievalDocument(t, db, probe, identity, invoiceBase, "发票说明.txt", "下单时可以选择开具电子发票。")
	publishRetrievalDocument(t, db, probe, identity, unboundBase, "配送说明.txt", "配送时效按收货地址计算。")

	agent, _ := newKnowledgeAgent(t, db, identity, []string{refundBase.ID, invoiceBase.ID})
	tasks, scheduler := newKnowledgeAgentScheduler(t, db)
	first, err := directchataction.NewSendFirstAgentTextMessageAction(db, testEnqueuer, scheduler).Execute(ctx, identity, directchataction.FirstAgentTextMessageInput{
		ConversationID: uuid.NewV7().String(), AgentIdentityID: agent.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "怎么退款和开发票",
	})
	require.NoError(t, err)
	runtime := &testKnowledgeRuntime{t: t}
	execute := newTestAgentRun(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), knowledgeaction.NewRetrievalService(db, modelcall.New(db, modelcall.Upstreams{Embedder: probe, Reranker: probe}, nil)), servertest.DisabledMail{}, nil, nil)

	// 两个绑定库的结果经统一融合返回，未绑定库不进入范围；命中记录可按游标读取相邻分段。
	runtime.check = func(search agentruntime.KnowledgeSearch) {
		result, err := search(ctx, knowledgeretrieval.Request{Queries: []string{"退款", "发票", "配送"}})
		require.NoError(t, err)
		require.NotEmpty(t, result.Records)
		bases := map[string]string{}
		for _, record := range result.Records {
			require.NotEqual(t, unboundBase.ID, record.KnowledgeBaseID, "record=%+v", record)
			require.NotEmpty(t, record.Cursor.SegmentID, "record=%+v", record)
			require.True(t, record.Matched, "record=%+v", record)
			require.NotNil(t, record.Score, "record=%+v", record)
			bases[record.KnowledgeBaseID] = record.KnowledgeBaseName
		}
		require.Equal(t, map[string]string{refundBase.ID: refundBase.Name, invoiceBase.ID: invoiceBase.Name}, bases)
		var cursor knowledgeretrieval.Cursor
		for _, record := range result.Records {
			if record.DocumentID == refundID && record.Position == 1 {
				cursor = record.Cursor
			}
		}
		window, err := search(ctx, knowledgeretrieval.Request{Cursor: &cursor, After: 1})
		require.NoError(t, err)
		require.Len(t, window.Records, 2)
		require.Equal(t, cursor.SegmentID, window.Records[0].SegmentID)
		require.Equal(t, 2, window.Records[1].Position)
		require.False(t, window.Records[0].Matched)
		// 阅读结果的游标可以继续读取，且携带知识库标识。
		next := window.Records[1]
		require.Equal(t, refundBase.ID, next.KnowledgeBaseID)
		require.Equal(t, refundBase.Name, next.KnowledgeBaseName)
		require.Equal(t, next.SegmentID, next.Cursor.SegmentID)
		require.Equal(t, 2, next.Cursor.Position)
		more, err := search(ctx, knowledgeretrieval.Request{Cursor: &next.Cursor, Before: 1})
		require.NoError(t, err)
		require.Len(t, more.Records, 2)
		require.Equal(t, 1, more.Records[0].Position)
		require.Equal(t, next.SegmentID, more.Records[1].SegmentID)
		foreign := knowledgeretrieval.Cursor{KnowledgeBaseID: unboundBase.ID, DocumentID: refundID, SegmentID: cursor.SegmentID, SegmentBatchID: cursor.SegmentBatchID, Position: 1}
		_, err = search(ctx, knowledgeretrieval.Request{Cursor: &foreign})
		require.Error(t, err, "unbound knowledge base cursor readable")
	}
	runQueuedAgentRun(t, db, execute, first.Conversation.ID)

	// 删除一个绑定库后，运行继续在剩余库中检索，结果不再包含已删除库的文档。
	require.NoError(t, knowledgeaction.NewDeleteKnowledgeBaseAction(db).Execute(ctx, identity, invoiceBase.ID))
	send := directchataction.NewSendAgentTextMessageAction(db, testEnqueuer, scheduler)
	_, err = send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "再问一次发票"})
	require.NoError(t, err)
	runtime.check = func(search agentruntime.KnowledgeSearch) {
		result, err := search(ctx, knowledgeretrieval.Request{Queries: []string{"退款", "发票"}})
		require.NoError(t, err)
		require.NotEmpty(t, result.Records)
		require.Equal(t, refundID, result.Records[0].DocumentID)
		for _, record := range result.Records {
			require.Equal(t, refundBase.ID, record.KnowledgeBaseID, "record=%+v", record)
			require.NotEqual(t, invoiceID, record.DocumentID, "record=%+v", record)
		}
	}
	runQueuedAgentRun(t, db, execute, first.Conversation.ID)

	// 绑定库全部删除后，员工当前版本不再引用知识库，运行不提供检索工具。
	require.NoError(t, knowledgeaction.NewDeleteKnowledgeBaseAction(db).Execute(ctx, identity, refundBase.ID))
	var boundIDs []string
	require.NoError(t, db.NewSelect().TableExpr("agents AS a").
		ColumnExpr("jsonb_array_elements_text(ar.configuration->'knowledgeBaseIds')").
		Join("JOIN agent_revisions AS ar ON ar.id = a.active_revision_id").
		Where("a.id = ?", agent.ID).Scan(ctx, &boundIDs))
	require.Empty(t, boundIDs)
	_, err = send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "还有资料吗"})
	require.NoError(t, err)
	runtime.check = nil
	runQueuedAgentRun(t, db, execute, first.Conversation.ID)
	history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: first.Conversation.ID})
	require.NoError(t, err)
	require.Len(t, history.Messages, 6)
}

// TestAgentKnowledgeSearchRevisionAndQA 验证运行按排队时锁定的配置版本确定知识库范围，问答库经检索工具返回完整答案。
func TestAgentKnowledgeSearchRevisionAndQA(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	defer store.Close()
	db := store.DB()
	installed, documentBase := newDocumentFixture(t, db)
	identity := installed.Identity
	qaBase, err := knowledgeaction.NewCreateKnowledgeBaseAction(db).Execute(ctx, identity, newKnowledgeBaseInput(t, db, identity, "售后问答", domain.KnowledgeBaseCategoryQA))
	require.NoError(t, err)
	probe := &retrievalProbe{}
	documentID := publishRetrievalDocument(t, db, probe, identity, documentBase, "退款政策.txt", "签收后七天内可以申请退款，退款金额原路返回。")
	answer := strings.Repeat("进入订单详情申请退款，审核通过后原路返回。", 40)
	entryID := publishQAEntry(t, db, probe, identity, qaBase, knowledgeaction.QAInput{
		Question: "如何退款？", Answer: answer, SimilarQuestions: []knowledgeaction.QASimilarQuestion{{Content: "怎么申请退款"}},
	})

	agent, providerID := newKnowledgeAgent(t, db, identity, []string{qaBase.ID})
	tasks, scheduler := newKnowledgeAgentScheduler(t, db)
	first, err := directchataction.NewSendFirstAgentTextMessageAction(db, testEnqueuer, scheduler).Execute(ctx, identity, directchataction.FirstAgentTextMessageInput{
		ConversationID: uuid.NewV7().String(), AgentIdentityID: agent.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "怎么退款",
	})
	require.NoError(t, err)
	// 首次运行排队后改为只绑定文档库，已排队的运行仍使用排队时的问答库范围。
	_, err = agentaction.NewUpdateExecutionAction(db).Execute(ctx, identity, agent.ID, agentaction.UpdateExecutionInput{ExecutionInput: agentaction.ExecutionInput{
		Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ModelID: aiModelID(t, db, providerID, "chat"), SystemInstruction: "依据知识库回答", KnowledgeBaseIDs: []string{documentBase.ID},
		},
	}})
	require.NoError(t, err)
	runtime := &testKnowledgeRuntime{t: t}
	execute := newTestAgentRun(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), knowledgeaction.NewRetrievalService(db, modelcall.New(db, modelcall.Upstreams{Embedder: probe, Reranker: probe}, nil)), servertest.DisabledMail{}, nil, nil)

	// 问题与答案分段的命中折叠为一条问答并携带完整答案，游标读取返回同一条目。
	runtime.check = func(search agentruntime.KnowledgeSearch) {
		result, err := search(ctx, knowledgeretrieval.Request{Queries: []string{"退款", "申请退款"}})
		require.NoError(t, err)
		require.Len(t, result.Records, 1)
		record := result.Records[0]
		require.Equal(t, qaBase.ID, record.KnowledgeBaseID)
		require.Equal(t, entryID, record.DocumentID)
		require.Equal(t, "如何退款？", record.DocumentName)
		require.NotNil(t, record.Answer)
		require.Equal(t, answer, *record.Answer)
		window, err := search(ctx, knowledgeretrieval.Request{Cursor: &record.Cursor, Before: 1, After: 1})
		require.NoError(t, err)
		require.Len(t, window.Records, 1)
		require.Equal(t, entryID, window.Records[0].SegmentID)
		require.NotNil(t, window.Records[0].Answer)
		require.Equal(t, answer, *window.Records[0].Answer)
	}
	runQueuedAgentRun(t, db, execute, first.Conversation.ID)

	// 新输入创建的运行使用切换后的配置版本，只在文档库中检索。
	_, err = directchataction.NewSendAgentTextMessageAction(db, testEnqueuer, scheduler).Execute(ctx, identity, directchataction.InternalTextMessageInput{
		ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "再说说退款",
	})
	require.NoError(t, err)
	runtime.check = func(search agentruntime.KnowledgeSearch) {
		result, err := search(ctx, knowledgeretrieval.Request{Queries: []string{"退款"}})
		require.NoError(t, err)
		require.NotEmpty(t, result.Records)
		require.Equal(t, documentID, result.Records[0].DocumentID)
		for _, record := range result.Records {
			require.Equal(t, documentBase.ID, record.KnowledgeBaseID, "record=%+v", record)
		}
	}
	runQueuedAgentRun(t, db, execute, first.Conversation.ID)
}
