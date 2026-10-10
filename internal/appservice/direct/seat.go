//go:build server

package direct

import (
	"context"

	seataction "github.com/runforyou-ai/luway/internal/actions/seat"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// seatOps 持有工作区席位的读取。
type seatOps struct {
	db    *bun.DB
	seats seataction.Seats
}

// newSeatOps 创建工作区席位的业务实现依赖。
func newSeatOps(db *bun.DB, seats seataction.Seats) *seatOps {
	return &seatOps{db: db, seats: seats}
}

// GetWorkspaceSeats 返回当前工作区的席位上限与启用的成员数。
func (o *seatOps) GetWorkspaceSeats(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.WorkspaceSeats, error) {
	usage, err := o.seats.Usage(ctx, o.db, identity.Workspace.ID)
	if err != nil {
		return appservice.WorkspaceSeats{}, appservice.FailedError(meta, i18n.ErrorWorkspaceSeatsReadFailed, err)
	}
	return appservice.WorkspaceSeats{Limit: usage.Limit, Used: usage.Used}, nil
}
