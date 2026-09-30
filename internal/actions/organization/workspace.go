//go:build server

package organization

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

const (
	ValidationSlugInvalid ValidationCode = "ORGANIZATION_SLUG_INVALID"
	ValidationSlugTaken   ValidationCode = "ORGANIZATION_SLUG_TAKEN"
)

// Workspace 描述账号可进入的工作区。
type Workspace struct {
	ID   string
	Name string
	Slug string
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

// Execute 校验名称和标识后创建工作区，账号以账号名称成为首位管理员成员。
func (a *CreateWorkspaceAction) Execute(ctx context.Context, identity *servermodels.AccountIdentity, input WorkspaceInput) (Workspace, error) {
	input, fields := NormalizeWorkspaceInput(input)
	if len(fields) > 0 {
		return Workspace{}, &ValidationError{Fields: fields}
	}
	var created *servermodels.Identity
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var err error
		created, err = Create(ctx, tx, CreateInput{Name: input.Name, Slug: input.Slug, Account: &identity.Account, AdminDisplayName: identity.Account.DisplayName})
		return err
	})
	if errors.Is(err, ErrSlugTaken) {
		return Workspace{}, &ValidationError{Fields: map[string]ValidationCode{"slug": ValidationSlugTaken}}
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("create workspace: %w", err)
	}
	return Workspace{ID: created.Organization.ID, Name: created.Organization.Name, Slug: created.Organization.Slug}, nil
}

// ListAccountWorkspacesQuery 读取账号作为有效成员可进入的工作区。
type ListAccountWorkspacesQuery struct {
	db *bun.DB
}

// NewListAccountWorkspacesQuery 创建账号工作区列表查询。
func NewListAccountWorkspacesQuery(db *bun.DB) *ListAccountWorkspacesQuery {
	return &ListAccountWorkspacesQuery{db: db}
}

// Execute 按工作区名称返回账号有有效成员身份的工作区。
func (q *ListAccountWorkspacesQuery) Execute(ctx context.Context, identity *servermodels.AccountIdentity) ([]Workspace, error) {
	var workspaces []Workspace
	if err := q.db.NewSelect().
		TableExpr("organizations AS o").
		ColumnExpr("o.id::text AS id, o.name, o.slug").
		Join("JOIN users AS u ON u.organization_id = o.id").
		Where("u.account_id = ?", identity.Account.ID).
		Where("u.status = ?", domain.IdentityStatusActive).
		OrderExpr("o.name ASC, o.id ASC").
		Scan(ctx, &workspaces); err != nil {
		return nil, fmt.Errorf("list account workspaces: %w", err)
	}
	return workspaces, nil
}
