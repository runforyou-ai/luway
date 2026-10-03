//go:build server

package organization

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	deploymentaction "github.com/runforyou-ai/luway/internal/actions/deployment"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

const (
	ValidationSlugInvalid ValidationCode = "ORGANIZATION_SLUG_INVALID"
	ValidationSlugTaken   ValidationCode = "ORGANIZATION_SLUG_TAKEN"
)

var (
	// ErrCreationNotAllowed 表示部署创建策略不允许该账号创建工作区。
	ErrCreationNotAllowed = errors.New("workspace creation is not allowed for the account")
	// ErrWorkspaceLimitReached 表示部署工作区数量已达到实例能力上限。
	ErrWorkspaceLimitReached = errors.New("deployment workspace limit is reached")
)

// Workspace 描述账号可进入的工作区。
type Workspace struct {
	ID     string
	Name   string
	Slug   string
	Status domain.OrganizationLifecycleStatus `bun:"lifecycle_status"`
}

// WorkspaceInput 定义工作区名称和标识。
type WorkspaceInput struct {
	Name string
	Slug string
}

// normalizeWorkspaceName 规范化并校验工作区名称，字段名与客户端表单一致。
func normalizeWorkspaceName(name string) (string, map[string]ValidationCode) {
	name = strings.TrimSpace(name)
	fields := make(map[string]ValidationCode)
	if name == "" {
		fields["name"] = ValidationNameRequired
	} else if utf8.RuneCountInString(name) > domain.OrganizationNameMaxLength {
		fields["name"] = ValidationNameTooLong
	}
	return name, fields
}

// NormalizeWorkspaceInput 规范化并校验工作区名称和标识，字段名与客户端表单一致。
func NormalizeWorkspaceInput(input WorkspaceInput) (WorkspaceInput, map[string]ValidationCode) {
	var fields map[string]ValidationCode
	input.Name, fields = normalizeWorkspaceName(input.Name)
	input.Slug = domain.NormalizeWorkspaceSlug(input.Slug)
	if !domain.WorkspaceSlugValid(input.Slug) {
		fields["slug"] = ValidationSlugInvalid
	}
	return input, fields
}

// CreateWorkspaceAction 由已登录账号创建工作区。
type CreateWorkspaceAction struct {
	db *bun.DB
}

// NewCreateWorkspaceAction 创建工作区新建操作。
func NewCreateWorkspaceAction(db *bun.DB) *CreateWorkspaceAction {
	return &CreateWorkspaceAction{db: db}
}

// Execute 校验名称和标识后，在部署创建策略和实例工作区上限允许时创建工作区，账号以账号名称成为首位管理员成员。
func (a *CreateWorkspaceAction) Execute(ctx context.Context, identity *servermodels.AccountIdentity, input WorkspaceInput) (Workspace, error) {
	input, fields := NormalizeWorkspaceInput(input)
	if len(fields) > 0 {
		return Workspace{}, &ValidationError{Fields: fields}
	}
	var created *servermodels.Identity
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// 锁定部署实例行，并发创建按同一份策略与工作区数量依次判断。
		deployment, err := deploymentaction.Lock(ctx, tx)
		if err != nil {
			return err
		}
		if err := checkWorkspaceCreation(ctx, tx, deployment, identity.Account.IsDeploymentAdmin); err != nil {
			return err
		}
		created, err = Create(ctx, tx, CreateInput{Name: input.Name, Slug: input.Slug, Account: &identity.Account, AdminDisplayName: identity.Account.DisplayName})
		return err
	})
	if errors.Is(err, ErrSlugTaken) {
		return Workspace{}, &ValidationError{Fields: map[string]ValidationCode{"slug": ValidationSlugTaken}}
	}
	if errors.Is(err, ErrCreationNotAllowed) || errors.Is(err, ErrWorkspaceLimitReached) {
		return Workspace{}, err
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("create workspace: %w", err)
	}
	return Workspace{ID: created.Organization.ID, Name: created.Organization.Name, Slug: created.Organization.Slug, Status: domain.OrganizationLifecycleActive}, nil
}

// checkWorkspaceCreation 按部署创建策略和实例工作区上限判断账号能否再创建一个工作区。
func checkWorkspaceCreation(ctx context.Context, db bun.IDB, deployment *servermodels.Deployment, deploymentAdmin bool) error {
	if !domain.WorkspaceCreationPolicy(deployment.WorkspaceCreationPolicy).Allows(deploymentAdmin) {
		return ErrCreationNotAllowed
	}
	capabilities, err := deploymentaction.Capabilities(ctx, db)
	if err != nil {
		return err
	}
	count, err := db.NewSelect().Model((*servermodels.Organization)(nil)).Count(ctx)
	if err != nil {
		return err
	}
	if !capabilities.AllowsAnotherWorkspace(count) {
		return ErrWorkspaceLimitReached
	}
	return nil
}

// CanCreateWorkspaceQuery 判断账号当前能否创建工作区。
type CanCreateWorkspaceQuery struct {
	db *bun.DB
}

// NewCanCreateWorkspaceQuery 创建工作区创建资格查询。
func NewCanCreateWorkspaceQuery(db *bun.DB) *CanCreateWorkspaceQuery {
	return &CanCreateWorkspaceQuery{db: db}
}

// Execute 返回部署创建策略和实例工作区上限是否允许账号再创建一个工作区。
func (q *CanCreateWorkspaceQuery) Execute(ctx context.Context, identity *servermodels.AccountIdentity) (bool, error) {
	deployment, err := deploymentaction.Load(ctx, q.db)
	if err != nil {
		return false, err
	}
	err = checkWorkspaceCreation(ctx, q.db, deployment, identity.Account.IsDeploymentAdmin)
	if errors.Is(err, ErrCreationNotAllowed) || errors.Is(err, ErrWorkspaceLimitReached) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check workspace creation: %w", err)
	}
	return true, nil
}

// ListAccountWorkspacesQuery 读取账号作为有效成员加入的工作区。
type ListAccountWorkspacesQuery struct {
	db *bun.DB
}

// NewListAccountWorkspacesQuery 创建账号工作区列表查询。
func NewListAccountWorkspacesQuery(db *bun.DB) *ListAccountWorkspacesQuery {
	return &ListAccountWorkspacesQuery{db: db}
}

// Execute 按工作区名称返回账号有有效成员身份的正常或已暂停工作区。
func (q *ListAccountWorkspacesQuery) Execute(ctx context.Context, identity *servermodels.AccountIdentity) ([]Workspace, error) {
	var workspaces []Workspace
	if err := q.db.NewSelect().
		TableExpr("organizations AS o").
		ColumnExpr("o.id::text AS id, o.name, o.slug, o.lifecycle_status").
		Join("JOIN users AS u ON u.organization_id = o.id").
		Where("u.account_id = ?", identity.Account.ID).
		Where("o.lifecycle_status IN (?)", bun.In([]domain.OrganizationLifecycleStatus{domain.OrganizationLifecycleActive, domain.OrganizationLifecycleSuspended})).
		Where("u.status = ?", domain.IdentityStatusActive).
		OrderExpr("o.name ASC, o.id ASC").
		Scan(ctx, &workspaces); err != nil {
		return nil, fmt.Errorf("list account workspaces: %w", err)
	}
	return workspaces, nil
}
