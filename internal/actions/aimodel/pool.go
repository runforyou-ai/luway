//go:build server

package aimodel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

var (
	// ErrPoolMemberNotFound 表示 Auto 池成员不存在。
	ErrPoolMemberNotFound = errors.New("auto pool member not found")
	// ErrPoolMemberInUse 表示 Auto 池成员仍被知识库 embedding 钉住引用。
	ErrPoolMemberInUse = errors.New("auto pool member is in use")
	// ErrPlatformNotFound 是 ErrPoolMemberNotFound 的别名，供既有调用保留。
	ErrPlatformNotFound = ErrPoolMemberNotFound
	// ErrPlatformInUse 是 ErrPoolMemberInUse 的别名，供既有调用保留。
	ErrPlatformInUse = ErrPoolMemberInUse
)

// 池成员的字段校验码；字符串值沿用既有平台模型词条键以兼容文案映射。
const (
	ValidationPoolIdentifierInvalid      common.FieldCode = "PLATFORM_AI_MODEL_NAME_INVALID"
	ValidationPoolIdentifierDuplicate    common.FieldCode = "PLATFORM_AI_MODEL_ROUTE_DUPLICATE"
	ValidationPoolTypeInvalid            common.FieldCode = "PLATFORM_AI_MODEL_TYPE_INVALID"
	ValidationPoolInputModalitiesInvalid common.FieldCode = "PLATFORM_AI_MODEL_INPUT_MODALITIES_INVALID"
	ValidationPoolContextWindowInvalid   common.FieldCode = "PLATFORM_AI_MODEL_CONTEXT_WINDOW_INVALID"
	ValidationPoolMaxOutputTokensInvalid common.FieldCode = "PLATFORM_AI_MODEL_MAX_OUTPUT_TOKENS_INVALID"
	ValidationPoolWeightInvalid          common.FieldCode = "PLATFORM_AI_MODEL_ROUTE_WEIGHT_INVALID"
	ValidationPoolProviderInvalid        common.FieldCode = "PLATFORM_AI_MODEL_ROUTES_INVALID"
	ValidationPlatformUsageConflict      common.FieldCode = "PLATFORM_AI_MODEL_USAGE_CONFLICT"
)

// MaxMemberWeight 是池成员选型权重的上限。
const MaxMemberWeight = 100

// MaxRouteWeight 是 MaxMemberWeight 的别名，供既有调用保留。
const MaxRouteWeight = MaxMemberWeight

// poolMemberColumns 是池成员创建与修改写入的列。
var poolMemberColumns = []string{
	"provider_id", "identifier", "model_type", "input_modalities", "context_window", "max_output_tokens",
	"priority", "weight", "enabled",
}

// PoolMemberInput 定义 Auto 池成员的属性：平台供应商、上游标识、能力与选型参数；无用户面向名称。
type PoolMemberInput struct {
	ProviderID      string
	Identifier      string
	Type            domain.AIModelType
	InputModalities []domain.AIModelInputModality
	ContextWindow   int64
	MaxOutputTokens int64
	Priority        int
	Weight          int
	Enabled         bool
}

// PoolMemberRecord 定义 Auto 池成员及其供应商展示信息。
type PoolMemberRecord struct {
	ID              string
	ProviderID      string
	ProviderName    string
	ProviderBrand   domain.AIProviderBrand
	Identifier      string
	Type            domain.AIModelType
	InputModalities []domain.AIModelInputModality
	ContextWindow   int64
	MaxOutputTokens int64
	Priority        int
	Weight          int
	Enabled         bool
}

// NormalizePoolMemberInput 规范化并校验池成员属性：上游标识非空，权重在 0 到 MaxMemberWeight 之间，类型与模态等与模型规范一致。
func NormalizePoolMemberInput(input PoolMemberInput) (PoolMemberInput, map[string]common.FieldCode) {
	fields := make(map[string]common.FieldCode)
	input.Identifier = strings.TrimSpace(input.Identifier)
	providerID, validProvider := str.NormalizeUUID(input.ProviderID)
	if !validProvider {
		fields["providerId"] = ValidationPoolProviderInvalid
	} else {
		input.ProviderID = providerID
	}
	if !ValidIdentifier(input.Identifier) {
		fields["identifier"] = ValidationPoolIdentifierInvalid
	}
	if input.Weight < 0 || input.Weight > MaxMemberWeight {
		fields["weight"] = ValidationPoolWeightInvalid
	}
	switch {
	case input.Type != domain.AIModelTypeChat && input.Type != domain.AIModelTypeEmbedding &&
		input.Type != domain.AIModelTypeRerank && input.Type != domain.AIModelTypeDecision:
		fields["type"] = ValidationPoolTypeInvalid
	case !validInputModalities(input.InputModalities):
		fields["inputModalities"] = ValidationPoolInputModalitiesInvalid
	case input.ContextWindow <= 0:
		fields["contextWindow"] = ValidationPoolContextWindowInvalid
	case input.Type != domain.AIModelTypeChat:
		input.MaxOutputTokens = 0
	case input.MaxOutputTokens <= 0:
		fields["maxOutputTokens"] = ValidationPoolMaxOutputTokensInvalid
	}
	return input, fields
}

// CreatePoolMember 在事务内创建 Auto 池成员，返回成员编号；input 须已经 NormalizePoolMemberInput 规范化，调用方以 PoolMemberConflictError 转换唯一冲突。
func CreatePoolMember(ctx context.Context, tx bun.Tx, input PoolMemberInput) (string, error) {
	if err := lockPlatformProvider(ctx, tx, input.ProviderID); err != nil {
		return "", err
	}
	record, err := poolMemberRecord(input)
	if err != nil {
		return "", err
	}
	if _, err := tx.NewInsert().Model(&record).
		Column(poolMemberColumns...).
		Returning("id").
		Exec(ctx); err != nil {
		return "", err
	}
	return record.ID, nil
}

// UpdatePoolMember 在事务内锁定并修改 Auto 池成员；被知识库钉住时不得停用或改类型，input 须已经 NormalizePoolMemberInput 规范化。
func UpdatePoolMember(ctx context.Context, tx bun.Tx, memberID string, input PoolMemberInput) error {
	if err := LockPoolMember(ctx, tx, memberID); err != nil {
		return err
	}
	var currentType domain.AIModelType
	if err := tx.NewSelect().Model((*servermodels.AutoPoolMember)(nil)).
		ColumnExpr("apm.model_type").
		Where("apm.id = ?", memberID).
		Scan(ctx, &currentType); err != nil {
		return err
	}
	if !input.Enabled || currentType != input.Type {
		used, err := PoolMemberStickyUsed(ctx, tx, memberID)
		if err != nil {
			return err
		}
		if used {
			if !input.Enabled {
				return &common.FieldError{Fields: map[string]common.FieldCode{"enabled": ValidationPlatformUsageConflict}}
			}
			return &common.FieldError{Fields: map[string]common.FieldCode{"type": ValidationPlatformUsageConflict}}
		}
	}
	if err := lockPlatformProvider(ctx, tx, input.ProviderID); err != nil {
		return err
	}
	record, err := poolMemberRecord(input)
	if err != nil {
		return err
	}
	record.ID = memberID
	_, err = tx.NewUpdate().Model(&record).
		Column(poolMemberColumns...).
		WherePK().
		Exec(ctx)
	return err
}

// PoolMemberStickyUsed 报告成员是否仍被知识库 embedding 钉住。
func PoolMemberStickyUsed(ctx context.Context, tx bun.Tx, memberID string) (bool, error) {
	return tx.NewSelect().TableExpr("knowledge_bases").
		Where("embedding_sticky_member_id = ?", memberID).
		Exists(ctx)
}

// DeletePoolMember 在事务内锁定并删除未被知识库 embedding 钉住的池成员，仍被钉住时返回 ErrPoolMemberInUse。
func DeletePoolMember(ctx context.Context, tx bun.Tx, memberID string) error {
	if err := LockPoolMember(ctx, tx, memberID); err != nil {
		return err
	}
	used, err := PoolMemberStickyUsed(ctx, tx, memberID)
	if err != nil {
		return err
	}
	if used {
		return ErrPoolMemberInUse
	}
	_, err = tx.NewDelete().Model((*servermodels.AutoPoolMember)(nil)).Where("id = ?", memberID).Exec(ctx)
	return err
}

// LockPoolMember 以 UPDATE 锁定 Auto 池成员，不存在时返回 ErrPoolMemberNotFound。
func LockPoolMember(ctx context.Context, tx bun.Tx, memberID string) error {
	err := tx.NewSelect().Model((*servermodels.AutoPoolMember)(nil)).
		ColumnExpr("apm.id::text").
		Where("apm.id = ?", memberID).
		For("UPDATE").
		Scan(ctx, new(string))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPoolMemberNotFound
	}
	return err
}

// poolMemberRow 定义池成员目录查询的一行。
type poolMemberRow struct {
	ID              string          `bun:"id"`
	ProviderID      string          `bun:"provider_id"`
	ProviderName    string          `bun:"provider_name"`
	ProviderBrand   string          `bun:"provider_brand"`
	Identifier      string          `bun:"identifier"`
	Type            string          `bun:"model_type"`
	InputModalities json.RawMessage `bun:"input_modalities"`
	ContextWindow   int64           `bun:"context_window"`
	MaxOutputTokens int64           `bun:"max_output_tokens"`
	Priority        int             `bun:"priority"`
	Weight          int             `bun:"weight"`
	Enabled         bool            `bun:"enabled"`
}

// LoadPoolMembers 读取 Auto 池成员及其供应商，memberIDs 为 nil 时读取全部成员，否则须为 UUID。
func LoadPoolMembers(ctx context.Context, db bun.IDB, memberIDs []string) ([]PoolMemberRecord, error) {
	rows := make([]poolMemberRow, 0)
	query := db.NewSelect().TableExpr("auto_pool_members AS apm").
		ColumnExpr("apm.id::text AS id, apm.identifier, apm.model_type, apm.input_modalities").
		ColumnExpr("apm.context_window, apm.max_output_tokens, apm.priority, apm.weight, apm.enabled").
		ColumnExpr("aip.id::text AS provider_id, aip.name AS provider_name, aip.brand AS provider_brand").
		Join("JOIN ai_providers AS aip ON aip.id = apm.provider_id AND aip.workspace_id IS NULL").
		OrderExpr("apm.priority ASC, lower(apm.identifier) ASC, apm.id ASC")
	if memberIDs != nil {
		query = query.Where("apm.id IN (?)", bun.List(memberIDs))
	}
	if err := query.Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("load auto pool members: %w", err)
	}
	records := make([]PoolMemberRecord, 0, len(rows))
	for _, row := range rows {
		inputModalities := make([]domain.AIModelInputModality, 0)
		if err := json.Unmarshal(row.InputModalities, &inputModalities); err != nil {
			return nil, fmt.Errorf("decode pool member %q input modalities: %w", row.ID, err)
		}
		records = append(records, PoolMemberRecord{
			ID: row.ID, ProviderID: row.ProviderID, ProviderName: row.ProviderName,
			ProviderBrand: domain.AIProviderBrand(row.ProviderBrand), Identifier: row.Identifier,
			Type: domain.AIModelType(row.Type), InputModalities: inputModalities,
			ContextWindow: row.ContextWindow, MaxOutputTokens: row.MaxOutputTokens,
			Priority: row.Priority, Weight: row.Weight, Enabled: row.Enabled,
		})
	}
	return records, nil
}

// lockPlatformProvider 以 KEY SHARE 锁定平台供应商，不存在时返回字段校验错误。
func lockPlatformProvider(ctx context.Context, tx bun.Tx, providerID string) error {
	err := tx.NewSelect().Model((*servermodels.AIProvider)(nil)).
		ColumnExpr("aip.id::text").
		Where("aip.id = ?", providerID).
		Where("aip.workspace_id IS NULL").
		For("KEY SHARE").
		Scan(ctx, new(string))
	if errors.Is(err, sql.ErrNoRows) {
		return &common.FieldError{Fields: map[string]common.FieldCode{"providerId": ValidationPoolProviderInvalid}}
	}
	return err
}

// poolMemberRecord 把输入转换为池成员存储模型。
func poolMemberRecord(input PoolMemberInput) (servermodels.AutoPoolMember, error) {
	inputModalities, err := json.Marshal(input.InputModalities)
	if err != nil {
		return servermodels.AutoPoolMember{}, err
	}
	return servermodels.AutoPoolMember{
		ProviderID: input.ProviderID, Identifier: input.Identifier, Type: string(input.Type),
		InputModalities: inputModalities, ContextWindow: input.ContextWindow, MaxOutputTokens: input.MaxOutputTokens,
		Priority: input.Priority, Weight: input.Weight, Enabled: input.Enabled,
	}, nil
}

// PoolMemberConflictError 把池成员写入事务结果中的供应商与上游标识唯一冲突转换为字段校验错误，其他错误返回 nil。
func PoolMemberConflictError(err error) error {
	if pgerr.UniqueViolationOn(err, "auto_pool_members_provider_identifier_unique") {
		return &common.FieldError{Fields: map[string]common.FieldCode{"identifier": ValidationPoolIdentifierDuplicate}}
	}
	return nil
}
