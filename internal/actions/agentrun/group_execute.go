//go:build server

package agentrun

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/runforyou-ai/luway/internal/actions/agentmessage"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/set"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/schema"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// markdownParser 按 CommonMark 语法解析回复正文，用于识别代码范围。
var markdownParser = goldmark.DefaultParser()

// groupMentionRunPolicy 定义群聊点名的运行策略。
type groupMentionRunPolicy struct {
	scheduler *Scheduler
}

// lockContext 锁定群聊会话并读取执行 Agent 当前的成员关系。
func (p groupMentionRunPolicy) lockContext(ctx context.Context, db bun.IDB, run *servermodels.AgentRun) (agentRunPolicyContext, error) {
	conversation, err := chatstate.LockConversation(ctx, db, run.WorkspaceID, run.ConversationID)
	if err != nil {
		return agentRunPolicyContext{}, err
	}
	if conversation.Type != string(domain.ConversationTypeGroup) {
		return agentRunPolicyContext{}, errors.New("agent run does not belong to a group conversation")
	}
	participantID, subjectID, err := lockGroupAgentParticipant(ctx, db, run.WorkspaceID, run.ConversationID, run.AgentIdentityID)
	if err != nil {
		return agentRunPolicyContext{}, err
	}
	return agentRunPolicyContext{Conversation: conversation, AgentParticipantID: participantID, AgentSubjectID: subjectID}, nil
}

// prepareLocked 校验群仍在使用且执行 Agent 仍是有效成员，失效时收敛本次运行。
func (p groupMentionRunPolicy) prepareLocked(ctx context.Context, db bun.IDB, policyContext agentRunPolicyContext, run *servermodels.AgentRun) (bool, error) {
	if policyContext.AgentParticipantID != "" &&
		domain.ConversationStatus(policyContext.Conversation.Status) == domain.ConversationStatusActive {
		return true, nil
	}
	if _, err := db.NewUpdate().Model(run).
		Set("status = ?", domain.AgentRunStatusCancelled).
		Set("error_code = ?", domain.AgentRunErrorCodeAgentRemoved).
		Set("last_error = ?", "group agent membership changed").
		Set("completed_at = now()").
		WherePK().
		Where("status IN (?)", bun.List(domain.AgentRunActiveStatuses)).
		Exec(ctx); err != nil {
		return false, fmt.Errorf("suppress group agent run: %w", err)
	}
	if err := agentprocess.SettleEndedRuns(ctx, db, run.WorkspaceID, run.ID); err != nil {
		return false, err
	}
	slog.WarnContext(logscope.WithWorkspace(ctx, run.WorkspaceID), "群内 AI 员工失去执行资格，运行已收敛",
		"conversation_id", run.ConversationID,
		"agent_identity_id", run.AgentIdentityID, "agent_run_id", run.ID)
	return false, nil
}

// loadMessages 读取不越过已认领输入的群聊上下文。
func (p groupMentionRunPolicy) loadMessages(ctx context.Context, db bun.IDB, run *servermodels.AgentRun, endSeq int64, links serverfilecontent.Links) ([]agentcontract.Message, error) {
	return loadClaimedGroupMessages(ctx, db, run, endSeq, links)
}

// persistMessage 以 Agent 成员身份追加群聊结果消息。
func (p groupMentionRunPolicy) persistMessage(ctx context.Context, db bun.IDB, policyContext agentRunPolicyContext, run *servermodels.AgentRun, messageID string, messageType domain.MessageType, content string) error {
	_, _, err := agentmessage.Append(ctx, db, p.scheduler.enqueuer, policyContext.Conversation, agentResultMessage(run, messageID, policyContext.AgentParticipantID, messageType, content, nil))
	return err
}

// sceneContext 给出群聊场景、群标题与可点名成员。
func (p groupMentionRunPolicy) sceneContext(ctx context.Context, db bun.IDB, execution executionContext) (agentruntime.SceneContext, error) {
	title := ""
	if err := db.NewSelect().Model((*servermodels.Conversation)(nil)).
		ColumnExpr("COALESCE(cv.title, '')").
		Where("cv.workspace_id = ? AND cv.id = ?", execution.Run.WorkspaceID, execution.Run.ConversationID).
		Scan(ctx, &title); err != nil {
		return agentruntime.SceneContext{}, fmt.Errorf("load group title for scene context: %w", err)
	}
	participants, err := loadGroupMentionParticipants(ctx, db, execution.Run.WorkspaceID, execution.Run.ConversationID, execution.Run.AgentIdentityID)
	if err != nil {
		return agentruntime.SceneContext{}, err
	}
	// 按名称排序列出群内名称唯一的可点名成员。
	names := make([]string, 0, len(participants))
	for name, matched := range participants {
		if len(matched) == 1 {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return agentruntime.SceneContext{Scene: agentruntime.SceneGroup, GroupTitle: title, MentionCandidates: names}, nil
}

// laneRevision 在目标 Agent 仍是有效群成员时返回其配置版本。
func (p groupMentionRunPolicy) laneRevision(ctx context.Context, db bun.IDB, policyContext agentRunPolicyContext, lane *servermodels.AgentLane) (string, bool, error) {
	if domain.ConversationStatus(policyContext.Conversation.Status) != domain.ConversationStatusActive {
		return "", false, nil
	}
	return loadGroupAgentRevision(ctx, db, lane.WorkspaceID, lane.ConversationID, lane.AgentIdentityID, false)
}

// lockGroupAgentParticipant 锁定 Agent 在群内的成员关系，已退出时返回空编号。
func lockGroupAgentParticipant(ctx context.Context, db bun.IDB, workspaceID, conversationID, agentIdentityID string) (string, string, error) {
	var row struct {
		ParticipantID string `bun:"participant_id"`
		SubjectID     string `bun:"subject_id"`
	}
	err := db.NewSelect().TableExpr("conversation_participants AS cp").
		ColumnExpr("cp.id AS participant_id").
		ColumnExpr("cs.id AS subject_id").
		Join("JOIN chat_subjects AS cs ON cs.id = cp.subject_id AND cs.workspace_id = cp.workspace_id AND cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Where("cp.workspace_id = ? AND cp.conversation_id = ?", workspaceID, conversationID).
		Where("cs.source_id = ? AND cp.left_at IS NULL", agentIdentityID).
		For("UPDATE OF cp").Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("lock group agent participant: %w", err)
	}
	return row.ParticipantID, row.SubjectID, nil
}

// loadGroupAgentRevision 读取仍是有效群成员的 Agent 的当前配置版本，requireActive 表示同时要求企业启用状态。
func loadGroupAgentRevision(ctx context.Context, db bun.IDB, workspaceID, conversationID, agentIdentityID string, requireActive bool) (string, bool, error) {
	var revisionID string
	query := db.NewSelect().TableExpr("agents AS a").
		ColumnExpr("a.active_revision_id").
		Join("JOIN agent_revisions AS ar ON ar.id = a.active_revision_id AND ar.agent_id = a.id AND ar.workspace_id = a.workspace_id AND ar.execution_mode = ? AND ar.schema_version = 1", domain.AgentExecutionModeManaged).
		Join("JOIN chat_subjects AS cs ON cs.workspace_id = a.workspace_id AND cs.source_id = a.identity_id AND cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("JOIN conversation_participants AS cp ON cp.workspace_id = cs.workspace_id AND cp.subject_id = cs.id AND cp.conversation_id = ? AND cp.left_at IS NULL", conversationID).
		Where("a.workspace_id = ?", workspaceID).
		Where("a.identity_id = ?", agentIdentityID)
	if requireActive {
		query = query.Where("a.status = ? AND a.paused_at IS NULL", domain.IdentityStatusActive)
	}
	err := query.Scan(ctx, &revisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("load group agent revision: %w", err)
	}
	return revisionID, true, nil
}

// groupMessageRow 是读取群聊上下文的一行：消息、发送者、全员点名、引用与附件。
type groupMessageRow struct {
	ID             string `bun:"id"`
	Body           string `bun:"body"`
	SenderSourceID string `bun:"sender_source_id"`
	SenderName     string `bun:"sender_name"`
	SenderType     string `bun:"sender_type"`
	MentionAll     bool   `bun:"mention_all"`
	claimedReplyRow
	contextAttachmentRow
}

// groupPersonalAgentKind 是群聊上下文中个人 AI 员工的成员种类。
const groupPersonalAgentKind = "personal_agent"

// groupMemberKind 返回别名企业身份在群聊上下文中的成员种类：个人 AI 员工为 personal_agent，其他身份为企业身份类型。
func groupMemberKind(alias string) schema.QueryWithArgs {
	name := bun.Ident(alias)
	return bun.SafeQuery(`CASE WHEN EXISTS (
		SELECT 1 FROM agents AS kind_a WHERE kind_a.workspace_id = ?.workspace_id AND kind_a.identity_id = ?.id AND ? = ANY(kind_a.service_audiences)
	) THEN ? ELSE ?.type END`, name, name, domain.ServiceAudiencePersonal, groupPersonalAgentKind, name)
}

// groupMessageSender 是上下文中的发送者或成员：名称与成员种类。
type groupMessageSender struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// groupMessageEnvelope 是群聊上下文中其他成员发言的结构化正文。
type groupMessageEnvelope struct {
	Sender         groupMessageSender       `json:"sender"`
	Body           string                   `json:"body"`
	AddressedToYou bool                     `json:"addressedToYou,omitempty"`
	Mentions       []groupMessageSender     `json:"mentions,omitempty"`
	MentionAll     bool                     `json:"mentionAll,omitempty"`
	Attachment     *contextAttachment       `json:"attachment,omitempty"`
	ReplyTo        *claimedMessageReference `json:"replyTo,omitempty"`
}

// loadClaimedGroupMessages 读取带发送者标识的群聊上下文，自己的发言投影为助手消息，并入提交给自己的工具调用结果事件。
func loadClaimedGroupMessages(ctx context.Context, db bun.IDB, run *servermodels.AgentRun, endSeq int64, links serverfilecontent.Links) ([]agentcontract.Message, error) {
	boundary, err := loadClaimedMessageBoundary(ctx, db, run, endSeq)
	if err != nil {
		return nil, err
	}
	rows := make([]groupMessageRow, 0, agentHistoryLimit)
	if err := db.NewSelect().TableExpr("messages AS msg").
		ColumnExpr("msg.id, msg.body").
		ColumnExpr("cs.source_id AS sender_source_id").
		ColumnExpr("oi.display_name AS sender_name").
		ColumnExpr("? AS sender_type", groupMemberKind("oi")).
		ColumnExpr("msg.mention_all").
		Join("JOIN conversation_participants AS cp ON cp.id = msg.sender_participant_id AND cp.workspace_id = msg.workspace_id AND cp.conversation_id = msg.conversation_id").
		Join("JOIN chat_subjects AS cs ON cs.id = cp.subject_id AND cs.workspace_id = cp.workspace_id AND cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("JOIN workspace_identities AS oi ON oi.id = cs.source_id AND oi.workspace_id = cs.workspace_id").
		Apply(withClaimedReply).
		Apply(withContextAttachments).
		Where("msg.workspace_id = ?", run.WorkspaceID).
		Where("msg.conversation_id = ?", run.ConversationID).
		Where("msg.deleted_at IS NULL").
		Where("msg.message_seq <= ?", boundary.MessageSeq).
		OrderExpr("msg.message_seq DESC").
		Limit(agentHistoryLimit).
		Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("load claimed group conversation context: %w", err)
	}
	slices.Reverse(rows)
	messageIDs := arr.Map(rows, func(row groupMessageRow) string { return row.ID })
	mentions, err := loadGroupMessageMentions(ctx, db, run.WorkspaceID, messageIDs)
	if err != nil {
		return nil, err
	}
	addressed, err := loadClaimedInputMessages(ctx, db, run, endSeq)
	if err != nil {
		return nil, err
	}
	messages := make([]agentcontract.Message, 0, len(rows))
	for _, row := range rows {
		// 自己的历史发言保持纯文本，其余成员的发言携带发送者标识与一层引用。
		if row.SenderSourceID == run.AgentIdentityID {
			messages = append(messages, agentcontract.Message{ID: row.ID, Role: agentcontract.MessageRoleAssistant, Content: row.Body})
			continue
		}
		envelope := groupMessageEnvelope{
			Sender:         groupMessageSender{Name: row.SenderName, Kind: row.SenderType},
			Body:           row.Body,
			Mentions:       mentions[row.ID],
			MentionAll:     row.MentionAll,
			AddressedToYou: addressed.Has(row.ID),
			Attachment:     row.attachment(ctx, row.ID, links),
		}
		if row.ReplyToMessageID != nil {
			reference := claimedMessageReference{MessageID: *row.ReplyToMessageID, Deleted: row.ReplyDeleted}
			if !row.ReplyDeleted {
				reference.SenderName, reference.Body = row.ReplySenderName, row.ReplyBody
				reference.Attachment = row.replyAttachment(ctx, links)
			}
			envelope.ReplyTo = &reference
		}
		encoded, err := json.Marshal(envelope)
		if err != nil {
			return nil, fmt.Errorf("encode group conversation context: %w", err)
		}
		messages = append(messages, agentcontract.Message{ID: row.ID, Revision: row.revision(), Role: agentcontract.MessageRoleUser, Content: string(encoded), Media: row.media()})
	}
	return mergeToolCallEvents(ctx, db, run, messages, boundary.MessageSeq)
}

// loadGroupMessageMentions 按消息读取被点名成员，供上下文说明本轮参与者。
func loadGroupMessageMentions(ctx context.Context, db bun.IDB, workspaceID string, messageIDs []string) (map[string][]groupMessageSender, error) {
	mentions := make(map[string][]groupMessageSender, len(messageIDs))
	if len(messageIDs) == 0 {
		return mentions, nil
	}
	rows := make([]struct {
		MessageID    string `bun:"message_id"`
		DisplayName  string `bun:"display_name"`
		IdentityType string `bun:"identity_type"`
	}, 0)
	if err := db.NewSelect().TableExpr("message_mentions AS mm").
		ColumnExpr("mm.message_id").
		ColumnExpr("oi.display_name").
		ColumnExpr("? AS identity_type", groupMemberKind("oi")).
		Join("JOIN chat_subjects AS cs ON cs.id = mm.subject_id AND cs.workspace_id = mm.workspace_id AND cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("JOIN workspace_identities AS oi ON oi.id = cs.source_id AND oi.workspace_id = cs.workspace_id").
		Where("mm.workspace_id = ?", workspaceID).
		Where("mm.message_id IN (?)", bun.List(messageIDs)).
		OrderExpr("mm.message_id ASC, oi.display_name ASC").
		Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("load group message mentions: %w", err)
	}
	for _, row := range rows {
		mentions[row.MessageID] = append(mentions[row.MessageID], groupMessageSender{Name: row.DisplayName, Kind: row.IdentityType})
	}
	return mentions, nil
}

// loadClaimedInputMessages 标记本次运行认领的输入各自来自哪条消息。
func loadClaimedInputMessages(ctx context.Context, db bun.IDB, run *servermodels.AgentRun, endSeq int64) (set.Set[string], error) {
	sourceIDs := make([]string, 0)
	if err := db.NewSelect().Model((*servermodels.AgentInput)(nil)).
		ColumnExpr("ai.source_message_id").
		Where("ai.lane_id = ?", run.LaneID).
		Where("ai.input_seq BETWEEN ? AND ?", run.InputStartSeq, endSeq).
		Scan(ctx, &sourceIDs); err != nil {
		return nil, fmt.Errorf("load claimed input messages: %w", err)
	}
	return set.Collect(sourceIDs), nil
}

// applyMentions 从回复正文提取点名成员，保存提醒关系并为有执行资格的 AI 员工追加接力输入。
func (p groupMentionRunPolicy) applyMentions(ctx context.Context, db bun.IDB, policyContext agentRunPolicyContext, run *servermodels.AgentRun, messageID, content string) error {
	participants, err := loadGroupMentionParticipants(ctx, db, run.WorkspaceID, run.ConversationID, run.AgentIdentityID)
	if err != nil {
		return err
	}
	targets := make([]groupMentionTarget, 0)
	for _, name := range extractMentionNames(content, slices.Collect(maps.Keys(participants))) {
		// 重名成员无法确定点名对象，整体忽略。
		if len(participants[name]) != 1 {
			slog.WarnContext(logscope.WithWorkspace(ctx, run.WorkspaceID), "群内点名的成员名称重复，已忽略",
				"conversation_id", run.ConversationID,
				"agent_run_id", run.ID, "display_name", name)
			continue
		}
		targets = append(targets, participants[name][0])
	}
	if len(targets) == 0 {
		return nil
	}
	rows := arr.Map(targets, func(target groupMentionTarget) *servermodels.MessageMention {
		return &servermodels.MessageMention{WorkspaceID: run.WorkspaceID, MessageID: messageID, SubjectID: target.ChatSubjectID}
	})
	if _, err := db.NewInsert().Model(&rows).
		Column("workspace_id", "message_id", "subject_id").Exec(ctx); err != nil {
		return fmt.Errorf("create group agent message mentions: %w", err)
	}
	ordinal := 0
	for _, target := range targets {
		if domain.WorkspaceIdentityType(target.IdentityType) != domain.WorkspaceIdentityTypeAgent {
			continue
		}
		revisionID, eligible, err := loadGroupAgentRevision(ctx, db, run.WorkspaceID, run.ConversationID, target.IdentityID, true)
		if err != nil {
			return err
		}
		if !eligible {
			slog.WarnContext(logscope.WithWorkspace(ctx, run.WorkspaceID), "被接力点名的 AI 员工不满足执行资格",
				"conversation_id", run.ConversationID,
				"agent_run_id", run.ID, "display_name", target.DisplayName)
			continue
		}
		if err := p.scheduler.appendInput(ctx, db, agentRunSpec{
			WorkspaceID: run.WorkspaceID, ConversationID: run.ConversationID,
			AgentIdentityID: target.IdentityID, RevisionID: revisionID,
			ScopeKind: domain.AgentExecutionScopeConversation, ScopeID: run.ConversationID,
			Kind: domain.AgentInputKindHandoff, SourceSubjectID: policyContext.AgentSubjectID, SourceOrdinal: ordinal,
		}, messageID); err != nil {
			return fmt.Errorf("append group handoff input: %w", err)
		}
		slog.InfoContext(logscope.WithWorkspace(ctx, run.WorkspaceID), "AI 员工点名接力已排队",
			"conversation_id", run.ConversationID,
			"agent_run_id", run.ID, "target_agent_identity_id", target.IdentityID)
		ordinal++
	}
	return nil
}

// groupMentionTarget 是群内可被点名的有效参与者。
type groupMentionTarget struct {
	ChatSubjectID string `bun:"chat_subject_id"`
	IdentityID    string `bun:"identity_id"`
	DisplayName   string `bun:"display_name"`
	IdentityType  string `bun:"identity_type"`
}

// loadGroupMentionParticipants 按显示名归集群内除自己以外的有效参与者。
func loadGroupMentionParticipants(ctx context.Context, db bun.IDB, workspaceID, conversationID, agentIdentityID string) (map[string][]groupMentionTarget, error) {
	rows := make([]groupMentionTarget, 0)
	if err := db.NewSelect().TableExpr("conversation_participants AS cp").
		ColumnExpr("cs.id AS chat_subject_id").
		ColumnExpr("cs.source_id AS identity_id").
		ColumnExpr("oi.display_name").
		ColumnExpr("oi.type AS identity_type").
		Join("JOIN chat_subjects AS cs ON cs.id = cp.subject_id AND cs.workspace_id = cp.workspace_id AND cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("JOIN workspace_identities AS oi ON oi.id = cs.source_id AND oi.workspace_id = cs.workspace_id").
		Where("cp.workspace_id = ? AND cp.conversation_id = ?", workspaceID, conversationID).
		Where("cp.left_at IS NULL").
		Where("cs.source_id <> ?", agentIdentityID).
		Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("load group mention participants: %w", err)
	}
	return arr.GroupBy(rows, func(row groupMentionTarget) string { return row.DisplayName }), nil
}

// extractMentionNames 按出现顺序返回正文中去重后的点名成员，@ 须位于开头或空白之后且成员名后不紧跟字母、组合标记、数字或下划线。
func extractMentionNames(content string, names []string) []string {
	source := []byte(content)
	// 代码块、围栏信息和行内代码的内容替换为空格，其中的 @ 不形成点名。
	blank := func(segment text.Segment) {
		for i := segment.Start; i < segment.Stop; i++ {
			source[i] = ' '
		}
	}
	_ = ast.Walk(markdownParser.Parse(text.NewReader(source)), func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch code := node.(type) {
		case *ast.FencedCodeBlock:
			if code.Info != nil {
				blank(code.Info.Segment)
			}
			for i := 0; i < code.Lines().Len(); i++ {
				blank(code.Lines().At(i))
			}
		case *ast.CodeBlock:
			for i := 0; i < code.Lines().Len(); i++ {
				blank(code.Lines().At(i))
			}
		case *ast.CodeSpan:
			for child := code.FirstChild(); child != nil; child = child.NextSibling() {
				if span, ok := child.(*ast.Text); ok {
					blank(span.Segment)
				}
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	plain := string(source)
	// 较长的成员名优先匹配，前缀相同的成员按完整名称区分。
	slices.SortFunc(names, func(a, b string) int { return len(b) - len(a) })
	found := make([]string, 0)
	for index, char := range plain {
		if char != '@' {
			continue
		}
		if previous, _ := utf8.DecodeLastRuneInString(plain[:index]); index > 0 && !unicode.IsSpace(previous) {
			continue
		}
		rest := plain[index+1:]
		for _, name := range names {
			if !strings.HasPrefix(rest, name) {
				continue
			}
			if next, size := utf8.DecodeRuneInString(rest[len(name):]); size > 0 && (unicode.IsLetter(next) || unicode.IsMark(next) || unicode.IsNumber(next) || next == '_') {
				continue
			}
			if !slices.Contains(found, name) {
				found = append(found, name)
			}
			break
		}
	}
	return found
}
