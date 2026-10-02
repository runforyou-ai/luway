//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// TestAgentKnowledgeScopes 验证本地知识库绑定的保存、企业隔离、版本快照和失效解绑。
func TestAgentKnowledgeScopes(t *testing.T) {
	t.Parallel()
	db, identity, _, modelID := newAIWorkspace(t)
	t.Helper()
	ctx := context.Background()
	bases := make([]string, 0, 2)
	for _, category := range []domain.KnowledgeBaseCategory{domain.KnowledgeBaseCategoryStandard, domain.KnowledgeBaseCategoryQA} {
		base, err := knowledgeaction.NewCreateKnowledgeBaseAction(db).Execute(ctx, identity, newKnowledgeBaseInput(t, db, identity, uuid.NewV7().String(), category))
		if err != nil {
			t.Fatal(err)
		}
		bases = append(bases, base.ID)
	}
	_, foreign := newQAFixture(t, db)
	input := agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
		ModelID: modelID, SystemInstruction: "回答产品问题", KnowledgeBaseIDs: []string{bases[0], bases[0]},
	}}
	create := agentaction.NewCreateAgentAction(db)
	created, err := create.Execute(ctx, identity, agentaction.CreateInput{DisplayName: "本地知识助手", Execution: input})
	if err != nil || !slices.Equal(created.Execution.Managed.KnowledgeBaseIDs, bases[:1]) {
		t.Fatalf("create=%+v err=%v", created, err)
	}
	originalRevisionID := created.Execution.RevisionID
	listAgents := knowledgeaction.NewListKnowledgeBaseAgentsQuery(db)
	// 核验知识库只列出当前配置版本绑定它的 AI 员工。
	assertAgents := func(knowledgeBaseID string, want []string) {
		t.Helper()
		agents, err := listAgents.Execute(ctx, identity, knowledgeBaseID)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(agents))
		for _, agent := range agents {
			got = append(got, agent.ID+":"+agent.DisplayName+":"+string(agent.Status))
		}
		if !slices.Equal(got, want) {
			t.Fatalf("knowledge base %s agents=%v want=%v", knowledgeBaseID, got, want)
		}
	}
	bound := []string{created.ID + ":本地知识助手:" + string(domain.IdentityStatusActive)}
	assertAgents(bases[0], bound)
	assertAgents(strings.ToUpper(bases[0]), bound)
	assertAgents(bases[1], []string{})
	if _, err := listAgents.Execute(ctx, identity, foreign.ID); !errors.Is(err, knowledgeaction.ErrNotFound) {
		t.Fatalf("foreign knowledge base agents err=%v", err)
	}
	update := agentaction.NewUpdateExecutionAction(db)
	before, err := db.NewSelect().Model((*servermodels.Agent)(nil)).Where("organization_id = ?", identity.Organization.ID).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 核验创建和编辑配置时的知识库归属及存在性。
	for _, id := range []string{foreign.ID, uuid.NewV7().String()} {
		input.Managed.KnowledgeBaseIDs = []string{id}
		var fields *common.FieldError
		if _, err := update.Execute(ctx, identity, created.ID, agentaction.UpdateExecutionInput{ExecutionInput: input}); !errors.As(err, &fields) || fields.Fields["knowledgeBaseIds"] != agentaction.ValidationKnowledgeBaseInvalid {
			t.Fatalf("invalid update err=%v", err)
		}
		if _, err := create.Execute(ctx, identity, agentaction.CreateInput{DisplayName: "无效绑定助手", Execution: input}); !errors.As(err, &fields) || fields.Fields["knowledgeBaseIds"] != agentaction.ValidationKnowledgeBaseInvalid {
			t.Fatalf("invalid create err=%v", err)
		}
	}
	count, err := db.NewSelect().Model((*servermodels.AgentRevision)(nil)).Where("agent_id = ?", created.ID).Count(ctx)
	if err != nil || count != 1 {
		t.Fatalf("invalid updates changed revisions: count=%d err=%v", count, err)
	}
	count, err = db.NewSelect().Model((*servermodels.Agent)(nil)).Where("organization_id = ?", identity.Organization.ID).Count(ctx)
	if err != nil || count != before {
		t.Fatalf("invalid creates left agents: count=%d before=%d err=%v", count, before, err)
	}
	input.Managed.KnowledgeBaseIDs = bases
	updated, err := update.Execute(ctx, identity, created.ID, agentaction.UpdateExecutionInput{ExecutionInput: input})
	if err != nil || updated.Execution.RevisionID == originalRevisionID || !slices.Equal(updated.Execution.Managed.KnowledgeBaseIDs, bases) {
		t.Fatalf("update=%+v err=%v", updated, err)
	}
	assertAgents(bases[1], bound)
	// 核验保存新配置后旧 Revision 的知识库范围保持原值。
	var revision servermodels.AgentRevision
	if err := db.NewSelect().Model(&revision).Where("id = ?", originalRevisionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		KnowledgeBaseIDs []string `json:"knowledgeBaseIds"`
	}
	if err := json.Unmarshal(revision.Configuration, &snapshot); err != nil || !slices.Equal(snapshot.KnowledgeBaseIDs, bases[:1]) {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	if err := knowledgeaction.NewDeleteKnowledgeBaseAction(db).Execute(ctx, identity, bases[1]); err != nil {
		t.Fatal(err)
	}
	// 删除知识库后员工当前版本移除该知识库，其余绑定保持不变。
	detail, err := agentaction.NewGetAgentQuery(db).Execute(ctx, identity, created.ID)
	if err != nil || !slices.Equal(detail.Execution.Managed.KnowledgeBaseIDs, bases[:1]) {
		t.Fatalf("deleted binding detail=%+v err=%v", detail, err)
	}
	if _, err := update.Execute(ctx, identity, created.ID, agentaction.UpdateExecutionInput{ExecutionInput: input}); err == nil {
		t.Fatal("deleted binding saved")
	}
	input.Managed.KnowledgeBaseIDs = []string{}
	cleared, err := update.Execute(ctx, identity, created.ID, agentaction.UpdateExecutionInput{ExecutionInput: input})
	if err != nil || len(cleared.Execution.Managed.KnowledgeBaseIDs) != 0 {
		t.Fatalf("clear=%+v err=%v", cleared, err)
	}
	assertAgents(bases[0], []string{})
}

// TestAgentExecutionLocksKnowledgeBasesBeforeAgent 验证保存配置先锁知识库再锁员工，与删除知识库的取锁顺序一致。
func TestAgentExecutionLocksKnowledgeBasesBeforeAgent(t *testing.T) {
	t.Parallel()
	db, identity, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	base, err := knowledgeaction.NewCreateKnowledgeBaseAction(db).Execute(ctx, identity, newKnowledgeBaseInput(t, db, identity, uuid.NewV7().String(), domain.KnowledgeBaseCategoryStandard))
	if err != nil {
		t.Fatal(err)
	}
	input := agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
		ModelID: modelID, SystemInstruction: "回答产品问题", KnowledgeBaseIDs: []string{base.ID},
	}}
	created, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{DisplayName: "锁顺序助手", Execution: input})
	if err != nil {
		t.Fatal(err)
	}

	// 模拟删除知识库已持有知识库锁。
	deleting, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = deleting.Rollback() }()
	if _, err := deleting.NewSelect().Model((*servermodels.KnowledgeBase)(nil)).Column("id").Where("id = ?", base.ID).For("UPDATE").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	saved := make(chan error, 1)
	go func() {
		_, err := agentaction.NewUpdateExecutionAction(db).Execute(ctx, identity, created.ID, agentaction.UpdateExecutionInput{ExecutionInput: input})
		saved <- err
	}()
	// 等待保存请求阻塞在知识库锁上。
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := db.NewRaw("SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE '%knowledge_bases%' AND query LIKE ?", "%"+base.ID+"%").Scan(ctx, &waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("保存配置未等待知识库锁")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// 保存请求等待知识库期间未持有员工锁，删除事务可以继续锁定员工。
	if _, err := deleting.NewSelect().Model((*servermodels.Agent)(nil)).Column("id").Where("id = ?", created.ID).For("UPDATE NOWAIT").Exec(ctx); err != nil {
		t.Fatalf("员工已被保存请求锁定: %v", err)
	}
	if err := deleting.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
}
