//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strconv"

	deliveryaction "github.com/runforyou-ai/cervi/internal/actions/customerdelivery"
	inboxaction "github.com/runforyou-ai/cervi/internal/actions/inbox"
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/common/messagepreview"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/i18n"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	servertask "github.com/runforyou-ai/cervi/internal/task/server"
	"github.com/uptrace/bun"
)

// inboxOps 持有收件箱与客户投递的 Action 和 Query。
type inboxOps struct {
	customerDeliveries    *deliveryaction.Manager
	loadInbox             *inboxaction.LoadInboxQuery
	listServiceAssignees  *inboxaction.ListServiceAssigneesQuery
	listServiceQueueTeams *inboxaction.ListServiceQueueTeamsQuery
}

// newInboxOps 创建收件箱与客户投递的业务实现依赖。
func newInboxOps(db *bun.DB, taskEnqueuer servertask.TxEnqueuer) inboxOps {
	return inboxOps{
		customerDeliveries:    deliveryaction.NewManager(db, taskEnqueuer),
		loadInbox:             inboxaction.NewLoadInboxQuery(db),
		listServiceAssignees:  inboxaction.NewListServiceAssigneesQuery(db),
		listServiceQueueTeams: inboxaction.NewListServiceQueueTeamsQuery(db),
	}
}

// inboxLoadInput 把传输契约中的会话筛选转换为收件箱查询条件。
func inboxLoadInput(query appservice.InboxQuery) inboxaction.LoadInput {
	kinds := make([]domain.ConversationType, 0, len(query.Kinds))
	for _, kind := range query.Kinds {
		kinds = append(kinds, domain.ConversationType(kind))
	}
	return inboxaction.LoadInput{
		Partition: domain.InboxPartition(query.Partition), Scope: domain.InboxScope(query.Scope),
		PendingKind: domain.InboxPendingKind(query.PendingKind), QueueFilter: domain.ServiceQueueFilter(query.QueueFilter), QueueTeamID: query.QueueTeamID,
		ChannelID: query.ChannelID, Source: domain.ServiceSource(query.Source), Audience: domain.ServiceAudience(query.Audience), ServiceStatus: domain.ServiceSessionStatus(query.ServiceStatus),
		AssigneeFilter: domain.InboxAssigneeFilter(query.AssigneeFilter), AssigneeIdentityID: query.AssigneeIdentityID, Kinds: kinds,
		Search: query.Search, SearchRange: inboxaction.SearchRange(query.SearchRange),
	}
}

// LoadInbox 返回当前企业的统一会话工作队列。
func (o *directOperations) LoadInbox(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.LoadInboxInput) (appservice.Inbox, error) {
	loadInput := inboxLoadInput(inboxQuery(input))
	loadInput.Cursor, loadInput.BeforeCursor, loadInput.Limit = input.Cursor, input.BeforeCursor, input.Limit
	page, unreadCounts, err := o.loadInbox.Execute(ctx, identity, loadInput)
	if err != nil {
		return appservice.Inbox{}, inboxReadError(ctx, meta, identity.Organization.ID, "列表", err)
	}
	conversations, err := o.inboxConversationsFromActions(ctx, meta, identity, page.Conversations)
	if err != nil {
		return appservice.Inbox{}, err
	}
	return appservice.Inbox{
		StartCursor: page.StartCursor, EndCursor: page.EndCursor, HasBefore: page.HasBefore,
		PinOrderVersion: strconv.FormatInt(page.PinOrderVersion, 10), Conversations: conversations,
		NextCursor: page.NextCursor, HasMore: page.HasMore,
		UnreadCount: unreadCounts.Unread, AttentionUnreadCount: unreadCounts.Attention,
		PendingCount: unreadCounts.Pending, PendingUnreadCount: unreadCounts.PendingUnread,
	}, nil
}

// ListArchivedConversations 返回当前用户已归档的群聊、单聊与 AI 聊天分页列表。
func (o *directOperations) ListArchivedConversations(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ArchivedConversationListInput) (appservice.ArchivedConversationList, error) {
	var kinds []domain.ConversationType
	if input.Kind != "" {
		kinds = []domain.ConversationType{domain.ConversationType(input.Kind)}
	}
	page, err := o.loadInbox.ListArchived(ctx, identity, inboxaction.ArchivedInput{Kinds: kinds, Search: input.Search, Page: input.Page, PageSize: input.PageSize})
	if err != nil {
		return appservice.ArchivedConversationList{}, inboxReadError(ctx, meta, identity.Organization.ID, "已归档聊天", err)
	}
	conversations, err := o.inboxConversationsFromActions(ctx, meta, identity, page.Conversations)
	if err != nil {
		return appservice.ArchivedConversationList{}, err
	}
	return appservice.ArchivedConversationList{
		Conversations: conversations,
		Page:          appservice.PageInfo{Number: page.Page.Number, Size: page.Page.Size, Total: page.Page.Total},
	}, nil
}

// inboxConversationsFromActions 为会话摘要统一解析头像并转换传输契约。
func (o *directOperations) inboxConversationsFromActions(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, summaries []inboxaction.ConversationSummary) ([]appservice.InboxConversation, error) {
	avatarFileIDs := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		if summary.Group != nil && summary.Group.ImageFileID != nil {
			avatarFileIDs = append(avatarFileIDs, *summary.Group.ImageFileID)
		}
		if summary.Direct != nil && summary.Direct.PeerAvatarFileID != nil {
			avatarFileIDs = append(avatarFileIDs, *summary.Direct.PeerAvatarFileID)
		}
		if summary.Agent != nil && summary.Agent.AgentAvatarFileID != nil {
			avatarFileIDs = append(avatarFileIDs, *summary.Agent.AgentAvatarFileID)
		}
		if summary.Service == nil {
			continue
		}
		if summary.Service.RequesterAvatarFileID != nil {
			avatarFileIDs = append(avatarFileIDs, *summary.Service.RequesterAvatarFileID)
		}
		if summary.Service.Assignee != nil && summary.Service.Assignee.AvatarFileID != nil {
			avatarFileIDs = append(avatarFileIDs, *summary.Service.Assignee.AvatarFileID)
		}
	}
	avatarURLs, err := o.activeFileURLs(ctx, identity, avatarFileIDs)
	if err != nil {
		slog.Warn("读取收件箱会话图片失败", "organization_id", identity.Organization.ID, "error", err)
		return nil, appservice.FailedError(meta, i18n.ErrorInboxLoadFailed)
	}
	conversations := make([]appservice.InboxConversation, 0, len(summaries))
	for _, summary := range summaries {
		conversation := inboxConversationFromAction(summary, avatarURLs)
		conversations = append(conversations, conversation)
	}
	return conversations, nil
}

// ListServiceQueueTeams 返回可作为客服队列的团队，本人所在团队排在前面。
func (o *directOperations) ListServiceQueueTeams(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.ServiceQueueTeamList, error) {
	items, err := o.listServiceQueueTeams.Execute(ctx, identity)
	if err != nil {
		if ctx.Err() != nil {
			return appservice.ServiceQueueTeamList{}, ctx.Err()
		}
		slog.Warn("读取客服队列团队失败", "organization_id", identity.Organization.ID, "error", err)
		return appservice.ServiceQueueTeamList{}, appservice.FailedError(meta, i18n.ErrorTeamListFailed)
	}
	teams := make([]appservice.ServiceQueueTeam, 0, len(items))
	for _, item := range items {
		teams = append(teams, appservice.ServiceQueueTeam{ID: item.ID, Name: item.Name, Mine: item.Mine, Available: item.Available})
	}
	return appservice.ServiceQueueTeamList{Teams: teams}, nil
}

// ListServiceAssignees 返回有效真人和 AI 客服。
func (o *directOperations) ListServiceAssignees(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.ServiceAssigneeList, error) {
	items, err := o.listServiceAssignees.Execute(ctx, identity)
	if err != nil {
		if ctx.Err() != nil {
			return appservice.ServiceAssigneeList{}, ctx.Err()
		}
		slog.Warn("读取客服候选失败", "organization_id", identity.Organization.ID, "error", err)
		return appservice.ServiceAssigneeList{}, appservice.FailedError(meta, i18n.ErrorUserListFailed)
	}
	avatarFileIDs := make([]string, 0, len(items))
	for _, item := range items {
		if item.AvatarFileID != nil {
			avatarFileIDs = append(avatarFileIDs, *item.AvatarFileID)
		}
	}
	avatarURLs, err := o.activeFileURLs(ctx, identity, avatarFileIDs)
	if err != nil {
		slog.Warn("读取客服候选头像失败", "organization_id", identity.Organization.ID, "error", err)
		return appservice.ServiceAssigneeList{}, appservice.FailedError(meta, i18n.ErrorUserListFailed)
	}
	assignees := make([]appservice.InboxAssignee, 0, len(items))
	for _, item := range items {
		assignees = append(assignees, appservice.InboxAssignee{IdentityID: item.IdentityID, Type: appservice.OrganizationIdentityType(item.Type), DisplayName: item.DisplayName, AvatarURL: optionalFileURL(avatarURLs, item.AvatarFileID)})
	}
	return appservice.ServiceAssigneeList{Assignees: assignees}, nil
}

// inboxConversationFromAction 转换完整会话摘要并填充头像地址。
func inboxConversationFromAction(summary inboxaction.ConversationSummary, avatarURLs map[string]string) appservice.InboxConversation {
	conversation := appservice.InboxConversation{PositionCursor: summary.PositionCursor, ID: summary.ID, LastActivityAt: summary.LastActivityAt, Type: appservice.ConversationType(summary.Type), UnreadCount: summary.UnreadCount, MentionedUnreadCount: summary.MentionedUnreadCount, Muted: summary.Muted, MarkedUnread: summary.MarkedUnread, Pinned: summary.Pinned, ArchivedAt: summary.ArchivedAt, LastMessageID: summary.LastMessageID, LastMessageType: (*appservice.MessageType)(summary.LastMessageType), LastReadMessageID: summary.LastReadMessageID}
	if summary.Pending != nil {
		conversation.Pending = &appservice.InboxPendingItem{Kind: appservice.InboxPendingKind(summary.Pending.Kind), Since: summary.Pending.Since, Mentioned: summary.Pending.Mentioned}
	}
	if service := summary.Service; service != nil {
		var assignee *appservice.InboxAssignee
		if service.Assignee != nil {
			assignee = &appservice.InboxAssignee{IdentityID: service.Assignee.IdentityID, Type: appservice.OrganizationIdentityType(service.Assignee.Type), DisplayName: service.Assignee.DisplayName, AvatarURL: optionalFileURL(avatarURLs, service.Assignee.AvatarFileID)}
		}
		var channel *appservice.ServiceInboxChannel
		if service.Channel != nil {
			// 不支持外发附件时不给出字节上限，客户端据此关闭入口。
			attachmentSupported := domain.ChannelSupportsOutboundAttachment(service.Channel.Type)
			attachmentByteLimit := int64(0)
			if attachmentSupported {
				attachmentByteLimit = domain.ChannelAttachmentLimit(service.Channel.Type)
			}
			channel = &appservice.ServiceInboxChannel{
				Type: appservice.ChannelType(service.Channel.Type), Name: service.Channel.Name,
				AttachmentSupported: attachmentSupported, AttachmentByteLimit: attachmentByteLimit,
				AttachmentCaptionLimit: domain.ChannelCaptionLimit(service.Channel.Type),
			}
		}
		conversation.Service = &appservice.ServiceInboxConversation{
			Title: service.Title, Source: appservice.ServiceSource(service.Source), Audience: appservice.ServiceAudience(service.Audience),
			RequesterName: service.RequesterName, RequesterContactNumber: service.RequesterContactNumber, RequesterChatSubjectID: service.RequesterChatSubjectID,
			RequesterAvatarURL:    optionalFileURL(avatarURLs, service.RequesterAvatarFileID),
			AssigneeChatSubjectID: service.AssigneeChatSubjectID,
			Channel:               channel,
			AgentIdentityID:       service.AgentIdentityID, AgentName: service.AgentName,
			Preview:           messagePreviewText(service.Preview, service.PreviewSenderIdentityType),
			PreviewVisibility: (*appservice.MessageVisibility)(service.PreviewVisibility), LastMessageAt: service.LastMessageAt,
			ServiceSessionID: service.ServiceSessionID, ServiceSessionStatus: appservice.ServiceSessionStatus(service.ServiceSessionStatus), Assignee: assignee,
			TeamID: service.TeamID, TeamName: service.TeamName,
			UnansweredMentionCount: service.UnansweredMentionCount,
		}
	}
	if summary.Direct != nil {
		conversation.Direct = &appservice.DirectInboxConversation{
			PeerIdentityID: summary.Direct.PeerIdentityID, PeerType: appservice.OrganizationIdentityType(summary.Direct.PeerType), PeerName: summary.Direct.PeerName, PeerAvatarURL: optionalFileURL(avatarURLs, summary.Direct.PeerAvatarFileID), PeerStatus: appservice.UserStatus(summary.Direct.PeerStatus), PeerWorkStatus: appservice.WorkStatus(summary.Direct.PeerWorkStatus),
			Preview: messagePreviewText(summary.Direct.Preview, summary.Direct.PreviewSenderIdentityType), LastMessageAt: summary.Direct.LastMessageAt,
		}
	}
	if summary.Agent != nil {
		var agentRunStatus *appservice.AgentRunStatus
		if summary.Agent.AgentRunStatus != nil {
			status := appservice.AgentRunStatus(*summary.Agent.AgentRunStatus)
			agentRunStatus = &status
		}
		// 只有助理携带在线状态。
		var assistantPresence *appservice.AssistantPresence
		if summary.Agent.AssistantPresence != "" {
			presence := appservice.AssistantPresence(summary.Agent.AssistantPresence)
			assistantPresence = &presence
		}
		conversation.Agent = &appservice.AgentInboxConversation{
			Title: summary.Agent.Title, AgentIdentityID: summary.Agent.AgentIdentityID, AgentName: summary.Agent.AgentName, AgentAvatarURL: optionalFileURL(avatarURLs, summary.Agent.AgentAvatarFileID), AgentStatus: appservice.UserStatus(summary.Agent.AgentStatus),
			AgentType: appservice.OrganizationIdentityType(summary.Agent.AgentType), AssistantPresence: assistantPresence,
			Preview: messagePreviewText(summary.Agent.Preview, summary.Agent.PreviewSenderIdentityType), LastMessageAt: summary.Agent.LastMessageAt, AgentRunStatus: agentRunStatus,
			ServiceOpen: summary.Agent.ServiceOpen,
		}
	}
	if summary.Group != nil {
		conversation.Group = &appservice.GroupInboxConversation{
			Title: summary.Group.Title, ImageURL: optionalFileURL(avatarURLs, summary.Group.ImageFileID),
			Status: appservice.ConversationStatus(summary.Group.Status), Preview: messagePreviewText(summary.Group.Preview, summary.Group.PreviewSenderIdentityType),
			LastMessageAt: summary.Group.LastMessageAt, MemberCount: summary.Group.MemberCount,
			MemberPreviewNames: summary.Group.MemberPreviewNames,
		}
	}
	return conversation
}

// GetInboxConversation 按编号读取当前用户可见的独立会话摘要。
func (o *directOperations) GetInboxConversation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.InboxConversation, error) {
	results, err := o.loadInbox.ReadByIDs(ctx, identity, []string{conversationID}, nil)
	if err != nil {
		return appservice.InboxConversation{}, inboxReadError(ctx, meta, identity.Organization.ID, "独立摘要", err)
	}
	if results[0].Conversation == nil {
		return appservice.InboxConversation{}, appservice.NotFoundError(meta, i18n.ErrorConversationNotFound).WithReason("conversation_unavailable")
	}
	conversations, err := o.inboxConversationsFromActions(ctx, meta, identity, []inboxaction.ConversationSummary{*results[0].Conversation})
	if err != nil {
		return appservice.InboxConversation{}, err
	}
	return conversations[0], nil
}

// ReadConversationAttention 读取会话摘要及已知消息之后计入本人提醒的未读消息。
func (o *directOperations) ReadConversationAttention(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.ConversationAttentionInput) (appservice.ConversationAttention, error) {
	attention, err := o.loadInbox.ReadAttention(ctx, identity, conversationID, input.AfterMessageID)
	if err != nil {
		return appservice.ConversationAttention{}, inboxReadError(ctx, meta, identity.Organization.ID, "提醒消息", err)
	}
	if attention == nil {
		return appservice.ConversationAttention{}, appservice.NotFoundError(meta, i18n.ErrorConversationNotFound).WithReason("conversation_unavailable")
	}
	conversations, err := o.inboxConversationsFromActions(ctx, meta, identity, []inboxaction.ConversationSummary{attention.Conversation})
	if err != nil {
		return appservice.ConversationAttention{}, err
	}
	output := appservice.ConversationAttention{Conversation: conversations[0], Messages: make([]appservice.ConversationAttentionMessage, 0, len(attention.Messages))}
	for _, message := range attention.Messages {
		output.Messages = append(output.Messages, appservice.ConversationAttentionMessage{
			ID: message.ID, Type: appservice.MessageType(message.Type), Visibility: appservice.MessageVisibility(message.Visibility),
			Preview:        *messagePreviewText(&message.Body, message.SenderIdentityType),
			AttachmentName: message.AttachmentName, SenderName: message.SenderName,
		})
	}
	return output, nil
}

// ReadInboxConversations 在每项中区分匹配、筛选外可读及不可用的会话。
func (o *directOperations) ReadInboxConversations(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ReadInboxConversationsInput) (appservice.InboxConversationResults, error) {
	// 未指定范围与搜索词时只核对阅读资格。
	var query *inboxaction.LoadInput
	if input.Query.Scope != "" || input.Query.Search != "" {
		loadInput := inboxLoadInput(input.Query)
		query = &loadInput
	}
	results, err := o.loadInbox.ReadByIDs(ctx, identity, input.ConversationIDs, query)
	if err != nil {
		return appservice.InboxConversationResults{}, inboxReadError(ctx, meta, identity.Organization.ID, "批量摘要", err)
	}
	summaries := make([]inboxaction.ConversationSummary, 0, len(results))
	for _, result := range results {
		if result.Conversation != nil {
			summaries = append(summaries, *result.Conversation)
		}
	}
	conversations, err := o.inboxConversationsFromActions(ctx, meta, identity, summaries)
	if err != nil {
		return appservice.InboxConversationResults{}, err
	}
	output := appservice.InboxConversationResults{Results: make([]appservice.InboxConversationResult, 0, len(results))}
	readable := 0
	for _, result := range results {
		item := appservice.InboxConversationResult{ID: result.ID, Availability: appservice.InboxConversationUnavailable}
		if result.Conversation != nil {
			item.Conversation = &conversations[readable]
			readable++
			item.Availability = appservice.InboxConversationOutsideQuery
			if result.MatchesQuery {
				item.Availability = appservice.InboxConversationMatching
			}
		}
		output.Results = append(output.Results, item)
	}
	return output, nil
}

// ListInboxChannels 返回按渠道类型与名称排列的渠道筛选候选，包含已停用渠道。
func (o *directOperations) ListInboxChannels(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.InboxChannelList, error) {
	records, err := o.listMessageChannels.ExecuteByType(ctx, identity)
	if err != nil {
		if ctx.Err() != nil {
			return appservice.InboxChannelList{}, ctx.Err()
		}
		slog.Warn("读取收件箱渠道候选失败", "organization_id", identity.Organization.ID, "error", err)
		return appservice.InboxChannelList{}, appservice.FailedError(meta, i18n.ErrorChannelListFailed)
	}
	channels := make([]appservice.InboxChannel, 0, len(records))
	for _, record := range records {
		channels = append(channels, appservice.InboxChannel{ID: record.ID, Type: appservice.ChannelType(record.Type), Name: record.Name, Enabled: record.Enabled})
	}
	return appservice.InboxChannelList{Channels: channels}, nil
}

// GetSyncHeads 返回当前用户可见会话数量、版本校验和与身份资料版本。
func (o *directOperations) GetSyncHeads(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.SyncHeads, error) {
	heads, err := o.loadInbox.SyncHeads(ctx, identity)
	if err != nil {
		return appservice.SyncHeads{}, inboxReadError(ctx, meta, identity.Organization.ID, "同步探针", err)
	}
	return appservice.SyncHeads{
		ConversationCount: heads.ConversationCount, ConversationChecksum: heads.ConversationChecksum,
		IdentityProfileVersion: strconv.FormatInt(heads.IdentityProfileVersion, 10),
		PinOrderVersion:        strconv.FormatInt(heads.PinOrderVersion, 10),
	}, nil
}

// inboxReadError 统一转换收件箱读取错误，并记录失败的查询入口。
func inboxReadError(ctx context.Context, meta appservice.RequestMeta, organizationID, operation string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, inboxaction.ErrCursorInvalid) {
		return appservice.InvalidError(meta, i18n.ErrorInboxCursorInvalid, nil).WithReason("inbox_cursor_invalid")
	}
	if errors.Is(err, inboxaction.ErrQueryInvalid) {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
	}
	slog.Warn("读取收件箱失败", "organization_id", organizationID, "operation", operation, "error", err)
	return appservice.FailedError(meta, i18n.ErrorInboxLoadFailed)
}

// SearchInbox 按范围检索会话、消息和人员，并统一解析会话图片与人员头像。
func (o *directOperations) SearchInbox(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.InboxSearchInput) (appservice.InboxSearchResult, error) {
	list := inboxLoadInput(appservice.InboxQuery{
		Scope: input.Scope, PendingKind: input.PendingKind, QueueFilter: input.QueueFilter, QueueTeamID: input.QueueTeamID,
		ChannelID: input.ChannelID, Source: input.Source, Audience: input.Audience, ServiceStatus: input.ServiceStatus,
		AssigneeFilter: input.AssigneeFilter, AssigneeIdentityID: input.AssigneeIdentityID, Kinds: input.Kinds,
	})
	result, err := o.loadInbox.Search(ctx, identity, inboxaction.SearchInput{
		Text: input.Query, Range: inboxaction.SearchRange(input.Range), List: list, ConversationID: input.ConversationID,
	})
	if err != nil {
		if ctx.Err() != nil {
			return appservice.InboxSearchResult{}, ctx.Err()
		}
		if errors.Is(err, inboxaction.ErrConversationUnavailable) {
			return appservice.InboxSearchResult{}, appservice.NotFoundError(meta, i18n.ErrorConversationNotFound).WithReason("conversation_unavailable")
		}
		if errors.Is(err, inboxaction.ErrQueryInvalid) {
			return appservice.InboxSearchResult{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
		}
		slog.Warn("检索收件箱失败", "organization_id", identity.Organization.ID, "range", input.Range, "error", err)
		return appservice.InboxSearchResult{}, appservice.FailedError(meta, i18n.ErrorInboxSearchFailed)
	}
	// 会话结果与各条消息的所在会话共用一次图片解析，转换后按原顺序拆回。
	summaries := slices.Clone(result.Conversations)
	for _, message := range result.Messages {
		summaries = append(summaries, message.Conversation)
	}
	conversations, err := o.inboxConversationsFromActions(ctx, meta, identity, summaries)
	if err != nil {
		return appservice.InboxSearchResult{}, err
	}
	avatarFileIDs := make([]string, 0, len(result.People))
	for _, person := range result.People {
		if person.AvatarFileID != nil {
			avatarFileIDs = append(avatarFileIDs, *person.AvatarFileID)
		}
	}
	avatarURLs, err := o.activeFileURLs(ctx, identity, avatarFileIDs)
	if err != nil {
		slog.Warn("读取检索人员头像失败", "organization_id", identity.Organization.ID, "error", err)
		return appservice.InboxSearchResult{}, appservice.FailedError(meta, i18n.ErrorInboxSearchFailed)
	}
	output := appservice.InboxSearchResult{
		Conversations: conversations[:len(result.Conversations)],
		Messages:      make([]appservice.InboxSearchMessage, 0, len(result.Messages)),
		People:        make([]appservice.InboxSearchPerson, 0, len(result.People)),
	}
	for index, message := range result.Messages {
		excerpt := make([]appservice.InboxSearchSegment, 0, len(message.Excerpt))
		for _, segment := range message.Excerpt {
			excerpt = append(excerpt, appservice.InboxSearchSegment{Text: segment.Text, Match: segment.Match})
		}
		output.Messages = append(output.Messages, appservice.InboxSearchMessage{
			ID: message.ID, Type: appservice.MessageType(message.Type), SenderName: message.SenderName, SenderContactNumber: message.SenderContactNumber, OriginatedAt: message.OriginatedAt,
			Excerpt: excerpt, Conversation: conversations[len(result.Conversations)+index],
		})
	}
	for _, person := range result.People {
		item := appservice.InboxSearchPerson{
			Kind: appservice.InboxSearchPersonKind(person.Kind), ID: person.ID, UserID: person.UserID, AgentID: person.AgentID, DisplayName: person.DisplayName, ContactNumber: person.ContactNumber,
			AvatarURL: optionalFileURL(avatarURLs, person.AvatarFileID), ConversationID: person.ConversationID,
		}
		if person.Kind == inboxaction.SearchPersonMember {
			identityType := appservice.OrganizationIdentityType(person.IdentityType)
			item.IdentityType = &identityType
		}
		output.People = append(output.People, item)
	}
	return output, nil
}

// messagePreviewText 把消息正文转换为单行纯文本摘要，AI 员工的回复按 Markdown 提取文字。
func messagePreviewText(body *string, senderType *domain.OrganizationIdentityType) *string {
	if body == nil {
		return nil
	}
	preview := messagepreview.Text(*body, senderType != nil && *senderType == domain.OrganizationIdentityTypeAgent)
	return &preview
}

// inboxQuery 返回不含分页边界的会话筛选。
func inboxQuery(input appservice.LoadInboxInput) appservice.InboxQuery {
	return appservice.InboxQuery{
		Partition: input.Partition, Scope: input.Scope,
		PendingKind: input.PendingKind, QueueFilter: input.QueueFilter, QueueTeamID: input.QueueTeamID,
		ChannelID: input.ChannelID, Source: input.Source, Audience: input.Audience, ServiceStatus: input.ServiceStatus,
		AssigneeFilter: input.AssigneeFilter, AssigneeIdentityID: input.AssigneeIdentityID, Kinds: input.Kinds,
		Search: input.Search, SearchRange: input.SearchRange,
	}
}
