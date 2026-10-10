//go:build server

package modelcall

import (
	"context"
	"log/slog"
	"math"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/set"
	"github.com/uptrace/bun"
)

const (
	// breakerFailures 是来源熔断所需的最近连续上游失败或上游超时数。
	breakerFailures = 3
	// breakerCooldown 是来源最近一次失败后保持熔断的时长。
	breakerCooldown = time.Minute
)

// attemptOrder 返回一次调用的来源尝试顺序：权重大于 0 的来源按权重随机排序，权重为 0 的来源按优先级排在其后；熔断中的来源保持相对顺序移到最后。
func attemptOrder(routes []aimodel.Route, tripped set.Set[string]) []aimodel.Route {
	type keyed struct {
		route aimodel.Route
		key   float64
	}
	// 按 Efraimidis–Spirakis 加权无放回抽样，每个来源取 -ln(u)/权重 作排序键，键小的在前。
	weighted := make([]keyed, 0, len(routes))
	backups := make([]aimodel.Route, 0, len(routes))
	for _, route := range routes {
		if route.Weight <= 0 {
			backups = append(backups, route)
			continue
		}
		weighted = append(weighted, keyed{route: route, key: -math.Log(1-rand.Float64()) / float64(route.Weight)})
	}
	slices.SortStableFunc(weighted, func(a, b keyed) int {
		switch {
		case a.key < b.key:
			return -1
		case a.key > b.key:
			return 1
		default:
			return 0
		}
	})
	order := append(arr.Map(weighted, func(item keyed) aimodel.Route { return item.route }), backups...)
	healthy := arr.Filter(order, func(route aimodel.Route) bool { return !tripped.Has(route.ID) })
	return append(healthy, arr.Filter(order, func(route aimodel.Route) bool { return tripped.Has(route.ID) })...)
}

// trippedRoutes 返回熔断中的来源：按结束时间最近的 breakerFailures 次已结束的上游尝试全部失败或上游超时，且最近一次在 breakerCooldown 内结束；
// 调用方取消、时限到期与进程中断的尝试记为已取消，不计入。冷却结束后来源重新按权重参与，再次失败即重新熔断。
func trippedRoutes(ctx context.Context, db bun.IDB, routes []aimodel.Route) set.Set[string] {
	var ids []string
	if len(routes) > 1 {
		err := db.NewSelect().TableExpr("ai_model_routes AS amr").
			ColumnExpr("amr.id::text").
			Join(`JOIN LATERAL (
				SELECT count(*) FILTER (WHERE recent.status <> ?) AS failed, max(recent.finished_at) AS last_finished_at
				FROM (
					SELECT a.status, a.finished_at FROM ai_model_call_attempts AS a
					WHERE a.route_id = amr.id AND a.status IN (?)
					ORDER BY a.finished_at DESC, a.id DESC
					LIMIT ?
				) AS recent
			) AS health ON true`,
				domain.AIModelCallStatusSucceeded,
				bun.List([]domain.AIModelCallStatus{domain.AIModelCallStatusSucceeded, domain.AIModelCallStatusFailed, domain.AIModelCallStatusTimedOut}),
				breakerFailures).
			Where("amr.id IN (?)", bun.List(arr.Map(routes, func(route aimodel.Route) string { return route.ID }))).
			Where("health.failed = ? AND health.last_finished_at > now() - make_interval(secs => ?)", breakerFailures, breakerCooldown.Seconds()).
			Scan(ctx, &ids)
		// 读取失败时按未熔断处理，调用仍按权重顺序尝试全部来源。
		if err != nil {
			slog.WarnContext(ctx, "读取模型来源熔断状态失败", "error", err)
		}
	}
	return set.Collect(ids)
}
