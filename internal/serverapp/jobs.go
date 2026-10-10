//go:build server

package serverapp

import (
	"context"
	"errors"
	serverinstanceaction "github.com/runforyou-ai/luway/internal/actions/serverinstance"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/runforyou-ai/luway/internal/realtime/broker"
	"github.com/uptrace/bun"
)

// jobsNATS 是任务队列与成员推送共用的 NATS 连接。
type jobsNATS = broker.Connection

// connectJobsNATS 检查部署模式并创建任务与成员推送的 NATS 连接。
func connectJobsNATS(ctx context.Context, db bun.IDB, config serverconfig.Config, instanceID, hostname string) (*jobsNATS, error) {
	conflict, err := serverinstanceaction.EmbeddedNATSConflict(ctx, db, instanceID, hostname, config.NATS.URL == "")
	if err != nil {
		return nil, err
	}
	if conflict {
		return nil, errors.New("embedded NATS only supports a single server: configure nats.url on every server of this deployment")
	}
	return broker.Open(config, instanceID)
}
