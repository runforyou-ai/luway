//go:build server

package filecontent

import (
	"context"
	"io"

	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// Writer 按文件记录写入内容。
type Writer interface {
	Save(context.Context, *servermodels.File, []byte) (string, error)
}

// Opener 按文件记录流式读取内容。
type Opener interface {
	Open(context.Context, *servermodels.File) (io.ReadCloser, error)
}

// Deleter 按文件记录删除内容。
type Deleter interface {
	Delete(context.Context, *servermodels.File) error
}
