//go:build server

package servicesummary

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/actions/knowledgegap"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// draftSimilarQuestionLimit 是起草问答保留的相似问题条数上限。
const draftSimilarQuestionLimit = 5

// knowledgeDraft 是模型起草的问答；QuestionIndex 为沟通记录中表达核心问题的客户消息下标。
type knowledgeDraft struct {
	Question         string   `json:"question"`
	SimilarQuestions []string `json:"similarQuestions"`
	Answer           string   `json:"answer"`
	QuestionIndex    *int     `json:"questionIndex"`
}

// DraftKnowledgeGap 用小结模型按周期沟通记录为待补知识起草问答；未设置小结模型时记为无法起草，复核来源另按 AI 判断更新客户提问消息。
func (w *Worker) DraftKnowledgeGap(ctx context.Context, input knowledgegap.DraftInput) error {
	gap, err := w.loadDraftingGap(ctx, input)
	if err != nil || gap == nil {
		return err
	}
	settings, err := customerservice.LoadServiceSummarySettings(ctx, w.db, input.OrganizationID)
	if err != nil {
		return err
	}
	model, err := customerservice.LoadModel(ctx, w.db, input.OrganizationID, settings.Summary, domain.AIModelTypeChat)
	if err != nil {
		return err
	}
	if model == nil {
		return w.saveDraft(ctx, input, func(query *bun.UpdateQuery) *bun.UpdateQuery {
			return query.Set("draft_status = ?", domain.KnowledgeGapDraftStatusUnavailable)
		})
	}
	transcript, err := LoadTranscript(ctx, w.db, input.OrganizationID, gap.ServiceSessionID, math.MaxInt64)
	if err != nil {
		return err
	}
	transcript = fitTranscript(transcript, model.ContextWindow)
	materials, err := transcriptInput(transcript)
	if err != nil {
		return err
	}
	instruction := "你负责把客服沟通整理成一条知识库问答，供 AI 客服日后回答同类问题。\n" +
		"- question 写客户想解决的核心问题，用一句通用的提问表达，去掉寒暄、个人信息和订单号等一次性细节。\n" +
		"- questionIndex 写沟通记录数组中最能表达这个核心问题的客户消息的下标，从 0 开始；没有合适的客户消息时为 null。\n" +
		"- similarQuestions 写 0 到 5 个同一问题的其他常见问法，不重复 question。\n" +
		"- answer 只依据真人客服（staff）的答复写成可直接回复任何客户的标准答案，去掉只适用于这位客户的细节；真人客服没有给出实质答复时 answer 为空字符串，不采用 AI 客服的答复，不编造内容。\n" +
		"- 使用真人客服答复的语言书写；没有真人客服答复时使用客户提问的语言。\n" +
		`- 只输出一个 JSON 对象，格式为 {"question":"问题","questionIndex":0,"similarQuestions":["相似问题"],"answer":"答案"}，不输出 JSON 以外的任何内容。`
	generateCtx, cancel := context.WithTimeout(ctx, summaryTimeout)
	defer cancel()
	response, err := w.caller.CallOnce(generateCtx, agentruntime.SingleCallRequest{Instruction: instruction, Model: model.ModelConfig(), Input: materials})
	if err != nil {
		return fmt.Errorf("draft knowledge gap: %w", err)
	}
	var draft knowledgeDraft
	if err := agentruntime.DecodeJSONObject(response.Text, &draft); err != nil {
		return fmt.Errorf("decode knowledge gap draft: %w", err)
	}
	question := strings.TrimSpace(draft.Question)
	if question == "" {
		return errors.New("knowledge gap draft question is empty")
	}
	// 去掉空白、与主问题重复和超出上限的相似问题。
	similar := make([]string, 0, draftSimilarQuestionLimit)
	seen := map[string]bool{question: true, "": true}
	for _, text := range draft.SimilarQuestions {
		text = strings.TrimSpace(text)
		if !seen[text] && len(similar) < draftSimilarQuestionLimit {
			similar = append(similar, text)
		}
		seen[text] = true
	}
	encoded, err := json.Marshal(similar)
	if err != nil {
		return fmt.Errorf("encode knowledge gap similar questions: %w", err)
	}
	answer := strings.TrimSpace(draft.Answer)
	// 复核来源没有转人工前的提问可依，采用 AI 判断的客户消息。
	var questionMessageID *string
	source := domain.KnowledgeGapSource(gap.Source)
	if (source == domain.KnowledgeGapSourceRatedUnresolved || source == domain.KnowledgeGapSourcePossiblyWrong) &&
		draft.QuestionIndex != nil && *draft.QuestionIndex >= 0 && *draft.QuestionIndex < len(transcript) &&
		transcript[*draft.QuestionIndex].Sender == "customer" {
		questionMessageID = &transcript[*draft.QuestionIndex].MessageID
	}
	if err := w.saveDraft(ctx, input, func(query *bun.UpdateQuery) *bun.UpdateQuery {
		query = query.
			Set("draft_status = ?", domain.KnowledgeGapDraftStatusReady).
			Set("draft_question = ?", question).
			Set("draft_similar_questions = ?::jsonb", string(encoded)).
			Set("draft_answer = ?", answer)
		if questionMessageID != nil {
			query = query.Set("question_message_id = ?", *questionMessageID)
		}
		return query
	}); err != nil {
		return err
	}
	slog.Info("待补知识问答草稿已生成", "organization_id", input.OrganizationID, "knowledge_gap_id", gap.ID, "has_answer", answer != "")
	return nil
}

// FinalizeKnowledgeGapDraftFailure 在起草任务耗尽重试后把仍在起草的请求记为起草失败。
func (w *Worker) FinalizeKnowledgeGapDraftFailure(ctx context.Context, input knowledgegap.DraftInput, runErr error) error {
	slog.Warn("待补知识问答草稿生成失败", "organization_id", input.OrganizationID, "knowledge_gap_id", input.KnowledgeGapID, "error", runErr)
	return w.saveDraft(ctx, input, func(query *bun.UpdateQuery) *bun.UpdateQuery {
		return query.Set("draft_status = ?", domain.KnowledgeGapDraftStatusFailed)
	})
}

// loadDraftingGap 读取仍在等待本次起草请求的待处理条目；条目已处理、重新请求起草或已有结果时返回 nil。
func (w *Worker) loadDraftingGap(ctx context.Context, input knowledgegap.DraftInput) (*servermodels.KnowledgeGap, error) {
	gap := &servermodels.KnowledgeGap{}
	err := w.db.NewSelect().Model(gap).Where("kg.organization_id = ? AND kg.id = ?", input.OrganizationID, input.KnowledgeGapID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load drafting knowledge gap: %w", err)
	}
	if domain.KnowledgeGapStatus(gap.Status) != domain.KnowledgeGapStatusPending ||
		domain.KnowledgeGapDraftStatus(gap.DraftStatus) != domain.KnowledgeGapDraftStatusPending ||
		!gap.DraftRequestedAt.Truncate(time.Microsecond).Equal(input.RequestedAt.Truncate(time.Microsecond)) {
		return nil, nil
	}
	return gap, nil
}

// saveDraft 只在条目仍待处理且仍在等待本次起草请求时写入起草结果，写入后通知企业客服受众。
func (w *Worker) saveDraft(ctx context.Context, input knowledgegap.DraftInput, apply func(*bun.UpdateQuery) *bun.UpdateQuery) error {
	return realtime.RunInTx(ctx, w.db, func(ctx context.Context, tx bun.Tx) error {
		query := tx.NewUpdate().Model((*servermodels.KnowledgeGap)(nil)).
			Set("updated_at = now()").
			Where("organization_id = ? AND id = ?", input.OrganizationID, input.KnowledgeGapID).
			Where("status = ? AND draft_status = ?", domain.KnowledgeGapStatusPending, domain.KnowledgeGapDraftStatusPending).
			Where("draft_requested_at = ?", input.RequestedAt)
		result, err := apply(query).Exec(ctx)
		if err != nil {
			return fmt.Errorf("save knowledge gap draft: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil {
			return fmt.Errorf("save knowledge gap draft: %w", err)
		} else if affected > 0 {
			realtime.Notify(ctx, realtime.ServiceInboxKnowledgeGapsChanged(input.OrganizationID))
		}
		return nil
	})
}
