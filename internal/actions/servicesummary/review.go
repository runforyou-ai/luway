//go:build server

package servicesummary

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	"github.com/runforyou-ai/cervi/internal/actions/customerservice"
	"github.com/runforyou-ai/cervi/internal/actions/knowledgegap"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/decision"
	"github.com/runforyou-ai/cervi/internal/realtime"
	"github.com/runforyou-ai/cervi/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ReviewActionName 在客服处理周期关闭后推断满意度并检查 AI 客服与真人客服的答复。
const ReviewActionName = "service_session.review"

const (
	// reviewFlagThreshold 是质检标记判为成立的最低概率。
	reviewFlagThreshold = 0.8
	// satisfactionThreshold 是满意度等级被采用的最低概率。
	satisfactionThreshold = 0.6
)

// satisfactionLevels 是满意度评分题从低到高的等级，下标与评分题等级一致。
var satisfactionLevels = []domain.ServiceSessionSatisfaction{
	domain.ServiceSessionSatisfactionDissatisfied, domain.ServiceSessionSatisfactionNeutral, domain.ServiceSessionSatisfactionSatisfied,
}

// ReviewInput 定义一次周期质检任务；ClosedAt 与周期当前关闭时间不一致时任务不生效。
type ReviewInput struct {
	OrganizationID   string    `json:"organizationId"`
	ServiceSessionID string    `json:"serviceSessionId"`
	ClosedAt         time.Time `json:"closedAt"`
}

// Review 由判断模型为本次关闭的周期推断满意度，检查 AI 客服是否答错、应转人工未转和态度问题，以及真人客服是否答错和态度问题，结果按周期覆盖写入，AI 答错时登记待补知识；周期已重开或再次关闭、客户没有发言或未设置判断模型时不写入。
func (w *Worker) Review(ctx context.Context, input ReviewInput) error {
	session := &servermodels.ServiceSession{}
	if err := w.db.NewSelect().Model(session).
		Where("ss.organization_id = ? AND ss.id = ?", input.OrganizationID, input.ServiceSessionID).
		Scan(ctx); err != nil {
		return fmt.Errorf("load reviewing service session: %w", err)
	}
	if !stillClosedAt(session, input.ClosedAt) {
		return nil
	}
	settings, err := customerservice.LoadServiceSummarySettings(ctx, w.db, input.OrganizationID)
	if err != nil {
		return err
	}
	decisionModel, err := customerservice.LoadModel(ctx, w.db, input.OrganizationID, settings.Decision, domain.AIModelTypeDecision)
	if err != nil || decisionModel == nil {
		return err
	}
	// 参与情况按完整周期判断，模型资料另按窗口截断。
	var participation struct {
		RequesterSpoke bool `bun:"requester_spoke"`
		AgentReplied   bool `bun:"agent_replied"`
		HumanReplied   bool `bun:"human_replied"`
		AIOnly         bool `bun:"ai_only"`
	}
	if err := w.db.NewSelect().TableExpr("service_sessions AS ss").
		ColumnExpr("? AS requester_spoke", messagequery.RequesterSpoke("ss")).
		ColumnExpr("? AS agent_replied", messagequery.AgentReplied("ss")).
		ColumnExpr("? AS human_replied", messagequery.HumanReplied("ss")).
		ColumnExpr("? AS ai_only", messagequery.AIOnly("ss")).
		Where("ss.organization_id = ? AND ss.id = ?", input.OrganizationID, input.ServiceSessionID).
		Scan(ctx, &participation); err != nil {
		return fmt.Errorf("load service session participation: %w", err)
	}
	if !participation.RequesterSpoke {
		return nil
	}
	transcript, err := LoadTranscript(ctx, w.db, input.OrganizationID, input.ServiceSessionID, math.MaxInt64)
	if err != nil {
		return err
	}
	questions := map[string]decision.Question{
		"satisfaction": {Kind: decision.KindScore, Levels: []string{"不满意", "一般", "满意"},
			Instructions: "根据客户在这段沟通中的表达，判断客户对本次服务的满意程度。"},
	}
	if participation.AgentReplied {
		questions["ai_incorrect"] = decision.Question{Kind: decision.KindYesNo,
			Instructions: "AI 客服的答复中有与事实不符或凭空编造的内容，例如给出了错误的时效、价格、政策或操作步骤。"}
		questions["ai_poor_attitude"] = decision.Question{Kind: decision.KindYesNo,
			Instructions: "AI 客服的答复敷衍、生硬、推诿，或反复答非所问。"}
	}
	if participation.AgentReplied && participation.AIOnly {
		questions["ai_missed_handoff"] = decision.Question{Kind: decision.KindYesNo,
			Instructions: "客户明确要求人工、提出投诉，或问题需要人工判断或办理，AI 客服却没有转人工而自行结束了沟通。"}
	}
	if participation.HumanReplied {
		questions["human_incorrect"] = decision.Question{Kind: decision.KindYesNo,
			Instructions: "真人客服的答复中有与事实不符的内容，例如给出了错误的时效、价格、政策或操作步骤。"}
		questions["human_poor_attitude"] = decision.Question{Kind: decision.KindYesNo,
			Instructions: "真人客服的答复敷衍、生硬、推诿，或反复答非所问。"}
	}
	generateCtx, cancel := context.WithTimeout(ctx, summaryTimeout)
	defer cancel()
	answers, err := w.decider.Decide(generateCtx, decision.Credential{BaseURL: decisionModel.APIURL, APIKey: decisionModel.APIKey},
		decisionModel.Identifier, decisionState(transcript, decisionModel.ContextWindow), questions)
	if err != nil {
		return fmt.Errorf("decide service session review: %w", err)
	}
	review := &servermodels.ServiceSessionReview{OrganizationID: input.OrganizationID, ServiceSessionID: input.ServiceSessionID}
	// 概率最高的等级达到阈值时采用，否则为未判定。
	if answer, ok := answers["satisfaction"]; ok && len(answer.LevelProbabilities) == len(satisfactionLevels) {
		best := 0
		for i, probability := range answer.LevelProbabilities {
			if probability > answer.LevelProbabilities[best] {
				best = i
			}
		}
		if answer.LevelProbabilities[best] >= satisfactionThreshold {
			review.Satisfaction = new(string(satisfactionLevels[best]))
		}
	}
	for key, target := range map[string]**bool{
		"ai_incorrect": &review.AIIncorrect, "ai_poor_attitude": &review.AIPoorAttitude, "ai_missed_handoff": &review.AIMissedHandoff,
		"human_incorrect": &review.HumanIncorrect, "human_poor_attitude": &review.HumanPoorAttitude,
	} {
		if answer, ok := answers[key]; ok {
			*target = new(answer.Probability >= reviewFlagThreshold)
		}
	}
	return realtime.RunInTx(ctx, w.db, func(ctx context.Context, tx bun.Tx) error {
		lockedSession, err := chatstate.LockServiceSessionByID(ctx, tx, input.OrganizationID, input.ServiceSessionID)
		if err != nil {
			return err
		}
		locked := lockedSession.Session
		if !stillClosedAt(locked, input.ClosedAt) {
			return nil
		}
		review.ClosedAt = *locked.ClosedAt
		if _, err := tx.NewInsert().Model(review).
			Column("organization_id", "service_session_id", "closed_at", "satisfaction", "ai_incorrect", "ai_missed_handoff", "ai_poor_attitude", "human_incorrect", "human_poor_attitude").
			On("CONFLICT (organization_id, service_session_id) DO UPDATE").
			Set("closed_at = EXCLUDED.closed_at").
			Set("satisfaction = EXCLUDED.satisfaction").
			Set("ai_incorrect = EXCLUDED.ai_incorrect").
			Set("ai_missed_handoff = EXCLUDED.ai_missed_handoff").
			Set("ai_poor_attitude = EXCLUDED.ai_poor_attitude").
			Set("human_incorrect = EXCLUDED.human_incorrect").
			Set("human_poor_attitude = EXCLUDED.human_poor_attitude").
			Set("updated_at = now()").
			Exec(ctx); err != nil {
			return fmt.Errorf("save service session review: %w", err)
		}
		if review.AIIncorrect != nil && *review.AIIncorrect {
			if err := knowledgegap.RecordPossiblyWrong(ctx, tx, w.enqueuer, locked); err != nil {
				return err
			}
		}
		realtime.Notify(ctx, realtime.ServiceInboxReportsChanged(input.OrganizationID))
		slog.Info("客服周期质检已完成", "organization_id", input.OrganizationID, "service_session_id", input.ServiceSessionID,
			"satisfaction", common.StringValue(review.Satisfaction), "questions", len(questions))
		return nil
	})
}
