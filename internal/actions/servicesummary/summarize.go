//go:build server

package servicesummary

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/actions/knowledgegap"
	"github.com/runforyou-ai/luway/internal/actions/servicecategory"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/decision"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

const (
	// yesThreshold 是是否题判为成立的最低概率。
	yesThreshold = 0.7
	// noThreshold 是是否题判为不成立的最高概率。
	noThreshold = 0.3
	// categoryThreshold 是咨询分类选项被采用的最低概率。
	categoryThreshold = 0.6
	// noCategoryOption 是咨询分类单选题中表示不属于任何分类的选项键。
	noCategoryOption = "none"
	// summaryTimeout 限制一次小结生成中判断与模型调用的总时长。
	summaryTimeout = 90 * time.Second
)

// SummarizeInput 定义一次周期小结任务；ClosedAt 与周期当前关闭时间不一致时任务不再生效。
type SummarizeInput struct {
	OrganizationID   string    `json:"organizationId"`
	ServiceSessionID string    `json:"serviceSessionId"`
	ClosedAt         time.Time `json:"closedAt"`
}

// MarkClosed 在调用方持有会话锁的 realtime.RunInTx 事务中通知 AI 表现变化，为刚关闭的周期登记或重新起草待补知识，在设置了判断模型时投递质检任务，并在设置了判断模型或小结模型时投递联系人资料抽取任务、准备小结：客服修改过的小结保持不变；AI 解决时是否解决记为已解决；需要生成小结时标记等待生成并投递任务。
func MarkClosed(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, session *servermodels.ServiceSession, reason domain.ServiceSessionCloseReason) error {
	realtime.Notify(ctx, realtime.ServiceInboxReportsChanged(session.OrganizationID))
	if err := knowledgegap.RecordClosed(ctx, db, enqueuer, session); err != nil {
		return err
	}
	settings, err := customerservice.LoadServiceSummarySettings(ctx, db, session.OrganizationID)
	if err != nil {
		return err
	}
	if settings.DecisionModelID != nil || settings.SummaryModelID != nil {
		if err := enqueue(ctx, db, enqueuer, session.OrganizationID, ExtractContactProfileActionName, ExtractContactProfileInput{
			OrganizationID: session.OrganizationID, ServiceSessionID: session.ID, ClosedAt: *session.ClosedAt,
		}); err != nil {
			return err
		}
	}
	if settings.DecisionModelID != nil {
		if err := enqueue(ctx, db, enqueuer, session.OrganizationID, ReviewActionName, ReviewInput{
			OrganizationID: session.OrganizationID, ServiceSessionID: session.ID, ClosedAt: *session.ClosedAt,
		}); err != nil {
			return err
		}
	}
	if session.SummaryEditedByID != nil {
		return nil
	}
	var resolved *bool
	if reason == domain.ServiceSessionCloseAIResolved {
		resolved = new(true)
	}
	var status *string
	if settings.DecisionModelID != nil || settings.SummaryModelID != nil {
		status = new(string(domain.ServiceSessionSummaryPending))
	}
	if _, err := db.NewUpdate().Model(session).
		Set("summary_status = ?", status).
		Set("summary = NULL").
		Set("resolved = ?", resolved).
		WherePK().Where("organization_id = ?", session.OrganizationID).
		Exec(ctx); err != nil {
		return fmt.Errorf("prepare service session summary: %w", err)
	}
	session.SummaryStatus, session.Summary, session.Resolved = status, nil, resolved
	if status == nil {
		return nil
	}
	return enqueue(ctx, db, enqueuer, session.OrganizationID, SummarizeActionName, SummarizeInput{
		OrganizationID: session.OrganizationID, ServiceSessionID: session.ID, ClosedAt: *session.ClosedAt,
	})
}

// MarkReopened 在调用方持有会话锁的 realtime.RunInTx 事务中清除重新打开周期的 AI 小结、交接摘要与质检结果并通知 AI 表现变化，客服修改过的小结保持不变。
func MarkReopened(ctx context.Context, db bun.IDB, session *servermodels.ServiceSession) error {
	realtime.Notify(ctx, realtime.ServiceInboxReportsChanged(session.OrganizationID))
	query := db.NewUpdate().Model(session).
		Set("handoff_message_id = NULL").
		Set("handoff_summary = NULL").
		WherePK().Where("organization_id = ?", session.OrganizationID)
	if session.SummaryEditedByID == nil {
		query = query.Set("summary_status = NULL").Set("summary = NULL").Set("resolved = NULL")
		session.SummaryStatus, session.Summary, session.Resolved = nil, nil, nil
	}
	if _, err := query.Exec(ctx); err != nil {
		return fmt.Errorf("clear reopened service session summary: %w", err)
	}
	if _, err := db.NewDelete().Model((*servermodels.ServiceSessionReview)(nil)).
		Where("organization_id = ? AND service_session_id = ?", session.OrganizationID, session.ID).
		Exec(ctx); err != nil {
		return fmt.Errorf("clear reopened service session review: %w", err)
	}
	session.HandoffMessageID, session.HandoffSummary = nil, nil
	return nil
}

// summaryResult 是一次小结生成得到的标注与正文；categoryID 为空且不清空时保留周期已有的咨询分类。
type summaryResult struct {
	status        domain.ServiceSessionSummaryStatus
	summary       *string
	resolved      *bool
	categoryID    *string
	clearCategory bool // 为 true 时清空周期已有的咨询分类。
}

// Summarize 为已关闭且等待生成小结的周期判断实质诉求、咨询分类与是否解决并生成正文；周期已重开、再次关闭或被客服修改时不写入。
func (w *Worker) Summarize(ctx context.Context, input SummarizeInput) error {
	session := &servermodels.ServiceSession{}
	if err := w.db.NewSelect().Model(session).
		Where("ss.organization_id = ? AND ss.id = ?", input.OrganizationID, input.ServiceSessionID).
		Scan(ctx); err != nil {
		return fmt.Errorf("load summarizing service session: %w", err)
	}
	if !summaryPending(session, input.ClosedAt) {
		return nil
	}
	settings, err := customerservice.LoadServiceSummarySettings(ctx, w.db, input.OrganizationID)
	if err != nil {
		return err
	}
	decisionModel, err := customerservice.LoadModel(ctx, w.db, input.OrganizationID, settings.DecisionModelID, domain.AIModelUsageDecision)
	if err != nil {
		return err
	}
	summaryModel, err := customerservice.LoadModel(ctx, w.db, input.OrganizationID, settings.SummaryModelID, domain.AIModelUsageSummary)
	if err != nil {
		return err
	}
	transcript, err := LoadTranscript(ctx, w.db, input.OrganizationID, input.ServiceSessionID, math.MaxInt64)
	if err != nil {
		return err
	}
	generateCtx, cancel := context.WithTimeout(ctx, summaryTimeout)
	defer cancel()
	result, err := w.generateSummary(generateCtx, session, settings.Locale, decisionModel, summaryModel, transcript)
	if err != nil {
		return err
	}
	return realtime.RunInTx(ctx, w.db, func(ctx context.Context, tx bun.Tx) error {
		lockedSession, err := chatstate.LockServiceSessionByID(ctx, tx, input.OrganizationID, input.ServiceSessionID)
		if err != nil {
			return err
		}
		conversation, locked := lockedSession.Conversation, lockedSession.Session
		if !summaryPending(locked, input.ClosedAt) {
			return nil
		}
		query := tx.NewUpdate().Model(locked).
			Set("summary_status = ?", result.status).
			Set("summary = ?", result.summary).
			WherePK().Where("organization_id = ?", input.OrganizationID)
		// 结束方式已决定是否解决时保持关闭时写入的结果。
		if domain.ServiceSessionCloseReason(*locked.CloseReason) == domain.ServiceSessionCloseManual {
			query = query.Set("resolved = ?", result.resolved)
		}
		if result.clearCategory {
			query = query.Set("category_id = NULL")
		}
		if result.categoryID != nil {
			category, err := servicecategory.FindActive(ctx, tx, input.OrganizationID, *result.categoryID)
			if err != nil {
				return err
			}
			if category != nil {
				query = query.Set("category_id = ?", category.ID)
			}
		}
		if _, err := query.Exec(ctx); err != nil {
			return fmt.Errorf("save service session summary: %w", err)
		}
		realtime.Notify(ctx, realtime.ServiceInboxReportsChanged(input.OrganizationID))
		slog.Info("客服周期小结已生成", "organization_id", input.OrganizationID, "service_session_id", input.ServiceSessionID,
			"status", result.status, "resolved", result.resolved, "category_id", result.categoryID)
		return chatstate.TouchConversation(ctx, tx, conversation, domain.ConversationChangeService)
	})
}

// FinalizeSummarizeFailure 在小结任务耗尽重试后把仍在等待的小结标记为生成失败。
func (w *Worker) FinalizeSummarizeFailure(ctx context.Context, input SummarizeInput, runErr error) error {
	return realtime.RunInTx(ctx, w.db, func(ctx context.Context, tx bun.Tx) error {
		locked, err := chatstate.LockServiceSessionByID(ctx, tx, input.OrganizationID, input.ServiceSessionID)
		if err != nil {
			return err
		}
		conversation, session := locked.Conversation, locked.Session
		if !summaryPending(session, input.ClosedAt) {
			return nil
		}
		if _, err := tx.NewUpdate().Model(session).
			Set("summary_status = ?", domain.ServiceSessionSummaryFailed).
			WherePK().Where("organization_id = ?", input.OrganizationID).
			Exec(ctx); err != nil {
			return fmt.Errorf("mark service session summary failed: %w", err)
		}
		slog.Warn("客服周期小结生成失败", "organization_id", input.OrganizationID, "service_session_id", input.ServiceSessionID, "error", runErr)
		return chatstate.TouchConversation(ctx, tx, conversation, domain.ConversationChangeService)
	})
}

// summaryPending 判断周期仍处于本次关闭、等待生成且未被客服修改的状态；关闭时间按数据库的微秒精度比较。
func summaryPending(session *servermodels.ServiceSession, closedAt time.Time) bool {
	return stillClosedAt(session, closedAt) && session.CloseReason != nil &&
		session.SummaryStatus != nil && domain.ServiceSessionSummaryStatus(*session.SummaryStatus) == domain.ServiceSessionSummaryPending &&
		session.SummaryEditedByID == nil
}

// generateSummary 先由判断模型标注实质诉求、咨询分类与是否解决，有实质诉求时再由小结模型生成正文；没有客户发言的周期直接记为无实质诉求。
func (w *Worker) generateSummary(ctx context.Context, session *servermodels.ServiceSession, locale domain.Locale,
	decisionModel, summaryModel *aimodel.Model, transcript []TranscriptEntry) (summaryResult, error) {
	// 周期内没有客户发言时不需要模型判断。
	customerSpoke := slices.ContainsFunc(transcript, func(entry TranscriptEntry) bool { return entry.Sender == "customer" })
	if !customerSpoke {
		return summaryResult{status: domain.ServiceSessionSummaryNoRequest, clearCategory: true}, nil
	}
	result := summaryResult{status: domain.ServiceSessionSummaryReady}
	reason := domain.ServiceSessionCloseReason(*session.CloseReason)
	if decisionModel != nil {
		categories, err := servicecategory.Active(ctx, w.db, session.OrganizationID)
		if err != nil {
			return summaryResult{}, err
		}
		questions := make(map[string]decision.Question, 3)
		// 客户确认解决的周期必然有实质诉求。
		if reason != domain.ServiceSessionCloseAIResolved {
			questions["request"] = decision.Question{Kind: decision.KindYesNo,
				Instructions: "客户在这段沟通中提出了需要企业解答或处理的问题、诉求或反馈；只打招呼、测试、发送无意义内容或始终没有说明来意都不算。"}
		}
		if reason == domain.ServiceSessionCloseManual {
			questions["resolved"] = decision.Question{Kind: decision.KindYesNo,
				Instructions: "客户在这段沟通中提出的问题或诉求已经得到解决或明确答复。"}
		}
		if len(categories) > 0 {
			options := make([]decision.Option, 0, len(categories)+1)
			for _, category := range categories {
				description := category.Name
				if text := strings.TrimSpace(category.Description); text != "" {
					description += "：" + text
				}
				options = append(options, decision.Option{Key: category.ID, Description: description})
			}
			options = append(options, decision.Option{Key: noCategoryOption, Description: "不属于以上任何分类"})
			questions["category"] = decision.Question{Kind: decision.KindChoice, Instructions: "选出最符合客户在这段沟通中的诉求的咨询分类。", Options: options}
		}
		if len(questions) > 0 {
			answers, err := w.invoker.Decide(ctx, sessionScope(session.OrganizationID, session.ID), decisionModel, decisionState(transcript, decisionModel.ContextWindow), questions)
			if err != nil {
				return summaryResult{}, fmt.Errorf("decide service session summary: %w", err)
			}
			if answer, ok := answers["request"]; ok && answer.Probability <= noThreshold {
				return summaryResult{status: domain.ServiceSessionSummaryNoRequest, clearCategory: true}, nil
			}
			if answer, ok := answers["resolved"]; ok {
				switch {
				case answer.Probability >= yesThreshold:
					result.resolved = new(true)
				case answer.Probability <= noThreshold:
					result.resolved = new(false)
				}
			}
			// 明确判为不属于任何分类时清空转人工写入的分类，概率不足时保留。
			if answer, ok := answers["category"]; ok && answer.Probabilities[answer.Choice] >= categoryThreshold {
				if answer.Choice == noCategoryOption {
					result.clearCategory = true
				} else {
					result.categoryID = &answer.Choice
				}
			}
		}
	}
	if summaryModel == nil {
		return result, nil
	}
	materials, err := transcriptInput(fitTranscript(transcript, summaryModel.ContextWindow))
	if err != nil {
		return summaryResult{}, err
	}
	instruction := "你负责为企业客服整理客服处理周期的小结，小结供企业客服日后查看和 AI 客服在客户下次来访时参考。\n" +
		"- 用 1 到 3 句话概括客户的诉求、处理过程和结果，写明关键事实，例如订单号、产品或时间。\n" +
		"- 只依据沟通记录，不编造记录中没有的信息，不评价客服表现。\n" +
		"- 使用" + localeLanguage(locale) + "书写。\n" +
		`- 只输出一个 JSON 对象，格式为 {"summary":"小结正文"}，不输出 JSON 以外的任何内容。`
	response, err := w.caller.CallOnce(ctx, agentruntime.SingleCallRequest{Instruction: instruction, Model: w.invoker.ModelConfig(sessionScope(session.OrganizationID, session.ID), summaryModel), Input: materials})
	if err != nil {
		return summaryResult{}, fmt.Errorf("generate service session summary: %w", err)
	}
	var payload struct {
		Summary string `json:"summary"`
	}
	if err := agentruntime.DecodeJSONObject(response.Text, &payload); err != nil {
		return summaryResult{}, fmt.Errorf("decode service session summary: %w", err)
	}
	if strings.TrimSpace(payload.Summary) == "" {
		return summaryResult{}, errors.New("service session summary is empty")
	}
	result.summary = new(strings.TrimSpace(payload.Summary))
	return result, nil
}
