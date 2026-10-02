//go:build server

// 会话业务操作依赖与共享错误映射。

package direct

import (
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/i18n"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// conversationOps 持有会话与消息的 Action 和 Query。
type conversationOps struct {
	sendAttachmentMessage           *directchataction.SendAttachmentMessageAction
	listConversationMessages        *conversationaction.ListConversationMessagesQuery
	updateConversationUnreadMark    *conversationaction.UpdateConversationUnreadMarkAction
	updateConversationPin           *conversationaction.UpdateConversationPinAction
	updateConversationArchive       *conversationaction.UpdateConversationArchiveAction
	markConversationRead            *conversationaction.MarkConversationReadAction
	reportConversationTyping        *conversationaction.ReportConversationTypingAction
	conversationNavigation          *conversationaction.GetConversationNavigationStateQuery
	pendingConversationMentions     *conversationaction.ListPendingConversationMentionsQuery
	reviewConversationMention       *conversationaction.MarkConversationMentionReviewedAction
	updateConversationNotifications *conversationaction.UpdateConversationNotificationSettingsAction
	sendServiceTextMessage          *servicesessionaction.SendServiceTextMessageAction
	sendServiceAttachmentMessage    *servicesessionaction.SendServiceAttachmentMessageAction
	claimServiceSession             *servicesessionaction.ClaimServiceSessionAction
	transferServiceSession          *servicesessionaction.TransferServiceSessionAction
	closeServiceSession             *servicesessionaction.CloseServiceSessionAction
	reopenServiceSession            *servicesessionaction.ReopenServiceSessionAction
	sendFirstAgentTextMessage       *directchataction.SendFirstAgentTextMessageAction
	sendAgentTextMessage            *directchataction.SendAgentTextMessageAction
	listServiceCopilotThreads       *directchataction.ListServiceCopilotThreadsQuery
	sendFirstServiceCopilotMessage  *directchataction.SendFirstServiceCopilotMessageAction
	sendServiceCopilotTextMessage   *directchataction.SendServiceCopilotTextMessageAction
	sendFirstDirectTextMessage      *directchataction.SendFirstDirectTextMessageAction
	findDirectConversation          *directchataction.FindDirectConversationQuery
	sendDirectTextMessage           *directchataction.SendDirectTextMessageAction
	createGroupConversation         *groupchataction.CreateGroupConversationAction
	getGroupConversation            *groupchataction.GetGroupConversationQuery
	updateGroupConversation         *groupchataction.UpdateGroupConversationAction
	addGroupConversationMembers     *groupchataction.AddGroupConversationMembersAction
	removeGroupConversationMember   *groupchataction.RemoveGroupConversationMemberAction
	transferGroupConversationOwner  *groupchataction.TransferGroupConversationOwnerAction
	leaveGroupConversation          *groupchataction.LeaveGroupConversationAction
	dissolveGroupConversation       *groupchataction.DissolveGroupConversationAction
	sendGroupTextMessage            *groupchataction.SendGroupTextMessageAction
	getAgentRunProcess              *conversationaction.GetAgentRunProcessQuery
	authorizeAgentRunStream         *conversationaction.AuthorizeAgentRunStreamQuery
}

// newConversationOps 创建会话与消息的业务实现依赖。
func newConversationOps(db *bun.DB, agentScheduler conversationaction.AgentMessageScheduler, agentCoordinator *agentrunaction.ExecuteAction, taskEnqueuer servertask.TxEnqueuer) conversationOps {
	return conversationOps{
		sendAttachmentMessage:           directchataction.NewSendAttachmentMessageAction(db, agentScheduler),
		listConversationMessages:        conversationaction.NewListConversationMessagesQuery(db),
		updateConversationUnreadMark:    conversationaction.NewUpdateConversationUnreadMarkAction(db),
		updateConversationPin:           conversationaction.NewUpdateConversationPinAction(db),
		updateConversationArchive:       conversationaction.NewUpdateConversationArchiveAction(db),
		markConversationRead:            conversationaction.NewMarkConversationReadAction(db),
		reportConversationTyping:        conversationaction.NewReportConversationTypingAction(db),
		conversationNavigation:          conversationaction.NewGetConversationNavigationStateQuery(db),
		pendingConversationMentions:     conversationaction.NewListPendingConversationMentionsQuery(db),
		reviewConversationMention:       conversationaction.NewMarkConversationMentionReviewedAction(db),
		updateConversationNotifications: conversationaction.NewUpdateConversationNotificationSettingsAction(db),
		sendServiceTextMessage:          servicesessionaction.NewSendServiceTextMessageAction(db, taskEnqueuer),
		sendServiceAttachmentMessage:    servicesessionaction.NewSendServiceAttachmentMessageAction(db, taskEnqueuer),
		claimServiceSession:             servicesessionaction.NewClaimServiceSessionAction(db, agentCoordinator, taskEnqueuer),
		transferServiceSession:          servicesessionaction.NewTransferServiceSessionAction(db, agentCoordinator, agentScheduler, taskEnqueuer),
		closeServiceSession:             servicesessionaction.NewCloseServiceSessionAction(db, agentCoordinator, taskEnqueuer),
		reopenServiceSession:            servicesessionaction.NewReopenServiceSessionAction(db),
		sendFirstAgentTextMessage:       directchataction.NewSendFirstAgentTextMessageAction(db, agentScheduler),
		sendAgentTextMessage:            directchataction.NewSendAgentTextMessageAction(db, agentScheduler),
		listServiceCopilotThreads:       directchataction.NewListServiceCopilotThreadsQuery(db),
		sendFirstServiceCopilotMessage:  directchataction.NewSendFirstServiceCopilotMessageAction(db, agentScheduler),
		sendServiceCopilotTextMessage:   directchataction.NewSendServiceCopilotTextMessageAction(db, agentScheduler),
		sendFirstDirectTextMessage:      directchataction.NewSendFirstDirectTextMessageAction(db),
		findDirectConversation:          directchataction.NewFindDirectConversationQuery(db),
		sendDirectTextMessage:           directchataction.NewSendDirectTextMessageAction(db),
		createGroupConversation:         groupchataction.NewCreateGroupConversationAction(db),
		getGroupConversation:            groupchataction.NewGetGroupConversationQuery(db),
		updateGroupConversation:         groupchataction.NewUpdateGroupConversationAction(db),
		addGroupConversationMembers:     groupchataction.NewAddGroupConversationMembersAction(db),
		removeGroupConversationMember:   groupchataction.NewRemoveGroupConversationMemberAction(db, agentCoordinator),
		transferGroupConversationOwner:  groupchataction.NewTransferGroupConversationOwnerAction(db),
		leaveGroupConversation:          groupchataction.NewLeaveGroupConversationAction(db, agentCoordinator),
		dissolveGroupConversation:       groupchataction.NewDissolveGroupConversationAction(db, agentCoordinator),
		sendGroupTextMessage:            groupchataction.NewSendGroupTextMessageAction(db, agentScheduler),
		getAgentRunProcess:              conversationaction.NewGetAgentRunProcessQuery(db),
		authorizeAgentRunStream:         conversationaction.NewAuthorizeAgentRunStreamQuery(db),
	}
}

// personalAgentConflictKeys 是个人 AI 员工无法接收新请求时的冲突提示。
var personalAgentConflictKeys = map[string]i18n.Key{
	conversationaction.ConflictReasonPersonalAgentPaused:  i18n.ErrorAgentPaused,
	conversationaction.ConflictReasonPersonalAgentUnbound: i18n.ErrorAgentDeviceUnbound,
	groupchataction.ConflictReasonPersonalAgentInactive:   i18n.ErrorAgentInactive,
}

var conversationMessageValidationKeys = map[conversationaction.ValidationCode]i18n.Key{
	conversationaction.ValidationConversationIDInvalid:       i18n.FieldConversationIDInvalid,
	conversationaction.ValidationClientMessageIDInvalid:      i18n.FieldClientMessageIDInvalid,
	conversationaction.ValidationLastReadMessageIDInvalid:    i18n.FieldClientMessageIDInvalid,
	conversationaction.ValidationReplyToMessageIDInvalid:     i18n.FieldReplyToMessageIDInvalid,
	groupchataction.ValidationMentionSubjectIDsInvalid:       i18n.FieldMentionSubjectIDsInvalid,
	servicesessionaction.ValidationMentionIdentityIDsInvalid: i18n.FieldMentionIdentityIDsInvalid,
	conversationaction.ValidationBodyRequired:                i18n.FieldMessageBodyRequired,
	conversationaction.ValidationBodyTooLong:                 i18n.FieldMessageBodyTooLong,
	servicesessionaction.ValidationTranslationInvalid:        i18n.FieldMessageTranslationInvalid,
	conversationaction.ValidationCursorInvalid:               i18n.FieldMessageCursorInvalid,
	servicesessionaction.ValidationMessageVisibilityInvalid:  i18n.FieldMessageVisibilityInvalid,
	conversationaction.ValidationFileIDInvalid:               i18n.ErrorFileNotFound,
	conversationaction.ValidationTargetIdentityIDInvalid:     i18n.FieldTargetIdentityIDInvalid,
	servicesessionaction.ValidationTargetTeamIDInvalid:       i18n.FieldTargetTeamIDInvalid,
	servicesessionaction.ValidationTransferTargetKindInvalid: i18n.FieldTransferTargetInvalid,
	groupchataction.ValidationGroupTitleTooLong:              i18n.FieldGroupTitleTooLong,
	groupchataction.ValidationGroupDescriptionTooLong:        i18n.FieldGroupDescriptionTooLong,
	groupchataction.ValidationGroupImageFileIDInvalid:        i18n.FieldGroupImageFileIDInvalid,
	groupchataction.ValidationGroupMembersRequired:           i18n.FieldGroupMembersRequired,
	groupchataction.ValidationGroupMembersTooMany:            i18n.FieldGroupMembersTooMany,
	groupchataction.ValidationGroupMemberIDsInvalid:          i18n.FieldGroupMemberIDsInvalid,
	groupchataction.ValidationGroupMemberIDInvalid:           i18n.FieldGroupMemberIDInvalid,
	groupchataction.ValidationGroupOwnerIDInvalid:            i18n.FieldGroupOwnerIDInvalid,
	conversationaction.ValidationNeighborIDInvalid:           i18n.FieldConversationPinTargetInvalid,
	conversationaction.ValidationPinPositionInvalid:          i18n.FieldConversationPinTargetInvalid,
	conversationaction.ValidationPinOrderVersionInvalid:      i18n.FieldConversationPinTargetInvalid,
}
