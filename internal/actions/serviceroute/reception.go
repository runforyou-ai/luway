//go:build server

package serviceroute

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

// Reception 定义访客端展示的接待方、在线状态与回复预期；HandlerType 为空表示由团队或公共队列接待，NextOpeningAt 只在回复预期为 scheduled 时有值。
type Reception struct {
	HandlerType   *domain.WorkspaceIdentityType
	HandlerName   string
	HandlerAvatar *ReceptionAvatar
	Online        bool
	Reply         domain.CustomerReceptionReply
	NextOpeningAt *time.Time
}

// ReceptionAvatar 定义接待方头像文件的存储位置。
type ReceptionAvatar struct {
	StorageBackend domain.FileStorageBackend
	StorageKey     string
}

const (
	// replyEstimateWindow 是估计队列回复时长所取的真人首响统计窗口。
	replyEstimateWindow = 7 * 24 * time.Hour
	// replyEstimateMinSamples 是估计队列回复时长所需的最少样本数。
	replyEstimateMinSamples = 5
)

// ReceptionResolver 在一次读取内解析同一企业的访客端接待状态，工作时间、各队列在线情况、回复时长估计与各接待身份只读取一次。
type ReceptionResolver struct {
	db          bun.IDB
	workspaceID string
	now         time.Time
	hours       *domain.BusinessHours
	queueOnline map[string]bool
	queueReply  map[string]domain.CustomerReceptionReply
	identities  map[string]*receptionIdentityRow
}

// receptionIdentityRow 定义接待身份及其头像文件。
type receptionIdentityRow struct {
	Type          domain.WorkspaceIdentityType `bun:"type"`
	DisplayName   string                       `bun:"display_name"`
	WorkStatus    domain.WorkStatus            `bun:"work_status"`
	AvatarBackend *domain.FileStorageBackend   `bun:"avatar_storage_backend"`
	AvatarKey     *string                      `bun:"avatar_storage_key"`
}

// NewReceptionResolver 创建指定企业的接待状态解析器。
func NewReceptionResolver(db bun.IDB, workspaceID string) *ReceptionResolver {
	return &ReceptionResolver{
		db: db, workspaceID: workspaceID,
		queueOnline: map[string]bool{}, queueReply: map[string]domain.CustomerReceptionReply{}, identities: map[string]*receptionIdentityRow{},
	}
}

// RefreshAt 返回接待状态随工作时间开关可能变化的下一时刻，未启用工作时间时返回空。
func (r *ReceptionResolver) RefreshAt(ctx context.Context) (*time.Time, error) {
	hours, err := r.businessHours(ctx)
	if err != nil {
		return nil, err
	}
	if next, ok := hours.NextChange(r.now); ok {
		return &next, nil
	}
	return nil, nil
}

// businessHours 返回企业客服工作时间并记下判断工作时间所用的数据库时刻，同一解析器只读取一次。
func (r *ReceptionResolver) businessHours(ctx context.Context) (domain.BusinessHours, error) {
	if r.hours == nil {
		hours, err := customerserviceaction.LoadBusinessHours(ctx, r.db, r.workspaceID)
		if err != nil {
			return domain.BusinessHours{}, err
		}
		if r.now, err = serverstorage.Now(ctx, r.db); err != nil {
			return domain.BusinessHours{}, err
		}
		r.hours = &hours
	}
	return *r.hours, nil
}

// ForNewSession 按新客服处理周期当前会进入的路由返回尚未开聊时的接待状态：AI 员工立即回复，工作中的真人尽快回复，团队与公共队列按队列在线情况与工作时间推导。
func (r *ReceptionResolver) ForNewSession(ctx context.Context, channel *servermodels.Channel) (Reception, error) {
	route, err := PeekNewSessionRoute(ctx, r.db, channel, "")
	if err != nil {
		return Reception{}, err
	}
	if route.AssigneeIdentityID != nil {
		reception, found, err := r.identityReception(ctx, *route.AssigneeIdentityID)
		if err != nil {
			return Reception{}, err
		}
		if found {
			// 新会话由真人首接待时尽快回复。
			if reception.Reply == domain.CustomerReceptionReplyNone {
				reception.Reply = domain.CustomerReceptionReplySoon
			}
			return reception, nil
		}
	}
	return r.queueReception(ctx, route.TeamID)
}

// ForServiceSession 返回客服处理周期当前的接待状态：有负责人时展示负责人，否则按所在队列推导；已关闭的周期不展示在线与回复预期。
func (r *ReceptionResolver) ForServiceSession(ctx context.Context, status domain.ServiceSessionStatus, teamID, assigneeIdentityID *string) (Reception, error) {
	reception, found := Reception{}, false
	if assigneeIdentityID != nil {
		var err error
		if reception, found, err = r.identityReception(ctx, *assigneeIdentityID); err != nil {
			return Reception{}, err
		}
	}
	if status == domain.ServiceSessionStatusClosed {
		return Reception{HandlerType: reception.HandlerType, HandlerName: reception.HandlerName, HandlerAvatar: reception.HandlerAvatar, Reply: domain.CustomerReceptionReplyNone}, nil
	}
	if found {
		return reception, nil
	}
	return r.queueReception(ctx, teamID)
}

// identityReception 返回负责人身份的接待状态：AI 员工始终在线并立即回复，真人工作中时在线且不展示回复预期；身份不存在时返回 false。
func (r *ReceptionResolver) identityReception(ctx context.Context, identityID string) (Reception, bool, error) {
	row, cached := r.identities[identityID]
	if !cached {
		loaded, err := r.loadIdentity(ctx, identityID)
		if err != nil {
			return Reception{}, false, err
		}
		row, r.identities[identityID] = loaded, loaded
	}
	if row == nil {
		return Reception{}, false, nil
	}
	reception := Reception{HandlerType: &row.Type, HandlerName: row.DisplayName, Reply: domain.CustomerReceptionReplyNone}
	if row.AvatarBackend != nil && row.AvatarKey != nil {
		reception.HandlerAvatar = &ReceptionAvatar{StorageBackend: *row.AvatarBackend, StorageKey: *row.AvatarKey}
	}
	if row.Type == domain.WorkspaceIdentityTypeAgent {
		reception.Online, reception.Reply = true, domain.CustomerReceptionReplyImmediate
	} else {
		reception.Online = row.WorkStatus == domain.WorkStatusWorking
	}
	return reception, true, nil
}

// loadIdentity 读取接待身份及其头像文件，身份不存在时返回空。
func (r *ReceptionResolver) loadIdentity(ctx context.Context, identityID string) (*receptionIdentityRow, error) {
	row := &receptionIdentityRow{}
	err := r.db.NewSelect().
		TableExpr("workspace_identities AS oi").
		ColumnExpr("oi.type, oi.display_name, oi.work_status").
		ColumnExpr("av.storage_backend AS avatar_storage_backend").
		ColumnExpr("av.storage_key AS avatar_storage_key").
		Join("LEFT JOIN files AS av ON av.id = oi.avatar_file_id AND av.workspace_id = oi.workspace_id AND av.status = ?", domain.FileStatusActive).
		Where("oi.workspace_id = ? AND oi.id = ?", r.workspaceID, identityID).
		Scan(ctx, row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load reception identity: %w", err)
	}
	return row, nil
}

// queueReception 返回团队或公共队列的接待状态：工作时间内且队列有工作中的接待成员时在线；在线时按队列近期按工作时间计的真人首响说明通常回复时长，样本不足、首响过长或离线时尽快回复；工作时间外在下个工作时段回复，没有后续工作时段时尽快回复。
func (r *ReceptionResolver) queueReception(ctx context.Context, teamID *string) (Reception, error) {
	hours, err := r.businessHours(ctx)
	if err != nil {
		return Reception{}, err
	}
	reception := Reception{Reply: domain.CustomerReceptionReplySoon}
	if !hours.Open(r.now) {
		if next, ok := hours.NextOpening(r.now); ok {
			reception.Reply, reception.NextOpeningAt = domain.CustomerReceptionReplyScheduled, &next
		}
		return reception, nil
	}
	key := support.Deref(teamID)
	online, cached := r.queueOnline[key]
	if !cached {
		// 队列内存在开启接待、账号有效且工作中的真人成员时视为在线。
		query := ApplyServiceHandlingUserConditions(r.db.NewSelect().
			TableExpr("workspace_identities AS oi").ColumnExpr("1").
			Where("oi.workspace_id = ? AND oi.type = ? AND oi.work_status = ?", r.workspaceID, domain.WorkspaceIdentityTypeUser, domain.WorkStatusWorking))
		if teamID != nil {
			query = query.Join("JOIN team_members AS tm ON tm.workspace_id = oi.workspace_id AND tm.identity_id = oi.id").
				Where("tm.team_id = ?", *teamID)
		}
		if online, err = query.Exists(ctx); err != nil {
			return Reception{}, fmt.Errorf("check reception queue online: %w", err)
		}
		r.queueOnline[key] = online
	}
	reception.Online = online
	if !online {
		return reception, nil
	}
	reply, cached := r.queueReply[key]
	if !cached {
		// 取该队列统计窗口内需要真人且有真人回复的周期，样本足够时按工作时间计的首响中位数取档位。
		var estimate struct {
			Samples int      `bun:"samples"`
			Median  *float64 `bun:"median"`
		}
		if err := r.db.NewSelect().TableExpr("service_sessions AS ss").
			ColumnExpr("count(*) AS samples").
			ColumnExpr("percentile_cont(0.5) WITHIN GROUP (ORDER BY ss.human_first_response_seconds) AS median").
			Where("ss.workspace_id = ? AND ss.team_id IS NOT DISTINCT FROM ?", r.workspaceID, teamID).
			Where("ss.human_first_response_seconds IS NOT NULL AND ss.human_requested_at >= now() - make_interval(secs => ?)", replyEstimateWindow.Seconds()).
			Scan(ctx, &estimate); err != nil {
			return Reception{}, fmt.Errorf("estimate reception queue reply: %w", err)
		}
		reply = domain.CustomerReceptionReplySoon
		if estimate.Samples >= replyEstimateMinSamples && estimate.Median != nil {
			reply = domain.CustomerReceptionReplyWithin(time.Duration(*estimate.Median * float64(time.Second)))
		}
		r.queueReply[key] = reply
	}
	reception.Reply = reply
	return reception, nil
}
