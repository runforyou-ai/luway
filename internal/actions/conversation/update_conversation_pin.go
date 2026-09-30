//go:build server

package conversation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// pinRankGap 是相邻置顶项之间预留的顺序值间隔。
const pinRankGap int64 = 1 << 20

// ConversationPinInput 定义个人置顶写入的目标位置与并发校验版本。
type ConversationPinInput struct {
	ConversationID          string
	NeighborID              string
	Position                domain.ConversationPinPosition
	ExpectedPinOrderVersion int64
	Pinned                  bool
}

// ConversationPinState 返回写入后的个人置顶事实与顺序版本。
type ConversationPinState struct {
	PinOrderVersion int64
	Pinned          bool
}

// UpdateConversationPinAction 保存当前用户的会话置顶事实与顺序。
type UpdateConversationPinAction struct{ db *bun.DB }

// NewUpdateConversationPinAction 创建个人置顶写入操作。
func NewUpdateConversationPinAction(db *bun.DB) *UpdateConversationPinAction {
	return &UpdateConversationPinAction{db: db}
}

// pinnedEntry 保存当前用户一条置顶记录的会话与顺序值。
type pinnedEntry struct {
	ConversationID string `bun:"conversation_id"`
	PinRank        int64  `bun:"pin_rank"`
}

// Execute 校验顺序版本与会话资格后写入置顶事实，并推进本人顺序版本。
func (a *UpdateConversationPinAction) Execute(ctx context.Context, identity *servermodels.Identity, input ConversationPinInput) (ConversationPinState, error) {
	input, err := normalizeConversationPinInput(input)
	if err != nil {
		return ConversationPinState{}, err
	}
	var state ConversationPinState
	err = realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 本人用户账号行已由 LockActiveUser 锁定，个人置顶顺序的写入因此串行。
		var currentVersion int64
		if err := tx.NewSelect().Table("users").Column("pin_order_version").
			Where("organization_id = ? AND id = ?", identity.Organization.ID, identity.User.ID).
			Scan(ctx, &currentVersion); err != nil {
			return fmt.Errorf("load pin order version: %w", err)
		}
		if input.ExpectedPinOrderVersion != currentVersion {
			return &ConflictError{Reason: ConflictReasonPinOrderVersionStale}
		}
		if err := authorizeConversationPin(ctx, tx, identity, input.ConversationID); err != nil {
			return err
		}
		var entries []pinnedEntry
		if err := tx.NewSelect().Table("conversation_user_states").
			Column("conversation_id", "pin_rank").
			Where("organization_id = ? AND user_id = ? AND pin_rank IS NOT NULL", identity.Organization.ID, identity.User.ID).
			OrderExpr("pin_rank ASC").Scan(ctx, &entries); err != nil {
			return fmt.Errorf("load pinned conversations: %w", err)
		}
		changed, err := a.applyPinOrder(ctx, tx, identity, input, entries)
		if err != nil {
			return err
		}
		state = ConversationPinState{Pinned: input.Pinned, PinOrderVersion: currentVersion}
		// 顺序未发生变化时不推进版本，客户端持有的版本继续有效。
		if !changed {
			return nil
		}
		state.PinOrderVersion, err = advancePinOrderVersion(ctx, tx, identity.Organization.ID, identity.User.ID)
		return err
	})
	if err != nil {
		return ConversationPinState{}, fmt.Errorf("update conversation pin: %w", err)
	}
	return state, nil
}

// normalizeConversationPinInput 校验目标会话、邻居会话与顺序版本。
func normalizeConversationPinInput(input ConversationPinInput) (ConversationPinInput, error) {
	fields := make(map[string]ValidationCode)
	conversationID, valid := common.NormalizeUUID(input.ConversationID)
	if !valid {
		fields["conversationId"] = ValidationConversationIDInvalid
	}
	input.ConversationID = conversationID
	if input.ExpectedPinOrderVersion < 0 {
		fields["expectedPinOrderVersion"] = ValidationPinOrderVersionInvalid
	}
	// before 与 after 必须带邻居，start 与 end 表示整个置顶区的首尾且不带邻居。
	switch input.Position {
	case "":
		if input.NeighborID != "" {
			fields["position"] = ValidationPinPositionInvalid
		}
	case domain.ConversationPinPositionBefore, domain.ConversationPinPositionAfter:
		neighborID, neighborValid := common.NormalizeUUID(input.NeighborID)
		if !neighborValid || neighborID == conversationID {
			fields["neighborId"] = ValidationNeighborIDInvalid
		}
		input.NeighborID = neighborID
	case domain.ConversationPinPositionStart, domain.ConversationPinPositionEnd:
		if input.NeighborID != "" {
			fields["neighborId"] = ValidationNeighborIDInvalid
		}
	default:
		fields["position"] = ValidationPinPositionInvalid
	}
	// 取消置顶不接受位置指令。
	if !input.Pinned && (input.Position != "" || input.NeighborID != "") {
		fields["position"] = ValidationPinPositionInvalid
	}
	if len(fields) > 0 {
		return ConversationPinInput{}, &ValidationError{Fields: fields}
	}
	return input, nil
}

// authorizeConversationPin 按会话类型校验当前用户的阅读资格。
func authorizeConversationPin(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, conversationID string) error {
	var conversationType domain.ConversationType
	err := tx.NewSelect().TableExpr("conversations AS cv").Column("cv.type").
		Where("cv.organization_id = ? AND cv.id = ?", identity.Organization.ID, conversationID).Scan(ctx, &conversationType)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConversationNotFound
	}
	if err != nil {
		return fmt.Errorf("load conversation pin type: %w", err)
	}
	served, err := tx.NewSelect().Model((*servermodels.ServiceConversation)(nil)).
		Where("svc.organization_id = ? AND svc.conversation_id = ?", identity.Organization.ID, conversationID).Exists(ctx)
	if err != nil {
		return fmt.Errorf("check service conversation pin access: %w", err)
	}
	if conversationType == domain.ConversationTypeChannel || served {
		return AuthorizeConversationHistory(ctx, tx, identity, conversationID)
	}
	_, err = chatstate.LockMember(ctx, tx, identity, conversationID)
	return err
}

// applyPinOrder 按目标位置重排个人置顶顺序，并返回是否产生实际变化。
func (a *UpdateConversationPinAction) applyPinOrder(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, input ConversationPinInput, entries []pinnedEntry) (bool, error) {
	ranks := make(map[string]int64, len(entries))
	order := make([]string, 0, len(entries)+1)
	// 目标移出当前顺序后再定位落点，原下标用于判断是否回到同一位置。
	originalIndex := -1
	for _, entry := range entries {
		ranks[entry.ConversationID] = entry.PinRank
		if entry.ConversationID == input.ConversationID {
			originalIndex = len(order)
			continue
		}
		order = append(order, entry.ConversationID)
	}
	alreadyPinned := originalIndex >= 0
	if !input.Pinned {
		if !alreadyPinned {
			return false, nil
		}
		_, err := ClearConversationPin(ctx, tx, identity.Organization.ID, identity.User.ID, input.ConversationID)
		return true, err
	}
	// 已置顶会话在没有位置指令时保持原位，重复置顶不打乱顺序。
	if alreadyPinned && input.Position == "" {
		return false, nil
	}
	target := len(order)
	switch input.Position {
	case domain.ConversationPinPositionStart:
		target = 0
	case domain.ConversationPinPositionBefore, domain.ConversationPinPositionAfter:
		neighbor := -1
		for index, id := range order {
			if id == input.NeighborID {
				neighbor = index
				break
			}
		}
		if neighbor < 0 {
			return false, &ConflictError{Reason: ConflictReasonPinNeighborNotPinned}
		}
		target = neighbor
		if input.Position == domain.ConversationPinPositionAfter {
			target = neighbor + 1
		}
	}
	// 落点与原位置相同时不产生写入，也不推进顺序版本。
	if alreadyPinned && target == originalIndex {
		return false, nil
	}
	order = append(order[:target], append([]string{input.ConversationID}, order[target:]...)...)
	if rank, ok := insertRank(order, ranks, target); ok {
		return true, writeConversationPinRank(ctx, tx, identity.Organization.ID, identity.User.ID, input.ConversationID, rank)
	}
	return true, renumberPinRanks(ctx, tx, identity, order, input.ConversationID)
}

// insertRank 在相邻顺序值之间取间隔中点，间隔耗尽时返回不可用。
func insertRank(order []string, ranks map[string]int64, target int) (int64, bool) {
	var previous, next *int64
	if target > 0 {
		rank := ranks[order[target-1]]
		previous = &rank
	}
	if target+1 < len(order) {
		rank := ranks[order[target+1]]
		next = &rank
	}
	switch {
	case previous == nil && next == nil:
		return pinRankGap, true
	case previous == nil:
		return *next - pinRankGap, true
	case next == nil:
		if *previous > *previous+pinRankGap {
			return 0, false
		}
		return *previous + pinRankGap, true
	default:
		middle := *previous + (*next-*previous)/2
		return middle, middle != *previous
	}
}

// renumberPinRanks 按目标顺序整区重写顺序值，唯一约束延迟到提交时校验。
// 除本次写入的目标外只更新仍在置顶区的记录，读取顺序集合之后被并发失权清除的置顶不会写回。
func renumberPinRanks(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, order []string, targetID string) error {
	for index, id := range order {
		rank := int64(index+1) * pinRankGap
		if id == targetID {
			if err := writeConversationPinRank(ctx, tx, identity.Organization.ID, identity.User.ID, id, rank); err != nil {
				return err
			}
			continue
		}
		if err := notifyConversationStateWrite(ctx, tx.NewUpdate().Model((*servermodels.ConversationUserState)(nil)).
			Set("pin_rank = ?", rank).Set("version = version + 1").Set("updated_at = now()").
			Where("organization_id = ? AND conversation_id = ? AND user_id = ? AND pin_rank IS NOT NULL",
				identity.Organization.ID, id, identity.User.ID).
			Returning("version"), identity.Organization.ID, id, identity.User.ID); err != nil {
			return fmt.Errorf("renumber conversation pin rank: %w", err)
		}
	}
	return nil
}

// writeConversationPinRank 写入指定会话的个人顺序值并推进个人状态版本，置顶的会话同时取消归档。
func writeConversationPinRank(ctx context.Context, tx bun.Tx, organizationID, userID, conversationID string, rank int64) error {
	state := &servermodels.ConversationUserState{
		OrganizationID: organizationID, ConversationID: conversationID,
		UserID: userID, PinRank: &rank, Version: 1,
	}
	if err := notifyConversationStateWrite(ctx, tx.NewInsert().Model(state).
		Column("organization_id", "conversation_id", "user_id", "pin_rank", "version").
		On("CONFLICT (organization_id, conversation_id, user_id) DO UPDATE").
		Set("pin_rank = EXCLUDED.pin_rank").Set("archived_at = NULL").Set("version = cus.version + 1").Set("updated_at = now()").
		Where("cus.pin_rank IS DISTINCT FROM EXCLUDED.pin_rank").
		Returning("version"), organizationID, conversationID, userID); err != nil {
		return fmt.Errorf("save conversation pin rank: %w", err)
	}
	return nil
}

// ClearConversationPin 清除指定会话的个人置顶并推进个人状态版本，返回是否存在需要清除的置顶。
// 失权清理不推进本人置顶顺序版本，因此不写他人的用户账号行：顺序版本只由本人的置顶命令推进，
// 失权由同一受众的会话失权通知触发整区重读。
func ClearConversationPin(ctx context.Context, tx bun.Tx, organizationID, userID, conversationID string) (bool, error) {
	var version int64
	err := tx.NewUpdate().Model((*servermodels.ConversationUserState)(nil)).
		Set("pin_rank = NULL").Set("version = version + 1").Set("updated_at = now()").
		Where("organization_id = ? AND conversation_id = ? AND user_id = ? AND pin_rank IS NOT NULL", organizationID, conversationID, userID).
		Returning("version").Scan(ctx, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("clear conversation pin: %w", err)
	}
	realtime.Notify(ctx, realtime.UserConversationStateChanged(organizationID, userID, conversationID, version))
	return true, nil
}

// advancePinOrderVersion 推进本人置顶顺序版本并登记顺序变更通知。
func advancePinOrderVersion(ctx context.Context, tx bun.Tx, organizationID, userID string) (int64, error) {
	var version int64
	if err := tx.NewUpdate().Model((*servermodels.User)(nil)).
		Set("pin_order_version = pin_order_version + 1").Set("updated_at = now()").
		Where("organization_id = ? AND id = ?", organizationID, userID).
		Returning("pin_order_version").Scan(ctx, &version); err != nil {
		return 0, fmt.Errorf("advance pin order version: %w", err)
	}
	realtime.Notify(ctx, realtime.UserPinOrderChanged(organizationID, userID, version))
	return version, nil
}
