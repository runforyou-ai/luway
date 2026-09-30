//go:build server

package agentrun

import (
	"context"
	"fmt"
	"slices"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	"github.com/runforyou-ai/cervi/internal/common/customeridentity"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/agentruntime"
	mcpintegration "github.com/runforyou-ai/cervi/internal/integration/mcp"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// runMCPServers 表示本次运行挂载的 MCP 服务，以及是否有按客户查询的服务因客户未验证而未挂载。
type runMCPServers struct {
	Servers               []agentruntime.MCPServer
	CustomerLoginRequired bool
}

// mcpMount 描述一次运行挂载 MCP 服务的场景：Service 表示服务场景，只挂载查询工具；Customer 非空表示可挂载按客户查询的服务，并由它读取已验证客户。
type mcpMount struct {
	Service  bool
	Customer func(context.Context) (ServiceSessionCustomer, error)
}

// loadRunMCPServers 读取本次运行的配置版本绑定且仍存在的同企业 MCP 服务；按客户查询的服务只在渠道来源的服务周期挂载。
func loadRunMCPServers(ctx context.Context, db bun.IDB, run *servermodels.AgentRun) (runMCPServers, error) {
	mount := mcpMount{Service: domain.AgentExecutionScopeKind(run.ScopeKind) == domain.AgentExecutionScopeServiceSession}
	if mount.Service {
		service, err := chatstate.LoadServiceConversation(ctx, db, run.OrganizationID, run.ConversationID)
		if err != nil {
			return runMCPServers{}, err
		}
		if domain.ServiceSource(service.Source) == domain.ServiceSourceChannel {
			mount.Customer = func(ctx context.Context) (ServiceSessionCustomer, error) {
				return LoadServiceSessionCustomer(ctx, db, run.OrganizationID, run.ScopeID)
			}
		}
	}
	return loadMCPServers(ctx, db, run.OrganizationID, run.AgentRevisionID, mount)
}

// loadMCPServers 读取配置版本绑定且仍存在的同企业 MCP 服务。
// 按客户查询的服务只在可读取客户时挂载：客户已验证时附加客户请求头，未验证时不挂载。
// 服务场景只挂载标记为查询的工具；按客户查询的服务排在前面，已挂载的按客户查询服务提供的工具名，其他服务的同名工具不再挂载。
func loadMCPServers(ctx context.Context, db bun.IDB, organizationID, revisionID string, mount mcpMount) (runMCPServers, error) {
	services := make([]servermodels.MCPServer, 0)
	err := db.NewSelect().Model(&services).
		Join("JOIN agent_revisions AS ar ON ar.id = ? AND ar.organization_id = ms.organization_id", revisionID).
		Where("ms.organization_id = ?", organizationID).
		Where("ar.configuration->'mcpServerIds' @> jsonb_build_array(ms.id::text)").
		OrderExpr("ms.customer_scoped DESC, ms.name").Scan(ctx)
	if err != nil {
		return runMCPServers{}, err
	}
	loaded := runMCPServers{Servers: make([]agentruntime.MCPServer, 0, len(services))}
	var customer *ServiceSessionCustomer
	customerTools := map[string]bool{}
	for _, service := range services {
		server := agentruntime.MCPServer{Source: agentruntime.MCPSourceOrganization, ID: service.ID, Name: service.Name, Config: mcpintegration.Config{
			URL: service.URL, ServerType: service.ServerType, AuthorizationToken: service.AuthorizationToken,
		}}
		if !mount.Service {
			if !service.CustomerScoped {
				loaded.Servers = append(loaded.Servers, server)
			}
			continue
		}
		if service.CustomerScoped && mount.Customer == nil {
			continue
		}
		// 按名称顺序收集查询工具，跳过已由按客户查询服务提供的同名工具，没有查询工具的服务不挂载。
		server.Tools = make([]string, 0, len(service.ToolPurposes))
		for name, purpose := range service.ToolPurposes {
			if purpose == domain.MCPToolPurposeQuery && !customerTools[name] {
				server.Tools = append(server.Tools, name)
			}
		}
		if len(server.Tools) == 0 {
			continue
		}
		slices.Sort(server.Tools)
		if service.CustomerScoped {
			if customer == nil {
				loadedCustomer, err := mount.Customer(ctx)
				if err != nil {
					return runMCPServers{}, err
				}
				customer = &loadedCustomer
			}
			if customer.UserID == "" {
				loaded.CustomerLoginRequired = true
				continue
			}
			server.Config.Headers = map[string]string{mcpintegration.CustomerIDHeader: customer.UserID}
			if customer.Email != "" {
				server.Config.Headers[mcpintegration.CustomerEmailHeader] = customer.Email
			}
			for _, name := range server.Tools {
				customerTools[name] = true
			}
		}
		loaded.Servers = append(loaded.Servers, server)
	}
	return loaded, nil
}

// ServiceSessionCustomer 表示渠道来源客服周期的已验证客户，未验证时企业用户编号为空。
type ServiceSessionCustomer struct {
	UserID string
	Email  string
}

// LoadServiceSessionCustomer 读取渠道来源客服周期的渠道身份对应的已验证客户与其主要邮箱，判定与客户上下文消息一致。
func LoadServiceSessionCustomer(ctx context.Context, db bun.IDB, organizationID, serviceSessionID string) (ServiceSessionCustomer, error) {
	row := struct {
		ExternalID     string  `bun:"external_id"`
		ExternalUserID *string `bun:"external_user_id"`
		Email          *string `bun:"email"`
	}{}
	if err := db.NewSelect().
		TableExpr("service_sessions AS ss").
		ColumnExpr("cci.external_id, c.external_user_id").
		ColumnExpr("(SELECT cm.value FROM contact_methods AS cm WHERE cm.organization_id = c.organization_id AND cm.contact_id = c.id AND cm.type = ? AND cm.is_primary) AS email", domain.ContactMethodTypeEmail).
		Join("JOIN channel_conversations AS cc ON cc.organization_id = ss.organization_id AND cc.conversation_id = ss.conversation_id").
		Join("JOIN contact_channel_identities AS cci ON cci.id = cc.contact_channel_identity_id AND cci.organization_id = cc.organization_id").
		Join("JOIN contacts AS c ON c.id = cci.contact_id AND c.organization_id = cci.organization_id").
		Where("ss.organization_id = ?", organizationID).
		Where("ss.id = ?", serviceSessionID).
		Scan(ctx, &row); err != nil {
		return ServiceSessionCustomer{}, fmt.Errorf("load service session customer: %w", err)
	}
	customer := ServiceSessionCustomer{}
	if row.ExternalUserID != nil && customeridentity.IsCustomerExternalID(row.ExternalID) {
		customer.UserID = *row.ExternalUserID
		if row.Email != nil {
			customer.Email = *row.Email
		}
	}
	return customer, nil
}
