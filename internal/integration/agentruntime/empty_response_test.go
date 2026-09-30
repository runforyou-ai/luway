package agentruntime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/cervi/internal/domain"
)

type emptyThenAnswerChatModel struct {
	mu           sync.Mutex
	calls        int
	emptyReplies int
}

func (m *emptyThenAnswerChatModel) Generate(_ context.Context, _ []*schema.AgenticMessage, _ ...model.Option) (*schema.AgenticMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	reply := assistantReply("重试后的回答")
	if m.calls <= m.emptyReplies {
		reply = assistantReply("")
	}
	reply.ResponseMeta = &schema.AgenticResponseMeta{TokenUsage: &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}}
	return reply, nil
}

// Stream 以单个分片返回当前测试步骤的模型输出。
func (m *emptyThenAnswerChatModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return singleChunkStream(m.Generate(ctx, input, opts...))
}

// TestEmptyFinalResponseRetryIsBounded 验证空正文按有界次数重试本次模型调用，被丢弃的输出计入用量。
func TestEmptyFinalResponseRetryIsBounded(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		emptyReplies int
		wantCalls    int
		wantErr      bool
	}{
		{"空正文后重试成功", emptyResponseRetryLimit, emptyResponseRetryLimit + 1, false},
		{"连续空正文达到上限", emptyResponseRetryLimit + 1, emptyResponseRetryLimit + 1, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			chatModel := &emptyThenAnswerChatModel{emptyReplies: scenario.emptyReplies}
			runtime := &EinoRuntime{
				newModel: func(context.Context, ModelConfig) (model.AgenticModel, error) { return chatModel, nil },
			}
			feed := &testInputFeed{}
			feed.appendUser("群里的问题")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result, err := runtime.Run(ctx, RunRequest{RunID: "test-run-id", Assignment: Assignment{AgentName: "群协作助手"}}, feed)
			if scenario.wantErr && err == nil {
				t.Fatalf("期望达到上限后失败，实际结果 = %#v", result)
			}
			if !scenario.wantErr && (err != nil || result.Content != "重试后的回答" || result.Usage.TotalTokens != 15*scenario.wantCalls) {
				t.Fatalf("结果 = %#v，error = %v", result, err)
			}
			if chatModel.calls != scenario.wantCalls {
				t.Fatalf("模型调用次数 = %d，期望 %d", chatModel.calls, scenario.wantCalls)
			}
		})
	}
}

// mediaThenEmptyChatModel 拒绝携带多模态内容的调用，去掉多模态内容后先返回一次空正文再回答。
type mediaThenEmptyChatModel struct {
	mu    sync.Mutex
	calls int
}

// Generate 按调用次数返回拒绝、空正文或回答。
func (m *mediaThenEmptyChatModel) Generate(_ context.Context, input []*schema.AgenticMessage, _ ...model.Option) (*schema.AgenticMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	switch {
	case carriesMedia(input):
		return nil, errors.New("unknown variant `image_url`")
	case m.calls == 2:
		return assistantReply(""), nil
	}
	return assistantReply("已按链接回答"), nil
}

// Stream 以单个分片返回模型输出。
func (m *mediaThenEmptyChatModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return singleChunkStream(m.Generate(ctx, input, opts...))
}

// TestMediaRetryKeepsEmptyResponseRetry 验证多模态重试不占用空正文重试的次数。
func TestMediaRetryKeepsEmptyResponseRetry(t *testing.T) {
	chatModel := &mediaThenEmptyChatModel{}
	runtime := &EinoRuntime{newModel: func(context.Context, ModelConfig) (model.AgenticModel, error) { return chatModel, nil }}
	feed := &testInputFeed{desired: 1, messages: []Message{{
		ID: "1", Role: MessageRoleUser, Content: "看图", Media: &Media{MIMEType: "image/png", ByteSize: 16},
	}}}
	result, err := runtime.Run(context.Background(), RunRequest{
		RunID: "media-then-empty", MaxTurns: 1,
		Assignment: Assignment{AgentName: "test-agent", Model: AssignmentModel{
			InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText, domain.AIModelInputModalityImage},
		}},
		ReadAttachment: func(context.Context, string) ([]byte, error) { return []byte("image"), nil },
	}, feed)
	if err != nil || result.Content != "已按链接回答" || chatModel.calls != 3 {
		t.Fatalf("result=%+v, err=%v, calls=%d", result, err, chatModel.calls)
	}
}
