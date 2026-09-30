//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"uuid"

	agentaction "github.com/runforyou-ai/cervi/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/cervi/internal/actions/agentrun"
	aiprovideraction "github.com/runforyou-ai/cervi/internal/actions/aiprovider"
	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/cervi/internal/actions/directchat"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/agentruntime"
	serverfilecontent "github.com/runforyou-ai/cervi/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

type testAttachmentFiles struct{}

// Open 以原件名称生成可辨认的文件内容。
func (testAttachmentFiles) Open(_ context.Context, file *servermodels.File) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("content:" + file.OriginalName)), nil
}

// testAttachmentReader 创建以 http 生成附件链接、按原件名称返回内容的附件读取器。
func testAttachmentReader(db *bun.DB) *agentrunaction.AttachmentReader {
	return agentrunaction.NewAttachmentReader(db, testAttachmentFiles{}, serverfilecontent.NewLinks(testPublicURL, ""))
}

type contextAttachmentContent struct {
	Body       string `json:"body"`
	Attachment struct {
		MessageID   string `json:"messageId"`
		Name        string `json:"name"`
		ContentType string `json:"contentType"`
		URL         string `json:"url"`
	} `json:"attachment"`
}

// TestAgentAttachmentInputs 验证 AI 聊天附件逐条进入输入流并合并为一次运行，上下文携带附件链接、可直传内容和引用附件。
func TestAgentAttachmentInputs(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	provider, err := aiprovideraction.NewCreateAIProviderAction(f.db).Execute(ctx, f.owner, aiprovideraction.Input{
		CredentialType: domain.AIProviderCredentialTypeAPIKey,
		Brand:          domain.AIProviderBrandOpenAI, Name: uuid.NewV7().String(), APIKey: "test-key", APIURL: "https://models.test/v1",
		Models: []aiprovideraction.Model{{
			Identifier: "vision", Name: "视觉模型", Type: domain.AIModelTypeChat,
			InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText, domain.AIModelInputModalityImage}, ContextWindow: 32000, MaxOutputTokens: 4096,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := agentaction.NewCreateAgentAction(f.db).Execute(ctx, f.owner, agentaction.CreateInput{
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, DisplayName: "附件助手",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ProviderID: provider.ID, ModelIdentifier: "vision", SystemInstruction: "阅读附件并回答",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tasks := newTestTasks(f.db)
	if err := tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	scheduler := agentrunaction.NewScheduler(tasks)
	send := directchataction.NewSendAttachmentMessageAction(f.db, scheduler)
	conversationID := uuid.NewV7().String()
	// inputSources 按输入序号返回本会话 Agent 输入的来源消息。
	inputSources := func() []string {
		t.Helper()
		sources := make([]string, 0)
		if err := f.db.NewSelect().Model((*servermodels.AgentInput)(nil)).ColumnExpr("ai.source_message_id").
			Join("JOIN agent_lanes AS al ON al.id = ai.lane_id").
			Where("al.conversation_id = ?", conversationID).OrderExpr("ai.input_seq ASC").Scan(ctx, &sources); err != nil {
			t.Fatal(err)
		}
		return sources
	}
	// runQueued 用可控 Runtime 执行会话中排队的运行，认领当前全部输入。
	runQueued := func(check func(agentruntime.RunRequest, agentruntime.ClaimedInput) error) {
		t.Helper()
		run := &servermodels.AgentRun{}
		if err := f.db.NewSelect().Model(run).Where("agr.conversation_id = ? AND agr.status = ?", conversationID, domain.AgentRunStatusQueued).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		runtime := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
			triggers, err := feed.Peek(ctx, 0)
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			input, err := feed.Claim(ctx, triggers[len(triggers)-1].Seq)
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			if err := check(request, input); err != nil {
				return agentruntime.RunResult{}, err
			}
			return agentruntime.RunResult{Content: "已阅读", EndSeq: input.EndSeq}, nil
		}}
		if err := agentrunaction.NewExecuteAction(f.db, tasks, runtime, testAttachmentReader(f.db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
			t.Fatal(err)
		}
	}

	// AI 聊天草稿以附件首发创建会话，第二个附件带说明，两个附件各追加一次输入，重放不追加。
	first, err := send.Execute(ctx, f.owner, directchataction.AttachmentMessageInput{
		ConversationID: conversationID, AgentIdentityID: agent.IdentityID, ClientMessageID: uuid.NewV7().String(),
		FileID: uploadedAttachment(t, f.db, f.owner, "photo.png", "image/png"),
	})
	if err != nil || first.ConversationID != conversationID || first.AgentConversation == nil || first.AgentConversation.ID != conversationID {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	// 无说明的附件首发以文件名作为会话标题。
	var title string
	if err := f.db.NewSelect().Table("conversations").Column("title").Where("id = ?", conversationID).Scan(ctx, &title); err != nil || title != "photo.png" {
		t.Fatalf("title=%q err=%v", title, err)
	}
	documentInput := directchataction.AttachmentMessageInput{
		ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "请看附件",
		FileID: uploadedAttachment(t, f.db, f.owner, "spec.pdf", "application/pdf"),
	}
	second, err := send.Execute(ctx, f.owner, documentInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := send.Execute(ctx, f.owner, documentInput); err != nil {
		t.Fatal(err)
	}
	image, document := first.Message, second.Message
	if sources := inputSources(); len(sources) != 2 || sources[0] != image.ID || sources[1] != document.ID {
		t.Fatalf("inputs=%v", sources)
	}
	// 一次运行认领两条附件输入，图片可直传读取，跨会话附件不可读取。
	runQueued(func(request agentruntime.RunRequest, input agentruntime.ClaimedInput) error {
		if !slices.Contains(request.Assignment.Model.InputModalities, domain.AIModelInputModalityImage) || len(input.Messages) != 2 {
			return errors.New("attachment inputs were not claimed together")
		}
		content, err := request.ReadAttachment(ctx, image.ID)
		if err != nil || string(content) != "content:photo.png" {
			return errors.New("image attachment content unavailable")
		}
		if _, err := request.ReadAttachment(ctx, uuid.NewV7().String()); err == nil {
			return errors.New("foreign attachment readable")
		}
		for index, expected := range []struct {
			message     conversationaction.ConversationMessage
			name        string
			contentType string
			body        string
		}{{image, "photo.png", "image/png", ""}, {document, "spec.pdf", "application/pdf", "请看附件"}} {
			var content contextAttachmentContent
			if err := json.Unmarshal([]byte(input.Messages[index].Content), &content); err != nil {
				return err
			}
			if input.Messages[index].ID != expected.message.ID || content.Body != expected.body || content.Attachment.MessageID != expected.message.ID ||
				content.Attachment.Name != expected.name || content.Attachment.ContentType != expected.contentType ||
				!strings.HasPrefix(content.Attachment.URL, testPublicURL+"/storage/") ||
				input.Messages[index].Media == nil || input.Messages[index].Media.MIMEType != expected.contentType {
				return errors.New("attachment context mismatch: " + input.Messages[index].Content)
			}
		}
		return nil
	})

	// 引用已发送附件的文字把附件描述带入引用上下文。
	if _, err := directchataction.NewSendAgentTextMessageAction(f.db, scheduler).Execute(ctx, f.owner, directchataction.InternalTextMessageInput{
		ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "说说这张图", ReplyToMessageID: image.ID,
	}); err != nil {
		t.Fatal(err)
	}
	runQueued(func(_ agentruntime.RunRequest, input agentruntime.ClaimedInput) error {
		for _, message := range input.Messages {
			var content struct {
				ReplyTo *struct {
					Attachment *struct {
						Name string `json:"name"`
					} `json:"attachment"`
				} `json:"replyTo"`
			}
			if json.Unmarshal([]byte(message.Content), &content) == nil && content.ReplyTo != nil && content.ReplyTo.Attachment != nil && content.ReplyTo.Attachment.Name == "photo.png" {
				return nil
			}
		}
		return errors.New("reply attachment missing from context")
	})

	// Agent 停用后不能继续发送附件，也不追加输入。
	if _, err := f.db.NewUpdate().Table("agents").Set("status = ?", domain.IdentityStatusInactive).Where("identity_id = ?", agent.IdentityID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := send.Execute(ctx, f.owner, directchataction.AttachmentMessageInput{
		ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), FileID: uploadedAttachment(t, f.db, f.owner, "c.txt", "text/plain"),
	}); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("inactive agent attachment=%v", err)
	}
	if sources := inputSources(); len(sources) != 3 {
		t.Fatalf("inputs after inactive agent attachment=%v", sources)
	}
}
