package agentruntime

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/cervi/internal/domain"
)

type mediaRejectingChatModel struct {
	mu         sync.Mutex
	calls      int
	mediaCalls int
	midStream  bool
}

func (m *mediaRejectingChatModel) Generate(_ context.Context, input []*schema.AgenticMessage, _ ...model.Option) (*schema.AgenticMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if carriesMedia(input) {
		m.mediaCalls++
		return nil, errors.New("unknown variant `image_url`")
	}
	return assistantReply("已按链接回答"), nil
}

// Stream 按测试场景在建立流或读取分片时拒绝直传附件，其余调用以单个分片返回模型输出。
func (m *mediaRejectingChatModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	if !m.midStream || !carriesMedia(input) {
		return singleChunkStream(m.Generate(ctx, input, opts...))
	}
	m.mu.Lock()
	m.calls++
	m.mediaCalls++
	m.mu.Unlock()
	reader, writer := schema.Pipe[*schema.AgenticMessage](2)
	writer.Send(assistantReply("部分回答"), nil)
	writer.Send(nil, errors.New("unknown variant `image_url`"))
	writer.Close()
	return reader, nil
}

// TestEinoRuntimeRetriesWithoutRejectedMedia 验证模型拒绝直传附件时去掉多模态内容重新执行一次并成功回复。
func TestEinoRuntimeRetriesWithoutRejectedMedia(t *testing.T) {
	for name, midStream := range map[string]bool{"open": false, "mid-stream": true} {
		t.Run(name, func(t *testing.T) {
			chatModel := &mediaRejectingChatModel{midStream: midStream}
			runtime := &EinoRuntime{newModel: func(context.Context, ModelConfig) (model.AgenticModel, error) {
				return chatModel, nil
			}}
			feed := &testInputFeed{desired: 1, messages: []Message{{
				ID: "1", Role: MessageRoleUser, Content: `{"body":"看图","attachment":{"name":"photo.png"}}`,
				Media: &Media{MIMEType: "image/png", ByteSize: 16},
			}}}
			result, err := runtime.Run(context.Background(), RunRequest{
				RunID: "media-fallback", MaxTurns: 2,
				Assignment: Assignment{AgentName: "test-agent", Model: AssignmentModel{
					InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText, domain.AIModelInputModalityImage},
				}},
				ReadAttachment: func(context.Context, string) ([]byte, error) {
					return []byte("image"), nil
				},
			}, feed)
			if err != nil || result.Content != "已按链接回答" || result.EndSeq != 1 {
				t.Fatalf("result = %#v, err = %v", result, err)
			}
			chatModel.mu.Lock()
			defer chatModel.mu.Unlock()
			if chatModel.calls != 2 || chatModel.mediaCalls != 1 {
				t.Fatalf("model calls = %d, media calls = %d", chatModel.calls, chatModel.mediaCalls)
			}
		})
	}
}

// TestTurnHistoryInlinesRecentMedia 验证附件按模型模态由新到旧在预算内直传，其余只保留正文。
func TestTurnHistoryInlinesRecentMedia(t *testing.T) {
	read := func(_ context.Context, messageID string) ([]byte, error) {
		if messageID == "broken" {
			return nil, errors.New("storage unavailable")
		}
		return []byte("media:" + messageID), nil
	}
	png := &Media{MIMEType: "image/png", ByteSize: 1024}
	visual := map[domain.AIModelInputModality]bool{domain.AIModelInputModalityImage: true, domain.AIModelInputModalityAudio: true}
	history := &turnHistory{}
	got := history.appendInput(context.Background(), []Message{
		{ID: "old", Role: MessageRoleUser, Content: "旧图", Media: png},
		{ID: "svg", Role: MessageRoleUser, Content: "矢量图", Media: &Media{MIMEType: "image/svg+xml", ByteSize: 10}},
		{ID: "video", Role: MessageRoleUser, Content: "视频", Media: &Media{MIMEType: "video/mp4", ByteSize: 10}},
		{ID: "large", Role: MessageRoleUser, Content: "大图", Media: &Media{MIMEType: "image/png", ByteSize: domain.AgentMediaMaxBytes + 1}},
		{ID: "voice", Role: MessageRoleUser, Content: "语音", Media: &Media{MIMEType: "audio/wav", ByteSize: 2048}},
		{ID: "new", Role: MessageRoleUser, Content: "新图", Media: png},
	}, mediaInput{read: read, modalities: visual, maxCount: 2})
	if len(got) != 6 {
		t.Fatalf("history = %#v", got)
	}
	for _, index := range []int{0, 1, 2, 3} {
		if len(got[index].ContentBlocks) != 1 || got[index].ContentBlocks[0].UserInputText == nil || messageText(got[index]) == "" {
			t.Fatalf("message %d inlined unexpectedly: %#v", index, got[index])
		}
	}
	voice := got[4].ContentBlocks
	if len(voice) != 2 || voice[0].UserInputText.Text != "语音" || voice[1].UserInputAudio == nil || voice[1].UserInputAudio.MIMEType != "audio/wav" {
		t.Fatalf("audio message = %#v", got[4])
	}
	parts := got[5].ContentBlocks
	if len(parts) != 2 || parts[0].UserInputText.Text != "新图" || parts[1].UserInputImage == nil ||
		parts[1].UserInputImage.MIMEType != "image/png" || parts[1].UserInputImage.Base64Data != base64.StdEncoding.EncodeToString([]byte("media:new")) {
		t.Fatalf("latest image message = %#v", got[5])
	}
	// 后续轮次补入的附件沿用本次运行已占用的数量预算。
	next := history.appendInput(context.Background(), []Message{{ID: "later", Role: MessageRoleUser, Content: "再一张", Media: png}}, mediaInput{read: read, modalities: visual, maxCount: 2})
	if len(next[6].ContentBlocks) != 1 {
		t.Fatalf("media beyond run budget inlined: %#v", next[6])
	}
	// 较新附件读取失败时不占用预算，数量上限留给更早的可读取附件。
	failed := (&turnHistory{}).appendInput(context.Background(), []Message{
		{ID: "readable", Role: MessageRoleUser, Content: "可读取", Media: png},
		{ID: "broken", Role: MessageRoleUser, Content: "读取失败", Media: png},
		{ID: "reply", Role: MessageRoleAssistant, Content: "助手消息", Media: png},
	}, mediaInput{read: read, modalities: visual, maxCount: 1})
	if len(failed[0].ContentBlocks) != 2 || messageText(failed[1]) != "读取失败" || len(failed[1].ContentBlocks) != 1 ||
		failed[2].Role != schema.AgenticRoleTypeAssistant || len(failed[2].ContentBlocks) != 1 {
		t.Fatalf("fallback history = %#v", failed)
	}
}

// TestCountContextTokensIncludesMediaParts 验证多模态消息的正文片段和媒体片段都计入上下文估算。
func TestCountContextTokensIncludesMediaParts(t *testing.T) {
	message := &schema.AgenticMessage{Role: schema.AgenticRoleTypeUser, ContentBlocks: []*schema.ContentBlock{
		schema.NewContentBlock(&schema.UserInputText{Text: "看图"}),
		schema.NewContentBlock(&schema.UserInputImage{Base64Data: "bWVkaWE=", MIMEType: "image/png"}),
		schema.NewContentBlock(&schema.UserInputVideo{Base64Data: "bWVkaWE=", MIMEType: "video/mp4"}),
	}}
	tokens, err := countContextTokens(context.Background(), []*schema.AgenticMessage{message}, nil)
	if err != nil || tokens != 2*mediaTokens+2 {
		t.Fatalf("tokens = %d, err = %v", tokens, err)
	}
}
