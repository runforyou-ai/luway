package agentruntime

import (
	"context"
	"encoding/base64"
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/einorun/llm"
)

const (
	ocrInstruction           = "提取图片中的全部文字。只输出识别到的文字，不要解释。"
	transcriptionInstruction = "把这段音频转写成文字。只输出转写结果，不要解释。"
	mediaProjectionExtension = "media_projection"
)

// MediaProjector 把模型不能直传的媒体投影为文本，并累计投影调用的用量。
type MediaProjector struct {
	OCR             llm.ModelFactory
	Transcription   llm.ModelFactory
	MediaURL        einorun.MediaURLFunc
	ReadMedia       einorun.MediaReader
	ConvertDocument func(ctx context.Context, name, contentType string, data []byte) (string, error)

	mu    sync.Mutex
	usage llm.Usage
}

// Name 返回扩展名称。
func (p *MediaProjector) Name() string { return mediaProjectionExtension }

// Usage 返回本运行中投影调用的累计用量。
func (p *MediaProjector) Usage() llm.Usage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.usage
}

// Adapt 按 MIME 投影媒体：图片 OCR、音频转写、文档转 Markdown；视频一期不投影。
func (p *MediaProjector) Adapt(ctx context.Context, ref einorun.MediaRef, _ einorun.AdaptPolicy) (einorun.Adaptation, error) {
	modality, ok := projectableModality(ref.MIME)
	switch {
	case ok && modality == llm.Image:
		text, err := p.projectMultimodal(ctx, p.OCR, ocrInstruction, ref, llm.Image)
		if err != nil {
			return einorun.Adaptation{}, err
		}
		return einorun.Adaptation{Parts: []einorun.AdaptPart{{Text: text, Kind: "ocr"}}}, nil
	case ok && modality == llm.Audio:
		text, err := p.projectMultimodal(ctx, p.Transcription, transcriptionInstruction, ref, llm.Audio)
		if err != nil {
			return einorun.Adaptation{}, err
		}
		return einorun.Adaptation{Parts: []einorun.AdaptPart{{Text: text, Kind: "transcript"}}}, nil
	case ok && modality == llm.File, isConvertibleDocument(ref.MIME):
		text, err := p.projectDocument(ctx, ref)
		if err != nil {
			return einorun.Adaptation{}, err
		}
		return einorun.Adaptation{Parts: []einorun.AdaptPart{{Text: text, Kind: "document"}}}, nil
	default:
		return einorun.Adaptation{}, fmt.Errorf("no media projection for %s", cmpOrMIME(ref.MIME))
	}
}

func isConvertibleDocument(mime string) bool {
	switch strings.ToLower(mime) {
	case "application/pdf",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return true
	default:
		return false
	}
}

func (p *MediaProjector) projectMultimodal(ctx context.Context, factory llm.ModelFactory, instruction string, ref einorun.MediaRef, modality llm.Modality) (string, error) {
	if factory == nil {
		return "", fmt.Errorf("%s model is not configured", modality)
	}
	block, err := p.mediaInputBlock(ctx, modality, ref)
	if err != nil {
		return "", err
	}
	model, err := factory(ctx, llm.ModelOptions{MaxOutputTokens: 4096, DisableThinking: true})
	if err != nil {
		return "", err
	}
	user := &schema.AgenticMessage{Role: schema.AgenticRoleTypeUser, ContentBlocks: []*schema.ContentBlock{
		schema.NewContentBlock(&schema.UserInputText{Text: "请处理以下媒体。"}),
		block,
	}}
	message, err := model.Generate(llm.WithModelCallID(ctx, llm.NewModelCallID()), []*schema.AgenticMessage{
		schema.SystemAgenticMessage(instruction), user,
	})
	if err != nil {
		return "", err
	}
	p.addUsage(llm.UsageOf(message.ResponseMeta))
	text := strings.TrimSpace(llm.Text(message))
	if text == "" {
		return "", fmt.Errorf("%s projection returned empty text", modality)
	}
	return text, nil
}

func (p *MediaProjector) projectDocument(ctx context.Context, ref einorun.MediaRef) (string, error) {
	if p.ConvertDocument == nil || p.ReadMedia == nil {
		return "", fmt.Errorf("document conversion is unavailable")
	}
	data, err := p.ReadMedia(ctx, ref)
	if err != nil {
		return "", err
	}
	text, err := p.ConvertDocument(ctx, documentConvertName(ref.Key, ref.MIME), ref.MIME, data)
	if err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("document conversion returned empty text")
	}
	return text, nil
}

// mediaInputBlock 构造投影调用的媒体块：音频走 base64（多数 OpenAI 兼容接口不接受音频 URL），图片/文件优先公开 URL。
func (p *MediaProjector) mediaInputBlock(ctx context.Context, modality llm.Modality, ref einorun.MediaRef) (*schema.ContentBlock, error) {
	if modality == llm.Audio {
		if p.ReadMedia == nil {
			return nil, fmt.Errorf("media reader is unavailable")
		}
		data, err := p.ReadMedia(ctx, ref)
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("audio media is empty")
		}
		return schema.NewContentBlock(&schema.UserInputAudio{
			Base64Data: base64.StdEncoding.EncodeToString(data),
			MIMEType:   ref.MIME,
		}), nil
	}
	url, err := p.resolveURL(ctx, ref)
	if err != nil {
		return nil, err
	}
	switch modality {
	case llm.Video:
		return schema.NewContentBlock(&schema.UserInputVideo{URL: url, MIMEType: ref.MIME}), nil
	case llm.File:
		return schema.NewContentBlock(&schema.UserInputFile{URL: url, MIMEType: ref.MIME}), nil
	default:
		return schema.NewContentBlock(&schema.UserInputImage{URL: url, MIMEType: ref.MIME}), nil
	}
}

func (p *MediaProjector) resolveURL(ctx context.Context, ref einorun.MediaRef) (string, error) {
	if p.MediaURL == nil {
		return "", fmt.Errorf("media URL resolver is unavailable")
	}
	url, err := p.MediaURL(ctx, ref)
	if err != nil {
		return "", err
	}
	if url == "" {
		return "", fmt.Errorf("media has no public URL")
	}
	return url, nil
}

func (p *MediaProjector) addUsage(used llm.Usage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.usage.Input += used.Input
	p.usage.Output += used.Output
}

// documentConvertName 给文档转换器一个带扩展名的文件名：共享路径保留 basename，消息附件等无扩展名的 key 按 MIME 补全。
func documentConvertName(key, mime string) string {
	base := path.Base(key)
	if i := strings.LastIndex(base, ":"); i >= 0 {
		base = base[i+1:]
	}
	if path.Ext(base) != "" {
		return base
	}
	switch strings.ToLower(mime) {
	case "application/pdf":
		return "document.pdf"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return "document.docx"
	case "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return "document.pptx"
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return "document.xlsx"
	default:
		return "document.bin"
	}
}

// projectableModality 返回可投影的模态；音频范围宽于 einorun 直传白名单，覆盖渠道常见语音格式。
func projectableModality(mime string) (llm.Modality, bool) {
	switch strings.ToLower(mime) {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return llm.Image, true
	case "audio/wav", "audio/wave", "audio/vnd.wav", "audio/vnd.wave", "audio/x-pn-wav",
		"audio/mpeg", "audio/mp3", "audio/mp4", "audio/x-m4a", "audio/ogg", "audio/amr":
		return llm.Audio, true
	case "video/mp4", "video/webm", "video/quicktime":
		return llm.Video, true
	case "application/pdf":
		return llm.File, true
	default:
		return "", false
	}
}

// IsAudioMIME 判断 MIME 是否为音频；直传时跳过公开 URL，让 einorun 回退 base64。
func IsAudioMIME(mime string) bool {
	modality, ok := projectableModality(mime)
	return ok && modality == llm.Audio
}

func cmpOrMIME(mime string) string {
	if mime != "" {
		return mime
	}
	return "media"
}
