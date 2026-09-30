//go:build server

package integrationtest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	customerchataction "github.com/runforyou-ai/cervi/internal/actions/customerchat"
	"github.com/runforyou-ai/cervi/internal/actions/filemaintenance"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/telegram"
	serverfilecontent "github.com/runforyou-ai/cervi/internal/storage/server/filecontent"
	models "github.com/runforyou-ai/cervi/internal/storage/server/models"
	servertask "github.com/runforyou-ai/cervi/internal/task/server"
	"github.com/runforyou-ai/cervi/pkg/connectiontest"
	"github.com/uptrace/bun"
)

// countingAgentScheduler 记录 AI 客服调度次数及最近一次调度的消息。
type countingAgentScheduler struct {
	calls    int
	messages []string
}

// ScheduleCustomerAuto 记录调度调用。
func (s *countingAgentScheduler) ScheduleCustomerAuto(_ context.Context, _ bun.IDB, _, _, _, messageID string) (bool, error) {
	s.calls++
	s.messages = append(s.messages, messageID)
	return true, nil
}

// mediaDownloaderStub 返回可控的媒体内容或错误。
type mediaDownloaderStub struct {
	data  []byte
	err   error
	calls int
}

// DownloadMedia 记录调用并返回预设结果。
func (d *mediaDownloaderStub) DownloadMedia(_ context.Context, _, _ string, _ int64) ([]byte, error) {
	d.calls++
	return d.data, d.err
}

type telegramMediaFixture struct {
	customerDeliveryFixture
	scheduler  *countingAgentScheduler
	downloader *mediaDownloaderStub
	receiver   *customerchataction.ReceiveTelegramWebhookAction
	retrieve   *customerchataction.RetrieveTelegramMediaAction
	local      *serverfilecontent.LocalStore
	directory  string
	nextUpdate int64
}

// newTelegramMediaFixture 建立带本地存储、取回任务和计数调度器的 Telegram 私聊。
func newTelegramMediaFixture(t *testing.T) *telegramMediaFixture {
	t.Helper()
	base := newCustomerDeliveryFixture(t)
	directory := t.TempDir()
	local, err := serverfilecontent.NewLocalStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	writer := serverfilecontent.NewWriter(local, serverfilecontent.S3Config{})
	scheduler := &countingAgentScheduler{}
	downloader := &mediaDownloaderStub{data: []byte("JPEGDATA")}
	tasks := newTestTasks(base.db)
	retrieve := customerchataction.NewRetrieveTelegramMediaAction(base.db, downloader, writer, scheduler)
	if err := tasks.Registry().RegisterJSONWithTerminalFailure(customerchataction.RetrieveTelegramMediaActionName, retrieve.Execute, retrieve.FinalizeFailure); err != nil {
		t.Fatal(err)
	}
	receiver := customerchataction.NewReceiveTelegramWebhookAction(base.db, scheduler, domain.FileStorageBackendLocal, tasks)
	return &telegramMediaFixture{customerDeliveryFixture: base, scheduler: scheduler, downloader: downloader, receiver: receiver, retrieve: retrieve, local: local, directory: directory, nextUpdate: 10}
}

// receiveMedia 以下一条平台消息编号送入一条媒体消息，返回写入的附件消息。
func (f *telegramMediaFixture) receiveMedia(t *testing.T, media telegram.InboundMedia, caption string) models.Message {
	t.Helper()
	f.nextUpdate++
	input := customerchataction.TelegramWebhookInput{Secret: "secret", UpdateID: f.nextUpdate, Message: &telegram.InboundMessage{
		ChatID: 12345, SenderID: 12345, MessageID: f.nextUpdate, DisplayName: "Telegram 客户", Body: caption, Media: &media, OriginatedAt: time.Now().UTC(),
	}}
	if err := f.receiver.Execute(context.Background(), f.channelID, input); err != nil {
		t.Fatal(err)
	}
	// 回调重放不产生新消息，也不重复投递任务。
	if err := f.receiver.Execute(context.Background(), f.channelID, input); err != nil {
		t.Fatal(err)
	}
	var message models.Message
	if err := f.db.NewSelect().Model(&message).Where("msg.organization_id = ? AND msg.conversation_id = ?", f.owner.Organization.ID, f.conversationID).OrderExpr("msg.message_seq DESC").Limit(1).Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return message
}

// attachmentState 读取附件行、文件行和取回任务数。
func (f *telegramMediaFixture) attachmentState(t *testing.T, messageID string) (attachment struct {
	FileID         *string `bun:"file_id"`
	Name           string  `bun:"name"`
	ContentType    string  `bun:"content_type"`
	ByteSize       int64   `bun:"byte_size"`
	ImageWidth     int     `bun:"image_width"`
	ImageHeight    int     `bun:"image_height"`
	TransferStatus string  `bun:"transfer_status"`
}, file *models.File, taskCount int) {
	t.Helper()
	ctx := context.Background()
	if err := f.db.NewSelect().TableExpr("message_attachments").ColumnExpr("file_id, name, content_type, byte_size, image_width, image_height, transfer_status").Where("message_id = ?", messageID).Scan(ctx, &attachment); err != nil {
		t.Fatal(err)
	}
	if attachment.FileID != nil {
		file = &models.File{}
		if err := f.db.NewSelect().Model(file).Where("f.id = ?", *attachment.FileID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.NewSelect().TableExpr("task_runs").ColumnExpr("count(*)").Where("action_name = ? AND payload->>'messageId' = ?", customerchataction.RetrieveTelegramMediaActionName, messageID).Scan(ctx, &taskCount); err != nil {
		t.Fatal(err)
	}
	return attachment, file, taskCount
}

// retrievalInput 按消息与文件构造取回任务输入。
func (f *telegramMediaFixture) retrievalInput(messageID, fileID, telegramFileID string) customerchataction.RetrieveTelegramMediaInput {
	return customerchataction.RetrieveTelegramMediaInput{OrganizationID: f.owner.Organization.ID, ChannelID: f.channelID, ConversationID: f.conversationID, MessageID: messageID, FileID: fileID, BotID: 123, TelegramFileID: telegramFileID}
}

// conversationVersion 读取会话当前版本。
func (f *telegramMediaFixture) conversationVersion(t *testing.T) int64 {
	t.Helper()
	var version int64
	if err := f.db.NewSelect().Table("conversations").Column("version").Where("id = ?", f.conversationID).Scan(context.Background(), &version); err != nil {
		t.Fatal(err)
	}
	return version
}

// TestTelegramInboundMediaRetrieval 验证照片先以取回中入库，取回后激活文件、置附件就绪并恰好调度一次 AI 客服。
func TestTelegramInboundMediaRetrieval(t *testing.T) {
	t.Parallel()
	f := newTelegramMediaFixture(t)
	ctx := context.Background()
	message := f.receiveMedia(t, telegram.InboundMedia{FileID: "tg-file-1", UniqueID: "unique-1", FileName: "photo.jpg", ContentType: "image/jpeg", ByteSize: 0, Width: 800, Height: 600}, "看看这张图")
	if message.Type != string(domain.MessageTypeAttachment) || message.Body != "看看这张图" {
		t.Fatalf("message=%+v", message)
	}
	attachment, file, taskCount := f.attachmentState(t, message.ID)
	if attachment.TransferStatus != string(domain.MessageAttachmentTransferPending) || file == nil || file.Status != string(domain.FileStatusPending) || file.ExternalID == nil || *file.ExternalID != "unique-1" || attachment.ImageWidth != 800 || taskCount != 1 || f.scheduler.calls != 0 {
		t.Fatalf("pending attachment=%+v file=%+v tasks=%d schedules=%d", attachment, file, taskCount, f.scheduler.calls)
	}
	if file.Purpose != string(domain.FilePurposeMessageAttachment) || file.UploaderChannelIdentityID != nil || file.ExpiresAt == nil {
		t.Fatalf("file=%+v", file)
	}
	history := f.replyHistory(t)
	last := history[len(history)-1]
	if last.ID != message.ID || last.Attachment == nil || last.Attachment.TransferStatus != domain.MessageAttachmentTransferPending || last.ReplyUnavailable {
		t.Fatalf("history=%+v", last)
	}
	before := f.conversationVersion(t)
	input := f.retrievalInput(message.ID, file.ID, "tg-file-1")
	if err := f.retrieve.Execute(ctx, input); err != nil {
		t.Fatal(err)
	}
	attachment, file, _ = f.attachmentState(t, message.ID)
	if attachment.TransferStatus != string(domain.MessageAttachmentTransferReady) || attachment.ByteSize != 8 || file == nil || file.Status != string(domain.FileStatusActive) || file.ExpiresAt != nil || file.ByteSize != 8 {
		t.Fatalf("ready attachment=%+v file=%+v", attachment, file)
	}
	if content, err := os.ReadFile(filepath.Join(f.directory, "objects", filepath.FromSlash(file.StorageKey))); err != nil || string(content) != "JPEGDATA" {
		t.Fatalf("content=%q err=%v", content, err)
	}
	if f.scheduler.calls != 1 || f.scheduler.messages[0] != message.ID || f.conversationVersion(t) <= before {
		t.Fatalf("schedules=%d version=%d", f.scheduler.calls, f.conversationVersion(t))
	}
	// 重复执行落到终态记录时不再下载也不再调度。
	if err := f.retrieve.Execute(ctx, input); err != nil {
		t.Fatal(err)
	}
	if f.scheduler.calls != 1 || f.downloader.calls != 1 {
		t.Fatalf("schedules=%d downloads=%d", f.scheduler.calls, f.downloader.calls)
	}
	history = f.replyHistory(t)
	last = history[len(history)-1]
	if last.Attachment == nil || last.Attachment.TransferStatus != domain.MessageAttachmentTransferReady || last.Attachment.ID != file.ID {
		t.Fatalf("history=%+v", last.Attachment)
	}
	// 成员可以引用客户发来的照片。
	delivery := f.sendReply(t, message.ID)
	if delivery.ReplyProviderMessageID == nil || *delivery.ReplyProviderMessageID != "11" {
		t.Fatalf("reply target=%+v", delivery)
	}
}

// TestTelegramInboundMediaDuplicateFile 验证同一平台文件重复发送时各自独立入库。
func TestTelegramInboundMediaDuplicateFile(t *testing.T) {
	t.Parallel()
	f := newTelegramMediaFixture(t)
	media := telegram.InboundMedia{FileID: "tg-file-dup", UniqueID: "unique-dup", FileName: "report.pdf", ContentType: "application/pdf", ByteSize: 2048}
	first := f.receiveMedia(t, media, "")
	second := f.receiveMedia(t, media, "")
	if first.ID == second.ID {
		t.Fatal("duplicate media shared one message")
	}
	_, firstFile, _ := f.attachmentState(t, first.ID)
	_, secondFile, _ := f.attachmentState(t, second.ID)
	if firstFile == nil || secondFile == nil || firstFile.ID == secondFile.ID || firstFile.StorageKey == secondFile.StorageKey {
		t.Fatalf("files=%+v %+v", firstFile, secondFile)
	}
	var title string
	if err := f.db.NewSelect().Table("messages").Column("body").Where("id = ?", first.ID).Scan(context.Background(), &title); err != nil || title != "" {
		t.Fatalf("body=%q err=%v", title, err)
	}
}

// TestTelegramInboundMediaOversized 验证超过入站上限的媒体直接落为取回失败并在入站事务内调度 AI 客服。
func TestTelegramInboundMediaOversized(t *testing.T) {
	t.Parallel()
	f := newTelegramMediaFixture(t)
	message := f.receiveMedia(t, telegram.InboundMedia{FileID: "tg-file-big", UniqueID: "unique-big", FileName: "movie.mp4", ContentType: "video/mp4", ByteSize: 21 << 20}, "太大了")
	attachment, file, taskCount := f.attachmentState(t, message.ID)
	if attachment.TransferStatus != string(domain.MessageAttachmentTransferFailed) || attachment.FileID != nil || file != nil || taskCount != 0 || f.scheduler.calls != 1 {
		t.Fatalf("attachment=%+v tasks=%d schedules=%d", attachment, taskCount, f.scheduler.calls)
	}
	var fileCount int
	if err := f.db.NewSelect().Table("files").ColumnExpr("count(*)").Where("external_id = ?", "unique-big").Scan(context.Background(), &fileCount); err != nil || fileCount != 0 {
		t.Fatalf("files=%d err=%v", fileCount, err)
	}
	history := f.replyHistory(t)
	last := history[len(history)-1]
	if last.Attachment == nil || last.Attachment.TransferStatus != domain.MessageAttachmentTransferFailed || last.Attachment.Name != "movie.mp4" {
		t.Fatalf("history=%+v", last.Attachment)
	}
}

// TestTelegramInboundMediaFailure 验证平台拒绝下载时任务永久失败，终态回调把附件置为失败、回收文件并调度一次 AI 客服。
func TestTelegramInboundMediaFailure(t *testing.T) {
	t.Parallel()
	f := newTelegramMediaFixture(t)
	ctx := context.Background()
	message := f.receiveMedia(t, telegram.InboundMedia{FileID: "tg-file-rejected", UniqueID: "unique-rejected", FileName: "voice.ogg", ContentType: "audio/ogg", ByteSize: 4096}, "")
	_, file, _ := f.attachmentState(t, message.ID)
	input := f.retrievalInput(message.ID, file.ID, "tg-file-rejected")
	// 网络类错误等待重试，附件保持取回中。
	f.downloader.err = connectiontest.NewError(connectiontest.StageConnect, connectiontest.FailureTimeout, errors.New("timeout"))
	if err := f.retrieve.Execute(ctx, input); err == nil || servertask.IsPermanent(err) {
		t.Fatalf("transient error=%v", err)
	}
	attachment, _, _ := f.attachmentState(t, message.ID)
	if attachment.TransferStatus != string(domain.MessageAttachmentTransferPending) || f.scheduler.calls != 0 {
		t.Fatalf("attachment=%+v schedules=%d", attachment, f.scheduler.calls)
	}
	f.downloader.err = connectiontest.NewError(connectiontest.StageCapability, connectiontest.FailureProtocol, nil)
	err := f.retrieve.Execute(ctx, input)
	if err == nil || !servertask.IsPermanent(err) {
		t.Fatalf("permanent error=%v", err)
	}
	before := f.conversationVersion(t)
	if err := f.retrieve.FinalizeFailure(ctx, input, err); err != nil {
		t.Fatal(err)
	}
	attachment, _, _ = f.attachmentState(t, message.ID)
	if attachment.TransferStatus != string(domain.MessageAttachmentTransferFailed) || attachment.FileID != nil || f.scheduler.calls != 1 || f.conversationVersion(t) <= before {
		t.Fatalf("attachment=%+v schedules=%d", attachment, f.scheduler.calls)
	}
	retired := &models.File{}
	if err := f.db.NewSelect().Model(retired).Where("f.id = ?", file.ID).Scan(ctx); err != nil || retired.Status != string(domain.FileStatusDeleting) || retired.ExpiresAt == nil {
		t.Fatalf("retired file=%+v err=%v", retired, err)
	}
	// 重复的终态回调不再改变附件，也不再调度。
	if err := f.retrieve.FinalizeFailure(ctx, input, err); err != nil {
		t.Fatal(err)
	}
	if f.scheduler.calls != 1 {
		t.Fatalf("schedules=%d", f.scheduler.calls)
	}
	// 过期清理删除已回收的文件记录。
	if err := filemaintenance.NewDeleteExpiredAction(f.db, serverfilecontent.NewDeleter(f.local, serverfilecontent.S3Config{})).Execute(ctx, filemaintenance.DeleteExpiredInput{FileID: file.ID}); err != nil {
		t.Fatal(err)
	}
	if exists, err := f.db.NewSelect().Table("files").Where("id = ?", file.ID).Exists(ctx); err != nil || exists {
		t.Fatalf("file exists=%t err=%v", exists, err)
	}
}

// TestTelegramInboundMediaExpiredCleanup 验证过期清理不抢先回收仍在取回的文件，由取回任务失败终态推进附件、通知与调度后再清理。
func TestTelegramInboundMediaExpiredCleanup(t *testing.T) {
	t.Parallel()
	f := newTelegramMediaFixture(t)
	ctx := context.Background()
	message := f.receiveMedia(t, telegram.InboundMedia{FileID: "tg-file-expired", UniqueID: "unique-expired", FileName: "notes.txt", ContentType: "text/plain", ByteSize: 12}, "")
	_, file, _ := f.attachmentState(t, message.ID)
	if _, err := f.db.ExecContext(ctx, "UPDATE files SET expires_at = now() - interval '1 minute' WHERE id = ?", file.ID); err != nil {
		t.Fatal(err)
	}
	cleanup := filemaintenance.NewDeleteExpiredAction(f.db, serverfilecontent.NewDeleter(f.local, serverfilecontent.S3Config{}))
	if err := cleanup.Execute(ctx, filemaintenance.DeleteExpiredInput{FileID: file.ID}); err != nil {
		t.Fatal(err)
	}
	attachment, kept, _ := f.attachmentState(t, message.ID)
	if attachment.TransferStatus != string(domain.MessageAttachmentTransferPending) || kept == nil || kept.Status != string(domain.FileStatusPending) {
		t.Fatalf("cleanup touched pending media: attachment=%+v file=%+v", attachment, kept)
	}
	// 过期后的取回任务永久失败，失败终态推进会话版本并调度一次 AI。
	input := f.retrievalInput(message.ID, file.ID, "tg-file-expired")
	before := f.conversationVersion(t)
	err := f.retrieve.Execute(ctx, input)
	if err == nil || !servertask.IsPermanent(err) {
		t.Fatalf("expired retrieval error=%v", err)
	}
	if err := f.retrieve.FinalizeFailure(ctx, input, err); err != nil {
		t.Fatal(err)
	}
	attachment, _, _ = f.attachmentState(t, message.ID)
	if attachment.TransferStatus != string(domain.MessageAttachmentTransferFailed) || attachment.FileID != nil || f.scheduler.calls != 1 || f.conversationVersion(t) <= before {
		t.Fatalf("attachment=%+v schedules=%d", attachment, f.scheduler.calls)
	}
	if err := cleanup.Execute(ctx, filemaintenance.DeleteExpiredInput{FileID: file.ID}); err != nil {
		t.Fatal(err)
	}
	if exists, err := f.db.NewSelect().Table("files").Where("id = ?", file.ID).Exists(ctx); err != nil || exists {
		t.Fatalf("file exists=%t err=%v", exists, err)
	}
}

// TestTelegramInboundMediaIdempotencyMismatch 验证同一平台消息编号换成不同媒体时按幂等冲突忽略。
func TestTelegramInboundMediaIdempotencyMismatch(t *testing.T) {
	t.Parallel()
	f := newTelegramMediaFixture(t)
	message := f.receiveMedia(t, telegram.InboundMedia{FileID: "tg-file-a", UniqueID: "unique-a", FileName: "a.png", ContentType: "image/png", ByteSize: 10, Width: 10, Height: 10}, "")
	input := customerchataction.TelegramWebhookInput{Secret: "secret", UpdateID: f.nextUpdate + 100, Message: &telegram.InboundMessage{
		ChatID: 12345, SenderID: 12345, MessageID: f.nextUpdate, DisplayName: "Telegram 客户", OriginatedAt: time.Now().UTC(),
		Media: &telegram.InboundMedia{FileID: "tg-file-b", UniqueID: "unique-b", FileName: "b.png", ContentType: "image/png", ByteSize: 10, Width: 10, Height: 10},
	}}
	if err := f.receiver.Execute(context.Background(), f.channelID, input); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.db.NewSelect().Table("messages").ColumnExpr("count(*)").Where("conversation_id = ? AND type = ?", f.conversationID, domain.MessageTypeAttachment).Scan(context.Background(), &count); err != nil || count != 1 {
		t.Fatalf("attachment messages=%d err=%v", count, err)
	}
	if _, _, taskCount := f.attachmentState(t, message.ID); taskCount != 1 {
		t.Fatalf("tasks=%d", taskCount)
	}
}
