//go:build server

package businesssystem

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// RunValues 是一次运行能提供的可信上下文值，运行无法提供的值不出现。
type RunValues map[domain.ContextValue]string

// MountOptions 定义一次运行挂载业务系统工具的条件。
type MountOptions struct {
	Grants []domain.BusinessSystemGrant
	Values RunValues
	// ReadOnly 为 true 时只挂载只读工具，用于不产生外部影响的回放。
	ReadOnly bool
	// Interventions 是本次运行有成员可以处理的人工介入，需要其他人工介入的工具不挂载。
	Interventions []domain.ToolIntervention
}

// RunTools 是本次运行挂载的业务系统，以及是否有工具因客户未核验身份而未挂载。
type RunTools struct {
	Systems               []agentcontract.BusinessSystem
	CustomerLoginRequired bool
}

// Names 按挂载顺序返回业务系统名称。
func (t RunTools) Names() []string {
	return arr.Map(t.Systems, func(system agentcontract.BusinessSystem) string { return system.Name })
}

// LoadRunTools 读取授权中仍存在的同工作区业务系统，按名称顺序给出本次运行挂载的工具，并经 connector 为每个业务系统建立调用会话；
// ctx 控制调用会话的生命周期，运行结束时由运行时关闭会话。
func LoadRunTools(ctx context.Context, db bun.IDB, connector *Connector, workspaceID string, options MountOptions) (RunTools, error) {
	loaded := RunTools{Systems: make([]agentcontract.BusinessSystem, 0)}
	if len(options.Grants) == 0 {
		return loaded, nil
	}
	grants := arr.KeyBy(options.Grants, func(grant domain.BusinessSystemGrant) string { return grant.BusinessSystemID })
	systems := make([]servermodels.BusinessSystem, 0)
	if err := db.NewSelect().Model(&systems).
		Where("bs.workspace_id = ?", workspaceID).
		Where("bs.id IN (?)", bun.List(slices.Collect(maps.Keys(grants)))).
		OrderExpr("lower(bs.name) ASC, bs.id ASC").Scan(ctx); err != nil {
		return RunTools{}, fmt.Errorf("load run business systems: %w", err)
	}
	for _, system := range systems {
		mounted, headers, customerMissing := Mount(system, grants[system.ID], options)
		loaded.CustomerLoginRequired = loaded.CustomerLoginRequired || customerMissing
		if len(mounted.Tools) > 0 {
			mounted.Caller = connector.Open(ctx, system, headers)
			loaded.Systems = append(loaded.Systems, mounted)
		}
	}
	return loaded, nil
}

// Mount 按授权、工具事实与可信上下文值给出业务系统在本次运行中挂载的工具与每个请求附加的凭据和绑定请求头，工具定义取自保存的工具目录：
// 停用、超出授权、需要本次运行无人处理的人工介入或缺少绑定值的工具不挂载；customerMissing 表示有获准的工具因客户未核验身份（缺少客户编号）而未挂载。
func Mount(system servermodels.BusinessSystem, grant domain.BusinessSystemGrant, options MountOptions) (mounted agentcontract.BusinessSystem, headers map[string]string, customerMissing bool) {
	mounted = agentcontract.BusinessSystem{ID: system.ID, Name: system.Name, Tools: make([]agentcontract.BusinessToolMount, 0)}
	// 绑定请求头在整次运行内固定，缺少任一绑定值时整个业务系统不挂载。
	bound, missing := bindValues(system.HeaderBindings, options.Values)
	headers = credentialHeaders(system.Credential)
	maps.Copy(headers, bound)
	for _, tool := range system.Tools {
		setting := system.ToolSettings[tool.Name]
		if setting.Disabled {
			continue
		}
		facts := domain.EffectiveToolFacts(tool, setting)
		if options.ReadOnly && !facts.ReadOnly {
			continue
		}
		level := domain.ToolLevel(facts, domain.IdentityBound(system.HeaderBindings, setting))
		permitted, intervention := grant.Permit(level, facts.Outbound)
		if !permitted || intervention != domain.ToolInterventionNone && !slices.Contains(options.Interventions, intervention) {
			continue
		}
		parameters, toolMissing := bindValues(setting.ParameterBindings, options.Values)
		toolMissing = append(toolMissing, missing...)
		if len(toolMissing) > 0 {
			// 缺少客户编号表示客户未核验身份；已核验客户缺少邮箱等其他值时不提示登录。
			customerMissing = customerMissing || slices.Contains(toolMissing, domain.ContextValueCustomerUserID)
			continue
		}
		mounted.Tools = append(mounted.Tools, agentcontract.BusinessToolMount{
			Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema,
			Bound: parameters, ReadOnly: facts.ReadOnly, ToolPolicy: agentcontract.ToolPolicy{Level: level, Intervention: intervention},
		})
	}
	return mounted, headers, customerMissing
}

// bindValues 按绑定取出可信上下文值，返回已取得的值与缺少的上下文值。
func bindValues[K comparable](bindings map[K]domain.ContextValue, values RunValues) (map[K]string, []domain.ContextValue) {
	bound := make(map[K]string, len(bindings))
	missing := make([]domain.ContextValue, 0)
	for key, source := range bindings {
		value, ok := values[source]
		if !ok || value == "" {
			missing = append(missing, source)
			continue
		}
		bound[key] = value
	}
	return bound, missing
}
