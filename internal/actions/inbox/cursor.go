//go:build server

package inbox

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

const inboxSortVersion = 2

// inboxOrder 表示列表的显示顺序。
type inboxOrder int

const (
	// inboxOrderActivity 按最近活动时间倒序，空活动时间排在最后。
	inboxOrderActivity inboxOrder = iota
	// inboxOrderPinned 按个人置顶顺序值升序。
	inboxOrderPinned
	// inboxOrderWaiting 按等待起点正序，等待最久的排在最前。
	inboxOrderWaiting
)

// order 返回当前范围与分区的显示顺序。
func (input LoadInput) order() inboxOrder {
	if input.Partition == domain.InboxPartitionPinned {
		return inboxOrderPinned
	}
	if input.Scope == domain.InboxScopePending {
		return inboxOrderWaiting
	}
	return inboxOrderActivity
}

// compareInboxPoints 按显示顺序比较位置，结果为负表示排在更前面。
func compareInboxPoints(order inboxOrder, left, right inboxCursorPoint) int {
	switch order {
	case inboxOrderPinned:
		if result := comparePinRanks(left.PinRank, right.PinRank); result != 0 {
			return result
		}
		return strings.Compare(left.ID, right.ID)
	case inboxOrderWaiting:
		if left.WaitingSince != nil && right.WaitingSince != nil {
			if result := left.WaitingSince.Compare(*right.WaitingSince); result != 0 {
				return result
			}
		}
		return strings.Compare(left.ID, right.ID)
	}
	if left.LastActivityAt == nil && right.LastActivityAt != nil {
		return 1
	}
	if left.LastActivityAt != nil && right.LastActivityAt == nil {
		return -1
	}
	if left.LastActivityAt != nil && right.LastActivityAt != nil {
		if result := left.LastActivityAt.Compare(*right.LastActivityAt); result != 0 {
			return -result
		}
	}
	return -strings.Compare(left.ID, right.ID)
}

// comparePinRanks 按顺序值升序比较，空顺序值排在最后。
func comparePinRanks(left, right *int64) int {
	if left == nil && right == nil {
		return 0
	}
	if left == nil {
		return 1
	}
	if right == nil {
		return -1
	}
	return cmp.Compare(*left, *right)
}

// inboxCursorPoint 保存数据库原始活动时间、个人置顶顺序值与待处理等待起点，空活动时间使用独立的编号边界。
type inboxCursorPoint struct {
	ID             string     `json:"id" bun:"id"`
	LastActivityAt *time.Time `json:"lastActivityAt" bun:"last_activity_at"`
	PinRank        *int64     `json:"pinRank" bun:"pin_rank"`
	WaitingSince   *time.Time `json:"waitingSince,omitempty" bun:"waiting_since"`
	// PendingKind 与 Mentioned 是待处理条目的类型和是否有未回应的提醒，只用于摘要，不参与排序与游标。
	PendingKind domain.InboxPendingKind `json:"-" bun:"pending_kind"`
	Mentioned   bool                    `json:"-" bun:"mentioned"`
}

// pending 返回待处理条目的摘要，非待处理候选返回空。
func (point inboxCursorPoint) pending() *PendingSummary {
	if point.PendingKind == "" || point.WaitingSince == nil {
		return nil
	}
	return &PendingSummary{Kind: point.PendingKind, Since: *point.WaitingSince, Mentioned: point.Mentioned}
}

// inboxCursor 将排序边界绑定到当前企业、用户、规范化筛选、搜索词和置顶分区。
type inboxCursor struct {
	inboxCursorPoint
	Version            int                         `json:"version"`
	OrganizationID     string                      `json:"organizationId"`
	UserID             string                      `json:"userId"`
	Partition          domain.InboxPartition       `json:"partition"`
	PinOrderVersion    int64                       `json:"pinOrderVersion"`
	Scope              domain.InboxScope           `json:"scope"`
	PendingKind        domain.InboxPendingKind     `json:"pendingKind"`
	QueueFilter        domain.ServiceQueueFilter   `json:"queueFilter"`
	QueueTeamID        string                      `json:"queueTeamId"`
	ChannelID          string                      `json:"channelId"`
	Source             domain.ServiceSource        `json:"source"`
	Audience           domain.ServiceAudience      `json:"audience"`
	ServiceStatus      domain.ServiceSessionStatus `json:"serviceStatus"`
	AssigneeFilter     domain.InboxAssigneeFilter  `json:"assigneeFilter"`
	AssigneeIdentityID string                      `json:"assigneeIdentityId"`
	Kinds              []domain.ConversationType   `json:"kinds"`
	Search             string                      `json:"search"`
	SearchRange        SearchRange                 `json:"searchRange"`
}

// encodeInboxCursor 编码原始排序边界、身份范围、搜索词、分区与排序版本；置顶区另外绑定个人顺序版本。
func encodeInboxCursor(identity *servermodels.Identity, input LoadInput, pinOrderVersion int64, point inboxCursorPoint) (string, error) {
	if input.Partition != domain.InboxPartitionPinned {
		pinOrderVersion = 0
	}
	data, err := json.Marshal(inboxCursor{
		inboxCursorPoint: point, Version: inboxSortVersion,
		OrganizationID: identity.Organization.ID, UserID: identity.User.ID,
		Partition: input.Partition, PinOrderVersion: pinOrderVersion,
		Scope: input.Scope, PendingKind: input.PendingKind, QueueFilter: input.QueueFilter, QueueTeamID: input.QueueTeamID,
		ChannelID: input.ChannelID, Source: input.Source, Audience: input.Audience, ServiceStatus: input.ServiceStatus,
		AssigneeFilter: input.AssigneeFilter, AssigneeIdentityID: input.AssigneeIdentityID, Kinds: input.Kinds,
		Search: input.Search, SearchRange: input.SearchRange,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// decodeInboxCursor 拒绝跨查询、跨用户、跨企业及未知排序版本的游标。
func decodeInboxCursor(value string, identity *servermodels.Identity, input LoadInput) (*inboxCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, ErrCursorInvalid
	}
	var cursor inboxCursor
	if json.Unmarshal(data, &cursor) != nil || cursor.Version != inboxSortVersion ||
		cursor.OrganizationID != identity.Organization.ID || cursor.UserID != identity.User.ID ||
		cursor.Partition != input.Partition ||
		cursor.Scope != input.Scope || cursor.PendingKind != input.PendingKind ||
		cursor.QueueFilter != input.QueueFilter || cursor.QueueTeamID != input.QueueTeamID ||
		cursor.ChannelID != input.ChannelID || cursor.Source != input.Source || cursor.Audience != input.Audience || cursor.ServiceStatus != input.ServiceStatus ||
		cursor.AssigneeFilter != input.AssigneeFilter || cursor.AssigneeIdentityID != input.AssigneeIdentityID || !slices.Equal(cursor.Kinds, input.Kinds) ||
		(input.order() == inboxOrderWaiting && cursor.WaitingSince == nil) ||
		cursor.Search != input.Search || cursor.SearchRange != input.SearchRange ||
		!common.ValidUUID(cursor.ID) {
		return nil, ErrCursorInvalid
	}
	// 数据库 UUID 返回小写，统一比较与锚点匹配所用的编号。
	cursor.ID = strings.ToLower(cursor.ID)
	return &cursor, nil
}
