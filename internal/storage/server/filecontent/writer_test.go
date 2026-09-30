//go:build server

package filecontent

import (
	"context"
	"io"
	"testing"

	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

// TestWriterSavesLocalImportedFile 验证服务端导入内容按记录写入本地目录。
func TestWriterSavesLocalImportedFile(t *testing.T) {
	local, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record := &servermodels.File{
		StorageBackend: string(domain.FileStorageBackendLocal),
		StorageKey:     "organizations/org/files/avatar.jpg", ByteSize: 3,
	}
	if _, err := NewWriter(local, S3Config{}).Save(context.Background(), record, []byte("jpg")); err != nil {
		t.Fatal(err)
	}
	file, _, err := local.Open(context.Background(), record.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil || string(data) != "jpg" {
		t.Fatalf("stored data = %q, error = %v", data, err)
	}
}
