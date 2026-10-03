//go:build !server && android

package storage

// excludeFromBackup 不做处理，Android 由清单中的备份规则排除该目录。
func excludeFromBackup(string) error {
	return nil
}
