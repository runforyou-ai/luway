//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/einorun"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	businesssystemaction "github.com/runforyou-ai/luway/internal/actions/businesssystem"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// newAgentBusinessSystem 创建远端不可用、工具目录含一个只读工具的授权测试业务系统。
func newAgentBusinessSystem(t *testing.T, db *bun.DB, identity *servermodels.Identity) string {
	t.Helper()
	readOnly := true
	system := &servermodels.BusinessSystem{
		WorkspaceID: identity.Workspace.ID, Name: uuid.NewV7().String(), Transport: domain.BusinessSystemTransportMCP,
		Connection: domain.BusinessSystemConnection{MCP: &domain.MCPConnection{URL: "http://127.0.0.1:1/mcp", ServerType: domain.MCPServerTypeStreamableHTTP}},
		Credential: domain.BusinessSystemCredential{Kind: domain.BusinessSystemCredentialNone}, ToolsFailure: "unavailable",
		Tools: []domain.BusinessTool{{Name: "lookup", Description: "查询", ReadOnlyHint: &readOnly}},
	}
	_, err := db.NewInsert().Model(system).Column("workspace_id", "name", "transport", "connection", "credential", "tools", "tools_failure").Returning("id").Exec(context.Background())
	require.NoError(t, err)
	return system.ID
}

// readOnlyGrants 为业务系统编号生成只读查询级别的授权。
func readOnlyGrants(ids ...string) []domain.BusinessSystemGrant {
	return arr.Map(ids, func(id string) domain.BusinessSystemGrant {
		return domain.BusinessSystemGrant{BusinessSystemID: id, ToolGrant: domain.ToolGrant{MaxLevel: domain.OperationLevelL1}}
	})
}

// grantIDs 返回授权中的业务系统编号。
func grantIDs(grants []domain.BusinessSystemGrant) []string {
	return arr.Map(grants, func(grant domain.BusinessSystemGrant) string { return grant.BusinessSystemID })
}

// TestAgentBusinessSystems 验证业务系统授权的整体保存、工作区隔离、删除联动和并发事务。
func TestAgentBusinessSystems(t *testing.T) {
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
	remove := businesssystemaction.NewDeleteBusinessSystemAction(db)
	agents := make([]*agentaction.Agent, 0, 2)
	for range 2 {
		agent, err := create.Execute(ctx, owner, agentaction.CreateInput{DisplayName: "业务系统授权助手", Execution: execution})
		require.NoError(t, err)
		require.Empty(t, agent.Execution.BusinessSystems, "new agent has business system grants")
		agents = append(agents, agent)
	}
	ids := []string{newAgentBusinessSystem(t, db, owner), newAgentBusinessSystem(t, db, owner)}
	slices.Sort(ids)
	foreignOwner, _ := newQAFixture(t, db)
	foreignID := newAgentBusinessSystem(t, db, foreignOwner)
	options, err := agentaction.NewListBusinessSystemOptionsQuery(db).Execute(ctx, owner)
	require.NoError(t, err)
	for _, id := range ids {
		require.True(t, slices.ContainsFunc(options, func(o agentaction.BusinessSystemOption) bool { return o.ID == id && o.ToolCount == 1 }), "missing offline service %s: %+v", id, options)
	}
	require.False(t, slices.ContainsFunc(options, func(o agentaction.BusinessSystemOption) bool { return o.ID == foreignID }), "foreign service leaked")
	for i, agent := range agents {
		saved, err := update.Execute(ctx, owner, agent.ID, agentaction.UpdateExecutionInput{ExecutionInput: execution, BusinessSystems: readOnlyGrants(ids[1], ids[0])})
		require.NoError(t, err)
		require.Equal(t, ids, grantIDs(saved.Execution.BusinessSystems))
		agents[i] = saved
	}
	var before servermodels.AgentRevision
	require.NoError(t, db.NewSelect().Model(&before).Where("agent_id = ?", agents[0].ID).OrderExpr("created_at DESC, id DESC").Limit(1).Scan(ctx))
	var snapshot map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(before.Configuration, &snapshot))
	var snapshotGrants []domain.BusinessSystemGrant
	require.NoError(t, json.Unmarshal(snapshot["businessSystems"], &snapshotGrants))
	require.Equal(t, readOnlyGrants(ids...), snapshotGrants)
	invalidGrants := [][]domain.BusinessSystemGrant{
		readOnlyGrants(foreignID), readOnlyGrants(uuid.NewV7().String()), readOnlyGrants("invalid"), readOnlyGrants(ids[0], ids[0]),
		{{BusinessSystemID: ids[0], ToolGrant: domain.ToolGrant{MaxLevel: "l9"}}},
	}
	for _, grants := range invalidGrants {
		_, err := update.Execute(ctx, owner, agents[0].ID, agentaction.UpdateExecutionInput{ExecutionInput: execution, BusinessSystems: grants})
		var fields *common.FieldError
		require.ErrorAs(t, err, &fields, "invalid grant %v", grants)
		require.Equal(t, agentaction.ValidationBusinessSystemInvalid, fields.Fields["businessSystems"], "invalid grant %v", grants)
	}
	// 核验字段校验失败后配置版本与当前版本指针保持原值。
	invalid := agentaction.ExecutionInput{Mode: execution.Mode, Managed: &agentaction.ManagedExecutionInput{ModelID: uuid.NewV7().String(), SystemInstruction: "新指令"}}
	_, err = update.Execute(ctx, owner, agents[0].ID, agentaction.UpdateExecutionInput{ExecutionInput: invalid})
	require.Error(t, err, "invalid model saved")
	detail, err := get.Execute(ctx, owner, agents[0].ID)
	require.NoError(t, err)
	require.Equal(t, before.ID, detail.Execution.RevisionID)
	require.Equal(t, ids, grantIDs(detail.Execution.BusinessSystems))
	require.ErrorIs(t, remove.Execute(ctx, foreignOwner, ids[0]), businesssystemaction.ErrNotFound, "foreign delete")
	require.NoError(t, remove.Execute(ctx, owner, ids[0]))
	for _, agent := range agents {
		detail, err := get.Execute(ctx, owner, agent.ID)
		require.NoError(t, err)
		require.NotEqual(t, agent.Execution.RevisionID, detail.Execution.RevisionID)
		require.Equal(t, ids[1:], grantIDs(detail.Execution.BusinessSystems))
		var current servermodels.AgentRevision
		require.NoError(t, db.NewSelect().Model(&current).Where("id = ?", detail.Execution.RevisionID).Scan(ctx))
		var configuration map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(current.Configuration, &configuration))
		for key, value := range snapshot {
			if key != "businessSystems" {
				require.Equal(t, string(value), string(configuration[key]), "deletion changed %s", key)
			}
		}
		var referenced []string
		require.NoError(t, db.NewSelect().Model((*servermodels.AgentRevisionBusinessSystem)(nil)).Column("business_system_id").
			Where("revision_id = ?", current.ID).OrderExpr("business_system_id ASC").Scan(ctx, &referenced))
		require.Equal(t, ids[1:], referenced, "revision business system references")
		count, err := db.NewSelect().Model((*servermodels.AgentRevision)(nil)).Where("agent_id = ?", agent.ID).Count(ctx)
		require.NoError(t, err)
		require.Equal(t, int64(3), count)
		require.Equal(t, owner.User.ID, current.CreatedByUserID)
	}
	var after servermodels.AgentRevision
	require.NoError(t, db.NewSelect().Model(&after).Where("id = ?", before.ID).Scan(ctx))
	require.Equal(t, string(before.Configuration), string(after.Configuration), "deletion changed revision")
	cleared, err := update.Execute(ctx, owner, agents[0].ID, agentaction.UpdateExecutionInput{ExecutionInput: execution})
	require.NoError(t, err)
	require.Empty(t, cleared.Execution.BusinessSystems)

	require.NoError(t, remove.Execute(ctx, owner, ids[1]))
	detail, err = get.Execute(ctx, owner, agents[0].ID)
	require.NoError(t, err)
	require.Equal(t, cleared.Execution.RevisionID, detail.Execution.RevisionID, "historical-only reference created revision")
	detail, err = get.Execute(ctx, owner, agents[1].ID)
	require.NoError(t, err)
	require.Empty(t, detail.Execution.BusinessSystems)

	// 使用两个用户和查询屏障控制服务及员工的取锁顺序。
	colleague := newChatLockUser(t, db, owner)
	db.AddQueryHook(chatQueryHook{})
	for _, saveFirst := range []bool{true, false} {
		name := "删除先取得锁"
		if saveFirst {
			name = "保存先取得锁"
		}
		t.Run(name, func(t *testing.T) {
			testAgentBusinessSystemDeleteRace(t, db, owner, colleague, agents[0].ID, execution, saveFirst)
		})
	}
	t.Run("删除失败回滚版本", func(t *testing.T) {
		testAgentBusinessSystemDeleteRollback(t, db, owner, agents[0].ID, execution)
	})
	t.Run("同时删除两个服务", func(t *testing.T) {
		testAgentBusinessSystemConcurrentDeletes(t, db, owner, colleague, agents[0].ID, execution)
	})
	t.Run("删除等待最新配置", func(t *testing.T) {
		testAgentBusinessSystemDeleteLatestRevision(t, db, owner, colleague, agents[0].ID, execution)
	})
	t.Run("两次保存串行替换", func(t *testing.T) {
		testAgentBusinessSystemSaveRace(t, db, owner, colleague, agents[0].ID, execution)
	})
}

// testAgentBusinessSystemDeleteRace 验证服务删除与绑定保存按取锁顺序收敛。
func testAgentBusinessSystemDeleteRace(t *testing.T, db *bun.DB, owner, colleague *servermodels.Identity, agentID string, execution agentaction.ExecutionInput, saveFirst bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id := newAgentBusinessSystem(t, db, owner)
	gate := newChatQueryGate(t, false, 1, func(e *bun.QueryEvent) bool {
		return strings.Contains(e.Query, `FROM "business_systems"`) && strings.Contains(e.Query, id) && strings.Contains(e.Query, "FOR ")
	})
	// 失败路径也要先释放事务，再交由外层连接清理。
	defer gate.open()
	gated := context.WithValue(ctx, chatQueryGateKey{}, gate)
	update := agentaction.NewUpdateExecutionAction(db)
	remove := businesssystemaction.NewDeleteBusinessSystemAction(db)
	saved, deleted := make(chan error, 1), make(chan error, 1)
	saveCtx, deleteCtx := ctx, gated
	if saveFirst {
		saveCtx, deleteCtx = gated, ctx
	}
	save := func() {
		_, err := update.Execute(saveCtx, owner, agentID, agentaction.UpdateExecutionInput{ExecutionInput: execution, BusinessSystems: readOnlyGrants(id)})
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
	waitChatDatabaseLock(t, ctx, db, `FROM "business_systems"`, id)
	gate.open()
	saveErr := waitChatResult(t, ctx, saved)
	if saveFirst {
		require.NoError(t, saveErr)
	}
	if !saveFirst {
		var fields *common.FieldError
		require.ErrorAs(t, saveErr, &fields, "stale save")
		require.Equal(t, agentaction.ValidationBusinessSystemInvalid, fields.Fields["businessSystems"], "stale save")
	}
	require.NoError(t, waitChatResult(t, ctx, deleted))
	count, err := db.NewSelect().Model((*servermodels.Agent)(nil)).
		Join("JOIN agent_revision_business_systems AS arbs ON arbs.workspace_id = a.workspace_id AND arbs.agent_id = a.id AND arbs.revision_id = a.active_revision_id").
		Where("a.workspace_id = ?", owner.Workspace.ID).
		Where("arbs.business_system_id = ?", id).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count, "dangling bindings")
}

// testAgentBusinessSystemSaveRace 验证同一员工并发保存时各草稿的版本独立性。
func testAgentBusinessSystemSaveRace(t *testing.T, db *bun.DB, owner, colleague *servermodels.Identity, agentID string, execution agentaction.ExecutionInput) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ids := []string{newAgentBusinessSystem(t, db, owner), newAgentBusinessSystem(t, db, owner)}
	gate := newChatQueryGate(t, false, 1, func(e *bun.QueryEvent) bool {
		return strings.Contains(e.Query, `FROM "agents"`) && strings.Contains(e.Query, agentID) && strings.Contains(e.Query, "FOR UPDATE")
	})
	defer gate.open()
	update := agentaction.NewUpdateExecutionAction(db)
	first, second := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := update.Execute(context.WithValue(ctx, chatQueryGateKey{}, gate), owner, agentID, agentaction.UpdateExecutionInput{ExecutionInput: execution, BusinessSystems: readOnlyGrants(ids[0])})
		first <- err
	}()
	waitChatSignal(t, ctx, gate.reached)
	go func() {
		_, err := update.Execute(ctx, colleague, agentID, agentaction.UpdateExecutionInput{ExecutionInput: execution, BusinessSystems: readOnlyGrants(ids[1])})
		second <- err
	}()
	waitChatDatabaseLock(t, ctx, db, `FROM "agents"`, agentID)
	gate.open()
	require.NoError(t, waitChatResult(t, ctx, first))
	require.NoError(t, waitChatResult(t, ctx, second))
	detail, err := agentaction.NewGetAgentQuery(db).Execute(ctx, owner, agentID)
	require.NoError(t, err)
	require.Equal(t, ids[1:], grantIDs(detail.Execution.BusinessSystems))
}

// testAgentBusinessSystemDeleteRollback 验证删除末尾失败会回滚服务、所有新版本和当前版本指针。
func testAgentBusinessSystemDeleteRollback(t *testing.T, db *bun.DB, owner *servermodels.Identity, agentID string, execution agentaction.ExecutionInput) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id := newAgentBusinessSystem(t, db, owner)
	saved, err := agentaction.NewUpdateExecutionAction(db).Execute(ctx, owner, agentID, agentaction.UpdateExecutionInput{ExecutionInput: execution, BusinessSystems: readOnlyGrants(id)})
	require.NoError(t, err)
	before, err := db.NewSelect().Model((*servermodels.AgentRevision)(nil)).Where("agent_id = ?", agentID).Count(ctx)
	require.NoError(t, err)
	gate := newChatQueryGate(t, true, 1, func(e *bun.QueryEvent) bool {
		return strings.Contains(e.Query, `DELETE FROM "business_systems"`) && strings.Contains(e.Query, id)
	})
	defer gate.open()
	deleteCtx, cancelDelete := context.WithCancel(context.WithValue(ctx, chatQueryGateKey{}, gate))
	defer cancelDelete()
	result := make(chan error, 1)
	go func() { result <- businesssystemaction.NewDeleteBusinessSystemAction(db).Execute(deleteCtx, owner, id) }()
	waitChatSignal(t, ctx, gate.reached)
	cancelDelete()
	require.Error(t, waitChatResult(t, ctx, result), "cancelled deletion succeeded")
	detail, err := agentaction.NewGetAgentQuery(db).Execute(ctx, owner, agentID)
	require.NoError(t, err)
	require.Equal(t, saved.Execution.RevisionID, detail.Execution.RevisionID)
	require.Equal(t, []string{id}, grantIDs(detail.Execution.BusinessSystems))
	after, err := db.NewSelect().Model((*servermodels.AgentRevision)(nil)).Where("agent_id = ?", agentID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after, "rollback revision count")
	exists, err := db.NewSelect().Model((*servermodels.BusinessSystem)(nil)).Where("id = ?", id).Exists(ctx)
	require.NoError(t, err)
	require.True(t, exists, "service after rollback")
}

// testAgentBusinessSystemConcurrentDeletes 验证两个服务同时删除时基于最新版本累积移除。
func testAgentBusinessSystemConcurrentDeletes(t *testing.T, db *bun.DB, owner, colleague *servermodels.Identity, agentID string, execution agentaction.ExecutionInput) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ids := []string{newAgentBusinessSystem(t, db, owner), newAgentBusinessSystem(t, db, owner)}
	_, err := agentaction.NewUpdateExecutionAction(db).Execute(ctx, owner, agentID, agentaction.UpdateExecutionInput{ExecutionInput: execution, BusinessSystems: readOnlyGrants(ids...)})
	require.NoError(t, err)
	gate := newChatQueryGate(t, false, 1, func(e *bun.QueryEvent) bool {
		return strings.Contains(e.Query, `FROM "agents"`) && strings.Contains(e.Query, "FOR UPDATE") && strings.Contains(e.Query, agentID)
	})
	defer gate.open()
	remove := businesssystemaction.NewDeleteBusinessSystemAction(db)
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- remove.Execute(context.WithValue(ctx, chatQueryGateKey{}, gate), owner, ids[0]) }()
	waitChatSignal(t, ctx, gate.reached)
	go func() { second <- remove.Execute(ctx, colleague, ids[1]) }()
	waitChatDatabaseLock(t, ctx, db, `FROM "agents"`, agentID)
	gate.open()
	require.NoError(t, waitChatResult(t, ctx, first))
	require.NoError(t, waitChatResult(t, ctx, second))
	detail, err := agentaction.NewGetAgentQuery(db).Execute(ctx, owner, agentID)
	require.NoError(t, err)
	require.Empty(t, detail.Execution.BusinessSystems, "concurrent delete")
}

// testAgentBusinessSystemDeleteLatestRevision 验证删除等待保存时保留新指令，且不为已解除的绑定创建版本。
func testAgentBusinessSystemDeleteLatestRevision(t *testing.T, db *bun.DB, owner, colleague *servermodels.Identity, agentID string, execution agentaction.ExecutionInput) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id := newAgentBusinessSystem(t, db, owner)
	update := agentaction.NewUpdateExecutionAction(db)
	_, err := update.Execute(ctx, owner, agentID, agentaction.UpdateExecutionInput{ExecutionInput: execution, BusinessSystems: readOnlyGrants(id)})
	require.NoError(t, err)
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
	go func() { second <- businesssystemaction.NewDeleteBusinessSystemAction(db).Execute(ctx, colleague, id) }()
	waitChatDatabaseLock(t, ctx, db, `FROM "agents"`, agentID)
	gate.open()
	require.NoError(t, waitChatResult(t, ctx, first))
	require.NoError(t, waitChatResult(t, ctx, second))
	detail, err := agentaction.NewGetAgentQuery(db).Execute(ctx, owner, agentID)
	require.NoError(t, err)
	require.Equal(t, saved.Execution.RevisionID, detail.Execution.RevisionID)
	require.Equal(t, managed.SystemInstruction, detail.Execution.Managed.SystemInstruction)
	require.Empty(t, detail.Execution.BusinessSystems)
}

// testAgentRunBusinessSystems 验证运行按配置版本的授权装配同工作区业务系统，未授权的业务系统不进入本次运行。
func testAgentRunBusinessSystems(t *testing.T, db *bun.DB, owner *servermodels.Identity, modelID string, tasks *servertest.Tasks) {
	t.Helper()
	ctx := context.Background()
	execution := agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
		ModelID: modelID, SystemInstruction: "调用外部工具",
	}}
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, owner, agentaction.CreateInput{DisplayName: "业务系统运行助手", Execution: execution})
	require.NoError(t, err)
	// 授权的业务系统改为 HTTP 接口，运行内的调用经统一执行入口附加凭据发出。
	var authorization atomic.Value
	erp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization.Store(r.Method + " " + r.URL.Path + " " + r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"status":"shipped"}`))
	}))
	defer erp.Close()
	bound := newAgentBusinessSystem(t, db, owner)
	// 同工作区内另一个未授权的业务系统不进入本次运行。
	newAgentBusinessSystem(t, db, owner)
	connection := domain.BusinessSystemConnection{HTTP: &domain.HTTPConnection{BaseURL: erp.URL, Spec: "{}"}}
	credential := domain.BusinessSystemCredential{Kind: domain.BusinessSystemCredentialBearer, Token: "运行期令牌"}
	tools := []domain.BusinessTool{{Name: "lookup", Description: "查询", HTTP: &domain.HTTPOperation{Method: "GET", Path: "/orders", Parameters: []domain.HTTPParameter{}}}}
	_, err = db.NewUpdate().Model(&servermodels.BusinessSystem{
		ID: bound, Transport: domain.BusinessSystemTransportHTTP, Connection: connection, Credential: credential, Tools: tools,
	}).Column("transport", "connection", "credential", "tools").WherePK().Exec(ctx)
	require.NoError(t, err)
	_, err = agentaction.NewUpdateExecutionAction(db).Execute(ctx, owner, agent.ID,
		agentaction.UpdateExecutionInput{ExecutionInput: execution, BusinessSystems: readOnlyGrants(bound)})
	require.NoError(t, err)
	_, run := createAgentLockChat(t, ctx, db, owner, agent.IdentityID, tasks)
	var systems []agentcontract.BusinessSystem
	var output string
	runtime := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		claimed, err := feed.Claim(ctx, 1)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		systems = request.BusinessSystems
		if len(systems) == 1 {
			if output, err = systems[0].Caller.Call(ctx, "lookup", json.RawMessage(`{}`)); err != nil {
				return agentruntime.RunResult{}, err
			}
		}
		return agentruntime.RunResult{Content: "已调用工具", EndSeq: claimed.EndSeq}, nil
	}}
	require.NoError(t, newTestAgentRun(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	var system servermodels.BusinessSystem
	require.NoError(t, db.NewSelect().Model(&system).Where("bs.id = ?", bound).Scan(ctx))
	require.Len(t, systems, 1)
	require.Equal(t, system.ID, systems[0].ID)
	require.Equal(t, system.Name, systems[0].Name)
	require.Len(t, systems[0].Tools, 1)
	require.Equal(t, domain.OperationLevelL0, systems[0].Tools[0].Level)
	require.True(t, systems[0].Tools[0].ReadOnly)
	require.Equal(t, "查询", systems[0].Tools[0].Description)
	require.Equal(t, `{"status":"shipped"}`, output)
	require.Equal(t, "GET /orders Bearer 运行期令牌", authorization.Load())
}
