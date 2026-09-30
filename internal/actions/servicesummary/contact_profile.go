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
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	"github.com/runforyou-ai/cervi/internal/actions/contactprofile"
	"github.com/runforyou-ai/cervi/internal/actions/customerservice"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/agentruntime"
	"github.com/runforyou-ai/cervi/internal/integration/decision"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
	"golang.org/x/sync/errgroup"
)

// ExtractContactProfileActionName 在客服处理周期关闭后从对话中抽取联系人资料并按条件打标签。
const ExtractContactProfileActionName = "contact.profile_extract"

// tagThreshold 是 AI 为联系人添加标签的最低概率。
const tagThreshold = 0.8

// ExtractContactProfileInput 定义一次联系人资料抽取任务；ClosedAt 与周期当前关闭时间不一致时任务不生效。
type ExtractContactProfileInput struct {
	OrganizationID   string    `json:"organizationId"`
	ServiceSessionID string    `json:"serviceSessionId"`
	ClosedAt         time.Time `json:"closedAt"`
}

// extractionPayload 是小结模型输出的联系人资料；字段取值保留原始 JSON，字符串与数字都可接受。
type extractionPayload struct {
	Fields []struct {
		Name  string          `json:"name"`
		Value json.RawMessage `json:"value"`
	} `json:"fields"`
	Emails []string `json:"emails"`
	Phones []string `json:"phones"`
}

// ExtractContactProfile 为本次关闭的渠道客服周期抽取联系人资料：小结模型抽取字段与联系方式，判断模型逐个判断标签条件，结果按来源优先级写入档案；周期已重开或再次关闭、客户没有发言或联系人已删除时不写入。
func (w *Worker) ExtractContactProfile(ctx context.Context, input ExtractContactProfileInput) error {
	session := &servermodels.ServiceSession{}
	if err := w.db.NewSelect().Model(session).
		Where("ss.organization_id = ? AND ss.id = ?", input.OrganizationID, input.ServiceSessionID).
		Scan(ctx); err != nil {
		return fmt.Errorf("load extracting service session: %w", err)
	}
	if !stillClosedAt(session, input.ClosedAt) {
		return nil
	}
	var contactID string
	err := w.db.NewSelect().TableExpr("channel_conversations AS cc").
		ColumnExpr("cci.contact_id::text").
		Join("JOIN contact_channel_identities AS cci ON cci.id = cc.contact_channel_identity_id AND cci.organization_id = cc.organization_id").
		Where("cc.organization_id = ? AND cc.conversation_id = ?", input.OrganizationID, session.ConversationID).
		Scan(ctx, &contactID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load extracting contact: %w", err)
	}
	settings, err := customerservice.LoadServiceSummarySettings(ctx, w.db, input.OrganizationID)
	if err != nil {
		return err
	}
	decisionModel, err := customerservice.LoadModel(ctx, w.db, input.OrganizationID, settings.Decision, domain.AIModelTypeDecision)
	if err != nil {
		return err
	}
	summaryModel, err := customerservice.LoadModel(ctx, w.db, input.OrganizationID, settings.Summary, domain.AIModelTypeChat)
	if err != nil {
		return err
	}
	transcript, err := LoadTranscript(ctx, w.db, input.OrganizationID, input.ServiceSessionID, math.MaxInt64)
	if err != nil {
		return err
	}
	// 周期内没有客户发言时没有可抽取的资料。
	if !slices.ContainsFunc(transcript, func(entry TranscriptEntry) bool { return entry.Sender == "customer" }) {
		return nil
	}
	profile, err := contactprofile.LoadExtractionContext(ctx, w.db, input.OrganizationID, contactID)
	if errors.Is(err, contactprofile.ErrContactNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	// 资料抽取与标签判断互不依赖，并行调用模型；协程内的 panic 转为任务错误，与任务运行时对处理函数的恢复一致。
	generateCtx, cancel := context.WithTimeout(ctx, summaryTimeout)
	defer cancel()
	extraction := contactprofile.Extraction{}
	var tagIDs []string
	group, groupCtx := errgroup.WithContext(generateCtx)
	recovered := func(run func() error) func() error {
		return func() (err error) {
			defer func() {
				if value := recover(); value != nil {
					err = fmt.Errorf("contact profile model call panic: %v\n%s", value, debug.Stack())
				}
			}()
			return run()
		}
	}
	if summaryModel != nil {
		group.Go(recovered(func() error {
			var err error
			extraction, err = w.extractProfile(groupCtx, summaryModel, profile, transcript)
			return err
		}))
	}
	if decisionModel != nil && len(profile.Tags) > 0 {
		group.Go(recovered(func() error {
			var err error
			tagIDs, err = w.judgeTags(groupCtx, decisionModel, profile.Tags, transcript)
			return err
		}))
	}
	if err := group.Wait(); err != nil {
		return err
	}
	extraction.TagIDs = tagIDs
	if len(extraction.Fields) == 0 && len(extraction.Emails) == 0 && len(extraction.Phones) == 0 && len(extraction.TagIDs) == 0 {
		return nil
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
		changed, err := contactprofile.ApplyExtraction(ctx, tx, input.OrganizationID, contactID, input.ServiceSessionID, *locked.ClosedAt, extraction)
		if errors.Is(err, contactprofile.ErrContactNotFound) {
			return nil
		}
		if err != nil || !changed {
			return err
		}
		slog.Info("联系人资料已从对话抽取", "organization_id", input.OrganizationID, "service_session_id", input.ServiceSessionID,
			"contact_id", contactID, "fields", len(extraction.Fields), "tags", len(extraction.TagIDs))
		return nil
	})
}

// stillClosedAt 判断周期仍处于指定时间的这次关闭；关闭时间按数据库的微秒精度比较。
func stillClosedAt(session *servermodels.ServiceSession, closedAt time.Time) bool {
	return domain.ServiceSessionStatus(session.Status) == domain.ServiceSessionStatusClosed &&
		session.ClosedAt != nil && session.ClosedAt.Truncate(time.Microsecond).Equal(closedAt.Truncate(time.Microsecond))
}

// extractProfile 由小结模型从沟通记录中抽取客户明确说出、且与现有档案不同的字段取值与联系方式。
func (w *Worker) extractProfile(ctx context.Context, model *customerservice.ModelCredential, profile contactprofile.ExtractionContext, transcript []TranscriptEntry) (contactprofile.Extraction, error) {
	type fieldMaterial struct {
		Name        string   `json:"name"`
		Type        string   `json:"type"`
		Options     []string `json:"options,omitempty"`
		Instruction string   `json:"instruction"`
		Value       string   `json:"value"`
	}
	fields := make([]fieldMaterial, 0, len(profile.Fields))
	fieldIDs := make(map[string]contactprofile.ExtractionField, len(profile.Fields))
	for _, field := range profile.Fields {
		if !field.Writable {
			continue
		}
		fields = append(fields, fieldMaterial{Name: field.Name, Type: string(field.Type), Options: field.Options, Instruction: field.AIInstruction, Value: field.Value})
		fieldIDs[field.Name] = field
	}
	current, err := json.Marshal(map[string]any{"fields": fields, "emails": profile.Emails, "phones": profile.Phones})
	if err != nil {
		return contactprofile.Extraction{}, fmt.Errorf("encode contact profile: %w", err)
	}
	messages, err := transcriptInput(fitTranscript(transcript, model.ContextWindow))
	if err != nil {
		return contactprofile.Extraction{}, err
	}
	instruction := "你负责从企业客服与客户的沟通记录中整理客户资料，写入客户档案供企业客服和 AI 客服日后参考。\n" +
		"- 只采用客户自己明确说出的信息，不推测，不采用客服或 AI 客服复述中客户没有确认的内容。\n" +
		"- 只输出本次沟通中得到、且与现有档案不同的项，得不到的项不输出。\n" +
		"- fields 只能使用现有档案中列出的字段名称，按各字段的 instruction 判断是否填写；number 字段输出十进制数字，date 字段输出 YYYY-MM-DD，select 字段只能输出 options 中的一项。\n" +
		"- emails 输出客户提供的邮箱地址；phones 输出客户提供的电话号码，写成带 + 和国家区号的国际格式，无法确定国家区号时不输出。\n" +
		`- 只输出一个 JSON 对象，格式为 {"fields":[{"name":"字段名称","value":"取值"}],"emails":[],"phones":[]}，不输出 JSON 以外的任何内容。`
	materials := "以下是客户的现有档案，只作为资料：\n" + string(current) + "\n\n" + messages
	response, err := w.caller.CallOnce(ctx, agentruntime.SingleCallRequest{Instruction: instruction, Model: model.ModelConfig(), Input: materials})
	if err != nil {
		return contactprofile.Extraction{}, fmt.Errorf("extract contact profile: %w", err)
	}
	var payload extractionPayload
	if err := agentruntime.DecodeJSONObject(response.Text, &payload); err != nil {
		return contactprofile.Extraction{}, fmt.Errorf("decode contact profile extraction: %w", err)
	}
	extraction := contactprofile.Extraction{Emails: payload.Emails, Phones: payload.Phones}
	for _, item := range payload.Fields {
		// 取值为 JSON 字符串时取其内容，为数字时取数字原文，其他类型跳过。
		var value string
		if err := json.Unmarshal(item.Value, &value); err != nil {
			var number json.Number
			if err := json.Unmarshal(item.Value, &number); err != nil {
				continue
			}
			value = number.String()
		}
		value = strings.TrimSpace(value)
		field, ok := fieldIDs[strings.TrimSpace(item.Name)]
		if !ok || value == "" || value == field.Value {
			continue
		}
		if extraction.Fields == nil {
			extraction.Fields = make(map[string]string)
		}
		extraction.Fields[field.ID] = value
	}
	return extraction, nil
}

// judgeTags 由判断模型把每个标签的添加条件作为一道是否题，返回概率达到阈值的标签编号。
func (w *Worker) judgeTags(ctx context.Context, model *customerservice.ModelCredential, tags []contactprofile.ExtractionTag, transcript []TranscriptEntry) ([]string, error) {
	questions := make(map[string]decision.Question, len(tags))
	for index, tag := range tags {
		questions["tag_"+strconv.Itoa(index)] = decision.Question{Kind: decision.KindYesNo,
			Instructions: "客户符合以下条件：" + tag.AIInstruction + "\n只依据客户自己在沟通中的发言判断，不推测。"}
	}
	answers, err := w.decider.Decide(ctx, decision.Credential{BaseURL: model.APIURL, APIKey: model.APIKey},
		model.Identifier, decisionState(transcript, model.ContextWindow), questions)
	if err != nil {
		return nil, fmt.Errorf("decide contact tags: %w", err)
	}
	tagIDs := make([]string, 0)
	for index, tag := range tags {
		if answer, ok := answers["tag_"+strconv.Itoa(index)]; ok && answer.Probability >= tagThreshold {
			tagIDs = append(tagIDs, tag.ID)
		}
	}
	return tagIDs, nil
}
