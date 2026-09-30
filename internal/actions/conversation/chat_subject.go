//go:build server

package conversation

import (
	"context"
	"fmt"
	"slices"
	"uuid"

	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// EnsureOrganizationIdentityChatSubjects 按身份编号升序批量取得共享主体，结果按身份编号索引。
func EnsureOrganizationIdentityChatSubjects(ctx context.Context, db bun.IDB, organizationID string, identityIDs []string) (map[string]*servermodels.ChatSubject, error) {
	ordered := slices.Clone(identityIDs)
	slices.Sort(ordered)
	ordered = slices.Compact(ordered)
	created := make([]*servermodels.ChatSubject, len(ordered))
	for index, identityID := range ordered {
		created[index] = &servermodels.ChatSubject{
			ID: uuid.NewV7().String(), OrganizationID: organizationID,
			Kind: string(domain.ChatSubjectKindOrganizationIdentity), SourceID: identityID,
		}
	}
	if _, err := db.NewInsert().Model(&created).
		Column("id", "organization_id", "kind", "source_id").
		On("CONFLICT (organization_id, kind, source_id) DO NOTHING").
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("create organization identity chat subjects: %w", err)
	}
	// 使用新查询读取本次创建和并发创建者已提交的主体。
	var rows []*servermodels.ChatSubject
	if err := db.NewSelect().Model(&rows).
		Where("cs.organization_id = ? AND cs.kind = ? AND cs.source_id IN (?)", organizationID, domain.ChatSubjectKindOrganizationIdentity, bun.In(ordered)).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("load organization identity chat subjects: %w", err)
	}
	subjects := make(map[string]*servermodels.ChatSubject, len(rows))
	for _, subject := range rows {
		subjects[subject.SourceID] = subject
	}
	return subjects, nil
}
