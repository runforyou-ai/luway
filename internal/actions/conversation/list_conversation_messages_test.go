//go:build server

package conversation

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// TestBuildConversationMessageHistoryMarksSessionOpeningMessage 验证单个处理周期也保留开始标记。
func TestBuildConversationMessageHistoryMarksSessionOpeningMessage(t *testing.T) {
	rows := makeConversationMessageRows(3)
	openingMessageID := rows[2].ID
	sequence := int64(1)
	startedAt := time.Unix(1, 0)
	status := string(domain.ServiceSessionStatusOpen)
	for index := range rows {
		rows[index].ServiceSessionOpeningMessageID = &openingMessageID
		rows[index].ServiceSessionSequence = &sequence
		rows[index].ServiceSessionStartedAt = &startedAt
		rows[index].ServiceSessionStatus = &status
	}

	slices.Reverse(rows)
	history, err := buildConversationMessageHistory(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Messages) != 3 || history.Messages[0].SessionStart == nil {
		t.Fatalf("messages = %#v", history.Messages)
	}
	if history.Messages[0].SessionStart.Sequence != sequence || history.Messages[0].SessionStart.StartedAt != startedAt || history.Messages[0].SessionStart.Status != domain.ServiceSessionStatusOpen {
		t.Fatalf("session start = %#v", history.Messages[0].SessionStart)
	}
	if history.Messages[1].SessionStart != nil || history.Messages[2].SessionStart != nil {
		t.Fatalf("non-opening session starts = %#v, %#v", history.Messages[1].SessionStart, history.Messages[2].SessionStart)
	}
}

// TestBuildConversationMessageHistoryMarksEachSessionOpeningMessage 验证多个处理周期分别标记首条消息。
func TestBuildConversationMessageHistoryMarksEachSessionOpeningMessage(t *testing.T) {
	rows := makeConversationMessageRows(4)
	firstOpeningMessageID := rows[3].ID
	secondOpeningMessageID := rows[1].ID
	firstSequence, secondSequence := int64(1), int64(2)
	firstStartedAt, secondStartedAt := time.Unix(1, 0), time.Unix(3, 0)
	closedStatus := string(domain.ServiceSessionStatusClosed)
	openStatus := string(domain.ServiceSessionStatusOpen)
	for index := 2; index < 4; index++ {
		rows[index].ServiceSessionOpeningMessageID = &firstOpeningMessageID
		rows[index].ServiceSessionSequence = &firstSequence
		rows[index].ServiceSessionStartedAt = &firstStartedAt
		rows[index].ServiceSessionStatus = &closedStatus
	}
	for index := 0; index < 2; index++ {
		rows[index].ServiceSessionOpeningMessageID = &secondOpeningMessageID
		rows[index].ServiceSessionSequence = &secondSequence
		rows[index].ServiceSessionStartedAt = &secondStartedAt
		rows[index].ServiceSessionStatus = &openStatus
	}

	slices.Reverse(rows)
	history, err := buildConversationMessageHistory(rows)
	if err != nil {
		t.Fatal(err)
	}
	if history.Messages[0].SessionStart == nil || history.Messages[0].SessionStart.Sequence != firstSequence {
		t.Fatalf("first session start = %#v", history.Messages[0].SessionStart)
	}
	if history.Messages[2].SessionStart == nil || history.Messages[2].SessionStart.Sequence != secondSequence {
		t.Fatalf("second session start = %#v", history.Messages[2].SessionStart)
	}
	if history.Messages[1].SessionStart != nil || history.Messages[3].SessionStart != nil {
		t.Fatalf("non-opening session starts = %#v, %#v", history.Messages[1].SessionStart, history.Messages[3].SessionStart)
	}
}

// makeConversationMessageRows 创建查询已经按目标方向排列的消息行。
func makeConversationMessageRows(count int) []conversationMessageRow {
	displayName := "访客"
	subjectID := "0198ddf0-a234-7f01-8d99-e3e0af0f0000"
	sourceID := "0198ddf0-a234-7f01-8d99-e3e0af0f0001"
	subjectKind := string(domain.ChatSubjectKindContact)
	rows := make([]conversationMessageRow, 0, count)
	for index := 0; index < count; index++ {
		rows = append(rows, conversationMessageRow{
			ID:   fmt.Sprintf("0198ddf0-a234-7f01-8d99-e3e0af0f%04x", index),
			Type: string(domain.MessageTypeText), Body: fmt.Sprintf("消息 %d", index),
			OriginatedAt: time.Unix(int64(count-index), 0), SourceOrder: int64(count - index),
			CreatedAt:       time.Unix(int64(count-index), 0),
			SenderSubjectID: &subjectID,
			SenderKind:      &subjectKind, SenderSourceID: &sourceID,
			SenderDisplayName: &displayName,
		})
	}
	return rows
}
