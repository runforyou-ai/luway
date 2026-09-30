//go:build server

package chatstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// EnsureOrganizationIdentityChatSubject 取得或创建企业身份聊天主体。
func EnsureOrganizationIdentityChatSubject(ctx context.Context, db bun.IDB, organizationID, identityID, subjectID string) (*servermodels.ChatSubject, error) {
	subject := &servermodels.ChatSubject{}
	err := db.NewSelect().Model(subject).
		Where("cs.organization_id = ?", organizationID).
		Where("cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Where("cs.source_id = ?", identityID).
		Scan(ctx)
	if err == nil {
		return subject, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("find organization identity chat subject: %w", err)
	}
	subject = &servermodels.ChatSubject{
		ID: subjectID, OrganizationID: organizationID,
		Kind: string(domain.ChatSubjectKindOrganizationIdentity), SourceID: identityID,
	}
	if _, err := db.NewInsert().Model(subject).
		Column("id", "organization_id", "kind", "source_id").
		On("CONFLICT (organization_id, kind, source_id) DO NOTHING").
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("create organization identity chat subject: %w", err)
	}
	// 使用新查询读取并发创建者已提交的主体。
	if err := db.NewSelect().Model(subject).
		Where("cs.organization_id = ? AND cs.kind = ? AND cs.source_id = ?", organizationID, domain.ChatSubjectKindOrganizationIdentity, identityID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("reload organization identity chat subject: %w", err)
	}
	return subject, nil
}
