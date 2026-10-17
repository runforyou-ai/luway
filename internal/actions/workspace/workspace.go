//go:build server

package workspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	licenseaction "github.com/runforyou-ai/luway/internal/actions/license"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	"github.com/runforyou-ai/luway/internal/actions/seat"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ErrDefaultWorkspaceMissing 表示平台尚未配置默认工作区。
var ErrDefaultWorkspaceMissing = errors.New("default workspace is not configured")

var (
	// ErrCreationNotAllowed 表示当前自建额度不允许该账号再创建工作区。
	ErrCreationNotAllowed = errors.New("workspace creation is not allowed for the account")
	// ErrWorkspaceLimitReached 表示账号自建工作区数量已达到额度上限。
	ErrWorkspaceLimitReached = errors.New("account workspace creation quota is reached")
)

// Workspace 描述账号可进入的工作区。
type Workspace struct {
	ID     string
	Name   string
	Slug   string
	Status domain.WorkspaceLifecycleStatus `bun:"lifecycle_status"`
}

// WorkspaceInput 定义新建工作区的名称。
type WorkspaceInput struct {
	Name string
}

// normalizeWorkspaceName 规范化并校验工作区名称，字段名与客户端表单一致。
func normalizeWorkspaceName(name string) (string, map[string]common.FieldCode) {
	name = strings.TrimSpace(name)
	fields := make(map[string]common.FieldCode)
	if name == "" {
		fields["name"] = ValidationNameRequired
	} else if utf8.RuneCountInString(name) > domain.WorkspaceNameMaxLength {
		fields["name"] = ValidationNameTooLong
	}
	return name, fields
}

// NormalizeWorkspaceInput 规范化并校验工作区名称，字段名与客户端表单一致。
func NormalizeWorkspaceInput(input WorkspaceInput) (WorkspaceInput, map[string]common.FieldCode) {
	var fields map[string]common.FieldCode
	input.Name, fields = normalizeWorkspaceName(input.Name)
	return input, fields
}

// CreateWorkspaceAction 由已登录账号创建工作区。
type CreateWorkspaceAction struct {
	db          *bun.DB
	initializer Initializer
}

// NewCreateWorkspaceAction 创建工作区新建操作。
func NewCreateWorkspaceAction(db *bun.DB, initializer Initializer) *CreateWorkspaceAction {
	return &CreateWorkspaceAction{db: db, initializer: initializer}
}

// Execute 校验名称后，在平台创建策略和平台工作区上限允许时创建工作区，账号以账号名称成为首位管理员成员。
func (a *CreateWorkspaceAction) Execute(ctx context.Context, identity *servermodels.AccountIdentity, input WorkspaceInput) (Workspace, error) {
	input, fields := NormalizeWorkspaceInput(input)
	if len(fields) > 0 {
		return Workspace{}, &common.FieldError{Fields: fields}
	}
	var created *servermodels.Identity
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		// 锁定平台行，并发创建按同一份策略与工作区数量依次判断。
		platform, err := platformaction.Lock(ctx, tx)
		if err != nil {
			return err
		}
		if err := checkWorkspaceCreation(ctx, tx, platform, identity.Account.ID); err != nil {
			return err
		}
		created, err = Create(ctx, tx, CreateInput{Name: input.Name, Account: &identity.Account, AdminDisplayName: identity.Account.DisplayName, Initializer: a.initializer})
		if err != nil {
			return err
		}
		if platform.DefaultWorkspaceID == "" {
			platform.DefaultWorkspaceID = created.Workspace.ID
			if _, err := tx.NewUpdate().Model(platform).Column("default_workspace_id").WherePK().Exec(ctx); err != nil {
				return err
			}
		}
		realtime.Notify(ctx, realtime.Notification{AudienceKind: realtime.AudienceAccount, AudienceID: identity.Account.ID, Kind: realtime.KindMembershipAdded})
		return nil
	})
	if errors.Is(err, ErrCreationNotAllowed) || errors.Is(err, ErrWorkspaceLimitReached) {
		return Workspace{}, err
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("create workspace: %w", err)
	}
	return Workspace{ID: created.Workspace.ID, Name: created.Workspace.Name, Slug: created.Workspace.Slug, Status: domain.WorkspaceLifecycleActive}, nil
}

// checkWorkspaceCreation 按账号自建额度判断能否再创建一个工作区（被邀请加入的不计入）。
// 平台尚无默认工作区时，允许平台管理员创建首个工作区以便升级后的恢复。
func checkWorkspaceCreation(ctx context.Context, db bun.IDB, platform *servermodels.Platform, accountID string) error {
	if platform.DefaultWorkspaceID == "" {
		var admin bool
		if err := db.NewSelect().Model((*servermodels.Account)(nil)).
			Column("is_platform_admin").
			Where("id = ?", accountID).
			Scan(ctx, &admin); err != nil {
			return err
		}
		if admin {
			return nil
		}
	}
	quota, err := licenseaction.EffectiveCreationQuota(ctx, db, platform.WorkspaceCreationQuota)
	if err != nil {
		return err
	}
	if quota <= 0 {
		return ErrCreationNotAllowed
	}
	count, err := db.NewSelect().Model((*servermodels.Workspace)(nil)).
		Where("created_by_account_id = ?", accountID).
		Where("lifecycle_status IN (?)", bun.List([]string{string(domain.WorkspaceLifecycleActive), string(domain.WorkspaceLifecycleSuspended)})).
		Count(ctx)
	if err != nil {
		return err
	}
	if int(count) >= quota {
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

// Execute 返回自建额度是否允许账号再创建一个工作区。
func (q *CanCreateWorkspaceQuery) Execute(ctx context.Context, identity *servermodels.AccountIdentity) (bool, error) {
	platform, err := platformaction.Load(ctx, q.db)
	if err != nil {
		return false, err
	}
	err = checkWorkspaceCreation(ctx, q.db, platform, identity.Account.ID)
	if errors.Is(err, ErrCreationNotAllowed) || errors.Is(err, ErrWorkspaceLimitReached) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check workspace creation: %w", err)
	}
	return true, nil
}

// PlaceRegisteredAccount 在无邀请的自助注册后落位：额度为 0 时加入默认工作区，额度大于 0 时自建个人工作区。
func PlaceRegisteredAccount(ctx context.Context, tx bun.Tx, platform *servermodels.Platform, account *servermodels.Account, initializer Initializer, seats seat.Seats) error {
	quota, err := licenseaction.EffectiveCreationQuota(ctx, tx, platform.WorkspaceCreationQuota)
	if err != nil {
		return err
	}
	if quota <= 0 {
		return joinDefaultWorkspace(ctx, tx, platform, account, seats)
	}
	name := personalWorkspaceName(account)
	created, err := Create(ctx, tx, CreateInput{Name: name, Account: account, AdminDisplayName: account.DisplayName, Initializer: initializer})
	if err != nil {
		// 姓名全平台唯一；冲突时附加短后缀重试一次。
		if validation, ok := errors.AsType[*common.FieldError](err); ok && validation.Fields["name"] == ValidationNameDuplicate {
			created, err = Create(ctx, tx, CreateInput{
				Name:             disambiguatedWorkspaceName(name),
				Account:          account,
				AdminDisplayName: account.DisplayName,
				Initializer:      initializer,
			})
		}
	}
	if err != nil {
		return err
	}
	if platform.DefaultWorkspaceID == "" {
		platform.DefaultWorkspaceID = created.Workspace.ID
		if _, err := tx.NewUpdate().Model(platform).Column("default_workspace_id").WherePK().Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

// personalWorkspaceName 为自助注册自建工作区选取初始名称。
func personalWorkspaceName(account *servermodels.Account) string {
	name := strings.TrimSpace(account.DisplayName)
	if name == "" {
		name = account.Email
	}
	if utf8.RuneCountInString(name) > domain.WorkspaceNameMaxLength {
		name = string([]rune(name)[:domain.WorkspaceNameMaxLength])
	}
	return name
}

// disambiguatedWorkspaceName 在名称过长时截断并附加随机后缀，避免重名导致注册失败。
func disambiguatedWorkspaceName(base string) string {
	var suffix [3]byte
	_, _ = rand.Read(suffix[:])
	tag := hex.EncodeToString(suffix[:])
	maxBase := domain.WorkspaceNameMaxLength - len(tag) - 1
	if maxBase < 1 {
		return tag
	}
	runes := []rune(base)
	if len(runes) > maxBase {
		runes = runes[:maxBase]
	}
	return string(runes) + "-" + tag
}

// joinDefaultWorkspace 将账号以成员角色加入平台默认工作区，并按席位上限校验。
func joinDefaultWorkspace(ctx context.Context, tx bun.Tx, platform *servermodels.Platform, account *servermodels.Account, seats seat.Seats) error {
	if platform.DefaultWorkspaceID == "" {
		return ErrDefaultWorkspaceMissing
	}
	var lifecycle string
	if err := tx.NewSelect().Model((*servermodels.Workspace)(nil)).
		Column("lifecycle_status").
		Where("id = ?", platform.DefaultWorkspaceID).
		Scan(ctx, &lifecycle); err != nil {
		return err
	}
	if lifecycle != string(domain.WorkspaceLifecycleActive) {
		return ErrDefaultWorkspaceMissing
	}
	var roleID string
	if err := tx.NewSelect().Model((*servermodels.Role)(nil)).
		Column("id").
		Where("workspace_id = ?", platform.DefaultWorkspaceID).
		Where("kind = ?", domain.RoleKindMember).
		Scan(ctx, &roleID); err != nil {
		return err
	}
	workspaceIdentity := &servermodels.WorkspaceIdentity{
		WorkspaceID: platform.DefaultWorkspaceID,
		Type:        string(domain.WorkspaceIdentityTypeUser),
		DisplayName: account.DisplayName,
		WorkStatus:  string(domain.WorkStatusWorking),
	}
	if _, err := tx.NewInsert().Model(workspaceIdentity).
		Column("workspace_id", "type", "display_name", "handles_service_requests", "work_status").
		Returning("id").Exec(ctx); err != nil {
		return err
	}
	user := &servermodels.User{
		IdentityID:  workspaceIdentity.ID,
		WorkspaceID: platform.DefaultWorkspaceID,
		AccountID:   account.ID,
		RoleID:      roleID,
		Status:      string(domain.IdentityStatusActive),
	}
	if _, err := tx.NewInsert().Model(user).
		Column("identity_id", "workspace_id", "account_id", "role_id", "status").
		Returning("id").Exec(ctx); err != nil {
		return err
	}
	if err := seats.Enforce(ctx, tx, platform.DefaultWorkspaceID); err != nil {
		return err
	}
	realtime.Notify(ctx, realtime.Notification{AudienceKind: realtime.AudienceAccount, AudienceID: account.ID, Kind: realtime.KindMembershipAdded})
	return nil
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
		TableExpr("workspaces AS o").
		ColumnExpr("o.id::text AS id, o.name, o.slug, o.lifecycle_status").
		Join("JOIN users AS u ON u.workspace_id = o.id").
		Where("u.account_id = ?", identity.Account.ID).
		Where("o.lifecycle_status IN (?)", bun.List([]domain.WorkspaceLifecycleStatus{domain.WorkspaceLifecycleActive, domain.WorkspaceLifecycleSuspended})).
		Where("u.status = ?", domain.IdentityStatusActive).
		OrderExpr("o.name ASC, o.id ASC").
		Scan(ctx, &workspaces); err != nil {
		return nil, fmt.Errorf("list account workspaces: %w", err)
	}
	return workspaces, nil
}
