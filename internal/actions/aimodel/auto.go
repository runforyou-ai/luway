//go:build server

package aimodel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// AutoName 是平台 Auto 在调用记录与选项中的展示名称。
const AutoName = "Auto"

// IsAuto 判断模型引用是否为平台 Auto。
func IsAuto(modelID string) bool {
	return modelID == domain.AIModelRefAuto
}

// NormalizeRef 规范化模型引用：平台 Auto 原样返回，否则按 UUID 规范化。
func NormalizeRef(modelID string) (string, bool) {
	modelID = strings.TrimSpace(modelID)
	if IsAuto(modelID) {
		return domain.AIModelRefAuto, true
	}
	return str.NormalizeUUID(modelID)
}

// loadAuto 读取对工作区可用的 Auto：池内匹配用途的已启用成员作为来源；stickyMemberID 非空时优先且仅使用该成员（仍须可用）。
func loadAuto(ctx context.Context, db bun.IDB, workspaceID string, usage domain.AIModelUsage, stickyMemberID string, lock bool) (*Model, error) {
	requirement := mustRequirement(usage)
	routes := make([]Route, 0)
	query := db.NewSelect().TableExpr("auto_pool_members AS apm").
		ColumnExpr("apm.id::text AS id, apm.identifier, apm.weight").
		ColumnExpr("aip.id::text AS provider_id, aip.name AS provider_name, aip.brand, aip.api_key, aip.api_url").
		Join("JOIN ai_providers AS aip ON aip.id = apm.provider_id AND aip.workspace_id IS NULL").
		Where("apm.enabled").
		Where("apm.model_type = ?", requirement.Type).
		Where("? OR apm.input_modalities @> ?::jsonb", !requirement.RequiresText, `["text"]`).
		Where(serverstorage.AutoPoolMemberAllowedFunction+"(?, apm.id)", workspaceID)
	for _, modality := range requirement.RequiredModalities {
		query = query.Where("apm.input_modalities @> ?::jsonb", modalityJSON(modality))
	}
	if stickyMemberID != "" {
		query = query.Where("apm.id = ?", stickyMemberID)
	} else {
		query = query.OrderExpr("apm.priority ASC, apm.id ASC")
	}
	if lock {
		query = query.For("KEY SHARE OF apm, aip")
	}
	if err := query.Scan(ctx, &routes); err != nil {
		return nil, fmt.Errorf("load Auto pool routes: %w", err)
	}
	if len(routes) == 0 {
		return nil, ErrUnavailable
	}

	var caps struct {
		Type            domain.AIModelType            `bun:"model_type"`
		InputModalities []domain.AIModelInputModality `bun:"input_modalities,type:jsonb"`
		ContextWindow   int64                         `bun:"context_window"`
		MaxOutputTokens int64                         `bun:"max_output_tokens"`
	}
	if err := db.NewSelect().TableExpr("auto_pool_members AS apm").
		ColumnExpr("MIN(apm.context_window) AS context_window, MIN(apm.max_output_tokens) AS max_output_tokens").
		ColumnExpr("(array_agg(apm.model_type))[1] AS model_type").
		ColumnExpr("(array_agg(apm.input_modalities))[1] AS input_modalities").
		Where("apm.id IN (?)", bun.List(routeIDs(routes))).
		Scan(ctx, &caps); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnavailable
	} else if err != nil {
		return nil, fmt.Errorf("load Auto pool capabilities: %w", err)
	}
	return &Model{
		ID: domain.AIModelRefAuto, Scope: domain.AIModelScopePlatform, Name: AutoName,
		Type: caps.Type, InputModalities: caps.InputModalities,
		ContextWindow: caps.ContextWindow, MaxOutputTokens: caps.MaxOutputTokens,
		Usage: usage, Routes: routes,
	}, nil
}

func routeIDs(routes []Route) []string {
	ids := make([]string, len(routes))
	for i, route := range routes {
		ids[i] = route.ID
	}
	return ids
}

// autoOption 在工作区对指定用途有可用池成员时返回 Auto 选项。
func autoOption(ctx context.Context, db bun.IDB, workspaceID string, usage domain.AIModelUsage) (*Option, error) {
	requirement := mustRequirement(usage)
	var one int
	query := db.NewSelect().TableExpr("auto_pool_members AS apm").
		ColumnExpr("1").
		Where("apm.enabled").
		Where("apm.model_type = ?", requirement.Type).
		Where("? OR apm.input_modalities @> ?::jsonb", !requirement.RequiresText, `["text"]`).
		Where(serverstorage.AutoPoolMemberAllowedFunction+"(?, apm.id)", workspaceID).
		Limit(1)
	for _, modality := range requirement.RequiredModalities {
		query = query.Where("apm.input_modalities @> ?::jsonb", modalityJSON(modality))
	}
	err := query.Scan(ctx, &one)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("check Auto availability: %w", err)
	}
	return &Option{
		ID: domain.AIModelRefAuto, Scope: domain.AIModelScopePlatform, Name: AutoName, Type: requirement.Type,
	}, nil
}
