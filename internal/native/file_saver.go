//go:build !server

package native

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// FileSaver 使用 Wails 原生保存对话框把文本写入用户选择的文件。
type FileSaver struct{}

// NewFileSaver 创建原生文件保存器。
func NewFileSaver() *FileSaver {
	return &FileSaver{}
}

// SaveTextFile 以建议文件名打开保存对话框并写入文本，用户取消时返回 false。
func (*FileSaver) SaveTextFile(_ context.Context, _ appservice.RequestMeta, input TextFileInput) (bool, error) {
	app := application.Get()
	if app == nil {
		return false, errors.New("application is not initialized")
	}
	path, err := app.Dialog.SaveFile().SetFilename(input.Name).CanCreateDirectories(true).PromptForSingleSelection()
	if err != nil {
		return false, fmt.Errorf("select save path: %w", err)
	}
	if path == "" {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(input.Content), 0o644); err != nil {
		return false, fmt.Errorf("write file: %w", err)
	}
	return true, nil
}
