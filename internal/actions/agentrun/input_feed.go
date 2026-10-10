//go:build server

package agentrun

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/runforyou-ai/einorun"
	deliveryaction "github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// errAgentRunSuppressed 表示运行已结束或已失去执行资格，本次认领不产生输入。
var errAgentRunSuppressed = errors.New("agent run suppressed")

// agentRunPolicyContext 是运行策略锁定的会话上下文：会话、服务周期及其来源、AI 员工的参与者与聊天主体和外发目标。
type agentRunPolicyContext struct {
	Conversation       *servermodels.Conversation
	ServiceSession     *servermodels.ServiceSession
	ServiceSource      domain.ServiceSource // 服务周期所属服务会话的来源，只在服务周期执行范围取值。
	AgentParticipantID string
	AgentSubjectID     string
	DeliveryRoute      deliveryaction.Route
}

// agentRunPolicy 定义一类执行范围的运行策略：锁定上下文、执行门禁、上下文读取、结果写入、轮转资格与场景。
type agentRunPolicy interface {
	lockContext(context.Context, bun.IDB, *servermodels.AgentRun) (agentRunPolicyContext, error)
	prepareLocked(context.Context, bun.IDB, agentRunPolicyContext, *servermodels.AgentRun) (bool, error)
	loadMessages(context.Context, bun.IDB, *servermodels.AgentRun, int64, serverfilecontent.Links) ([]agentcontract.Message, error)
	persistMessage(context.Context, bun.IDB, agentRunPolicyContext, *servermodels.AgentRun, string, domain.MessageType, string) error
	laneRevision(context.Context, bun.IDB, agentRunPolicyContext, *servermodels.AgentLane) (string, bool, error)
	sceneContext(context.Context, bun.IDB, executionContext) (agentruntime.SceneContext, error)
}

// decisionPolicy 由按运行结束方式更新业务状态的执行范围实现，在结果消息写入后调用。
type decisionPolicy interface {
	applyDecision(context.Context, bun.IDB, agentRunPolicyContext, *servermodels.AgentRun, *servermodels.AgentLane, agentruntime.RunResult, string) error
}

// customerHistoryPolicy 由关联客户会话的执行范围实现，给出检索同一客户历史沟通时作为锚点的客服周期。
type customerHistoryPolicy interface {
	historyServiceSession(context.Context, bun.IDB, *servermodels.AgentRun) (string, error)
}

// mentionReplyPolicy 由从回复正文提取点名成员的执行范围实现。
type mentionReplyPolicy interface {
	applyMentions(context.Context, bun.IDB, agentRunPolicyContext, *servermodels.AgentRun, string, string) error
}

// lockedAgentRun 是事务中锁定的策略上下文、输入队列与运行记录。
type lockedAgentRun struct {
	PolicyContext agentRunPolicyContext
	Lane          *servermodels.AgentLane
	Run           *servermodels.AgentRun
}

// databaseInputFeed 以数据库中的持久输入队列为运行提供输入。
type databaseInputFeed struct {
	db          *bun.DB
	enqueuer    servertask.TxEnqueuer
	execution   executionContext
	policy      agentRunPolicy
	attachments *AttachmentReader
}

// Pending 返回可认领的最新输入序号：队列中尚未处理、大于 afterSeq 且不越过目标序号的输入，没有时返回 0。
func (f *databaseInputFeed) Pending(ctx context.Context, afterSeq int64) (int64, error) {
	var pending struct {
		Latest    sql.NullInt64 `bun:"latest"`
		Desired   int64         `bun:"desired_seq"`
		Processed int64         `bun:"processed_seq"`
	}
	err := f.db.NewSelect().TableExpr("agent_lanes AS al").
		ColumnExpr("al.desired_seq, al.processed_seq").
		ColumnExpr("(SELECT MAX(ai.input_seq) FROM agent_inputs AS ai WHERE ai.lane_id = al.id AND ai.input_seq > al.processed_seq) AS latest").
		Where("al.id = ? AND al.workspace_id = ?", f.execution.Run.LaneID, f.execution.Run.WorkspaceID).
		Scan(ctx, &pending)
	if err != nil {
		return 0, fmt.Errorf("check pending agent inputs: %w", err)
	}
	latest := min(pending.Latest.Int64, pending.Desired)
	if !pending.Latest.Valid || latest <= max(afterSeq, pending.Processed) {
		return 0, nil
	}
	return latest, nil
}

// Watch 订阅运行所属输入队列的新增输入信号。
func (f *databaseInputFeed) Watch(context.Context) (<-chan struct{}, func(), error) {
	signals, stop := realtime.WatchAgentLaneInputs(f.execution.Run.WorkspaceID, f.execution.Run.LaneID)
	return signals, stop, nil
}

// Claim 绑定当前所有已持久化输入，并按运行策略重建截至该边界的会话上下文。
func (f *databaseInputFeed) Claim(ctx context.Context, throughSeq int64) (einorun.Claim, error) {
	if throughSeq <= 0 {
		return einorun.Claim{}, errors.New("agent input sequence is invalid")
	}
	links := f.attachments.links
	var output einorun.Claim
	var previousEndSeq int64
	suppressed := false
	err := realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
		locked, err := lockAgentRun(ctx, tx, f.policy, &f.execution.Run, false)
		if err != nil {
			return fmt.Errorf("lock agent input: %w", err)
		}
		policyContext, lane, run := locked.PolicyContext, locked.Lane, locked.Run
		if agentRunStatusTerminal(run.Status) {
			suppressed = true
			return nil
		}
		allowed, err := f.policy.prepareLocked(ctx, tx, policyContext, run)
		if err != nil {
			return err
		}
		if !allowed {
			suppressed = true
			if err := chatstate.TouchConversation(ctx, tx, policyContext.Conversation, domain.ConversationChangeTimeline|domain.ConversationChangeService); err != nil {
				return err
			}
			return scheduleNextRun(ctx, tx, f.enqueuer, f.policy, policyContext, run.WorkspaceID, domain.AgentExecutionScopeKind(run.ScopeKind), run.ScopeID)
		}
		if run.Status != string(domain.AgentRunStatusRunning) || lane.DesiredSeq <= lane.ProcessedSeq {
			return errors.New("agent run has no claimable input")
		}
		previousEndSeq = support.DerefOr(run.InputEndSeq, previousEndSeq)
		claimEnd := min(throughSeq, lane.DesiredSeq)
		if claimEnd <= lane.ProcessedSeq || claimEnd < run.InputStartSeq {
			return errors.New("agent run input boundary is not claimable")
		}
		claimedSeqs, err := claimLaneInputs(ctx, tx, run, lane.ProcessedSeq, claimEnd)
		if err != nil {
			return fmt.Errorf("claim agent inputs: %w", err)
		}
		if int64(len(claimedSeqs)) != claimEnd-lane.ProcessedSeq {
			return errors.New("agent input sequence is not contiguous")
		}
		if _, err := tx.NewUpdate().Model(run).
			Set("input_start_seq = LEAST(input_start_seq, ?)", lane.ProcessedSeq+1).
			Set("input_end_seq = ?", claimEnd).
			WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("update agent run input boundary: %w", err)
		}
		messages, err := f.policy.loadMessages(ctx, tx, run, claimEnd, links)
		if err != nil {
			return err
		}
		if len(messages) == 0 {
			return errors.New("agent run has no context messages")
		}
		output = einorun.Claim{Messages: feedMessages(messages), EndSeq: claimEnd}
		return nil
	})
	if err != nil {
		return einorun.Claim{}, err
	}
	if suppressed {
		return einorun.Claim{}, errAgentRunSuppressed
	}
	if domain.AgentExecutionScopeKind(f.execution.Run.ScopeKind) == domain.AgentExecutionScopeServiceSession {
		slog.InfoContext(ctx, "客户 Agent 输入已认领",
			"agent_run_id", f.execution.Run.ID,
			"conversation_id", f.execution.Run.ConversationID,
			"service_session_id", f.execution.Run.ScopeID,
			"input_start_seq", f.execution.Run.InputStartSeq,
			"previous_end_seq", previousEndSeq,
			"input_end_seq", output.EndSeq,
			"context_message_count", len(output.Messages),
		)
	}
	return output, nil
}

// feedMessages 把会话上下文消息转换为运行输入：附件以消息编号为键直传，系统给出的事实消息带上事实标记。
func feedMessages(messages []agentcontract.Message) []einorun.Message {
	return arr.Map(messages, func(message agentcontract.Message) einorun.Message {
		converted := einorun.Message{ID: message.ID, Revision: message.Revision, Role: einorun.Role(message.Role), Content: message.Content}
		if media := message.Media; media != nil {
			converted.Media = &einorun.MediaRef{Key: agentcontract.MediaKeyMessage + message.ID, MIME: media.MIMEType, Size: media.ByteSize}
		}
		if message.Fact {
			converted.Meta = map[string]string{agentcontract.MetaFact: "true"}
		}
		return converted
	})
}

// lockAgentRun 按策略会话上下文、输入队列和运行记录的顺序取得事务锁，并校验当前任务尝试未被更新的尝试取代；claim 为真表示本次尝试开始认领运行。
func lockAgentRun(ctx context.Context, db bun.IDB, policy agentRunPolicy, initial *servermodels.AgentRun, claim bool) (lockedAgentRun, error) {
	policyContext, err := policy.lockContext(ctx, db, initial)
	if err != nil {
		return lockedAgentRun{}, err
	}
	lane := &servermodels.AgentLane{}
	if err := db.NewSelect().Model(lane).
		Where("al.id = ?", initial.LaneID).
		Where("al.workspace_id = ?", initial.WorkspaceID).
		For("UPDATE").Scan(ctx); err != nil {
		return lockedAgentRun{}, fmt.Errorf("lock agent lane: %w", err)
	}
	run := &servermodels.AgentRun{}
	if err := db.NewSelect().Model(run).Where("agr.id = ?", initial.ID).For("UPDATE").Scan(ctx); err != nil {
		return lockedAgentRun{}, err
	}
	if !agentRunStatusTerminal(run.Status) {
		if err := checkExecution(ctx, run, claim); err != nil {
			return lockedAgentRun{}, err
		}
	}
	return lockedAgentRun{PolicyContext: policyContext, Lane: lane, Run: run}, nil
}

// claimLaneInputs 把连续范围内的队列输入绑定到运行。
func claimLaneInputs(ctx context.Context, db bun.IDB, run *servermodels.AgentRun, afterSeq, throughSeq int64) ([]int64, error) {
	claimedSeqs := make([]int64, 0, throughSeq-afterSeq)
	err := db.NewRaw(`
		UPDATE agent_inputs
		SET agent_run_id = ?
		WHERE lane_id = ?
			AND input_seq > ?
			AND input_seq <= ?
		RETURNING input_seq
	`, run.ID, run.LaneID, afterSeq, throughSeq).Scan(ctx, &claimedSeqs)
	return claimedSeqs, err
}

// messageBoundary 是本次认领可见的会话消息序号上界。
type messageBoundary struct {
	MessageSeq int64 `bun:"message_seq"`
}

// loadClaimedMessageBoundary 读取本次认领可见的消息上界：取会话当前最新消息，且不越过本队列尚未认领的输入。
func loadClaimedMessageBoundary(ctx context.Context, db bun.IDB, run *servermodels.AgentRun, endSeq int64) (messageBoundary, error) {
	boundary := messageBoundary{}
	if err := db.NewRaw(`
		SELECT COALESCE(
			(
				SELECT MIN(pending_msg.message_seq) - 1
				FROM agent_inputs AS pending
				JOIN messages AS pending_msg ON pending_msg.id = pending.source_message_id AND pending_msg.workspace_id = pending.workspace_id
				WHERE pending.lane_id = ? AND pending.input_seq > ?
			),
			(
				SELECT MAX(latest.message_seq)
				FROM messages AS latest
				WHERE latest.workspace_id = ? AND latest.conversation_id = ?
			)
		) AS message_seq
	`, run.LaneID, endSeq, run.WorkspaceID, run.ConversationID).Scan(ctx, &boundary); err != nil {
		return messageBoundary{}, fmt.Errorf("load claimed input boundary: %w", err)
	}
	return boundary, nil
}

// claimedMessageRow 是读取会话上下文的一行：消息、发送者、引用与附件。
type claimedMessageRow struct {
	ID             string `bun:"id"`
	Body           string `bun:"body"`
	SenderSourceID string `bun:"sender_source_id"`
	SenderName     string `bun:"sender_name"`
	claimedReplyRow
	contextAttachmentRow
}

// claimedReplyRow 是上下文消息引用的一层消息摘要与发送者。
type claimedReplyRow struct {
	ReplyToMessageID *string `bun:"reply_to_message_id"`
	ReplyBody        string  `bun:"reply_body"`
	ReplySenderID    string  `bun:"reply_sender_id"`
	ReplySenderName  string  `bun:"reply_sender_name"`
	ReplyDeleted     bool    `bun:"reply_deleted"`
}

// withClaimedReply 为以 msg 为别名的消息查询补充一层引用消息的摘要、发送者与删除状态。
func withClaimedReply(query *bun.SelectQuery) *bun.SelectQuery {
	return query.
		ColumnExpr("msg.reply_to_message_id").
		ColumnExpr("? AS reply_body", messagequery.Summary("reply")).
		ColumnExpr("COALESCE(reply_cs.source_id::text, '') AS reply_sender_id").
		ColumnExpr("COALESCE(reply_oi.display_name, '') AS reply_sender_name").
		ColumnExpr("reply.deleted_at IS NOT NULL AS reply_deleted").
		Join("LEFT JOIN messages AS reply ON reply.id = msg.reply_to_message_id AND reply.workspace_id = msg.workspace_id AND reply.conversation_id = msg.conversation_id AND reply.type IN (?, ?)", domain.MessageTypeText, domain.MessageTypeAttachment).
		Join("LEFT JOIN conversation_participants AS reply_cp ON reply_cp.id = reply.sender_participant_id AND reply_cp.workspace_id = reply.workspace_id AND reply_cp.conversation_id = reply.conversation_id").
		Join("LEFT JOIN chat_subjects AS reply_cs ON reply_cs.id = reply_cp.subject_id AND reply_cs.workspace_id = reply_cp.workspace_id AND reply_cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("LEFT JOIN workspace_identities AS reply_oi ON reply_oi.id = reply_cs.source_id AND reply_oi.workspace_id = reply_cs.workspace_id")
}

// claimedMessageReference 是上下文消息结构化正文中的一层引用。
type claimedMessageReference struct {
	MessageID  string             `json:"messageId"`
	SenderID   string             `json:"senderIdentityId,omitempty"`
	SenderName string             `json:"senderName,omitempty"`
	Body       string             `json:"body,omitempty"`
	Attachment *contextAttachment `json:"attachment,omitempty"`
	Deleted    bool               `json:"deleted,omitempty"`
}

// loadClaimedConversationMessages 读取不越过已认领输入的最近会话上下文并并入提交给本 AI 员工的工具调用结果事件，withSender 为 true 时成员消息携带发送者名称。
func loadClaimedConversationMessages(ctx context.Context, db bun.IDB, run *servermodels.AgentRun, endSeq int64, links serverfilecontent.Links, withSender bool) ([]agentcontract.Message, error) {
	boundary, err := loadClaimedMessageBoundary(ctx, db, run, endSeq)
	if err != nil {
		return nil, err
	}
	rows := make([]claimedMessageRow, 0, agentHistoryLimit)
	if err := db.NewSelect().TableExpr("messages AS msg").
		ColumnExpr("msg.id, msg.body").
		ColumnExpr("cs.source_id AS sender_source_id").
		ColumnExpr("COALESCE(sender_oi.display_name, '') AS sender_name").
		Join("JOIN conversation_participants AS cp ON cp.id = msg.sender_participant_id AND cp.workspace_id = msg.workspace_id AND cp.conversation_id = msg.conversation_id").
		Join("JOIN chat_subjects AS cs ON cs.id = cp.subject_id AND cs.workspace_id = cp.workspace_id AND cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("LEFT JOIN workspace_identities AS sender_oi ON sender_oi.id = cs.source_id AND sender_oi.workspace_id = cs.workspace_id").
		Apply(withClaimedReply).
		Apply(withContextAttachments).
		Where("msg.workspace_id = ?", run.WorkspaceID).
		Where("msg.conversation_id = ?", run.ConversationID).
		Where("msg.visibility = ?", domain.MessageVisibilityShared).
		Where("msg.deleted_at IS NULL").
		Where("msg.message_seq <= ?", boundary.MessageSeq).
		OrderExpr("msg.message_seq DESC").
		Limit(agentHistoryLimit).
		Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("load claimed conversation context: %w", err)
	}
	slices.Reverse(rows)
	messages := make([]agentcontract.Message, 0, len(rows))
	for _, row := range rows {
		role := agentcontract.MessageRoleUser
		if row.SenderSourceID == run.AgentIdentityID {
			role = agentcontract.MessageRoleAssistant
		}
		content := row.Body
		attachment := row.attachment(ctx, row.ID, links)
		var sender *groupMessageSender
		if withSender && role == agentcontract.MessageRoleUser {
			sender = &groupMessageSender{Name: row.SenderName, Kind: string(domain.WorkspaceIdentityTypeUser)}
		}
		// 在同一对话消息的结构化正文中携带发送者、附件描述和一层引用。
		if sender != nil || row.ReplyToMessageID != nil || attachment != nil {
			var replyTo *claimedMessageReference
			if row.ReplyToMessageID != nil {
				replyTo = &claimedMessageReference{MessageID: *row.ReplyToMessageID, Deleted: row.ReplyDeleted}
				if !row.ReplyDeleted {
					replyTo.SenderID, replyTo.SenderName, replyTo.Body = row.ReplySenderID, row.ReplySenderName, row.ReplyBody
					replyTo.Attachment = row.replyAttachment(ctx, links)
				}
			}
			encoded, _ := json.Marshal(struct {
				Sender     *groupMessageSender      `json:"sender,omitempty"`
				Body       string                   `json:"body"`
				Attachment *contextAttachment       `json:"attachment,omitempty"`
				ReplyTo    *claimedMessageReference `json:"replyTo,omitempty"`
			}{Sender: sender, Body: row.Body, Attachment: attachment, ReplyTo: replyTo})
			content = string(encoded)
		}
		messages = append(messages, agentcontract.Message{ID: row.ID, Revision: row.revision(), Role: role, Content: content, Media: row.media()})
	}
	return mergeToolCallEvents(ctx, db, run, messages, boundary.MessageSeq)
}
