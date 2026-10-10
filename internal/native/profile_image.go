//go:build !server

// Package native 提供原生端应用服务的平台能力实现。
package native

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// maxImageByteSize 是可选图片的字节上限。
const maxImageByteSize int64 = 5 * 1024 * 1024

// imageContentTypes 是可选图片扩展名对应的内容类型。
var imageContentTypes = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".webp": "image/webp",
}

// ImageSelector 使用 Wails 原生对话框选择图片文件。
type ImageSelector struct{}

// NewImageSelector 创建原生图片文件选择器。
func NewImageSelector() *ImageSelector {
	return &ImageSelector{}
}

// SelectImage 选择并读取图片。
func (*ImageSelector) SelectImage(_ context.Context, meta appservice.RequestMeta) (ImageFile, error) {
	app := application.Get()
	if app == nil {
		return ImageFile{}, errors.New("application is not initialized")
	}

	messages := i18n.LocalizeMap(string(meta.Locale), map[string]i18n.Key{
		"title":  i18n.DialogImageTitle,
		"choose": i18n.DialogImageChoose,
	})
	title, buttonText := messages["title"], messages["choose"]
	path, err := app.Dialog.OpenFile().
		CanChooseFiles(true).
		CanChooseDirectories(false).
		AllowsOtherFileTypes(false).
		TreatsFilePackagesAsDirectories(false).
		AddFilter(title, "*.jpg;*.jpeg;*.png;*.webp").
		SetTitle(title).
		SetButtonText(buttonText).
		PromptForSingleSelection()
	if err != nil {
		slog.WarnContext(context.Background(), "打开图片文件选择器失败", "error", err)
		return ImageFile{}, fmt.Errorf("select image: %w", err)
	}
	if path == "" {
		return ImageFile{}, nil
	}

	selected, err := readImageFile(path)
	if err != nil {
		slog.WarnContext(context.Background(), "读取图片文件失败", "file_name", filepath.Base(path), "error", err)
		return ImageFile{}, err
	}
	return selected, nil
}

// readImageFile 校验并读取图片。
func readImageFile(path string) (ImageFile, error) {
	contentType, allowed := imageContentTypes[strings.ToLower(filepath.Ext(path))]
	if !allowed {
		return ImageFile{}, errors.New("unsupported image type")
	}
	info, err := os.Stat(path)
	if err != nil {
		return ImageFile{}, fmt.Errorf("stat image: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxImageByteSize {
		return ImageFile{}, errors.New("invalid image size")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return ImageFile{}, fmt.Errorf("read image: %w", err)
	}
	if http.DetectContentType(content) != contentType {
		return ImageFile{}, errors.New("image content does not match its extension")
	}
	return ImageFile{
		Name:        filepath.Base(path),
		ContentType: contentType,
		DataBase64:  base64.StdEncoding.EncodeToString(content),
	}, nil
}
