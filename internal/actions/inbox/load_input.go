//go:build server

package inbox

import (
	"slices"
	"strings"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/set"
	"github.com/runforyou-ai/support/str"
	"golang.org/x/text/unicode/norm"
)

// chatKinds 是一级栏聊天可筛选的会话类型，顺序用于规范化筛选值。
var chatKinds = []domain.ConversationType{domain.ConversationTypeDirect, domain.ConversationTypeGroup, domain.ConversationTypeAgent}

// normalizeChatKinds 校验会话类型属于聊天范围，并按固定顺序去重；覆盖全部类型等同不限类型。
func normalizeChatKinds(kinds []domain.ConversationType) ([]domain.ConversationType, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	if !arr.Every(kinds, func(kind domain.ConversationType) bool { return slices.Contains(chatKinds, kind) }) {
		return nil, ErrQueryInvalid
	}
	selected := set.Collect(kinds)
	if selected.Len() == len(chatKinds) {
		return nil, nil
	}
	return arr.Filter(chatKinds, selected.Has), nil
}

// normalizeQueueFilter 规范化待领取条目的队列筛选：只在待领取类型生效，指定团队时须带有效团队编号。
func normalizeQueueFilter(input LoadInput) (LoadInput, error) {
	if input.PendingKind != domain.InboxPendingKindQueue {
		input.QueueFilter, input.QueueTeamID = "", ""
		return input, nil
	}
	if input.QueueFilter == "" {
		input.QueueFilter = domain.ServiceQueueFilterAll
	}
	switch input.QueueFilter {
	case domain.ServiceQueueFilterAll, domain.ServiceQueueFilterPublic:
		if input.QueueTeamID != "" {
			return input, ErrQueryInvalid
		}
	case domain.ServiceQueueFilterTeam:
		if !str.IsUUID(input.QueueTeamID) {
			return input, ErrQueryInvalid
		}
	default:
		return input, ErrQueryInvalid
	}
	return input, nil
}

// normalizeAssigneeFilter 规范化全部范围的负责人筛选：指定企业身份时须带有效身份编号。
func normalizeAssigneeFilter(input LoadInput) (LoadInput, error) {
	if input.AssigneeFilter == "" {
		input.AssigneeFilter = domain.InboxAssigneeFilterAll
	}
	switch input.AssigneeFilter {
	case domain.InboxAssigneeFilterAll, domain.InboxAssigneeFilterUnassigned:
		if input.AssigneeIdentityID != "" {
			return input, ErrQueryInvalid
		}
	case domain.InboxAssigneeFilterIdentity:
		if !str.IsUUID(input.AssigneeIdentityID) {
			return input, ErrQueryInvalid
		}
	default:
		return input, ErrQueryInvalid
	}
	return input, nil
}

// normalizeServiceFilters 校验服务会话共用的来源与服务对象筛选。
func normalizeServiceFilters(input LoadInput) error {
	if input.ChannelID != "" && !str.IsUUID(input.ChannelID) {
		return ErrQueryInvalid
	}
	if input.Audience != "" && !slices.Contains([]domain.ServiceAudience{domain.ServiceAudienceCustomer, domain.ServiceAudienceEmployee, domain.ServiceAudiencePartner}, input.Audience) {
		return ErrQueryInvalid
	}
	// 按渠道筛选只适用于渠道来源。
	if input.Source != "" && (!slices.Contains([]domain.ServiceSource{domain.ServiceSourceChannel, domain.ServiceSourceDirect}, input.Source) ||
		(input.ChannelID != "" && input.Source != domain.ServiceSourceChannel)) {
		return ErrQueryInvalid
	}
	return nil
}

// normalizeLoadInput 规范化并校验会话列表范围与筛选；可读范围搜索不带列表范围和筛选，其余读取按范围保留适用的筛选。
func normalizeLoadInput(input LoadInput) (LoadInput, error) {
	input.Scope = domain.InboxScope(strings.TrimSpace(string(input.Scope)))
	input.PendingKind = domain.InboxPendingKind(strings.TrimSpace(string(input.PendingKind)))
	input.QueueFilter = domain.ServiceQueueFilter(strings.TrimSpace(string(input.QueueFilter)))
	input.QueueTeamID = strings.TrimSpace(input.QueueTeamID)
	input.ChannelID = strings.TrimSpace(input.ChannelID)
	input.Source = domain.ServiceSource(strings.TrimSpace(string(input.Source)))
	input.Audience = domain.ServiceAudience(strings.TrimSpace(string(input.Audience)))
	input.ServiceStatus = domain.ServiceSessionStatus(strings.TrimSpace(string(input.ServiceStatus)))
	input.AssigneeFilter = domain.InboxAssigneeFilter(strings.TrimSpace(string(input.AssigneeFilter)))
	input.AssigneeIdentityID = strings.TrimSpace(input.AssigneeIdentityID)
	input.Partition = domain.InboxPartition(strings.TrimSpace(string(input.Partition)))
	if input.Partition == "" {
		input.Partition = domain.InboxPartitionAll
	}
	if !slices.Contains([]domain.InboxPartition{domain.InboxPartitionAll, domain.InboxPartitionPinned, domain.InboxPartitionRegular, domain.InboxPartitionArchived}, input.Partition) {
		return input, ErrQueryInvalid
	}
	// 搜索词按 NFKC 规范化并合并连续空白；搜索读取完整排序或已归档的聊天，不区分置顶分区。
	input.Search = str.Squish(norm.NFKC.String(input.Search))
	if input.Search == "" {
		input.SearchRange = ""
	} else {
		if input.SearchRange == "" {
			input.SearchRange = SearchRangeList
		}
		if (input.Partition != domain.InboxPartitionAll && input.Partition != domain.InboxPartitionArchived) || (input.SearchRange != SearchRangeList && input.SearchRange != SearchRangeReadable) {
			return input, ErrQueryInvalid
		}
	}
	if input.SearchRange == SearchRangeReadable {
		if input.Partition != domain.InboxPartitionAll || input.Scope != "" || input.PendingKind != "" || input.QueueFilter != "" || input.QueueTeamID != "" || input.ChannelID != "" || input.Source != "" || input.Audience != "" ||
			input.ServiceStatus != "" || input.AssigneeFilter != "" || input.AssigneeIdentityID != "" || len(input.Kinds) > 0 {
			return input, ErrQueryInvalid
		}
		return input, nil
	}
	pending, all, chat := input.Scope == domain.InboxScopePending, input.Scope == domain.InboxScopeAll, input.Scope == domain.InboxScopeChat
	// 已归档分区只适用于聊天范围。
	if (!pending && !all && !chat) || (!chat && input.Partition == domain.InboxPartitionArchived) {
		return input, ErrQueryInvalid
	}
	if chat {
		kinds, err := normalizeChatKinds(input.Kinds)
		if err != nil {
			return input, err
		}
		return LoadInput{Cursor: input.Cursor, BeforeCursor: input.BeforeCursor, Limit: input.Limit, Partition: input.Partition, Scope: input.Scope, Kinds: kinds, Search: input.Search, SearchRange: input.SearchRange}, nil
	}
	input.Kinds = nil
	if err := normalizeServiceFilters(input); err != nil {
		return input, err
	}
	if pending {
		// 待处理按等待起点排序，不区分置顶分区；服务状态与负责人不适用。
		if input.Partition != domain.InboxPartitionAll ||
			(input.PendingKind != "" && !slices.Contains([]domain.InboxPendingKind{domain.InboxPendingKindReply, domain.InboxPendingKindQueue, domain.InboxPendingKindMention}, input.PendingKind)) {
			return input, ErrQueryInvalid
		}
		input.ServiceStatus, input.AssigneeFilter, input.AssigneeIdentityID = "", "", ""
		return normalizeQueueFilter(input)
	}
	input.PendingKind, input.QueueFilter, input.QueueTeamID = "", "", ""
	if input.ServiceStatus == "" {
		input.ServiceStatus = domain.ServiceSessionStatusOpen
	}
	if input.ServiceStatus != domain.ServiceSessionStatusOpen && input.ServiceStatus != domain.ServiceSessionStatusClosed {
		return input, ErrQueryInvalid
	}
	return normalizeAssigneeFilter(input)
}
