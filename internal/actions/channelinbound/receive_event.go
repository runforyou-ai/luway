//go:build server

package channelinbound

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	"github.com/runforyou-ai/luway/internal/actions/channelbinding"
	"github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	"github.com/runforyou-ai/luway/internal/actions/channelmessage"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

// ReceiveEventAction 处理长连接与回调推送渠道的入站事件：私聊消息写入发起人会话并交给负责人、按需同步发起人头像，私聊消息与互动按渠道适配器的规则开启回复窗口，服务员工的渠道向未绑定的外部账号发送绑定链接，不支持的消息写入会话并在首次写入时投递提示任务，群聊提及回复提示。
type ReceiveEventAction struct {
	db             *bun.DB
	adapters       *channeladapter.Registry
	agentScheduler conversationaction.CustomerAgentMessageScheduler
	mediaBackend   func() domain.FileStorageBackend
	tasks          servertask.TxEnqueuer
	retrieve       *RetrieveMediaAction
	publicURL      func() string
}

// NewReceiveEventAction 创建渠道入站事件处理任务，mediaBackend 返回入站媒体的存储类型，retrieve 在事务提交后立即取回媒体，publicURL 返回生成绑定链接的部署地址。
func NewReceiveEventAction(db *bun.DB, adapters *channeladapter.Registry, agentScheduler conversationaction.CustomerAgentMessageScheduler, mediaBackend func() domain.FileStorageBackend, tasks servertask.TxEnqueuer, retrieve *RetrieveMediaAction, publicURL func() string) *ReceiveEventAction {
	return &ReceiveEventAction{db: db, adapters: adapters, agentScheduler: agentScheduler, mediaBackend: mediaBackend, tasks: tasks, retrieve: retrieve, publicURL: publicURL}
}

// Execute 在渠道仍启用且连接的平台账号未变化时处理事件；需要回复提示或绑定链接时在事务提交后经渠道发送。
func (a *ReceiveEventAction) Execute(ctx context.Context, input ReceiveEventInput) error {
	event := input.Event
	var channel *servermodels.Channel
	var reply string
	var link *channelbinding.IssuedLink
	var media []RetrieveMediaInput
	err := conversationaction.RunInTxWithUniqueRetry(ctx, a.db, RetryableConstraintNames, func(ctx context.Context, tx bun.Tx) error {
		channel, reply, link, media = &servermodels.Channel{}, "", nil, nil
		err := tx.NewSelect().Model(channel).
			Where("c.id = ? AND c.workspace_id = ? AND c.provider_account_id = ?", input.ChannelID, input.WorkspaceID, input.AccountID).
			Where(channelaction.AcceptsCustomersCondition("c")).
			For("SHARE OF c").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			channel = nil
			return nil
		}
		if err != nil {
			return fmt.Errorf("load connection event channel: %w", err)
		}
		locale := domain.CustomerLocale(channel.DefaultLocale)
		if event.Kind == channeladapter.InboundEventGroupMention {
			reply = i18n.LocalizeCustomerTemplate(locale, i18n.EmployeeChannelPrivateOnly, nil)
			return nil
		}
		// 服务员工的渠道只为已绑定成员提供服务，未绑定的外部账号收到绑定链接。
		if domain.ChannelCapabilitiesOf(domain.ChannelType(channel.Type)).Audience == domain.ServiceAudienceEmployee {
			_, err := LoadBoundIdentity(ctx, tx, channel, event.Sender)
			// 平台只允许向发过消息的对方主动发送，进入会话等事件不签发链接。
			if errors.Is(err, ErrIdentityUnbound) && event.Kind != channeladapter.InboundEventMessage {
				return nil
			}
			if errors.Is(err, ErrIdentityUnbound) {
				if link, err = channelbinding.IssueLink(ctx, tx, channel, event.Sender); err != nil || link == nil {
					return err
				}
				reply = i18n.LocalizeCustomerTemplate(locale, i18n.EmployeeChannelBindingLink, map[string]any{"Link": channelbinding.Link(a.publicURL(), link.Token)})
				return nil
			}
			if err != nil {
				return err
			}
		}
		// 互动事件只为已有渠道身份开启回复窗口。
		if event.Kind == channeladapter.InboundEventInteraction {
			var identityID string
			err := tx.NewSelect().Model((*servermodels.ChannelIdentity)(nil)).Column("id").
				Where("workspace_id = ? AND channel_id = ? AND external_id = ?", channel.WorkspaceID, channel.ID, event.Sender).
				Scan(ctx, &identityID)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("load interaction channel identity: %w", err)
			}
			return a.openReplyWindow(ctx, tx, channel, identityID, event)
		}
		if event.Kind != channeladapter.InboundEventMessage {
			return nil
		}
		var identityID string
		var inserted bool
		if media, identityID, inserted, err = a.receiveMessage(ctx, tx, channel, input); err != nil {
			return err
		}
		// 不支持的消息首次写入时在同一事务中投递提示任务，平台重推不重复提示；经长连接收发的渠道由持有连接的实例发送。
		if event.Unsupported && inserted {
			options := servertask.EnqueueOptions{
				WorkspaceID: channel.WorkspaceID, Queue: servertask.QueueDelivery,
				IdempotencyKey: "chnotice:" + channel.ID + ":" + input.AccountID + ":" + event.ID,
			}
			if domain.ChannelCapabilitiesOf(domain.ChannelType(channel.Type)).Connection {
				options.Route = channeladapter.ConnectionRoute(channel.ID)
			}
			if _, err := a.tasks.EnqueueIn(ctx, SendNoticeActionName, SendNoticeInput{
				WorkspaceID: channel.WorkspaceID, ChannelID: channel.ID, ChannelType: domain.ChannelType(channel.Type), AccountID: input.AccountID,
				Recipient: event.ChatID, Body: i18n.LocalizeCustomerTemplate(locale, i18n.ChannelUnsupportedMessage, nil),
			}, options); err != nil {
				return fmt.Errorf("enqueue unsupported message notice: %w", err)
			}
		}
		return a.openReplyWindow(ctx, tx, channel, identityID, event)
	})
	// 同一事件编号已写入内容不同的消息时保留已写入的消息。
	var conflict *conversationaction.ConflictError
	if errors.As(err, &conflict) && conflict.Reason == conversationaction.ConflictReasonIdempotencyMismatch {
		slog.WarnContext(ctx, "渠道入站消息幂等冲突已忽略", "channel_id", input.ChannelID, "event_id", event.ID)
		return nil
	}
	if err != nil || channel == nil {
		return err
	}
	// 平台媒体地址很快失效，提交后立即取回，失败时由已投递的取回任务重试。
	for _, item := range media {
		if err := a.retrieve.Execute(ctx, item); err != nil {
			slog.WarnContext(ctx, "渠道入站媒体即时取回失败，等待任务重试", "channel_id", item.ChannelID, "file_id", item.FileID, "error", err)
		}
	}
	if reply == "" {
		return nil
	}
	return a.reply(ctx, channel, input, reply, link)
}

// openReplyWindow 按渠道适配器的规则为渠道身份开启入站事件对应的回复窗口，开启新窗口时推进渠道身份所在会话的版本。
func (a *ReceiveEventAction) openReplyWindow(ctx context.Context, tx bun.Tx, channel *servermodels.Channel, identityID string, event channeladapter.InboundEvent) error {
	rules, ok := channeladapter.Lookup[channeladapter.ReplyWindows](a.adapters, domain.ChannelType(channel.Type))
	if !ok {
		return nil
	}
	window, ok := rules.ReplyWindow(event)
	if !ok {
		return nil
	}
	opened, err := channeldelivery.OpenReplyWindow(ctx, tx, channel.WorkspaceID, identityID, window)
	if err != nil || !opened {
		return err
	}
	return chatstate.TouchChannelIdentityConversations(ctx, tx, channel.WorkspaceID, identityID)
}

// receiveMessage 把私聊消息的正文与每个媒体分别写成一条消息，媒体说明随媒体写入，内容不受支持的消息写成一条不支持消息；待取回的媒体投递取回任务并在取回终态时交给负责人，不支持消息不交给负责人，其余新写入的消息（含超出入站上限直接失败的媒体）交给负责人，渠道适配器提供头像时按同步间隔投递头像同步任务。返回待取回的媒体、发起人的渠道身份与是否写入了新消息。
func (a *ReceiveEventAction) receiveMessage(ctx context.Context, tx bun.Tx, channel *servermodels.Channel, input ReceiveEventInput) ([]RetrieveMediaInput, string, bool, error) {
	event := input.Event
	parts := make([]Input, 0, len(event.Media)+1)
	base := "chmsg:" + channel.ID + ":evt:" + input.AccountID + ":" + event.ID + ":"
	if event.Unsupported {
		parts = append(parts, Input{Unsupported: true})
	}
	if event.Text != "" {
		parts = append(parts, Input{Body: event.Text})
	}
	for _, item := range event.Media {
		parts = append(parts, Input{Body: item.Caption, ExternalMedia: &ExternalMedia{
			ExternalID: item.Ref, FileName: item.FileName, ContentType: item.ContentType, ByteSize: item.ByteSize,
			ImageWidth: item.Width, ImageHeight: item.Height, StorageBackend: a.mediaBackend(),
		}})
	}
	displayName := support.NilIfZero(event.SenderName)
	var media []RetrieveMediaInput
	var identityID string
	inserted := false
	for index, part := range parts {
		part.ExternalID, part.DisplayName, part.SingleConversation = event.Sender, displayName, true
		part.IdempotencyKey, part.OriginatedAt = base+strconv.Itoa(index), event.OccurredAt
		// 平台消息编号对应写入的第一条消息，引用按平台消息编号关联。
		if index == 0 && event.MessageID != "" {
			part.ChannelMessage = &channelmessage.Inbound{AccountID: input.AccountID, ConversationID: event.ChatID, MessageID: event.MessageID}
			if reply := event.Reply; reply != nil {
				part.ChannelMessage.Reply = &channelmessage.Reply{MessageID: reply.MessageID, Body: reply.Body, SenderName: reply.SenderName, SenderIsBot: reply.SenderIsBot}
			}
		}
		if assertion := input.Assertion; assertion != nil {
			part.IdentityAsserted, part.VerifiedUserID, part.Email, part.SignedProfile = true, assertion.UserID, assertion.Email, assertion.Profile
		}
		received, err := Receive(ctx, tx, a.tasks, channel, part)
		if err != nil {
			return nil, "", false, err
		}
		identityID = received.ChannelIdentityID
		if !received.Inserted {
			continue
		}
		inserted = true
		if received.Attachment != nil && received.Attachment.TransferStatus == domain.MessageAttachmentTransferPending {
			retrieval := RetrieveMediaInput{
				WorkspaceID: channel.WorkspaceID, ChannelID: channel.ID, ConversationID: received.Message.ConversationID,
				MessageID: received.Message.ID, FileID: received.Attachment.ID, AccountID: input.AccountID, MediaRef: part.ExternalMedia.ExternalID,
			}
			if _, err := a.tasks.EnqueueIn(ctx, RetrieveMediaActionName, retrieval, servertask.EnqueueOptions{
				WorkspaceID: channel.WorkspaceID, Queue: servertask.QueueFiles, MaxAttempts: MediaRetrieveMaxAttempts,
				IdempotencyKey: "chmedia:" + received.Attachment.ID,
			}); err != nil {
				return nil, "", false, fmt.Errorf("enqueue connection media retrieval: %w", err)
			}
			media = append(media, retrieval)
			continue
		}
		if part.Unsupported {
			continue
		}
		if _, err := a.agentScheduler.ScheduleCustomerAuto(ctx, tx, channel.WorkspaceID, received.Message.ConversationID, received.Session.ID, received.Message.ID); err != nil {
			return nil, "", false, fmt.Errorf("schedule connection channel agent: %w", err)
		}
	}
	if !inserted {
		return media, identityID, inserted, nil
	}
	if _, ok := channeladapter.Lookup[channeladapter.Avatars](a.adapters, domain.ChannelType(channel.Type)); !ok {
		return media, identityID, inserted, nil
	}
	// 渠道身份超过同步间隔未同步头像时记录发起时间并投递头像同步任务。
	var due string
	err := tx.NewUpdate().Model((*servermodels.ChannelIdentity)(nil)).
		Set("avatar_checked_at = now()").
		Where("workspace_id = ? AND id = ?", channel.WorkspaceID, identityID).
		Where("avatar_checked_at IS NULL OR avatar_checked_at <= now() - ?::interval", avatarRefreshInterval).
		Returning("id").Scan(ctx, &due)
	if errors.Is(err, sql.ErrNoRows) {
		return media, identityID, inserted, nil
	}
	if err != nil {
		return nil, "", false, fmt.Errorf("mark channel identity avatar refresh: %w", err)
	}
	if _, err := a.tasks.EnqueueIn(ctx, RefreshAvatarActionName, RefreshAvatarInput{
		WorkspaceID: channel.WorkspaceID, ChannelID: channel.ID, AccountID: input.AccountID, ChannelIdentityID: identityID,
	}, servertask.EnqueueOptions{
		WorkspaceID: channel.WorkspaceID, MaxAttempts: 1, IdempotencyKey: "chavatar:" + identityID,
	}); err != nil {
		return nil, "", false, fmt.Errorf("enqueue channel identity avatar refresh: %w", err)
	}
	return media, identityID, inserted, nil
}

// reply 经渠道向事件所在会话发送提示或绑定链接；绑定链接确认送达时记录送达，平台未接受时撤销本次签发的令牌并交给任务重试。
func (a *ReceiveEventAction) reply(ctx context.Context, channel *servermodels.Channel, input ReceiveEventInput, body string, link *channelbinding.IssuedLink) error {
	target := channeladapter.Target{WorkspaceID: channel.WorkspaceID, ChannelID: channel.ID, AccountID: input.AccountID, Recipient: input.Event.ChatID, Group: input.Event.Kind == channeladapter.InboundEventGroupMention}
	result, err := sendNotice(ctx, a.adapters, domain.ChannelType(channel.Type), target, body)
	if err != nil {
		return err
	}
	// 确认送达的链接才计入发送间隔；无法确认时保留令牌，下次入站仍可签发新链接。
	switch result.Outcome {
	case channeladapter.OutcomeSent:
		if link != nil {
			return channelbinding.MarkLinkDelivered(context.WithoutCancel(ctx), a.db, channel.WorkspaceID, link.TokenID)
		}
		return nil
	case channeladapter.OutcomeUncertain:
		return nil
	}
	if link != nil {
		if err := channelbinding.RevokeLink(context.WithoutCancel(ctx), a.db, channel.WorkspaceID, link.TokenID); err != nil {
			return err
		}
	}
	return noticeError(ctx, channel.ID, result)
}
