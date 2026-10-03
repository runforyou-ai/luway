//go:build server

package organization

import (
	"context"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// TestInsertOrganizationRetriesSlugConflict 验证标识冲突后重新插入成功，重试耗尽后事务仍可查询。
func TestInsertOrganizationRetriesSlugConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	taken := "ws-" + strings.ToLower(rand.Text())
	first, err := insertOrganization(ctx, tx, "first-"+taken[3:15], func() string { return taken })
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	second, err := insertOrganization(ctx, tx, "second-"+taken[3:15], func() string {
		attempts++
		if attempts == 1 {
			return taken
		}
		return "ws-" + strings.ToLower(rand.Text())
	})
	if err != nil || attempts != 2 || second.ID == first.ID || second.Slug == taken || !domain.WorkspaceSlugValid(second.Slug) {
		t.Fatalf("second = %#v, attempts = %d, err = %v", second, attempts, err)
	}
	attempts = 0
	_, err = insertOrganization(ctx, tx, "exhausted-"+taken[3:15], func() string {
		attempts++
		return taken
	})
	if err == nil || attempts != 3 {
		t.Fatalf("exhausted attempts = %d, err = %v", attempts, err)
	}
	count, err := tx.NewSelect().Model((*servermodels.Organization)(nil)).Where("id IN (?, ?)", first.ID, second.ID).Count(ctx)
	if err != nil || count != 2 {
		t.Fatalf("transaction count = %d, err = %v", count, err)
	}
}
