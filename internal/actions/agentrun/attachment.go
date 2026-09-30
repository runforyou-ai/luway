//go:build server

package agentrun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ErrAttachmentUnavailable 表示附件不属于本次运行会话、已删除或尚未上传完成。
var ErrAttachmentUnavailable = errors.New("agent attachment is unavailable")

// FileOpener 按原件记录流式读取文件内容。
type FileOpener interface {
	Open(context.Context, *servermodels.File) (io.ReadCloser, error)
}

// AttachmentReader 读取 Agent 运行所属会话中已上传完成的附件，并生成上下文中的附件链接。
type AttachmentReader struct {
	db    *bun.DB
	files FileOpener
	links serverfilecontent.Links
}

// NewAttachmentReader 创建 Agent 运行附件读取器，links 生成上下文中的附件链接。
func NewAttachmentReader(db *bun.DB, files FileOpener, links serverfilecontent.Links) *AttachmentReader {
	return &AttachmentReader{db: db, files: files, links: links}
}

// Content 读取本次运行会话中指定附件消息的文件内容。
func (r *AttachmentReader) Content(ctx context.Context, run *servermodels.AgentRun, messageID string) ([]byte, error) {
	if !common.ValidUUID(messageID) {
		return nil, ErrAttachmentUnavailable
	}
	file := &servermodels.File{}
	err := r.db.NewSelect().Model(file).
		Join("JOIN message_attachments AS ma ON ma.organization_id = f.organization_id AND ma.file_id = f.id").
		Join("JOIN messages AS msg ON msg.organization_id = ma.organization_id AND msg.id = ma.message_id").
		Where("msg.organization_id = ? AND msg.conversation_id = ? AND msg.id = ?", run.OrganizationID, run.ConversationID, messageID).
		Where("msg.deleted_at IS NULL AND f.status = ?", domain.FileStatusActive).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAttachmentUnavailable
	}
	if err != nil {
		return nil, fmt.Errorf("load agent attachment file: %w", err)
	}
	content, err := r.files.Open(ctx, file)
	if err != nil {
		return nil, fmt.Errorf("open agent attachment file: %w", err)
	}
	defer content.Close()
	return io.ReadAll(content)
}

// attachmentURL 按附件实际存储位置生成稳定公开地址，无法生成时返回空链接。
func attachmentURL(links serverfilecontent.Links, backend, key *string) string {
	if backend == nil || key == nil {
		return ""
	}
	link, err := links.URL(domain.FileStorageBackend(*backend), *key)
	if err != nil {
		slog.Warn("生成 Agent 上下文附件链接失败", "storage_backend", *backend, "error", err)
		return ""
	}
	return link
}

// contextAttachmentRow 定义上下文消息及其引用消息的附件列，非附件消息为空。
type contextAttachmentRow struct {
	AttachmentName                *string `bun:"attachment_name"`
	AttachmentTransfer            string  `bun:"attachment_transfer_status"`
	AttachmentContentType         string  `bun:"attachment_content_type"`
	AttachmentByteSize            int64   `bun:"attachment_byte_size"`
	AttachmentStorageBackend      *string `bun:"attachment_storage_backend"`
	AttachmentStorageKey          *string `bun:"attachment_storage_key"`
	ReplyAttachmentName           *string `bun:"reply_attachment_name"`
	ReplyAttachmentContentType    string  `bun:"reply_attachment_content_type"`
	ReplyAttachmentByteSize       int64   `bun:"reply_attachment_byte_size"`
	ReplyAttachmentStorageBackend *string `bun:"reply_attachment_storage_backend"`
	ReplyAttachmentStorageKey     *string `bun:"reply_attachment_storage_key"`
}

// contextAttachment 定义模型上下文中的附件描述和稳定公开地址。
type contextAttachment struct {
	MessageID   string `json:"messageId,omitempty"`
	Name        string `json:"name"`
	ContentType string `json:"contentType"`
	ByteSize    int64  `json:"byteSize,omitempty"`
	URL         string `json:"url,omitempty"`
}

// withContextAttachments 为上下文查询补充附件列，只保留文本消息和附件消息，内容尚未就绪的附件不带存储位置；调用前须已关联 reply 引用消息。
func withContextAttachments(query *bun.SelectQuery) *bun.SelectQuery {
	return query.
		ColumnExpr("ma.name AS attachment_name, COALESCE(ma.transfer_status, '') AS attachment_transfer_status, COALESCE(ma.content_type, '') AS attachment_content_type, COALESCE(ma.byte_size, 0) AS attachment_byte_size").
		ColumnExpr("af.storage_backend AS attachment_storage_backend, af.storage_key AS attachment_storage_key").
		ColumnExpr("reply_ma.name AS reply_attachment_name, COALESCE(reply_ma.content_type, '') AS reply_attachment_content_type, COALESCE(reply_ma.byte_size, 0) AS reply_attachment_byte_size").
		ColumnExpr("reply_af.storage_backend AS reply_attachment_storage_backend, reply_af.storage_key AS reply_attachment_storage_key").
		Join("LEFT JOIN message_attachments AS ma ON ma.message_id = msg.id AND ma.organization_id = msg.organization_id").
		Join("LEFT JOIN files AS af ON af.id = ma.file_id AND af.organization_id = ma.organization_id AND ma.transfer_status = ?", domain.MessageAttachmentTransferReady).
		Join("LEFT JOIN message_attachments AS reply_ma ON reply_ma.message_id = reply.id AND reply_ma.organization_id = reply.organization_id").
		Join("LEFT JOIN files AS reply_af ON reply_af.id = reply_ma.file_id AND reply_af.organization_id = reply_ma.organization_id AND reply_ma.transfer_status = ?", domain.MessageAttachmentTransferReady).
		Where("msg.type IN (?, ?)", domain.MessageTypeText, domain.MessageTypeAttachment)
}

// attachment 返回消息自身的附件描述，messageId 标识附件所在消息。
func (r contextAttachmentRow) attachment(messageID string, links serverfilecontent.Links) *contextAttachment {
	if r.AttachmentName == nil {
		return nil
	}
	return &contextAttachment{
		MessageID: messageID, Name: *r.AttachmentName, ContentType: r.AttachmentContentType, ByteSize: r.AttachmentByteSize,
		URL: attachmentURL(links, r.AttachmentStorageBackend, r.AttachmentStorageKey),
	}
}

// replyAttachment 返回被引用消息的附件描述。
func (r contextAttachmentRow) replyAttachment(links serverfilecontent.Links) *contextAttachment {
	if r.ReplyAttachmentName == nil {
		return nil
	}
	return &contextAttachment{
		Name: *r.ReplyAttachmentName, ContentType: r.ReplyAttachmentContentType, ByteSize: r.ReplyAttachmentByteSize,
		URL: attachmentURL(links, r.ReplyAttachmentStorageBackend, r.ReplyAttachmentStorageKey),
	}
}

// media 返回内容已就绪的消息附件格式和大小，是否直传由运行期按模型输入模态决定。
func (r contextAttachmentRow) media() *agentruntime.Media {
	if r.AttachmentName == nil || r.AttachmentTransfer != string(domain.MessageAttachmentTransferReady) {
		return nil
	}
	return &agentruntime.Media{MIMEType: r.AttachmentContentType, ByteSize: r.AttachmentByteSize}
}

// revision 返回附件取回状态作为消息修订标识，附件就绪后同一消息重新进入运行期历史。
func (r contextAttachmentRow) revision() string {
	return r.AttachmentTransfer
}
