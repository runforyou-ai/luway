//go:build server

package servicesummary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// HandoffSummaryInput 定义一次交接摘要任务；MessageID 为转人工系统事件，周期已有更新的转人工时任务不再生效。
type HandoffSummaryInput struct {
	OrganizationID   string `json:"organizationId"`
	ServiceSessionID string `json:"serviceSessionId"`
	MessageID        string `json:"messageId"`
}

// MarkHandedOff 在调用方持有会话锁的事务中记录最近一次转人工事件并清空旧的交接摘要，设置了小结模型时投递交接摘要任务。
func MarkHandedOff(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, session *servermodels.ServiceSession, messageID string) error {
	if _, err := db.NewUpdate().Model(session).
		Set("handoff_message_id = ?", messageID).
		Set("handoff_summary = NULL").
		WherePK().Where("organization_id = ?", session.OrganizationID).
		Exec(ctx); err != nil {
		return fmt.Errorf("record service session handoff: %w", err)
	}
	session.HandoffMessageID, session.HandoffSummary = &messageID, nil
	settings, err := customerservice.LoadServiceSummarySettings(ctx, db, session.OrganizationID)
	if err != nil {
		return err
	}
	if settings.SummaryModelID == nil {
		return nil
	}
	return enqueue(ctx, db, enqueuer, session.OrganizationID, HandoffSummaryActionName, HandoffSummaryInput{
		OrganizationID: session.OrganizationID, ServiceSessionID: session.ID, MessageID: messageID,
	})
}

// HandoffSummary 按转人工事件之前的沟通记录与转人工原因生成交接摘要；周期已关闭或已有更新的转人工时不写入。
func (w *Worker) HandoffSummary(ctx context.Context, input HandoffSummaryInput) error {
	event := &servermodels.Message{}
	if err := w.db.NewSelect().Model(event).
		Column("message_seq", "system_event_payload").
		Where("organization_id = ? AND id = ? AND service_session_id = ?", input.OrganizationID, input.MessageID, input.ServiceSessionID).
		Scan(ctx); err != nil {
		return fmt.Errorf("load service session handoff event: %w", err)
	}
	var handedOff domain.ServiceSessionHandedOffEvent
	if err := json.Unmarshal(event.SystemEventPayload, &handedOff); err != nil {
		return fmt.Errorf("decode service session handoff event: %w", err)
	}
	settings, err := customerservice.LoadServiceSummarySettings(ctx, w.db, input.OrganizationID)
	if err != nil {
		return err
	}
	model, err := customerservice.LoadModel(ctx, w.db, input.OrganizationID, settings.SummaryModelID, domain.AIModelUsageSummary)
	if err != nil || model == nil {
		return err
	}
	transcript, err := LoadTranscript(ctx, w.db, input.OrganizationID, input.ServiceSessionID, event.MessageSeq)
	if err != nil {
		return err
	}
	materials, err := transcriptInput(fitTranscript(transcript, model.ContextWindow))
	if err != nil {
		return err
	}
	reason, _ := json.Marshal(struct {
		Reason     domain.AgentHandoffReason `json:"reason"`
		ReasonText string                    `json:"reasonText,omitempty"`
	}{Reason: handedOff.Reason, ReasonText: handedOff.ReasonText})
	instruction := "AI 客服刚把这次客户咨询转交给真人客服，你负责为承接的真人客服写交接摘要，让对方不必翻看记录就能接手。\n" +
		"- request：客户要什么，一句话写明诉求和关键事实，例如订单号、产品或时间。\n" +
		"- progress：AI 已经做了什么、告诉了客户什么；没有做任何处理时写明尚未处理。\n" +
		"- blocker：卡在哪里、需要真人做什么；参考转人工原因，其中 knowledge_gap 表示知识不足，customer_requested 表示客户要求真人，needs_human_judgment 表示需要人工判断，complaint 表示投诉，其余为系统原因。\n" +
		"- 只依据沟通记录和转人工原因，不编造信息；每项一到两句话。\n" +
		"- 使用" + localeLanguage(settings.Locale) + "书写。\n" +
		`- 只输出一个 JSON 对象，格式为 {"request":"","progress":"","blocker":""}，不输出 JSON 以外的任何内容。`
	generateCtx, cancel := context.WithTimeout(ctx, summaryTimeout)
	defer cancel()
	response, err := w.caller.CallOnce(generateCtx, agentruntime.SingleCallRequest{
		Instruction: instruction, Model: model.ModelConfig(), Input: materials + "\n\n转人工原因：" + string(reason),
	})
	if err != nil {
		return fmt.Errorf("generate handoff summary: %w", err)
	}
	var summary domain.HandoffSummary
	if err := agentruntime.DecodeJSONObject(response.Text, &summary); err != nil {
		return fmt.Errorf("decode handoff summary: %w", err)
	}
	summary = domain.HandoffSummary{Request: strings.TrimSpace(summary.Request), Progress: strings.TrimSpace(summary.Progress), Blocker: strings.TrimSpace(summary.Blocker)}
	if summary.Request == "" {
		return errors.New("handoff summary request is empty")
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("encode handoff summary: %w", err)
	}
	return realtime.RunInTx(ctx, w.db, func(ctx context.Context, tx bun.Tx) error {
		locked, err := chatstate.LockServiceSessionByID(ctx, tx, input.OrganizationID, input.ServiceSessionID)
		if err != nil {
			return err
		}
		conversation, session := locked.Conversation, locked.Session
		if domain.ServiceSessionStatus(session.Status) != domain.ServiceSessionStatusOpen || session.HandoffMessageID == nil || *session.HandoffMessageID != input.MessageID {
			return nil
		}
		if _, err := tx.NewUpdate().Model(session).
			Set("handoff_summary = ?::jsonb", string(encoded)).
			WherePK().Where("organization_id = ?", input.OrganizationID).
			Exec(ctx); err != nil {
			return fmt.Errorf("save handoff summary: %w", err)
		}
		slog.Info("交接摘要已生成", "organization_id", input.OrganizationID, "service_session_id", input.ServiceSessionID, "message_id", input.MessageID)
		return chatstate.TouchConversation(ctx, tx, conversation, domain.ConversationChangeService)
	})
}
