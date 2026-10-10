//go:build server

package direct

import (
	"context"
	"errors"
	"slices"
	"strconv"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	deliveryaction "github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common/messagepreview"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/searchtext"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// inboxOps 持有收件箱与客户投递的 Action 和 Query。
type inboxOps struct {
	customerDeliveries    *deliveryaction.Manager
	loadInbox             *inboxaction.LoadInboxQuery
	listServiceAssignees  *inboxaction.ListServiceAssigneesQuery
	listServiceQueueTeams *inboxaction.ListServiceQueueTeamsQuery
	// listMessageChannels 列出收件箱渠道筛选候选。
	listMessageChannels *channelaction.ListMessageChannelsQuery
	// files 解析文件地址。
	files *fileOps
}

// newInboxOps 创建收件箱与客户投递的业务实现依赖。
func newInboxOps(db *bun.DB, taskEnqueuer servertask.TxEnqueuer, files *fileOps) *inboxOps {
	return &inboxOps{
		customerDeliveries:    deliveryaction.NewManager(db, taskEnqueuer),
		loadInbox:             inboxaction.NewLoadInboxQuery(db),
		listServiceAssignees:  inboxaction.NewListServiceAssigneesQuery(db),
		listServiceQueueTeams: inboxaction.NewListServiceQueueTeamsQuery(db),
		listMessageChannels:   channelaction.NewListMessageChannelsQuery(db),
		files:                 files,
	}
}

// inboxLoadInput 把传输契约中的会话筛选转换为收件箱查询条件。
func inboxLoadInput(query appservice.InboxQuery) inboxaction.LoadInput {
	kinds := append(make([]domain.ConversationType, 0, len(query.Kinds)), query.Kinds...)
	return inboxaction.LoadInput{
		Partition: domain.InboxPartition(query.Partition), Scope: query.Scope,
		PendingKind: query.PendingKind, QueueFilter: query.QueueFilter, QueueTeamID: query.QueueTeamID,
		ChannelID: query.ChannelID, Source: query.Source, Audience: query.Audience, ServiceStatus: query.ServiceStatus,
		AssigneeFilter: query.AssigneeFilter, AssigneeIdentityID: query.AssigneeIdentityID, Kinds: kinds,
		Search: query.Search, SearchRange: inboxaction.SearchRange(query.SearchRange),
	}
}

// LoadInbox 返回当前企业的统一会话工作队列。
func (o *inboxOps) LoadInbox(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.LoadInboxInput) (appservice.Inbox, error) {
	loadInput := inboxLoadInput(inboxQuery(input))
	loadInput.Cursor, loadInput.BeforeCursor, loadInput.Limit = input.Cursor, input.BeforeCursor, input.Limit
	page, unreadCounts, err := o.loadInbox.Execute(ctx, identity, loadInput)
	if err != nil {
		return appservice.Inbox{}, inboxReadError(meta, err)
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
func (o *inboxOps) ListArchivedConversations(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ArchivedConversationListInput) (appservice.ArchivedConversationList, error) {
	var kinds []domain.ConversationType
	if input.Kind != "" {
		kinds = []domain.ConversationType{input.Kind}
	}
	page, err := o.loadInbox.ListArchived(ctx, identity, inboxaction.ArchivedInput{Kinds: kinds, Search: input.Search, Page: input.Page, PageSize: input.PageSize})
	if err != nil {
		return appservice.ArchivedConversationList{}, inboxReadError(meta, err)
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
func (o *inboxOps) inboxConversationsFromActions(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, summaries []inboxaction.ConversationSummary) ([]appservice.InboxConversation, error) {
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
	avatarURLs, err := o.files.activeFileURLs(ctx, identity, avatarFileIDs)
	if err != nil {
		return nil, appservice.FailedError(meta, i18n.ErrorInboxLoadFailed, err)
	}
	return arr.OrEmpty(arr.Map(summaries, func(summary inboxaction.ConversationSummary) appservice.InboxConversation {
		return inboxConversationFromAction(summary, avatarURLs)
	})), nil
}

// ListServiceQueueTeams 返回可作为客服队列的团队，本人所在团队排在前面。
func (o *inboxOps) ListServiceQueueTeams(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.ServiceQueueTeamList, error) {
	items, err := o.listServiceQueueTeams.Execute(ctx, identity)
	if err != nil {
		return appservice.ServiceQueueTeamList{}, appservice.FailedError(meta, i18n.ErrorTeamListFailed, err)
	}
	teams := arr.Map(items, func(item inboxaction.ServiceQueueTeam) appservice.ServiceQueueTeam {
		return appservice.ServiceQueueTeam{ID: item.ID, Name: item.Name, Mine: item.Mine, Available: item.Available}
	})
	return appservice.ServiceQueueTeamList{Teams: teams}, nil
}

// ListServiceAssignees 返回有效真人和 AI 客服。
func (o *inboxOps) ListServiceAssignees(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.ServiceAssigneeList, error) {
	items, err := o.listServiceAssignees.Execute(ctx, identity)
	if err != nil {
		return appservice.ServiceAssigneeList{}, appservice.FailedError(meta, i18n.ErrorUserListFailed, err)
	}
	avatarFileIDs := arr.FilterMap(items, func(item inboxaction.ServiceAssignee) (string, bool) {
		return support.Deref(item.AvatarFileID), item.AvatarFileID != nil
	})
	avatarURLs, err := o.files.activeFileURLs(ctx, identity, avatarFileIDs)
	if err != nil {
		return appservice.ServiceAssigneeList{}, appservice.FailedError(meta, i18n.ErrorUserListFailed, err)
	}
	assignees := make([]appservice.InboxAssignee, 0, len(items))
	for _, item := range items {
		audiences := append(make([]appservice.ServiceAudience, 0, len(item.ServiceAudiences)), item.ServiceAudiences...)
		assignees = append(assignees, appservice.InboxAssignee{IdentityID: item.IdentityID, Type: item.Type, DisplayName: item.DisplayName, AvatarURL: optionalFileURL(avatarURLs, item.AvatarFileID), ServiceAudiences: audiences})
	}
	return appservice.ServiceAssigneeList{Assignees: assignees}, nil
}

// inboxConversationFromAction 转换完整会话摘要并填充头像地址。
func inboxConversationFromAction(summary inboxaction.ConversationSummary, avatarURLs map[string]string) appservice.InboxConversation {
	conversation := appservice.InboxConversation{PositionCursor: summary.PositionCursor, ID: summary.ID, LastActivityAt: summary.LastActivityAt, Type: summary.Type, UnreadCount: summary.UnreadCount, MentionedUnreadCount: summary.MentionedUnreadCount, Muted: summary.Muted, MarkedUnread: summary.MarkedUnread, Pinned: summary.Pinned, ArchivedAt: summary.ArchivedAt, LastMessageID: summary.LastMessageID, LastMessageType: summary.LastMessageType, LastSystemEventType: summary.LastSystemEventType, LastReadMessageID: summary.LastReadMessageID}
	if summary.Pending != nil {
		conversation.Pending = &appservice.InboxPendingItem{Kind: summary.Pending.Kind, Since: summary.Pending.Since, Mentioned: summary.Pending.Mentioned}
	}
	if service := summary.Service; service != nil {
		assignee := support.MapPtr(service.Assignee, func(assignee inboxaction.AssigneeSummary) appservice.InboxAssignee {
			return appservice.InboxAssignee{IdentityID: assignee.IdentityID, Type: assignee.Type, DisplayName: assignee.DisplayName, AvatarURL: optionalFileURL(avatarURLs, assignee.AvatarFileID)}
		})
		channel := support.MapPtr(service.Channel, func(channel inboxaction.ServiceChannelSummary) appservice.ServiceInboxChannel {
			return appservice.ServiceInboxChannel{Type: channel.Type, Name: channel.Name, Capabilities: appservice.NewChannelCapabilities(channel.Type)}
		})
		replyWindow := support.MapPtr(service.ReplyWindow, func(window inboxaction.ReplyWindowSummary) appservice.ServiceReplyWindow {
			return appservice.ServiceReplyWindow{ExpiresAt: window.ExpiresAt, Remaining: window.Remaining}
		})
		conversation.Service = &appservice.ServiceInboxConversation{
			Title: service.Title, Source: service.Source, Audience: service.Audience,
			RequesterName: service.RequesterName, RequesterContactNumber: service.RequesterContactNumber, RequesterChatSubjectID: service.RequesterChatSubjectID,
			RequesterAvatarURL:    optionalFileURL(avatarURLs, service.RequesterAvatarFileID),
			AssigneeChatSubjectID: service.AssigneeChatSubjectID,
			Channel:               channel, ReplyWindow: replyWindow,
			AgentIdentityID: service.AgentIdentityID, AgentName: service.AgentName,
			Preview:           messagePreviewText(service.Preview, service.PreviewSenderIdentityType),
			PreviewVisibility: service.PreviewVisibility, LastMessageAt: service.LastMessageAt,
			ServiceSessionID: service.ServiceSessionID, ServiceSessionStatus: service.ServiceSessionStatus, Assignee: assignee,
			TeamID: service.TeamID, TeamName: service.TeamName,
			UnansweredMentionCount: service.UnansweredMentionCount,
		}
	}
	if summary.Direct != nil {
		conversation.Direct = &appservice.DirectInboxConversation{
			PeerIdentityID: summary.Direct.PeerIdentityID, PeerType: summary.Direct.PeerType, PeerName: summary.Direct.PeerName, PeerAvatarURL: optionalFileURL(avatarURLs, summary.Direct.PeerAvatarFileID), PeerStatus: appservice.UserStatus(summary.Direct.PeerStatus), PeerWorkStatus: summary.Direct.PeerWorkStatus,
			Preview: messagePreviewText(summary.Direct.Preview, summary.Direct.PreviewSenderIdentityType), LastMessageAt: summary.Direct.LastMessageAt,
		}
	}
	if summary.Agent != nil {
		var agentRunStatus *appservice.AgentRunStatus
		if summary.Agent.AgentRunStatus != nil {
			agentRunStatus = new(*summary.Agent.AgentRunStatus)
		}
		conversation.Agent = &appservice.AgentInboxConversation{
			Title: summary.Agent.Title, AgentIdentityID: summary.Agent.AgentIdentityID, AgentName: summary.Agent.AgentName, AgentAvatarURL: optionalFileURL(avatarURLs, summary.Agent.AgentAvatarFileID), AgentStatus: appservice.UserStatus(summary.Agent.AgentStatus),
			PersonalPresence: support.NilIfZero(summary.Agent.PersonalPresence),
			Preview:          messagePreviewText(summary.Agent.Preview, summary.Agent.PreviewSenderIdentityType), LastMessageAt: summary.Agent.LastMessageAt, AgentRunStatus: agentRunStatus,
			ServiceOpen: summary.Agent.ServiceOpen,
		}
	}
	if summary.Group != nil {
		conversation.Group = &appservice.GroupInboxConversation{
			Title: summary.Group.Title, ImageURL: optionalFileURL(avatarURLs, summary.Group.ImageFileID),
			Status: summary.Group.Status, Preview: messagePreviewText(summary.Group.Preview, summary.Group.PreviewSenderIdentityType),
			LastMessageAt: summary.Group.LastMessageAt, MemberCount: summary.Group.MemberCount,
			MemberPreviewNames: summary.Group.MemberPreviewNames,
		}
	}
	return conversation
}

// GetInboxConversation 按编号读取当前用户可见的独立会话摘要。
func (o *inboxOps) GetInboxConversation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.InboxConversation, error) {
	results, err := o.loadInbox.ReadByIDs(ctx, identity, []string{conversationID}, nil)
	if err != nil {
		return appservice.InboxConversation{}, inboxReadError(meta, err)
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

// ReadInboxConversations 在每项中区分匹配、筛选外可读及不可用的会话。
func (o *inboxOps) ReadInboxConversations(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.ReadInboxConversationsInput) (appservice.InboxConversationResults, error) {
	// 未指定范围与搜索词时只核对阅读资格。
	var query *inboxaction.LoadInput
	if input.Query.Scope != "" || input.Query.Search != "" {
		query = new(inboxLoadInput(input.Query))
	}
	results, err := o.loadInbox.ReadByIDs(ctx, identity, input.ConversationIDs, query)
	if err != nil {
		return appservice.InboxConversationResults{}, inboxReadError(meta, err)
	}
	summaries := arr.FilterMap(results, func(result inboxaction.ConversationResult) (inboxaction.ConversationSummary, bool) {
		return support.Deref(result.Conversation), result.Conversation != nil
	})
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
func (o *inboxOps) ListInboxChannels(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.InboxChannelList, error) {
	records, err := o.listMessageChannels.ExecuteByType(ctx, identity)
	if err != nil {
		return appservice.InboxChannelList{}, appservice.FailedError(meta, i18n.ErrorChannelListFailed, err)
	}
	channels := arr.Map(records, func(record channelaction.MessageChannelRecord) appservice.InboxChannel {
		return appservice.InboxChannel{ID: record.ID, Type: appservice.ChannelType(record.Type), Name: record.Name, Enabled: record.Enabled}
	})
	return appservice.InboxChannelList{Channels: channels}, nil
}

// GetSyncHeads 返回当前用户可见会话数量、版本校验和与身份资料版本。
func (o *inboxOps) GetSyncHeads(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.SyncHeads, error) {
	heads, err := o.loadInbox.SyncHeads(ctx, identity)
	if err != nil {
		return appservice.SyncHeads{}, inboxReadError(meta, err)
	}
	return appservice.SyncHeads{
		ConversationCount: heads.ConversationCount, ConversationChecksum: heads.ConversationChecksum,
		IdentityProfileVersion: strconv.FormatInt(heads.IdentityProfileVersion, 10),
		PinOrderVersion:        strconv.FormatInt(heads.PinOrderVersion, 10),
	}, nil
}

// inboxReadErrors 是收件箱读取的错误转换规则。
var inboxReadErrors = dispatch.Catalog{
	dispatch.Is(inboxaction.ErrCursorInvalid, dispatch.WithReason(dispatch.Invalid(i18n.ErrorInboxCursorInvalid), "inbox_cursor_invalid")),
	dispatch.Is(inboxaction.ErrQueryInvalid, dispatch.Invalid(i18n.ErrorValidationFailed)),
}

// inboxReadError 统一转换收件箱读取错误。
func inboxReadError(meta appservice.RequestMeta, err error) error {
	return inboxReadErrors.Translate(meta, err, i18n.ErrorInboxLoadFailed)
}

// SearchInbox 按范围检索会话、消息和人员，并统一解析会话图片与人员头像。
func (o *inboxOps) SearchInbox(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.InboxSearchInput) (appservice.InboxSearchResult, error) {
	list := inboxLoadInput(appservice.InboxQuery{
		Scope: input.Scope, PendingKind: input.PendingKind, QueueFilter: input.QueueFilter, QueueTeamID: input.QueueTeamID,
		ChannelID: input.ChannelID, Source: input.Source, Audience: input.Audience, ServiceStatus: input.ServiceStatus,
		AssigneeFilter: input.AssigneeFilter, AssigneeIdentityID: input.AssigneeIdentityID, Kinds: input.Kinds,
	})
	result, err := o.loadInbox.Search(ctx, identity, inboxaction.SearchInput{
		Text: input.Query, Range: inboxaction.SearchRange(input.Range), List: list, ConversationID: input.ConversationID,
	})
	if err != nil {
		if errors.Is(err, inboxaction.ErrConversationUnavailable) {
			return appservice.InboxSearchResult{}, appservice.NotFoundError(meta, i18n.ErrorConversationNotFound).WithReason("conversation_unavailable")
		}
		if errors.Is(err, inboxaction.ErrQueryInvalid) {
			return appservice.InboxSearchResult{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
		}
		return appservice.InboxSearchResult{}, appservice.FailedError(meta, i18n.ErrorInboxSearchFailed, err)
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
	avatarFileIDs := arr.FilterMap(result.People, func(person inboxaction.SearchPerson) (string, bool) {
		return support.Deref(person.AvatarFileID), person.AvatarFileID != nil
	})
	avatarURLs, err := o.files.activeFileURLs(ctx, identity, avatarFileIDs)
	if err != nil {
		return appservice.InboxSearchResult{}, appservice.FailedError(meta, i18n.ErrorInboxSearchFailed, err)
	}
	output := appservice.InboxSearchResult{
		Conversations: conversations[:len(result.Conversations)],
		Messages:      make([]appservice.InboxSearchMessage, 0, len(result.Messages)),
		People:        make([]appservice.InboxSearchPerson, 0, len(result.People)),
	}
	for index, message := range result.Messages {
		output.Messages = append(output.Messages, appservice.InboxSearchMessage{
			ID: message.ID, Type: message.Type, SenderName: message.SenderName, SenderContactNumber: message.SenderContactNumber, OriginatedAt: message.OriginatedAt,
			Excerpt: arr.Map(message.Excerpt, func(segment searchtext.Segment) appservice.InboxSearchSegment {
				return appservice.InboxSearchSegment{Text: segment.Text, Match: segment.Match}
			}),
			Conversation: conversations[len(result.Conversations)+index],
		})
	}
	for _, person := range result.People {
		item := appservice.InboxSearchPerson{
			Kind: appservice.InboxSearchPersonKind(person.Kind), ID: person.ID, UserID: person.UserID, AgentID: person.AgentID, Personal: person.Personal, DisplayName: person.DisplayName, ContactNumber: person.ContactNumber,
			AvatarURL: optionalFileURL(avatarURLs, person.AvatarFileID), ConversationID: person.ConversationID,
		}
		if person.Kind == inboxaction.SearchPersonMember {
			item.IdentityType = new(person.IdentityType)
		}
		output.People = append(output.People, item)
	}
	return output, nil
}

// messagePreviewText 把消息正文转换为单行纯文本摘要，AI 员工的回复按 Markdown 提取文字。
func messagePreviewText(body *string, senderType *domain.WorkspaceIdentityType) *string {
	return support.MapPtr(body, func(body string) string {
		return messagepreview.Text(body, senderType != nil && *senderType == domain.WorkspaceIdentityTypeAgent)
	})
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
