//go:build server

package reportpage

import (
	"context"
	"database/sql"
	"errors"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// ErrServiceScopeInvalid 表示服务报表的服务对象、来源与渠道组合无效。
var ErrServiceScopeInvalid = errors.New("invalid service report scope")

// ValidateServiceScope 校验报表服务对象、来源和渠道所属对象的一致性。
func ValidateServiceScope(ctx context.Context, db bun.IDB, workspaceID string, audience domain.ServiceAudience, source domain.ServiceSource, channelID string) error {
	if audience != "" && audience != domain.ServiceAudienceCustomer && audience != domain.ServiceAudienceEmployee {
		return ErrServiceScopeInvalid
	}
	if source != "" && source != domain.ServiceSourceChannel && source != domain.ServiceSourceDirect {
		return ErrServiceScopeInvalid
	}
	if source == domain.ServiceSourceDirect && (channelID != "" || audience == domain.ServiceAudienceCustomer) {
		return ErrServiceScopeInvalid
	}
	if channelID == "" {
		return nil
	}
	var kind domain.ChannelType
	if err := db.NewSelect().TableExpr("channels").Column("type").Where("workspace_id = ? AND id = ?", workspaceID, channelID).Scan(ctx, &kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrServiceScopeInvalid
		}
		return err
	}
	if audience != "" && domain.ChannelCapabilitiesOf(kind).Audience != audience {
		return ErrServiceScopeInvalid
	}
	return nil
}
