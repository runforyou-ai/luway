package agentruntime

import (
	"context"
	"encoding/base64"
	"log/slog"

	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/cervi/internal/domain"
)

const (
	// mediaWindowPercent 随消息直传的附件最多占模型窗口的百分比，超出预算的较早附件只保留正文中的链接。
	mediaWindowPercent = 20
	// mediaTokens 按常见多模态模型单个图片或媒体片段的输入上限估算 Token。
	mediaTokens = 1280
	// maxRunMediaBytes 单次运行上下文直传附件的累计字节上限。
	maxRunMediaBytes = 20 << 20
)

// inlineMediaTypes 按输入模态列出可随请求直传的附件格式；音频限于模型组件能识别格式的 wav。
var inlineMediaTypes = map[string]domain.AIModelInputModality{
	"image/png":       domain.AIModelInputModalityImage,
	"image/jpeg":      domain.AIModelInputModalityImage,
	"image/webp":      domain.AIModelInputModalityImage,
	"image/gif":       domain.AIModelInputModalityImage,
	"audio/wav":       domain.AIModelInputModalityAudio,
	"audio/wave":      domain.AIModelInputModalityAudio,
	"audio/vnd.wav":   domain.AIModelInputModalityAudio,
	"audio/vnd.wave":  domain.AIModelInputModalityAudio,
	"audio/x-pn-wav":  domain.AIModelInputModalityAudio,
	"video/mp4":       domain.AIModelInputModalityVideo,
	"video/webm":      domain.AIModelInputModalityVideo,
	"video/quicktime": domain.AIModelInputModalityVideo,
}

// mediaInput 定义本次运行读取附件的方式、模型接受的输入模态和直传附件数量上限。
type mediaInput struct {
	read       AttachmentContent
	modalities map[domain.AIModelInputModality]bool
	maxCount   int
}

// carriesMedia 判断模型输入是否包含直传的图片、音频或视频内容块，或工具结果中的非文本内容。
func carriesMedia(input []*schema.AgenticMessage) bool {
	for _, message := range input {
		for _, block := range message.ContentBlocks {
			switch block.Type {
			case schema.ContentBlockTypeUserInputImage, schema.ContentBlockTypeUserInputAudio, schema.ContentBlockTypeUserInputVideo:
				return true
			case schema.ContentBlockTypeFunctionToolResult:
				if block.FunctionToolResult == nil {
					continue
				}
				for _, content := range block.FunctionToolResult.Content {
					if content != nil && content.Type != schema.FunctionToolResultContentBlockTypeText {
						return true
					}
				}
			}
		}
	}
	return false
}

// withoutMedia 返回去掉多模态内容的模型输入：直传附件的用户消息只保留正文，工具结果中的非文本内容改为说明文字；不含多模态内容的消息原样复用。
func withoutMedia(input []*schema.AgenticMessage) []*schema.AgenticMessage {
	output := make([]*schema.AgenticMessage, len(input))
	for i, message := range input {
		if !carriesMedia([]*schema.AgenticMessage{message}) {
			output[i] = message
			continue
		}
		stripped := *message
		stripped.ContentBlocks = make([]*schema.ContentBlock, 0, len(message.ContentBlocks))
		for _, block := range message.ContentBlocks {
			switch {
			case block.Type == schema.ContentBlockTypeUserInputImage, block.Type == schema.ContentBlockTypeUserInputAudio, block.Type == schema.ContentBlockTypeUserInputVideo:
			case block.Type == schema.ContentBlockTypeFunctionToolResult && block.FunctionToolResult != nil:
				result := *block.FunctionToolResult
				result.Content = make([]*schema.FunctionToolResultContentBlock, 0, len(block.FunctionToolResult.Content))
				for _, content := range block.FunctionToolResult.Content {
					if content != nil && content.Type != schema.FunctionToolResultContentBlockTypeText {
						content = &schema.FunctionToolResultContentBlock{Type: schema.FunctionToolResultContentBlockTypeText,
							Text: &schema.UserInputText{Text: "[" + string(content.Type) + "：当前模型无法查看此内容]"}}
					}
					result.Content = append(result.Content, content)
				}
				copied := *block
				copied.FunctionToolResult = &result
				stripped.ContentBlocks = append(stripped.ContentBlocks, &copied)
			default:
				stripped.ContentBlocks = append(stripped.ContentBlocks, block)
			}
		}
		output[i] = &stripped
	}
	return output
}

// mediaUserMessage 读取附件并构造正文块与多模态块并列的用户消息，读取失败或模态不可直传时返回 false。
func mediaUserMessage(ctx context.Context, message Message, modality domain.AIModelInputModality, read AttachmentContent) (*schema.AgenticMessage, bool) {
	content, err := read(ctx, message.ID)
	if err != nil {
		slog.Warn("读取直传附件失败，仅以正文链接提供给模型",
			"agent_run_id", runIDFromContext(ctx), "message_id", message.ID, "error", err)
		return nil, false
	}
	data := base64.StdEncoding.EncodeToString(content)
	var media *schema.ContentBlock
	switch modality {
	case domain.AIModelInputModalityImage:
		media = schema.NewContentBlock(&schema.UserInputImage{Base64Data: data, MIMEType: message.Media.MIMEType})
	case domain.AIModelInputModalityAudio:
		media = schema.NewContentBlock(&schema.UserInputAudio{Base64Data: data, MIMEType: message.Media.MIMEType})
	case domain.AIModelInputModalityVideo:
		media = schema.NewContentBlock(&schema.UserInputVideo{Base64Data: data, MIMEType: message.Media.MIMEType})
	default:
		return nil, false
	}
	return &schema.AgenticMessage{Role: schema.AgenticRoleTypeUser, ContentBlocks: []*schema.ContentBlock{
		schema.NewContentBlock(&schema.UserInputText{Text: message.Content}),
		media,
	}}, true
}
