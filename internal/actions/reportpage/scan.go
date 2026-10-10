//go:build server

// Package reportpage 在一条原始 SQL 中读取报表集合的总行数与一页结果，并定义报表共用的 AI 员工筛选范围。
package reportpage

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/uptrace/bun"
)

// Scan 基于 with 子句定义的 source 集合同时计算总行数与按 order 排序的一页，把该页解码为 T，name 写入错误描述。
func Scan[T any](ctx context.Context, db bun.IDB, with, source, order string, args []any, page, pageSize int, name string) (int, []T, error) {
	var total int
	var raw json.RawMessage
	if err := db.NewRaw(with+`
SELECT (SELECT count(*) FROM `+source+`) AS total,
	(SELECT coalesce(json_agg(p ORDER BY p.position), '[]') FROM (
		SELECT `+source+`.*, row_number() OVER (ORDER BY `+order+`) AS position
		FROM `+source+`
		ORDER BY position
		LIMIT ? OFFSET ?
	) p) AS rows`, slices.Concat(args, []any{pageSize, (page - 1) * pageSize})...).Scan(ctx, &total, &raw); err != nil {
		return 0, nil, fmt.Errorf("list %s: %w", name, err)
	}
	var rows []T
	if err := json.Unmarshal(raw, &rows); err != nil {
		return 0, nil, fmt.Errorf("decode %s: %w", name, err)
	}
	return total, rows, nil
}
