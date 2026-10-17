//go:build server

// 会话共享文件区：成员与 AI 员工共用的文件列表、读取、写入、删除与从消息附件保存，只保留每个文件的当前版本。
package conversation

import (
	"context"
	"fmt"
	"io"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

func (s *Store) Read(ctx context.Context, workspaceID, conversationID, filePath string, limit int64) (Content, error) {
	normalized, ok := domain.NormalizeConversationFilePath(filePath)
	if !ok {
		return Content{}, ErrInvalidPath
	}
	entry, file, err := s.Lookup(ctx, workspaceID, conversationID, "", normalized)
	if err != nil {
		return Content{}, err
	}
	if entry.ByteSize > limit {
		return Content{Entry: entry}, nil
	}
	data, err := s.open(ctx, file)
	if err != nil {
		return Content{}, err
	}
	return Content{Entry: entry, Data: data}, nil
}

// open 读取文件记录的全部内容。
func (s *Store) open(ctx context.Context, file *servermodels.File) ([]byte, error) {
	reader, err := s.opener.Open(ctx, file)
	if err != nil {
		return nil, fmt.Errorf("open conversation file content: %w", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read conversation file content: %w", err)
	}
	return data, nil
}
