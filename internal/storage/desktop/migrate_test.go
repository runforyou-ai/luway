//go:build !server && !ios && !android

package desktop

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenRejectsAppliedMigrationWithoutSource 验证版本表记录了源文件中不存在的迁移时拒绝打开数据库。
func TestOpenRejectsAppliedMigrationWithoutSource(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "app.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, "INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, ?)", 20260927092051, true); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, databasePath); err == nil || !strings.Contains(err.Error(), "20260927092051") {
		t.Fatalf("open error = %v, want missing migration source", err)
	}
}
