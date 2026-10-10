//go:build server

// 会话业务操作依赖与共享错误映射。

package direct

import (
	"github.com/runforyou-ai/luway/internal/actions/agentprocess/processquery"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	translationaction "github.com/runforyou-ai/luway/internal/actions/translation"
	"github.com/runforyou-ai/luway/internal/i18n"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// conversationOps 持有会话与消息的 Action 和 Query。
type conversationOps struct {
	runSnapshots                    RunSnapshotReader
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
	getAgentRunProcess              *processquery.GetRunProcessQuery
	toolCallProcess                 *processquery.ToolCallProcessQuery
	stopLocalAgent                  *conversationaction.StopLocalAgentAction
	authorizeAgentRunStream         *processquery.AuthorizeRunStreamQuery
	// translator 校验并翻译对客回复。
	translator *translationaction.Translator
	// runCancellation 停止 Copilot 回复。
	runCancellation *agentrunaction.RunCancellation
	// files 解析文件地址。
	files *fileOps
}

// newConversationOps 创建会话与消息的业务实现依赖。
func newConversationOps(db *bun.DB, agentScheduler conversationaction.AgentMessageScheduler, runCancellation *agentrunaction.RunCancellation, taskEnqueuer servertask.TxEnqueuer, translator *translationaction.Translator, files *fileOps) *conversationOps {
	return &conversationOps{
		sendAttachmentMessage:           directchataction.NewSendAttachmentMessageAction(db, taskEnqueuer, agentScheduler),
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
		claimServiceSession:             servicesessionaction.NewClaimServiceSessionAction(db, runCancellation, taskEnqueuer),
		transferServiceSession:          servicesessionaction.NewTransferServiceSessionAction(db, runCancellation, agentScheduler, taskEnqueuer),
		closeServiceSession:             servicesessionaction.NewCloseServiceSessionAction(db, runCancellation, taskEnqueuer),
		reopenServiceSession:            servicesessionaction.NewReopenServiceSessionAction(db, taskEnqueuer),
		sendFirstAgentTextMessage:       directchataction.NewSendFirstAgentTextMessageAction(db, taskEnqueuer, agentScheduler),
		sendAgentTextMessage:            directchataction.NewSendAgentTextMessageAction(db, taskEnqueuer, agentScheduler),
		listServiceCopilotThreads:       directchataction.NewListServiceCopilotThreadsQuery(db),
		sendFirstServiceCopilotMessage:  directchataction.NewSendFirstServiceCopilotMessageAction(db, taskEnqueuer, agentScheduler),
		sendServiceCopilotTextMessage:   directchataction.NewSendServiceCopilotTextMessageAction(db, taskEnqueuer, agentScheduler),
		sendFirstDirectTextMessage:      directchataction.NewSendFirstDirectTextMessageAction(db, taskEnqueuer),
		findDirectConversation:          directchataction.NewFindDirectConversationQuery(db),
		sendDirectTextMessage:           directchataction.NewSendDirectTextMessageAction(db, taskEnqueuer),
		createGroupConversation:         groupchataction.NewCreateGroupConversationAction(db),
		getGroupConversation:            groupchataction.NewGetGroupConversationQuery(db),
		updateGroupConversation:         groupchataction.NewUpdateGroupConversationAction(db),
		addGroupConversationMembers:     groupchataction.NewAddGroupConversationMembersAction(db),
		removeGroupConversationMember:   groupchataction.NewRemoveGroupConversationMemberAction(db, taskEnqueuer, runCancellation),
		transferGroupConversationOwner:  groupchataction.NewTransferGroupConversationOwnerAction(db),
		leaveGroupConversation:          groupchataction.NewLeaveGroupConversationAction(db, taskEnqueuer, runCancellation),
		dissolveGroupConversation:       groupchataction.NewDissolveGroupConversationAction(db, runCancellation),
		sendGroupTextMessage:            groupchataction.NewSendGroupTextMessageAction(db, taskEnqueuer, agentScheduler),
		getAgentRunProcess:              processquery.NewGetRunProcessQuery(db),
		toolCallProcess:                 processquery.NewToolCallProcessQuery(db),
		stopLocalAgent:                  conversationaction.NewStopLocalAgentAction(db),
		authorizeAgentRunStream:         processquery.NewAuthorizeRunStreamQuery(db),
		translator:                      translator,
		runCancellation:                 runCancellation,
		files:                           files,
	}
}

// personalAgentConflictKeys 是个人 AI 员工无法接收新请求时的冲突提示。
var personalAgentConflictKeys = map[string]i18n.Key{
	conversationaction.ConflictReasonPersonalAgentPaused:  i18n.ErrorAgentPaused,
	conversationaction.ConflictReasonPersonalAgentUnbound: i18n.ErrorAgentComputerUnbound,
	groupchataction.ConflictReasonPersonalAgentInactive:   i18n.ErrorAgentInactive,
}

// conversationMessageValidationKeys 把消息输入校验错误码映射为本地化文案键。
var conversationMessageValidationKeys = map[conversationaction.ValidationCode]i18n.Key{
	conversationaction.ValidationConversationIDInvalid:       i18n.FieldConversationIDInvalid,
	conversationaction.ValidationClientMessageIDInvalid:      i18n.FieldClientMessageIDInvalid,
	conversationaction.ValidationReplyToMessageIDInvalid:     i18n.FieldReplyToMessageIDInvalid,
	servicesessionaction.ValidationMentionIdentityIDsInvalid: i18n.FieldMentionIdentityIDsInvalid,
	conversationaction.ValidationBodyRequired:                i18n.FieldMessageBodyRequired,
	conversationaction.ValidationBodyTooLong:                 i18n.FieldMessageBodyTooLong,
	servicesessionaction.ValidationTranslationInvalid:        i18n.FieldMessageTranslationInvalid,
	conversationaction.ValidationCursorInvalid:               i18n.FieldMessageCursorInvalid,
	conversationaction.ValidationFileIDInvalid:               i18n.ErrorFileNotFound,
	conversationaction.ValidationTargetIdentityIDInvalid:     i18n.FieldTargetIdentityIDInvalid,
	servicesessionaction.ValidationTargetTeamIDInvalid:       i18n.FieldTargetTeamIDInvalid,
	servicesessionaction.ValidationTransferTargetKindInvalid: i18n.FieldTransferTargetInvalid,
	groupchataction.ValidationGroupMembersTooMany:            i18n.FieldGroupMembersTooMany,
	groupchataction.ValidationGroupMemberIDsInvalid:          i18n.FieldGroupMemberIDsInvalid,
	conversationaction.ValidationNeighborIDInvalid:           i18n.FieldConversationPinTargetInvalid,
	conversationaction.ValidationPinPositionInvalid:          i18n.FieldConversationPinTargetInvalid,
	conversationaction.ValidationPinOrderVersionInvalid:      i18n.FieldConversationPinTargetInvalid,
}
