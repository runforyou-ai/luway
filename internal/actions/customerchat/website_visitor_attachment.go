//go:build server

package customerchat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	contactaction "github.com/runforyou-ai/cervi/internal/actions/contact"
	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	fileaction "github.com/runforyou-ai/cervi/internal/actions/file"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/common/customeridentity"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// CreateWebsiteVisitorUploadAction 创建网站访客附件的待上传文件。
type CreateWebsiteVisitorUploadAction struct {
	db      *bun.DB
	backend domain.FileStorageBackend
}

// NewCreateWebsiteVisitorUploadAction 创建网站访客附件上传操作。
func NewCreateWebsiteVisitorUploadAction(db *bun.DB, backend domain.FileStorageBackend) *CreateWebsiteVisitorUploadAction {
	return &CreateWebsiteVisitorUploadAction{db: db, backend: backend}
}

// Execute 按渠道上限校验元数据，幂等建立渠道身份后按企业存储配置创建临时文件。
func (a *CreateWebsiteVisitorUploadAction) Execute(ctx context.Context, input WebsiteVisitorUploadInput) (*servermodels.File, error) {
	fields := map[string]conversationaction.ValidationCode{}
	if !common.ValidUUID(input.ChannelID) {
		fields["channelId"] = ValidationChannelIDInvalid
	}
	if !customeridentity.ValidExternalID(input.ExternalID) || (input.Customer != nil && input.ExternalID != customeridentity.CustomerExternalID(input.Customer.UserID)) {
		fields["visitorToken"] = ValidationExternalIDInvalid
	}
	if len(fields) > 0 {
		return nil, &conversationaction.ValidationError{Fields: fields}
	}
	if limit := domain.ChannelInboundAttachmentLimit(domain.ChannelTypeWebsite); input.ByteSize > limit {
		return nil, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonAttachmentTooLarge}
	}
	var record *servermodels.File
	err := conversationaction.RunInTxWithUniqueRetry(ctx, a.db, inboundMessageRetryableConstraintNames, func(ctx context.Context, tx bun.Tx) error {
		channel, err := loadWebsiteChannel(ctx, tx, input.ChannelID)
		if err != nil {
			return err
		}
		// 访客的首条消息可以是附件，渠道身份在创建上传这一步落库，登录用户同时关联企业用户编号与邮箱。
		ids := generateIDs()
		identityInput := contactaction.EnsureChannelIdentityInput{
			OrganizationID: channel.OrganizationID, ChannelID: channel.ID, ExternalID: input.ExternalID,
			ContactID: ids.contact, IdentityID: ids.channelIdentity,
		}
		if input.Customer != nil {
			identityInput.ExternalUserID, identityInput.Email = input.Customer.UserID, input.Customer.Email
		}
		ensured, identityErr := contactaction.EnsureChannelIdentity(ctx, tx, identityInput)
		if identityErr != nil {
			return identityErr
		}
		record, err = fileaction.CreateVisitorPending(ctx, tx, a.backend, fileaction.VisitorUploadInput{
			OrganizationID: channel.OrganizationID, CreatedByUserID: channel.CreatedByUserID, ChannelIdentityID: ensured.Identity.ID,
			Upload: fileaction.UploadInput{
				Purpose: domain.FilePurposeMessageAttachment, FileName: input.FileName,
				ContentType: input.ContentType, ByteSize: input.ByteSize,
			},
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return record, nil
}

// CompleteWebsiteVisitorUploadAction 核验网站访客上传的附件内容并标记完成。
type CompleteWebsiteVisitorUploadAction struct {
	db *bun.DB
}

// NewCompleteWebsiteVisitorUploadAction 创建网站访客附件上传完成操作。
func NewCompleteWebsiteVisitorUploadAction(db *bun.DB) *CompleteWebsiteVisitorUploadAction {
	return &CompleteWebsiteVisitorUploadAction{db: db}
}

// Execute 按访客渠道身份核对文件归属并推进上传状态。
func (a *CompleteWebsiteVisitorUploadAction) Execute(ctx context.Context, channelID, externalID, fileID string, finalize fileaction.FinalizeFunc) (*servermodels.File, error) {
	channel, identity, err := loadWebsiteVisitor(ctx, a.db, channelID, externalID)
	if err != nil {
		return nil, err
	}
	return fileaction.CompleteVisitorUpload(ctx, a.db, channel.OrganizationID, identity.ID, fileID, finalize)
}

// GetWebsiteVisitorAttachmentQuery 读取网站访客可见消息的附件文件。
type GetWebsiteVisitorAttachmentQuery struct {
	db *bun.DB
}

// NewGetWebsiteVisitorAttachmentQuery 创建网站访客附件文件查询。
func NewGetWebsiteVisitorAttachmentQuery(db *bun.DB) *GetWebsiteVisitorAttachmentQuery {
	return &GetWebsiteVisitorAttachmentQuery{db: db}
}

// Execute 返回该访客会话中已就绪附件对应的文件，供重新签发预览和下载地址。
func (q *GetWebsiteVisitorAttachmentQuery) Execute(ctx context.Context, channelID, externalID, conversationID, messageID string) (*servermodels.File, error) {
	if !common.ValidUUID(conversationID) || !common.ValidUUID(messageID) {
		return nil, fileaction.ErrFileNotFound
	}
	channel, identity, err := loadWebsiteVisitor(ctx, q.db, channelID, externalID)
	if err != nil {
		return nil, err
	}
	record := &servermodels.File{}
	err = q.db.NewSelect().Model(record).
		Join("JOIN message_attachments AS ma ON ma.file_id = f.id AND ma.organization_id = f.organization_id").
		Join("JOIN messages AS msg ON msg.id = ma.message_id AND msg.organization_id = ma.organization_id").
		Join("JOIN channel_conversations AS cc ON cc.conversation_id = msg.conversation_id AND cc.organization_id = msg.organization_id").
		Where("f.organization_id = ? AND f.status = ?", channel.OrganizationID, domain.FileStatusActive).
		Where("ma.transfer_status = ?", domain.MessageAttachmentTransferReady).
		Where("msg.id = ? AND msg.conversation_id = ?", messageID, conversationID).
		Where("msg.visibility = ? AND msg.deleted_at IS NULL", domain.MessageVisibilityShared).
		Where("cc.contact_channel_identity_id = ?", identity.ID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fileaction.ErrFileNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get website visitor attachment: %w", err)
	}
	return record, nil
}

// loadWebsiteVisitor 读取启用网站渠道下已建立的访客渠道身份。
func loadWebsiteVisitor(ctx context.Context, db bun.IDB, channelID, externalID string) (*servermodels.Channel, *servermodels.ContactChannelIdentity, error) {
	fields := map[string]conversationaction.ValidationCode{}
	if !common.ValidUUID(channelID) {
		fields["channelId"] = ValidationChannelIDInvalid
	}
	if !customeridentity.ValidExternalID(externalID) {
		fields["visitorToken"] = ValidationExternalIDInvalid
	}
	if len(fields) > 0 {
		return nil, nil, &conversationaction.ValidationError{Fields: fields}
	}
	channel, err := loadWebsiteChannel(ctx, db, channelID)
	if err != nil {
		return nil, nil, err
	}
	identity, found, err := loadWebsiteVisitorIdentity(ctx, db, channel, externalID)
	if err != nil {
		return nil, nil, err
	}
	if !found {
		return nil, nil, conversationaction.ErrConversationNotFound
	}
	return channel, identity, nil
}
