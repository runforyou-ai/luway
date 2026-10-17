//go:build server

package agentrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	workspaceaction "github.com/runforyou-ai/luway/internal/actions/workspace"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// mediaHandlers 返回本运行的媒体公开地址解析与文本投影适配器。
func (a *ExecuteAction) mediaHandlers(ctx context.Context, run *servermodels.AgentRun, shared agentruntime.SharedFiles) (einorun.MediaURLFunc, einorun.MediaAdapter) {
	mediaURL := func(ctx context.Context, ref einorun.MediaRef) (string, error) {
		// OpenAI 兼容等适配器不接受音频 URL，空地址让 einorun 回退读字节并走 base64。
		if agentruntime.IsAudioMIME(ref.MIME) {
			return "", nil
		}
		if messageID, ok := strings.CutPrefix(ref.Key, agentcontract.MediaKeyMessage); ok {
			return a.attachments.PublicURL(ctx, run, messageID)
		}
		if path, ok := strings.CutPrefix(ref.Key, agentcontract.MediaKeyShared); ok {
			areaID := run.ConversationID
			if files, ok := shared.(*runSharedFiles); ok {
				areaID = files.area
			}
			if a.sharedFiles == nil {
				return "", fmt.Errorf("shared files are unavailable")
			}
			_, file, err := a.sharedFiles.Lookup(ctx, run.WorkspaceID, areaID, "", path)
			if err != nil {
				return "", err
			}
			return a.attachments.links.URL(file.StorageKey)
		}
		return "", fmt.Errorf("unknown media key %q", ref.Key)
	}
	readMedia := func(ctx context.Context, ref einorun.MediaRef) ([]byte, error) {
		if messageID, ok := strings.CutPrefix(ref.Key, agentcontract.MediaKeyMessage); ok {
			return a.attachments.Content(ctx, run, messageID)
		}
		if path, ok := strings.CutPrefix(ref.Key, agentcontract.MediaKeyShared); ok {
			if shared == nil {
				return nil, fmt.Errorf("shared files are unavailable")
			}
			content, err := shared.Read(ctx, path)
			if err != nil {
				return nil, err
			}
			if content.Data == nil {
				return nil, fmt.Errorf("shared file %s has no readable content", path)
			}
			return content.Data, nil
		}
		return nil, fmt.Errorf("unknown media key %q", ref.Key)
	}
	settings, err := workspaceaction.LoadMediaSettings(ctx, a.db, run.WorkspaceID)
	if err != nil {
		slog.WarnContext(ctx, "加载媒体理解设置失败", "error", err)
		settings = workspaceaction.MediaSettings{}
	}
	projector := &agentruntime.MediaProjector{MediaURL: mediaURL, ReadMedia: readMedia}
	if a.documents != nil {
		projector.ConvertDocument = func(ctx context.Context, name, _ string, data []byte) (string, error) {
			return a.documents.Convert(ctx, name, "", bytes.NewReader(data))
		}
	}
	scope := runScope(run)
	if settings.OCRModelID != nil {
		if model, err := aimodel.Resolve(ctx, a.db, run.WorkspaceID, *settings.OCRModelID, domain.AIModelUsageOCR); err == nil {
			projector.OCR = a.invoker.ChatModels(scope, model)
		} else if !errors.Is(err, aimodel.ErrUnavailable) {
			slog.WarnContext(ctx, "解析 OCR 模型失败", "error", err)
		}
	}
	if settings.TranscriptionModelID != nil {
		if model, err := aimodel.Resolve(ctx, a.db, run.WorkspaceID, *settings.TranscriptionModelID, domain.AIModelUsageTranscription); err == nil {
			projector.Transcription = a.invoker.ChatModels(scope, model)
		} else if !errors.Is(err, aimodel.ErrUnavailable) {
			slog.WarnContext(ctx, "解析转写模型失败", "error", err)
		}
	}
	return mediaURL, projector
}
