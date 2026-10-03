//go:build !server && !windows

package native

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestOpenSQLitePermissions 验证数据目录与数据库文件只允许当前用户访问。
func TestOpenSQLitePermissions(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(directory, "test.db")
	db, err := openSQLite(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE example (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}

	expected := map[string]os.FileMode{
		directory:             0o700,
		databasePath:          0o600,
		databasePath + "-wal": 0o600,
		databasePath + "-shm": 0o600,
	}
	for path, mode := range expected {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Errorf("%s 权限为 %o，应为 %o", filepath.Base(path), info.Mode().Perm(), mode)
		}
	}
}
