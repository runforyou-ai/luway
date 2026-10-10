//go:build server

package usernotification

import (
	"strings"

	"github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/common/messagepreview"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
)

// serviceAttentionBodies 是各客服处理周期提醒原因对应的通知正文。
var serviceAttentionBodies = map[domain.ServiceAttentionReason]i18n.Key{
	domain.ServiceAttentionAssigned:        i18n.NotificationServiceAssigned,
	domain.ServiceAttentionResponseOverdue: i18n.NotificationServiceResponseOverdue,
	domain.ServiceAttentionQueueWaiting:    i18n.NotificationServiceQueueWaiting,
	domain.ServiceAttentionReturned:        i18n.NotificationServiceReturned,
}

// toolDecisionBodies 是各人工介入对应的通知正文。
var toolDecisionBodies = map[domain.ToolIntervention]i18n.Key{
	domain.ToolInterventionConfirmation: i18n.NotificationToolConfirmation,
	domain.ToolInterventionApproval:     i18n.NotificationToolApproval,
}

// localize 按接收人语言渲染通知文案。
func localize(locale string, key i18n.Key, data map[string]any) string {
	return i18n.LocalizeTemplate(locale, key, data)
}

// notificationView 返回点击通知后打开会话的视图。
func notificationView(summary inbox.ConversationSummary) domain.NotificationView {
	switch {
	case summary.Service != nil:
		return domain.NotificationViewService
	case summary.Agent != nil:
		return domain.NotificationViewAgent
	case summary.Direct != nil:
		return domain.NotificationViewDirect
	}
	return domain.NotificationViewGroup
}

// conversationTitle 返回会话在接收人界面中的名称：AI 聊天为 AI 员工与聊天标题，单聊为对方名称，服务会话为客户名称或访客编号，群聊为群名或成员名称拼接。
func conversationTitle(locale string, summary inbox.ConversationSummary) string {
	unknown := localize(locale, i18n.NotificationUnknownSender, nil)
	switch {
	case summary.Agent != nil:
		return summary.Agent.AgentName + " · " + summary.Agent.Title
	case summary.Direct != nil:
		if name := strings.TrimSpace(summary.Direct.PeerName); name != "" {
			return name
		}
		return unknown
	case summary.Service != nil:
		// 客户有名称时使用名称，没有名称时显示访客编号。
		if summary.Service.RequesterName != nil && strings.TrimSpace(*summary.Service.RequesterName) != "" {
			return strings.TrimSpace(*summary.Service.RequesterName)
		}
		if summary.Service.RequesterContactNumber != nil {
			return localize(locale, i18n.NotificationVisitorNumber, map[string]any{"Number": *summary.Service.RequesterContactNumber})
		}
		return unknown
	case summary.Group != nil:
		return groupTitle(locale, *summary.Group)
	}
	return unknown
}

// groupTitle 返回群聊名称：有名称时使用名称，未命名时按除接收人外的成员名称拼接，成员较多时标明总人数。
func groupTitle(locale string, group inbox.GroupConversationSummary) string {
	if title := strings.TrimSpace(group.Title); title != "" {
		return title
	}
	if len(group.MemberPreviewNames) == 0 {
		return localize(locale, i18n.NotificationGroupUntitled, nil)
	}
	names := strings.Join(group.MemberPreviewNames, localize(locale, i18n.NotificationGroupNameSeparator, nil))
	more := group.MemberCount - 1 - len(group.MemberPreviewNames)
	if more <= 0 {
		return names
	}
	return localize(locale, i18n.NotificationGroupMemberNamesMore, map[string]any{"Names": names, "Count": group.MemberCount, "More": more})
}

// messageBody 返回新消息通知正文：附件显示文件名，运行失败与服务进度使用固定文案，其余消息使用正文摘要；内部备注与群聊标明发送者。
func messageBody(locale string, summary inbox.ConversationSummary, message inbox.AttentionMessage) string {
	var preview string
	switch {
	case message.AttachmentName != nil && *message.AttachmentName != "":
		preview = localize(locale, i18n.NotificationAttachment, map[string]any{"Name": *message.AttachmentName})
	case message.Type == domain.MessageTypeUnsupported:
		preview = localize(locale, i18n.NotificationUnsupportedMessage, nil)
	case message.Type == domain.MessageTypeAgentError:
		preview = localize(locale, i18n.NotificationAgentRunFailed, nil)
	case message.Type == domain.MessageTypeSystem:
		preview = localize(locale, i18n.NotificationServiceStatusUpdated, nil)
	default:
		agentSender := message.SenderIdentityType != nil && *message.SenderIdentityType == domain.WorkspaceIdentityTypeAgent
		preview = messagepreview.Text(message.Body, agentSender)
	}
	sender := localize(locale, i18n.NotificationUnknownSender, nil)
	if message.SenderName != nil && strings.TrimSpace(*message.SenderName) != "" {
		sender = strings.TrimSpace(*message.SenderName)
	}
	switch {
	case message.Visibility == domain.MessageVisibilityInternal:
		return localize(locale, i18n.NotificationInternalNoteBody, map[string]any{"Sender": sender, "Preview": preview})
	case summary.Group != nil:
		return localize(locale, i18n.NotificationGroupBody, map[string]any{"Sender": sender, "Preview": preview})
	}
	return preview
}
