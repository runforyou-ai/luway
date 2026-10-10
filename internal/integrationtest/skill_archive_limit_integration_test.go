//go:build server

package integrationtest

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/runforyou-ai/luway/internal/executor/localskill"
	"github.com/stretchr/testify/require"
)

// TestSkillArchiveEntryLimit 验证技能压缩包条目数超过上限时安装被拒绝，且不留下技能。
func TestSkillArchiveEntryLimit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archive := filepath.Join(dir, "many.zip")
	file, err := os.Create(archive)
	require.NoError(t, err)
	writer := zip.NewWriter(file)
	for index := range 100001 {
		entry, err := writer.Create(fmt.Sprintf("files/%05d.txt", index))
		require.NoError(t, err)
		_, err = entry.Write([]byte("x"))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	require.NoError(t, file.Close())
	installDir := filepath.Join(dir, "skills")
	store := localskill.NewStore([]localskill.Dir{{Path: installDir, Source: localskill.SourceManaged}}, func() {})
	_, err = store.Install(context.Background(), archive, "many")
	require.ErrorIs(t, err, localskill.ErrArchiveTooLarge)
	_, statErr := os.Stat(filepath.Join(installDir, "many"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}
