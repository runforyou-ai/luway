//go:build server

package aiprovider

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	deploymentaction "github.com/runforyou-ai/luway/internal/actions/deployment"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/modelprovider"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ErrPlatformInUse 表示平台供应商仍是平台模型的来源。
var ErrPlatformInUse = errors.New("platform AI provider is in use")

// PlatformInput 定义平台供应商的名称与连接配置，修改时品牌沿用创建时的值。
type PlatformInput struct {
	Brand          domain.AIProviderBrand
	Name           string
	CredentialType domain.AIProviderCredentialType
	APIKey         string
	APIURL         string
}

// PlatformRecord 定义平台供应商详情。
type PlatformRecord struct {
	ID             string
	Brand          domain.AIProviderBrand
	Name           string
	CredentialType domain.AIProviderCredentialType
	APIKey         string
	APIURL         string
}

// PlatformSummary 定义平台供应商列表项，ModelCount 是以该供应商为来源的平台模型数。
type PlatformSummary struct {
	ID         string                 `bun:"id"`
	Brand      domain.AIProviderBrand `bun:"brand"`
	Name       string                 `bun:"name"`
	APIURL     string                 `bun:"api_url"`
	ModelCount int                    `bun:"model_count"`
}

// ListPlatformQuery 读取部署的平台供应商。
type ListPlatformQuery struct{ db *bun.DB }

// NewListPlatformQuery 创建平台供应商列表查询。
func NewListPlatformQuery(db *bun.DB) *ListPlatformQuery {
	return &ListPlatformQuery{db: db}
}

// Execute 按添加顺序返回平台供应商及其服务的平台模型数。
func (q *ListPlatformQuery) Execute(ctx context.Context) ([]PlatformSummary, error) {
	providers := make([]PlatformSummary, 0)
	if err := q.db.NewSelect().TableExpr("ai_providers AS aip").
		ColumnExpr("aip.id::text AS id, aip.brand, aip.name, aip.api_url").
		ColumnExpr("(SELECT count(DISTINCT amr.model_id) FROM ai_model_routes AS amr WHERE amr.provider_id = aip.id) AS model_count").
		Where("aip.organization_id IS NULL").
		OrderExpr("aip.created_at ASC, aip.id ASC").
		Scan(ctx, &providers); err != nil {
		return nil, fmt.Errorf("list platform AI providers: %w", err)
	}
	return providers, nil
}

// GetPlatformQuery 读取平台供应商详情。
type GetPlatformQuery struct{ db *bun.DB }

// NewGetPlatformQuery 创建平台供应商详情查询。
func NewGetPlatformQuery(db *bun.DB) *GetPlatformQuery {
	return &GetPlatformQuery{db: db}
}

// Execute 返回平台供应商详情，不存在时返回 ErrNotFound。
func (q *GetPlatformQuery) Execute(ctx context.Context, providerID string) (*PlatformRecord, error) {
	provider, err := loadPlatformProvider(ctx, q.db, providerID, false)
	if err != nil {
		return nil, fmt.Errorf("get platform AI provider: %w", err)
	}
	output := platformRecordFromModel(*provider)
	return &output, nil
}

// CreatePlatformAction 创建平台供应商。
type CreatePlatformAction struct{ db *bun.DB }

// NewCreatePlatformAction 创建平台供应商创建操作。
func NewCreatePlatformAction(db *bun.DB) *CreatePlatformAction {
	return &CreatePlatformAction{db: db}
}

// Execute 校验名称与连接配置并创建平台供应商。
func (a *CreatePlatformAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, input PlatformInput) (*PlatformRecord, error) {
	name, connection, fields := normalizeProvider(input.Name, ConnectionInput{
		Brand: input.Brand, CredentialType: input.CredentialType, APIKey: input.APIKey, APIURL: input.APIURL,
	})
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	provider := servermodels.AIProvider{
		Brand: string(connection.Brand), Name: name, CredentialType: string(connection.CredentialType),
		APIKey: connection.APIKey, APIURL: connection.APIURL,
	}
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := deploymentaction.LockAdmin(ctx, tx, operator); err != nil {
			return err
		}
		_, err := tx.NewInsert().Model(&provider).
			Column("organization_id", "brand", "name", "credential_type", "api_key", "api_url").
			Returning("*").
			Exec(ctx)
		return err
	})
	if conflict := conflictError(err); conflict != nil {
		return nil, conflict
	}
	if err != nil {
		return nil, fmt.Errorf("create platform AI provider: %w", err)
	}
	output := platformRecordFromModel(provider)
	return &output, nil
}

// UpdatePlatformAction 修改平台供应商。
type UpdatePlatformAction struct{ db *bun.DB }

// NewUpdatePlatformAction 创建平台供应商修改操作。
func NewUpdatePlatformAction(db *bun.DB) *UpdatePlatformAction {
	return &UpdatePlatformAction{db: db}
}

// Execute 修改平台供应商的名称与连接配置，品牌保持不变。
func (a *UpdatePlatformAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, providerID string, input PlatformInput) (*PlatformRecord, error) {
	var provider *servermodels.AIProvider
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := deploymentaction.LockAdmin(ctx, tx, operator); err != nil {
			return err
		}
		current, err := loadPlatformProvider(ctx, tx, providerID, true)
		if err != nil {
			return err
		}
		name, connection, fields := normalizeProvider(input.Name, ConnectionInput{
			Brand: domain.AIProviderBrand(current.Brand), CredentialType: input.CredentialType, APIKey: input.APIKey, APIURL: input.APIURL,
		})
		if len(fields) > 0 {
			return &ValidationError{Fields: fields}
		}
		current.Name = name
		current.CredentialType = string(connection.CredentialType)
		current.APIKey = connection.APIKey
		current.APIURL = connection.APIURL
		if _, err := tx.NewUpdate().Model(current).
			Column("name", "credential_type", "api_key", "api_url").
			Set("updated_at = now()").
			Where("organization_id IS NULL").
			WherePK().
			Exec(ctx); err != nil {
			return err
		}
		provider = current
		return nil
	})
	if conflict := conflictError(err); conflict != nil {
		return nil, conflict
	}
	if err != nil {
		return nil, fmt.Errorf("update platform AI provider: %w", err)
	}
	output := platformRecordFromModel(*provider)
	return &output, nil
}

// DeletePlatformAction 删除平台供应商。
type DeletePlatformAction struct{ db *bun.DB }

// NewDeletePlatformAction 创建平台供应商删除操作。
func NewDeletePlatformAction(db *bun.DB) *DeletePlatformAction {
	return &DeletePlatformAction{db: db}
}

// Execute 删除不是任何平台模型来源的平台供应商，仍是来源时返回 ErrPlatformInUse。
func (a *DeletePlatformAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, providerID string) error {
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := deploymentaction.LockAdmin(ctx, tx, operator); err != nil {
			return err
		}
		provider, err := loadPlatformProvider(ctx, tx, providerID, true)
		if err != nil {
			return err
		}
		used, err := tx.NewSelect().Model((*servermodels.AIModelRoute)(nil)).Where("provider_id = ?", provider.ID).Exists(ctx)
		if err != nil {
			return err
		}
		if used {
			return ErrPlatformInUse
		}
		_, err = tx.NewDelete().Model(provider).Where("organization_id IS NULL").WherePK().Exec(ctx)
		return err
	})
	if err != nil {
		return fmt.Errorf("delete platform AI provider: %w", err)
	}
	return nil
}

// ListPlatformModelsQuery 读取平台供应商可提供的模型。
type ListPlatformModelsQuery struct {
	db       *bun.DB
	discover *DiscoverModelsAction
	registry *modelprovider.Registry
}

// NewListPlatformModelsQuery 创建平台供应商可选模型查询。
func NewListPlatformModelsQuery(db *bun.DB, registry *modelprovider.Registry) *ListPlatformModelsQuery {
	return &ListPlatformModelsQuery{db: db, discover: NewDiscoverModelsAction(registry), registry: registry}
}

// Execute 返回平台供应商可提供的模型：模型目录由服务实例提供的品牌按已保存的连接配置读取实例，其余品牌返回预设目录。
func (q *ListPlatformModelsQuery) Execute(ctx context.Context, providerID string) ([]Model, error) {
	provider, err := loadPlatformProvider(ctx, q.db, providerID, false)
	if err != nil {
		return nil, err
	}
	brand := domain.AIProviderBrand(provider.Brand)
	if !q.registry.SupportsDiscovery(brand) {
		return AvailableModels(brand), nil
	}
	return q.discover.Execute(ctx, ConnectionInput{
		Brand: brand, CredentialType: domain.AIProviderCredentialType(provider.CredentialType),
		APIKey: provider.APIKey, APIURL: provider.APIURL,
	})
}

// loadPlatformProvider 读取平台供应商，lock 为真时以 UPDATE 锁定。
func loadPlatformProvider(ctx context.Context, db bun.IDB, providerID string, lock bool) (*servermodels.AIProvider, error) {
	if !common.ValidUUID(providerID) {
		return nil, ErrNotFound
	}
	provider := &servermodels.AIProvider{}
	query := db.NewSelect().Model(provider).
		Where("aip.id = ?", providerID).
		Where("aip.organization_id IS NULL")
	if lock {
		query = query.For("UPDATE")
	}
	if err := query.Scan(ctx); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	return provider, nil
}

// platformRecordFromModel 转换平台供应商存储模型。
func platformRecordFromModel(provider servermodels.AIProvider) PlatformRecord {
	return PlatformRecord{
		ID: provider.ID, Brand: domain.AIProviderBrand(provider.Brand), Name: provider.Name,
		CredentialType: domain.AIProviderCredentialType(provider.CredentialType),
		APIKey:         provider.APIKey, APIURL: provider.APIURL,
	}
}
