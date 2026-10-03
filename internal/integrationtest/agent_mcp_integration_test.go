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
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	mcpaction "github.com/runforyou-ai/luway/internal/actions/mcpserver"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// newAgentMCPService 创建远端不可用、工具目录为空的绑定测试服务。
func newAgentMCPService(t *testing.T, db *bun.DB, identity *servermodels.Identity) string {
	t.Helper()
	service := &servermodels.MCPServer{
		OrganizationID: identity.Organization.ID, Name: uuid.NewV7().String(),
		URL: "http://127.0.0.1:1/mcp", ServerType: domain.MCPServerTypeStreamableHTTP, ToolsFailure: "unavailable",
	}
	if _, err := db.NewInsert().Model(service).Column("organization_id", "name", "url", "server_type", "tools_failure").Returning("id").Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	return service.ID
}

// TestAgentMCPServices 验证整体保存、企业隔离、删除联动和并发事务。
func TestAgentMCPServices(t *testing.T) {
	t.Parallel()
	db, owner, _, modelID := newAIWorkspace(t)
	t.Helper()
	ctx := context.Background()
	execution := agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
		ModelID: modelID, SystemInstruction: "回答产品问题",
	}}
	create := agentaction.NewCreateAgentAction(db)
	update := agentaction.NewUpdateExecutionAction(db)
	get := agentaction.NewGetAgentQuery(db)
	remove := mcpaction.NewDeleteMCPServerAction(db)
	agents := make([]*agentaction.Agent, 0, 2)
	for range 2 {
		agent, err := create.Execute(ctx, owner, agentaction.CreateInput{DisplayName: "MCP 配置助手", Execution: execution})
		if err != nil {
			t.Fatal(err)
		}
		if len(agent.Execution.MCPServerIDs) != 0 {
			t.Fatal("new agent has MCP bindings")
		}
		agents = append(agents, agent)
	}
	ids := []string{newAgentMCPService(t, db, owner), newAgentMCPService(t, db, owner)}
	slices.Sort(ids)
	foreignOwner, _ := newQAFixture(t, db)
	foreignID := newAgentMCPService(t, db, foreignOwner)
	options, err := agentaction.NewListMCPServerOptionsQuery(db).Execute(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if !slices.ContainsFunc(options, func(o agentaction.MCPServerOption) bool { return o.ID == id && o.ToolCount == 0 }) {
			t.Fatalf("missing offline service %s: %+v", id, options)
		}
	}
	if slices.ContainsFunc(options, func(o agentaction.MCPServerOption) bool { return o.ID == foreignID }) {
		t.Fatal("foreign service leaked")
	}
	for i, agent := range agents {
		saved, err := update.Execute(ctx, owner, agent.ID, agentaction.UpdateExecutionInput{ExecutionInput: execution, MCPServerIDs: []string{ids[1], ids[0], ids[0]}})
		if err != nil || !slices.Equal(saved.Execution.MCPServerIDs, ids) {
			t.Fatalf("save=%+v err=%v", saved, err)
		}
		agents[i] = saved
	}
	var before servermodels.AgentRevision
	if err := db.NewSelect().Model(&before).Where("agent_id = ?", agents[0].ID).OrderExpr("created_at DESC, id DESC").Limit(1).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal(before.Configuration, &snapshot); err != nil {
		t.Fatal(err)
	}
	var snapshotIDs []string
	if err := json.Unmarshal(snapshot["mcpServerIds"], &snapshotIDs); err != nil || !slices.Equal(snapshotIDs, ids) {
		t.Fatalf("revision MCP selection=%v err=%v", snapshotIDs, err)
	}
	for _, id := range []string{foreignID, uuid.NewV7().String(), "invalid"} {
		_, err := update.Execute(ctx, owner, agents[0].ID, agentaction.UpdateExecutionInput{ExecutionInput: execution, MCPServerIDs: []string{id}})
		var fields *common.FieldError
		if !errors.As(err, &fields) || fields.Fields["mcpServerIds"] != agentaction.ValidationMCPServerInvalid {
			t.Fatalf("invalid binding: %v", err)
		}
	}
	// 核验字段校验失败后配置版本与当前版本指针保持原值。
	invalid := agentaction.ExecutionInput{Mode: execution.Mode, Managed: &agentaction.ManagedExecutionInput{ModelID: uuid.NewV7().String(), SystemInstruction: "新指令"}}
	if _, err := update.Execute(ctx, owner, agents[0].ID, agentaction.UpdateExecutionInput{ExecutionInput: invalid}); err == nil {
		t.Fatal("invalid model saved")
	}
	detail, err := get.Execute(ctx, owner, agents[0].ID)
	if err != nil || detail.Execution.RevisionID != before.ID || !slices.Equal(detail.Execution.MCPServerIDs, ids) {
		t.Fatalf("rollback detail=%+v err=%v", detail, err)
	}
	if err := remove.Execute(ctx, foreignOwner, ids[0]); !errors.Is(err, mcpaction.ErrNotFound) {
		t.Fatalf("foreign delete: %v", err)
	}
	if err := remove.Execute(ctx, owner, ids[0]); err != nil {
		t.Fatal(err)
	}
	for _, agent := range agents {
		detail, err := get.Execute(ctx, owner, agent.ID)
		if err != nil || detail.Execution.RevisionID == agent.Execution.RevisionID || !slices.Equal(detail.Execution.MCPServerIDs, ids[1:]) {
			t.Fatalf("deleted binding detail=%+v err=%v", detail, err)
		}
		var current servermodels.AgentRevision
		if err := db.NewSelect().Model(&current).Where("id = ?", detail.Execution.RevisionID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		var configuration map[string]json.RawMessage
		if err := json.Unmarshal(current.Configuration, &configuration); err != nil {
			t.Fatal(err)
		}
		for key, value := range snapshot {
			if key != "mcpServerIds" && string(value) != string(configuration[key]) {
				t.Fatalf("deletion changed %s: %s", key, configuration[key])
			}
		}
		count, err := db.NewSelect().Model((*servermodels.AgentRevision)(nil)).Where("agent_id = ?", agent.ID).Count(ctx)
		if err != nil || count != 3 || current.CreatedByUserID != owner.User.ID {
			t.Fatalf("delete revision count=%d author=%s err=%v", count, current.CreatedByUserID, err)
		}
	}
	var after servermodels.AgentRevision
	if err := db.NewSelect().Model(&after).Where("id = ?", before.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if string(before.Configuration) != string(after.Configuration) {
		t.Fatal("deletion changed revision")
	}
	cleared, err := update.Execute(ctx, owner, agents[0].ID, agentaction.UpdateExecutionInput{ExecutionInput: execution})
	if err != nil || len(cleared.Execution.MCPServerIDs) != 0 {
		t.Fatalf("clear=%+v err=%v", cleared, err)
	}

	if err := remove.Execute(ctx, owner, ids[1]); err != nil {
		t.Fatal(err)
	}
	detail, err = get.Execute(ctx, owner, agents[0].ID)
	if err != nil || detail.Execution.RevisionID != cleared.Execution.RevisionID {
		t.Fatalf("historical-only reference created revision: %+v err=%v", detail, err)
	}
	detail, err = get.Execute(ctx, owner, agents[1].ID)
	if err != nil || len(detail.Execution.MCPServerIDs) != 0 {
		t.Fatalf("last service deletion=%+v err=%v", detail, err)
	}

	// 使用两个用户和查询屏障控制服务及员工的取锁顺序。
	colleague := newChatLockUser(t, db, owner)
	db.AddQueryHook(chatQueryHook{})
	for _, saveFirst := range []bool{true, false} {
		name := "删除先取得锁"
		if saveFirst {
			name = "保存先取得锁"
		}
		t.Run(name, func(t *testing.T) {
			testAgentMCPDeleteRace(t, db, owner, colleague, agents[0].ID, execution, saveFirst)
		})
	}
	t.Run("删除失败回滚版本", func(t *testing.T) {
		testAgentMCPDeleteRollback(t, db, owner, agents[0].ID, execution)
	})
	t.Run("同时删除两个服务", func(t *testing.T) {
		testAgentMCPConcurrentDeletes(t, db, owner, colleague, agents[0].ID, execution)
	})
	t.Run("删除等待最新配置", func(t *testing.T) {
		testAgentMCPDeleteLatestRevision(t, db, owner, colleague, agents[0].ID, execution)
	})
	t.Run("两次保存串行替换", func(t *testing.T) {
		testAgentMCPSaveRace(t, db, owner, colleague, agents[0].ID, execution)
	})
}

// testAgentMCPDeleteRace 验证服务删除与绑定保存按取锁顺序收敛。
func testAgentMCPDeleteRace(t *testing.T, db *bun.DB, owner, colleague *servermodels.Identity, agentID string, execution agentaction.ExecutionInput, saveFirst bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id := newAgentMCPService(t, db, owner)
	gate := newChatQueryGate(t, false, 1, func(e *bun.QueryEvent) bool {
		return strings.Contains(e.Query, `FROM "mcp_servers"`) && strings.Contains(e.Query, id) && strings.Contains(e.Query, "FOR ")
	})
	// 失败路径也要先释放事务，再交由外层连接清理。
	defer gate.open()
	gated := context.WithValue(ctx, chatQueryGateKey{}, gate)
	update := agentaction.NewUpdateExecutionAction(db)
	remove := mcpaction.NewDeleteMCPServerAction(db)
	saved, deleted := make(chan error, 1), make(chan error, 1)
	saveCtx, deleteCtx := ctx, gated
	if saveFirst {
		saveCtx, deleteCtx = gated, ctx
	}
	save := func() {
		_, err := update.Execute(saveCtx, owner, agentID, agentaction.UpdateExecutionInput{ExecutionInput: execution, MCPServerIDs: []string{id}})
		saved <- err
	}
	deletion := func() { deleted <- remove.Execute(deleteCtx, colleague, id) }
	if saveFirst {
		go save()
	} else {
		go deletion()
	}
	waitChatSignal(t, ctx, gate.reached)
	if saveFirst {
		go deletion()
	} else {
		go save()
	}
	waitChatDatabaseLock(t, ctx, db, `FROM "mcp_servers"`, id)
	gate.open()
	saveErr := waitChatResult(t, ctx, saved)
	if saveFirst && saveErr != nil {
		t.Fatal(saveErr)
	}
	if !saveFirst {
		var fields *common.FieldError
		if !errors.As(saveErr, &fields) || fields.Fields["mcpServerIds"] != agentaction.ValidationMCPServerInvalid {
			t.Fatalf("stale save: %v", saveErr)
		}
	}
	if err := waitChatResult(t, ctx, deleted); err != nil {
		t.Fatal(err)
	}
	count, err := db.NewSelect().Model((*servermodels.Agent)(nil)).
		Join("JOIN agent_revisions AS ar ON ar.id = a.active_revision_id AND ar.organization_id = a.organization_id AND ar.agent_id = a.id").
		Where("a.organization_id = ?", owner.Organization.ID).
		Where("ar.configuration->'mcpServerIds' @> jsonb_build_array(?::text)", id).Count(ctx)
	if err != nil || count != 0 {
		t.Fatalf("dangling bindings=%d err=%v", count, err)
	}
}

// testAgentMCPSaveRace 验证同一员工并发保存时各草稿的版本独立性。
func testAgentMCPSaveRace(t *testing.T, db *bun.DB, owner, colleague *servermodels.Identity, agentID string, execution agentaction.ExecutionInput) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ids := []string{newAgentMCPService(t, db, owner), newAgentMCPService(t, db, owner)}
	gate := newChatQueryGate(t, false, 1, func(e *bun.QueryEvent) bool {
		return strings.Contains(e.Query, `FROM "agents"`) && strings.Contains(e.Query, agentID) && strings.Contains(e.Query, "FOR UPDATE")
	})
	defer gate.open()
	update := agentaction.NewUpdateExecutionAction(db)
	first, second := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := update.Execute(context.WithValue(ctx, chatQueryGateKey{}, gate), owner, agentID, agentaction.UpdateExecutionInput{ExecutionInput: execution, MCPServerIDs: ids[:1]})
		first <- err
	}()
	waitChatSignal(t, ctx, gate.reached)
	go func() {
		_, err := update.Execute(ctx, colleague, agentID, agentaction.UpdateExecutionInput{ExecutionInput: execution, MCPServerIDs: ids[1:]})
		second <- err
	}()
	waitChatDatabaseLock(t, ctx, db, `FROM "agents"`, agentID)
	gate.open()
	if err := waitChatResult(t, ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := waitChatResult(t, ctx, second); err != nil {
		t.Fatal(err)
	}
	detail, err := agentaction.NewGetAgentQuery(db).Execute(ctx, owner, agentID)
	if err != nil || !slices.Equal(detail.Execution.MCPServerIDs, ids[1:]) {
		t.Fatalf("concurrent save=%+v err=%v", detail, err)
	}
}

// testAgentMCPDeleteRollback 验证删除末尾失败会回滚服务、所有新版本和当前版本指针。
func testAgentMCPDeleteRollback(t *testing.T, db *bun.DB, owner *servermodels.Identity, agentID string, execution agentaction.ExecutionInput) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id := newAgentMCPService(t, db, owner)
	saved, err := agentaction.NewUpdateExecutionAction(db).Execute(ctx, owner, agentID, agentaction.UpdateExecutionInput{ExecutionInput: execution, MCPServerIDs: []string{id}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := db.NewSelect().Model((*servermodels.AgentRevision)(nil)).Where("agent_id = ?", agentID).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gate := newChatQueryGate(t, true, 1, func(e *bun.QueryEvent) bool {
		return strings.Contains(e.Query, `DELETE FROM "mcp_servers"`) && strings.Contains(e.Query, id)
	})
	defer gate.open()
	deleteCtx, cancelDelete := context.WithCancel(context.WithValue(ctx, chatQueryGateKey{}, gate))
	defer cancelDelete()
	result := make(chan error, 1)
	go func() { result <- mcpaction.NewDeleteMCPServerAction(db).Execute(deleteCtx, owner, id) }()
	waitChatSignal(t, ctx, gate.reached)
	cancelDelete()
	if err := waitChatResult(t, ctx, result); err == nil {
		t.Fatal("cancelled deletion succeeded")
	}
	detail, err := agentaction.NewGetAgentQuery(db).Execute(ctx, owner, agentID)
	if err != nil || detail.Execution.RevisionID != saved.Execution.RevisionID || !slices.Equal(detail.Execution.MCPServerIDs, []string{id}) {
		t.Fatalf("rollback detail=%+v err=%v", detail, err)
	}
	after, err := db.NewSelect().Model((*servermodels.AgentRevision)(nil)).Where("agent_id = ?", agentID).Count(ctx)
	if err != nil || after != before {
		t.Fatalf("rollback revision count=%d want=%d err=%v", after, before, err)
	}
	if exists, err := db.NewSelect().Model((*servermodels.MCPServer)(nil)).Where("id = ?", id).Exists(ctx); err != nil || !exists {
		t.Fatalf("service after rollback=%v err=%v", exists, err)
	}
}

// testAgentMCPConcurrentDeletes 验证两个服务同时删除时基于最新版本累积移除。
func testAgentMCPConcurrentDeletes(t *testing.T, db *bun.DB, owner, colleague *servermodels.Identity, agentID string, execution agentaction.ExecutionInput) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ids := []string{newAgentMCPService(t, db, owner), newAgentMCPService(t, db, owner)}
	if _, err := agentaction.NewUpdateExecutionAction(db).Execute(ctx, owner, agentID, agentaction.UpdateExecutionInput{ExecutionInput: execution, MCPServerIDs: ids}); err != nil {
		t.Fatal(err)
	}
	gate := newChatQueryGate(t, false, 1, func(e *bun.QueryEvent) bool {
		return strings.Contains(e.Query, `FROM "agents"`) && strings.Contains(e.Query, "FOR UPDATE") && strings.Contains(e.Query, agentID)
	})
	defer gate.open()
	remove := mcpaction.NewDeleteMCPServerAction(db)
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- remove.Execute(context.WithValue(ctx, chatQueryGateKey{}, gate), owner, ids[0]) }()
	waitChatSignal(t, ctx, gate.reached)
	go func() { second <- remove.Execute(ctx, colleague, ids[1]) }()
	waitChatDatabaseLock(t, ctx, db, `FROM "agents"`, agentID)
	gate.open()
	if err := waitChatResult(t, ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := waitChatResult(t, ctx, second); err != nil {
		t.Fatal(err)
	}
	detail, err := agentaction.NewGetAgentQuery(db).Execute(ctx, owner, agentID)
	if err != nil || len(detail.Execution.MCPServerIDs) != 0 {
		t.Fatalf("concurrent delete=%+v err=%v", detail, err)
	}
}

// testAgentMCPDeleteLatestRevision 验证删除等待保存时保留新指令，且不为已解除的绑定创建版本。
func testAgentMCPDeleteLatestRevision(t *testing.T, db *bun.DB, owner, colleague *servermodels.Identity, agentID string, execution agentaction.ExecutionInput) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id := newAgentMCPService(t, db, owner)
	update := agentaction.NewUpdateExecutionAction(db)
	if _, err := update.Execute(ctx, owner, agentID, agentaction.UpdateExecutionInput{ExecutionInput: execution, MCPServerIDs: []string{id}}); err != nil {
		t.Fatal(err)
	}
	gate := newChatQueryGate(t, false, 1, func(e *bun.QueryEvent) bool {
		return strings.Contains(e.Query, `FROM "agents"`) && strings.Contains(e.Query, "FOR UPDATE") && strings.Contains(e.Query, agentID)
	})
	defer gate.open()
	managed := *execution.Managed
	managed.SystemInstruction = "并发保存的新指令"
	execution.Managed = &managed
	var saved *agentaction.Agent
	first, second := make(chan error, 1), make(chan error, 1)
	go func() {
		var err error
		saved, err = update.Execute(context.WithValue(ctx, chatQueryGateKey{}, gate), owner, agentID, agentaction.UpdateExecutionInput{ExecutionInput: execution})
		first <- err
	}()
	waitChatSignal(t, ctx, gate.reached)
	go func() { second <- mcpaction.NewDeleteMCPServerAction(db).Execute(ctx, colleague, id) }()
	waitChatDatabaseLock(t, ctx, db, `FROM "agents"`, agentID)
	gate.open()
	if err := waitChatResult(t, ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := waitChatResult(t, ctx, second); err != nil {
		t.Fatal(err)
	}
	detail, err := agentaction.NewGetAgentQuery(db).Execute(ctx, owner, agentID)
	if err != nil || detail.Execution.RevisionID != saved.Execution.RevisionID || detail.Execution.Managed.SystemInstruction != managed.SystemInstruction || len(detail.Execution.MCPServerIDs) != 0 {
		t.Fatalf("delete after save=%+v err=%v", detail, err)
	}
}

// testAgentRunMCPServices 验证运行按配置版本装配同企业 MCP 服务，未绑定的服务不进入本次运行。
func testAgentRunMCPServices(t *testing.T, db *bun.DB, owner *servermodels.Identity, modelID string, tasks *servertask.Runtime) {
	t.Helper()
	ctx := context.Background()
	execution := agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
		ModelID: modelID, SystemInstruction: "调用外部工具",
	}}
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, owner, agentaction.CreateInput{DisplayName: "MCP 运行助手", Execution: execution})
	if err != nil {
		t.Fatal(err)
	}
	bound := newAgentMCPService(t, db, owner)
	// 同企业内另一个未绑定的服务不进入本次运行。
	newAgentMCPService(t, db, owner)
	if _, err := db.NewUpdate().Model((*servermodels.MCPServer)(nil)).
		Set("authorization_token = ?", "运行期令牌").Where("id = ?", bound).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := agentaction.NewUpdateExecutionAction(db).Execute(ctx, owner, agent.ID,
		agentaction.UpdateExecutionInput{ExecutionInput: execution, MCPServerIDs: []string{bound}}); err != nil {
		t.Fatal(err)
	}
	_, run := createAgentLockChat(t, ctx, db, owner, agent.IdentityID, tasks)
	var servers []agentruntime.MCPServer
	runtime := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
		claimed, err := feed.Claim(ctx, 1)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		servers = request.MCPConnections
		return agentruntime.RunResult{Content: "已调用工具", EndSeq: claimed.EndSeq}, nil
	}}
	if err := agentrunaction.NewExecuteAction(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	var service servermodels.MCPServer
	if err := db.NewSelect().Model(&service).Where("ms.id = ?", bound).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Source != agentruntime.MCPSourceOrganization || servers[0].ID != service.ID ||
		servers[0].Name != service.Name || servers[0].Config.URL != service.URL ||
		servers[0].Config.ServerType != service.ServerType || servers[0].Config.AuthorizationToken != "运行期令牌" {
		t.Fatalf("run mcp services = %+v", servers)
	}
}
