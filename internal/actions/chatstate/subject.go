//go:build server

package chatstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"uuid"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// EnsureSubject 取得或创建工作区内指定来源的聊天主体，新建时使用 subjectID。
func EnsureSubject(ctx context.Context, db bun.IDB, workspaceID string, kind domain.ChatSubjectKind, sourceID, subjectID string) (*servermodels.ChatSubject, error) {
	subject, err := findSubject(ctx, db, workspaceID, kind, sourceID)
	if err == nil {
		return subject, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("find %s chat subject: %w", kind, err)
	}
	subject = &servermodels.ChatSubject{ID: subjectID, WorkspaceID: workspaceID, Kind: string(kind), SourceID: sourceID}
	err = db.NewInsert().Model(subject).
		Column("id", "workspace_id", "kind", "source_id").
		On("CONFLICT (workspace_id, kind, source_id) DO NOTHING").
		Returning("*").
		Scan(ctx)
	if err == nil {
		return subject, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("create %s chat subject: %w", kind, err)
	}
	// 未插入时由新查询读取并发创建者已提交的主体。
	if subject, err = findSubject(ctx, db, workspaceID, kind, sourceID); err != nil {
		return nil, fmt.Errorf("reload %s chat subject: %w", kind, err)
	}
	return subject, nil
}

// EnsureSubjects 按来源编号升序批量取得或创建同类聊天主体，结果按来源编号索引。
func EnsureSubjects(ctx context.Context, db bun.IDB, workspaceID string, kind domain.ChatSubjectKind, sourceIDs []string) (map[string]*servermodels.ChatSubject, error) {
	ordered := slices.Clone(sourceIDs)
	slices.Sort(ordered)
	ordered = slices.Compact(ordered)
	subjects := make(map[string]*servermodels.ChatSubject, len(ordered))
	if err := loadSubjects(ctx, db, workspaceID, kind, ordered, subjects); err != nil {
		return nil, fmt.Errorf("find %s chat subjects: %w", kind, err)
	}
	missing := make([]*servermodels.ChatSubject, 0, len(ordered)-len(subjects))
	for _, sourceID := range ordered {
		if _, found := subjects[sourceID]; !found {
			missing = append(missing, &servermodels.ChatSubject{ID: uuid.NewV7().String(), WorkspaceID: workspaceID, Kind: string(kind), SourceID: sourceID})
		}
	}
	if len(missing) == 0 {
		return subjects, nil
	}
	var created []*servermodels.ChatSubject
	if _, err := db.NewInsert().Model(&missing).
		Column("id", "workspace_id", "kind", "source_id").
		On("CONFLICT (workspace_id, kind, source_id) DO NOTHING").
		Returning("*").
		Exec(ctx, &created); err != nil {
		return nil, fmt.Errorf("create %s chat subjects: %w", kind, err)
	}
	for _, subject := range created {
		subjects[subject.SourceID] = subject
	}
	if len(created) == len(missing) {
		return subjects, nil
	}
	// 部分未插入时由新查询读取并发创建者已提交的主体。
	if err := loadSubjects(ctx, db, workspaceID, kind, ordered, subjects); err != nil {
		return nil, fmt.Errorf("reload %s chat subjects: %w", kind, err)
	}
	return subjects, nil
}

// findSubject 读取工作区内指定来源的聊天主体。
func findSubject(ctx context.Context, db bun.IDB, workspaceID string, kind domain.ChatSubjectKind, sourceID string) (*servermodels.ChatSubject, error) {
	subject := &servermodels.ChatSubject{}
	err := db.NewSelect().Model(subject).
		Where("cs.workspace_id = ? AND cs.kind = ? AND cs.source_id = ?", workspaceID, kind, sourceID).
		Scan(ctx)
	return subject, err
}

// loadSubjects 把工作区内指定来源的已有聊天主体按来源编号写入 subjects。
func loadSubjects(ctx context.Context, db bun.IDB, workspaceID string, kind domain.ChatSubjectKind, sourceIDs []string, subjects map[string]*servermodels.ChatSubject) error {
	if len(sourceIDs) == 0 {
		return nil
	}
	var rows []*servermodels.ChatSubject
	if err := db.NewSelect().Model(&rows).
		Where("cs.workspace_id = ? AND cs.kind = ? AND cs.source_id IN (?)", workspaceID, kind, bun.List(sourceIDs)).
		Scan(ctx); err != nil {
		return err
	}
	for _, subject := range rows {
		subjects[subject.SourceID] = subject
	}
	return nil
}
