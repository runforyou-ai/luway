//go:build server

package organization

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	"github.com/uptrace/bun"
)

// CreateInput 定义创建工作区及首位管理员成员所需的已校验字段。
type CreateInput struct {
	Name             string
	Account          *servermodels.Account
	AdminDisplayName string
}

// Create 在调用方事务内创建工作区、客服设置、内置角色与默认权限，并把账号加为首位管理员成员。
func Create(ctx context.Context, tx bun.Tx, input CreateInput) (*servermodels.Identity, error) {
	// 工作区访问标识由服务端随机生成并在创建后保持固定。
	organization, err := insertOrganization(ctx, tx, input.Name, func() string {
		return "ws-" + strings.ToLower(rand.Text())
	})
	if err != nil {
		return nil, err
	}
	// 客服设置行随工作区创建，各项取列默认值。
	if _, err := tx.NewInsert().Model(&servermodels.CustomerServiceSetting{OrganizationID: organization.ID}).
		Column("organization_id").
		Exec(ctx); err != nil {
		return nil, err
	}

	var adminRoleID string
	for _, kind := range domain.BuiltInRoleKinds() {
		role := &servermodels.Role{OrganizationID: organization.ID, Kind: string(kind)}
		if _, err := tx.NewInsert().
			Model(role).
			Column("organization_id", "kind").
			Returning("id").
			Exec(ctx); err != nil {
			return nil, err
		}
		if kind == domain.RoleKindAdmin {
			adminRoleID = role.ID
		}
		permissions := domain.DefaultRolePermissions(kind)
		if len(permissions) == 0 {
			continue
		}
		records := make([]servermodels.RolePermission, 0, len(permissions))
		for _, permission := range permissions {
			records = append(records, servermodels.RolePermission{
				OrganizationID: organization.ID,
				RoleID:         role.ID,
				Permission:     string(permission),
			})
		}
		if _, err := tx.NewInsert().
			Model(&records).
			Column("organization_id", "role_id", "permission").
			Exec(ctx); err != nil {
			return nil, err
		}
	}

	// 工作区创建者默认开启处理服务请求，创建后即可处理服务会话。
	organizationIdentity := &servermodels.OrganizationIdentity{
		OrganizationID:         organization.ID,
		Type:                   string(domain.OrganizationIdentityTypeUser),
		DisplayName:            input.AdminDisplayName,
		HandlesServiceRequests: true,
		WorkStatus:             string(domain.WorkStatusWorking),
	}
	if _, err := tx.NewInsert().Model(organizationIdentity).
		Column("organization_id", "type", "display_name", "handles_service_requests", "work_status").
		Returning("id, work_status, work_status_updated_at").Exec(ctx); err != nil {
		return nil, err
	}
	user := &servermodels.User{
		IdentityID:     organizationIdentity.ID,
		OrganizationID: organization.ID,
		AccountID:      input.Account.ID,
		RoleID:         adminRoleID,
		Status:         string(domain.IdentityStatusActive),
	}
	if _, err := tx.NewInsert().
		Model(user).
		Column("identity_id", "organization_id", "account_id", "role_id", "status").
		Returning("id, message_notifications_enabled").
		Exec(ctx); err != nil {
		return nil, err
	}
	return &servermodels.Identity{
		Organization:         *organization,
		OrganizationIdentity: *organizationIdentity,
		User:                 *user,
		Account:              *input.Account,
	}, nil
}

// insertOrganization 插入工作区，标识冲突时重新生成，最多尝试三次。
func insertOrganization(ctx context.Context, tx bun.Tx, name string, generateSlug func() string) (*servermodels.Organization, error) {
	for range 3 {
		organization := &servermodels.Organization{Slug: generateSlug(), Name: name}
		result, err := tx.NewInsert().
			Model(organization).
			Column("slug", "name").
			On("CONFLICT (slug) DO NOTHING").
			Returning("id, lifecycle_status, created_at, updated_at").
			Exec(ctx)
		if pgerr.UniqueViolationOn(err, "organizations_name_unique") {
			return nil, &ValidationError{Fields: map[string]ValidationCode{"name": ValidationNameDuplicate}}
		}
		if err != nil {
			return nil, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if count == 1 {
			return organization, nil
		}
	}
	return nil, fmt.Errorf("generate unique workspace slug: attempts exhausted")
}
