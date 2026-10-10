//go:build server

package integrationtest

import (
	"context"
	"sync"
	"testing"
	"uuid"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// ensureConcurrency 是并发取得或创建测试同时发起的事务数，小于测试连接池上限。
const ensureConcurrency = 6

// openEnsureTestDB 打开测试库并创建独立工作区，返回连接与工作区编号。
func openEnsureTestDB(t *testing.T, name string) (*bun.DB, string) {
	t.Helper()
	store, err := openSharedTestDatabase(context.Background(), servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	installed := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: name, DisplayName: "管理员", Email: servertest.UniqueEmail("ensure-owner"), Password: "password123"})
	return db, installed.Identity.Workspace.ID
}

// runConcurrently 让 ensureConcurrency 个事务都开始后同时执行 fn，返回各事务的结果。
func runConcurrently[T any](t *testing.T, db *bun.DB, fn func(context.Context, bun.Tx, int) (T, error)) []T {
	t.Helper()
	results := make([]T, ensureConcurrency)
	start := make(chan struct{})
	var ready, done sync.WaitGroup
	for index := range ensureConcurrency {
		ready.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			err := db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
				// 事务取得快照后等待其他事务就绪。
				if _, err := tx.ExecContext(ctx, "SELECT 1"); err != nil {
					ready.Done()
					return err
				}
				ready.Done()
				<-start
				var err error
				results[index], err = fn(ctx, tx, index)
				return err
			})
			assert.NoError(t, err)
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
	require.False(t, t.Failed())
	return results
}

// TestEnsureSubjectConcurrent 验证并发取得或创建同一来源的聊天主体只产生一行，所有调用返回同一记录。
func TestEnsureSubjectConcurrent(t *testing.T) {
	t.Parallel()
	db, workspaceID := openEnsureTestDB(t, "主体并发")
	ctx := context.Background()
	sourceID := uuid.NewV7().String()

	subjects := runConcurrently(t, db, func(ctx context.Context, tx bun.Tx, _ int) (*servermodels.ChatSubject, error) {
		return chatstate.EnsureSubject(ctx, tx, workspaceID, domain.ChatSubjectKindContact, sourceID, uuid.NewV7().String())
	})
	var stored []*servermodels.ChatSubject
	require.NoError(t, db.NewSelect().Model(&stored).Where("cs.workspace_id = ? AND cs.kind = ? AND cs.source_id = ?", workspaceID, domain.ChatSubjectKindContact, sourceID).Scan(ctx))
	require.Len(t, stored, 1)
	for _, subject := range subjects {
		require.Empty(t, cmp.Diff(stored[0], subject))
	}

	// 已存在时直接返回原记录，传入的新编号不生效。
	again, err := chatstate.EnsureSubject(ctx, db, workspaceID, domain.ChatSubjectKindContact, sourceID, uuid.NewV7().String())
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(stored[0], again))
}

// TestEnsureSubjectsConcurrent 验证并发批量取得或创建部分重叠的聊天主体时每个来源只产生一行，所有调用返回同一记录。
func TestEnsureSubjectsConcurrent(t *testing.T) {
	t.Parallel()
	db, workspaceID := openEnsureTestDB(t, "批量主体并发")
	ctx := context.Background()
	shared := []string{uuid.NewV7().String(), uuid.NewV7().String()}
	existing, err := chatstate.EnsureSubject(ctx, db, workspaceID, domain.ChatSubjectKindWorkspaceIdentity, shared[0], uuid.NewV7().String())
	require.NoError(t, err)

	results := runConcurrently(t, db, func(ctx context.Context, tx bun.Tx, index int) (map[string]*servermodels.ChatSubject, error) {
		// 各调用携带一个共享来源的重复项与一个独有来源。
		return chatstate.EnsureSubjects(ctx, tx, workspaceID, domain.ChatSubjectKindWorkspaceIdentity, []string{shared[1], shared[0], shared[1], uuid.NewV7().String()})
	})
	var stored []*servermodels.ChatSubject
	require.NoError(t, db.NewSelect().Model(&stored).Where("cs.workspace_id = ? AND cs.kind = ? AND cs.source_id IN (?)", workspaceID, domain.ChatSubjectKindWorkspaceIdentity, bun.List(shared)).Scan(ctx))
	require.Len(t, stored, 2)
	bySource := arr.KeyBy(stored, func(subject *servermodels.ChatSubject) string { return subject.SourceID })
	require.Empty(t, cmp.Diff(existing, bySource[shared[0]]))
	for _, subjects := range results {
		require.Len(t, subjects, 3)
		for _, sourceID := range shared {
			require.Empty(t, cmp.Diff(bySource[sourceID], subjects[sourceID]))
		}
	}
}

// TestEnsureParticipantConcurrent 验证并发取得或创建同一会话参与者只产生一行成员参与者，所有调用返回同一记录。
func TestEnsureParticipantConcurrent(t *testing.T) {
	t.Parallel()
	db, workspaceID := openEnsureTestDB(t, "参与者并发")
	ctx := context.Background()
	conversationID := uuid.NewV7().String()
	subject, err := chatstate.EnsureSubject(ctx, db, workspaceID, domain.ChatSubjectKindContact, uuid.NewV7().String(), uuid.NewV7().String())
	require.NoError(t, err)

	participants := runConcurrently(t, db, func(ctx context.Context, tx bun.Tx, _ int) (*servermodels.ConversationParticipant, error) {
		return chatstate.EnsureParticipant(ctx, tx, workspaceID, conversationID, subject.ID, uuid.NewV7().String())
	})
	var stored []*servermodels.ConversationParticipant
	require.NoError(t, db.NewSelect().Model(&stored).Where("cp.workspace_id = ? AND cp.conversation_id = ? AND cp.subject_id = ?", workspaceID, conversationID, subject.ID).Scan(ctx))
	require.Len(t, stored, 1)
	require.Equal(t, string(domain.ConversationParticipantRoleMember), stored[0].Role)
	require.Nil(t, stored[0].LeftAt)
	for _, participant := range participants {
		require.Empty(t, cmp.Diff(stored[0], participant))
	}
}

// TestEnsureParticipantRejoin 验证在会参与者原样返回不写库，已退出的参与者沿用原记录以成员角色重新加入。
func TestEnsureParticipantRejoin(t *testing.T) {
	t.Parallel()
	db, workspaceID := openEnsureTestDB(t, "参与者重新加入")
	ctx := context.Background()
	conversationID := uuid.NewV7().String()
	subject, err := chatstate.EnsureSubject(ctx, db, workspaceID, domain.ChatSubjectKindWorkspaceIdentity, uuid.NewV7().String(), uuid.NewV7().String())
	require.NoError(t, err)
	created, err := chatstate.EnsureParticipant(ctx, db, workspaceID, conversationID, subject.ID, uuid.NewV7().String())
	require.NoError(t, err)

	// 在会参与者的角色与更新时间保持不变。
	_, err = db.NewUpdate().Model((*servermodels.ConversationParticipant)(nil)).
		Set("role = ?", domain.ConversationParticipantRoleOwner).
		Where("workspace_id = ? AND id = ?", workspaceID, created.ID).Exec(ctx)
	require.NoError(t, err)
	active := &servermodels.ConversationParticipant{}
	require.NoError(t, db.NewSelect().Model(active).Where("cp.id = ?", created.ID).Scan(ctx))
	got, err := chatstate.EnsureParticipant(ctx, db, workspaceID, conversationID, subject.ID, uuid.NewV7().String())
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(active, got))

	// 已退出的参与者清空退出时间并恢复为成员，编号与加入时间沿用原记录。
	_, err = db.NewUpdate().Model((*servermodels.ConversationParticipant)(nil)).
		Set("left_at = now()").
		Where("workspace_id = ? AND id = ?", workspaceID, created.ID).Exec(ctx)
	require.NoError(t, err)
	got, err = chatstate.EnsureParticipant(ctx, db, workspaceID, conversationID, subject.ID, uuid.NewV7().String())
	require.NoError(t, err)
	restored := &servermodels.ConversationParticipant{}
	require.NoError(t, db.NewSelect().Model(restored).Where("cp.id = ?", created.ID).Scan(ctx))
	require.Nil(t, restored.LeftAt)
	require.Equal(t, string(domain.ConversationParticipantRoleMember), restored.Role)
	require.True(t, restored.UpdatedAt.After(active.UpdatedAt))
	require.Empty(t, cmp.Diff(active, restored, cmpopts.IgnoreFields(servermodels.ConversationParticipant{}, "Role", "UpdatedAt")))
	require.Empty(t, cmp.Diff(restored, got, cmpopts.IgnoreFields(servermodels.ConversationParticipant{}, "UpdatedAt")))
}
