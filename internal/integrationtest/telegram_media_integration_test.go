//go:build server

package integrationtest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	channelinboundaction "github.com/runforyou-ai/luway/internal/actions/channelinbound"
	servertest "github.com/runforyou-ai/luway/internal/servertest"

	"github.com/runforyou-ai/luway/internal/actions/filemaintenance"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/stretchr/testify/require"
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

// telegramMediaFixture 是 Telegram 入站媒体取回的测试环境。
type telegramMediaFixture struct {
	channelDeliveryFixture
	scheduler  *countingAgentScheduler
	downloader *mediaDownloaderStub
	receiver   *telegramWebhook
	retrieve   *channelinboundaction.RetrieveMediaAction
	local      *serverfilecontent.LocalStore
	directory  string
	tasks      *servertest.Tasks
	nextUpdate int64
}

// newTelegramMediaFixture 建立带本地存储、取回任务和计数调度器的 Telegram 私聊。
func newTelegramMediaFixture(t *testing.T) *telegramMediaFixture {
	t.Helper()
	base := newChannelDeliveryFixture(t)
	directory := t.TempDir()
	local, err := serverfilecontent.NewLocalStore(directory)
	require.NoError(t, err)
	writer := serverfilecontent.NewWriter(local, func() serverfilecontent.S3Config { return serverfilecontent.S3Config{} })
	scheduler := &countingAgentScheduler{}
	downloader := &mediaDownloaderStub{data: []byte("JPEGDATA")}
	tasks := servertest.NewTasks()
	retrieve := channelinboundaction.NewRetrieveMediaAction(base.db, telegramAdapters(base.db, nil, downloader), writer, scheduler)
	// 回调后的即时取回遇到网络超时，附件保持取回中，由测试按任务参数执行取回。
	unreachable := &mediaDownloaderStub{err: connectiontest.NewError(connectiontest.StageConnect, connectiontest.FailureTimeout, errors.New("timeout"))}
	webhookAdapters := telegramAdapters(base.db, nil, unreachable)
	receiver := newTelegramWebhookWith(base.db, webhookAdapters, scheduler, tasks, channelinboundaction.NewRetrieveMediaAction(base.db, webhookAdapters, writer, scheduler))
	return &telegramMediaFixture{channelDeliveryFixture: base, scheduler: scheduler, downloader: downloader, receiver: receiver, retrieve: retrieve, local: local, directory: directory, nextUpdate: 10, tasks: tasks}
}

// receiveMedia 以下一条平台消息编号送入一条媒体消息，返回写入的附件消息。
func (f *telegramMediaFixture) receiveMedia(t *testing.T, media telegram.InboundMedia, caption string) models.Message {
	t.Helper()
	f.nextUpdate++
	input := telegramUpdate{Secret: "secret", UpdateID: f.nextUpdate, Message: &telegram.InboundMessage{
		ChatID: 12345, SenderID: 12345, MessageID: f.nextUpdate, DisplayName: "Telegram 客户", Body: caption, Media: &media, OriginatedAt: time.Now().UTC(),
	}}
	require.NoError(t, f.receiver.Execute(context.Background(), f.channelID, input))
	// 回调重放不产生新消息，也不重复投递任务。
	require.NoError(t, f.receiver.Execute(context.Background(), f.channelID, input))
	var message models.Message
	require.NoError(t, f.db.NewSelect().Model(&message).Where("msg.workspace_id = ? AND msg.conversation_id = ?", f.owner.Workspace.ID, f.conversationID).OrderExpr("msg.message_seq DESC").Limit(1).Scan(context.Background()))
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
	require.NoError(t, f.db.NewSelect().TableExpr("message_attachments").ColumnExpr("file_id, name, content_type, byte_size, image_width, image_height, transfer_status").Where("message_id = ?", messageID).Scan(ctx, &attachment))
	if attachment.FileID != nil {
		file = &models.File{}
		require.NoError(t, f.db.NewSelect().Model(file).Where("f.id = ?", *attachment.FileID).Scan(ctx))
	}
	taskCount = len(servertest.QueuedInputs(t, f.tasks, channelinboundaction.RetrieveMediaActionName, func(input channelinboundaction.RetrieveMediaInput) bool { return input.MessageID == messageID }))
	return attachment, file, taskCount
}

// retrievalInput 按消息与文件构造取回任务输入。
func (f *telegramMediaFixture) retrievalInput(messageID, fileID, telegramFileID string) channelinboundaction.RetrieveMediaInput {
	return channelinboundaction.RetrieveMediaInput{WorkspaceID: f.owner.Workspace.ID, ChannelID: f.channelID, ConversationID: f.conversationID, MessageID: messageID, FileID: fileID, AccountID: "123", MediaRef: telegramFileID}
}

// conversationVersion 读取会话当前版本。
func (f *telegramMediaFixture) conversationVersion(t *testing.T) int64 {
	t.Helper()
	var version int64
	require.NoError(t, f.db.NewSelect().Table("conversations").Column("version").Where("id = ?", f.conversationID).Scan(context.Background(), &version))
	return version
}

// TestTelegramInboundMediaRetrieval 验证照片先以取回中入库，取回后激活文件、置附件就绪并恰好调度一次 AI 客服。
func TestTelegramInboundMediaRetrieval(t *testing.T) {
	t.Parallel()
	f := newTelegramMediaFixture(t)
	ctx := context.Background()
	message := f.receiveMedia(t, telegram.InboundMedia{FileID: "tg-file-1", UniqueID: "unique-1", FileName: "photo.jpg", ContentType: "image/jpeg", ByteSize: 0, Width: 800, Height: 600}, "看看这张图")
	require.Equal(t, string(domain.MessageTypeAttachment), message.Type, "message")
	require.Equal(t, "看看这张图", message.Body, "message")
	attachment, file, taskCount := f.attachmentState(t, message.ID)
	require.Equal(t, string(domain.MessageAttachmentTransferPending), attachment.TransferStatus, "pending attachment")
	require.NotNil(t, file, "pending file")
	require.Equal(t, string(domain.FileStatusPending), file.Status, "pending file")
	require.NotNil(t, file.ExternalID, "pending file")
	require.Equal(t, "tg-file-1", *file.ExternalID, "pending file")
	require.Equal(t, 800, attachment.ImageWidth, "pending attachment")
	require.Equal(t, 1, taskCount, "pending tasks")
	require.Equal(t, 0, f.scheduler.calls, "pending schedules")
	require.Equal(t, string(domain.FilePurposeMessageAttachment), file.Purpose, "file")
	require.Nil(t, file.UploaderChannelIdentityID, "file")
	require.NotNil(t, file.ExpiresAt, "file")
	history := f.replyHistory(t)
	last := history[len(history)-1]
	require.Equal(t, message.ID, last.ID, "history")
	require.NotNil(t, last.Attachment, "history")
	require.Equal(t, domain.MessageAttachmentTransferPending, last.Attachment.TransferStatus, "history")
	require.False(t, last.ReplyUnavailable, "history")
	before := f.conversationVersion(t)
	input := f.retrievalInput(message.ID, file.ID, "tg-file-1")
	require.NoError(t, f.retrieve.Execute(ctx, input))
	attachment, file, _ = f.attachmentState(t, message.ID)
	require.Equal(t, string(domain.MessageAttachmentTransferReady), attachment.TransferStatus, "ready attachment")
	require.Equal(t, int64(8), attachment.ByteSize, "ready attachment")
	require.NotNil(t, file, "ready file")
	require.Equal(t, string(domain.FileStatusActive), file.Status, "ready file")
	require.Nil(t, file.ExpiresAt, "ready file")
	require.Equal(t, int64(8), file.ByteSize, "ready file")
	content, err := os.ReadFile(filepath.Join(f.directory, "objects", filepath.FromSlash(file.StorageKey)))
	require.NoError(t, err)
	require.Equal(t, "JPEGDATA", string(content))
	require.Equal(t, 1, f.scheduler.calls, "schedules")
	require.Equal(t, message.ID, f.scheduler.messages[0], "schedules")
	require.Greater(t, f.conversationVersion(t), before, "version")
	// 重复执行落到终态记录时不再下载也不再调度。
	require.NoError(t, f.retrieve.Execute(ctx, input))
	require.Equal(t, 1, f.scheduler.calls, "schedules")
	require.Equal(t, 1, f.downloader.calls, "downloads")
	history = f.replyHistory(t)
	last = history[len(history)-1]
	require.NotNil(t, last.Attachment, "history")
	require.Equal(t, domain.MessageAttachmentTransferReady, last.Attachment.TransferStatus, "history")
	require.Equal(t, file.ID, last.Attachment.ID, "history")
	// 成员可以引用客户发来的照片。
	delivery := f.sendReply(t, message.ID)
	require.NotNil(t, delivery.ReplyProviderMessageID, "reply target")
	require.Equal(t, "11", *delivery.ReplyProviderMessageID, "reply target")
}

// TestTelegramInboundMediaOversized 验证超过入站上限的媒体直接落为取回失败并在入站事务内调度 AI 客服。
func TestTelegramInboundMediaOversized(t *testing.T) {
	t.Parallel()
	f := newTelegramMediaFixture(t)
	message := f.receiveMedia(t, telegram.InboundMedia{FileID: "tg-file-big", UniqueID: "unique-big", FileName: "movie.mp4", ContentType: "video/mp4", ByteSize: 21 << 20}, "太大了")
	attachment, file, taskCount := f.attachmentState(t, message.ID)
	require.Equal(t, string(domain.MessageAttachmentTransferFailed), attachment.TransferStatus, "attachment")
	require.Nil(t, attachment.FileID, "attachment")
	require.Nil(t, file, "file")
	require.Equal(t, 0, taskCount, "tasks")
	require.Equal(t, 1, f.scheduler.calls, "schedules")
	var fileCount int
	require.NoError(t, f.db.NewSelect().Table("files").ColumnExpr("count(*)").Where("external_id = ?", "tg-file-big").Scan(context.Background(), &fileCount))
	require.Zero(t, fileCount, "files")
	history := f.replyHistory(t)
	last := history[len(history)-1]
	require.NotNil(t, last.Attachment, "history")
	require.Equal(t, domain.MessageAttachmentTransferFailed, last.Attachment.TransferStatus, "history")
	require.Equal(t, "movie.mp4", last.Attachment.Name, "history")
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
	transient := f.retrieve.Execute(ctx, input)
	require.Error(t, transient, "transient error")
	require.False(t, servertask.IsPermanent(transient), "transient error=%v", transient)
	attachment, _, _ := f.attachmentState(t, message.ID)
	require.Equal(t, string(domain.MessageAttachmentTransferPending), attachment.TransferStatus, "attachment")
	require.Equal(t, 0, f.scheduler.calls, "schedules")
	f.downloader.err = connectiontest.NewError(connectiontest.StageCapability, connectiontest.FailureProtocol, nil)
	err := f.retrieve.Execute(ctx, input)
	require.Error(t, err, "permanent error")
	require.True(t, servertask.IsPermanent(err), "permanent error=%v", err)
	before := f.conversationVersion(t)
	require.NoError(t, f.retrieve.FinalizeFailure(ctx, input, err))
	attachment, _, _ = f.attachmentState(t, message.ID)
	require.Equal(t, string(domain.MessageAttachmentTransferFailed), attachment.TransferStatus, "attachment")
	require.Nil(t, attachment.FileID, "attachment")
	require.Equal(t, 1, f.scheduler.calls, "schedules")
	require.Greater(t, f.conversationVersion(t), before, "version")
	retired := &models.File{}
	require.NoError(t, f.db.NewSelect().Model(retired).Where("f.id = ?", file.ID).Scan(ctx))
	require.Equal(t, string(domain.FileStatusDeleting), retired.Status, "retired file")
	require.NotNil(t, retired.ExpiresAt, "retired file")
	// 重复的终态回调不再改变附件，也不再调度。
	require.NoError(t, f.retrieve.FinalizeFailure(ctx, input, err))
	require.Equal(t, 1, f.scheduler.calls, "schedules")
	// 过期清理删除已回收的文件记录。
	require.NoError(t, filemaintenance.NewDeleteExpiredAction(f.db, serverfilecontent.NewDeleter(f.local, func() serverfilecontent.S3Config { return serverfilecontent.S3Config{} })).Execute(ctx, filemaintenance.DeleteExpiredInput{FileID: file.ID}))
	exists, err := f.db.NewSelect().Table("files").Where("id = ?", file.ID).Exists(ctx)
	require.NoError(t, err)
	require.False(t, exists, "file exists")
}

// TestTelegramInboundMediaExpiredCleanup 验证过期清理不抢先回收仍在取回的文件，由取回任务失败终态推进附件、通知与调度后再清理。
func TestTelegramInboundMediaExpiredCleanup(t *testing.T) {
	t.Parallel()
	f := newTelegramMediaFixture(t)
	ctx := context.Background()
	message := f.receiveMedia(t, telegram.InboundMedia{FileID: "tg-file-expired", UniqueID: "unique-expired", FileName: "notes.txt", ContentType: "text/plain", ByteSize: 12}, "")
	_, file, _ := f.attachmentState(t, message.ID)
	_, err := f.db.ExecContext(ctx, "UPDATE files SET expires_at = now() - interval '1 minute' WHERE id = ?", file.ID)
	require.NoError(t, err)
	cleanup := filemaintenance.NewDeleteExpiredAction(f.db, serverfilecontent.NewDeleter(f.local, func() serverfilecontent.S3Config { return serverfilecontent.S3Config{} }))
	require.NoError(t, cleanup.Execute(ctx, filemaintenance.DeleteExpiredInput{FileID: file.ID}))
	attachment, kept, _ := f.attachmentState(t, message.ID)
	require.Equal(t, string(domain.MessageAttachmentTransferPending), attachment.TransferStatus, "cleanup touched pending media: attachment")
	require.NotNil(t, kept, "cleanup touched pending media: file")
	require.Equal(t, string(domain.FileStatusPending), kept.Status, "cleanup touched pending media: file")
	// 过期后的取回任务永久失败，失败终态推进会话版本并调度一次 AI。
	input := f.retrievalInput(message.ID, file.ID, "tg-file-expired")
	before := f.conversationVersion(t)
	err = f.retrieve.Execute(ctx, input)
	require.Error(t, err, "expired retrieval error")
	require.True(t, servertask.IsPermanent(err), "expired retrieval error=%v", err)
	require.NoError(t, f.retrieve.FinalizeFailure(ctx, input, err))
	attachment, _, _ = f.attachmentState(t, message.ID)
	require.Equal(t, string(domain.MessageAttachmentTransferFailed), attachment.TransferStatus, "attachment")
	require.Nil(t, attachment.FileID, "attachment")
	require.Equal(t, 1, f.scheduler.calls, "schedules")
	require.Greater(t, f.conversationVersion(t), before, "version")
	require.NoError(t, cleanup.Execute(ctx, filemaintenance.DeleteExpiredInput{FileID: file.ID}))
	exists, err := f.db.NewSelect().Table("files").Where("id = ?", file.ID).Exists(ctx)
	require.NoError(t, err)
	require.False(t, exists, "file exists")
}

// TestTelegramInboundMediaIdempotencyMismatch 验证同一平台消息编号换成不同媒体时按幂等冲突忽略。
func TestTelegramInboundMediaIdempotencyMismatch(t *testing.T) {
	t.Parallel()
	f := newTelegramMediaFixture(t)
	message := f.receiveMedia(t, telegram.InboundMedia{FileID: "tg-file-a", UniqueID: "unique-a", FileName: "a.png", ContentType: "image/png", ByteSize: 10, Width: 10, Height: 10}, "")
	input := telegramUpdate{Secret: "secret", UpdateID: f.nextUpdate + 100, Message: &telegram.InboundMessage{
		ChatID: 12345, SenderID: 12345, MessageID: f.nextUpdate, DisplayName: "Telegram 客户", OriginatedAt: time.Now().UTC(),
		Media: &telegram.InboundMedia{FileID: "tg-file-b", UniqueID: "unique-b", FileName: "b.png", ContentType: "image/png", ByteSize: 10, Width: 10, Height: 10},
	}}
	require.NoError(t, f.receiver.Execute(context.Background(), f.channelID, input))
	var count int
	require.NoError(t, f.db.NewSelect().Table("messages").ColumnExpr("count(*)").Where("conversation_id = ? AND type = ?", f.conversationID, domain.MessageTypeAttachment).Scan(context.Background(), &count))
	require.Equal(t, 1, count, "attachment messages")
	_, _, taskCount := f.attachmentState(t, message.ID)
	require.Equal(t, 1, taskCount, "tasks")
}
