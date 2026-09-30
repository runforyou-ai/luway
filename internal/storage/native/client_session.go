//go:build !server

package native

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/cervi/internal/clientsession"
	nativemodels "github.com/runforyou-ai/cervi/internal/storage/native/models"
)

const currentClientSessionID = "current"

// LoadClientSession 读取原生端当前登录凭据。
func (s *Store) LoadClientSession(ctx context.Context) (clientsession.Credential, bool, error) {
	record := &nativemodels.ClientSession{}
	err := s.db.NewSelect().
		Model(record).
		Where("id = ?", currentClientSessionID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return clientsession.Credential{}, false, nil
	}
	if err != nil {
		return clientsession.Credential{}, false, err
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, record.ExpiresAt)
	if err != nil {
		return clientsession.Credential{}, false, fmt.Errorf("parse client session expiration: %w", err)
	}
	return clientsession.Credential{
		ServerURL: record.ServerURL,
		AccountID: record.AccountID,
		Token:     record.Token,
		ExpiresAt: expiresAt,
	}, true, nil
}

// SaveClientSession 保存原生端当前登录凭据。
func (s *Store) SaveClientSession(ctx context.Context, credential clientsession.Credential) error {
	record := &nativemodels.ClientSession{
		ID:        currentClientSessionID,
		ServerURL: credential.ServerURL,
		AccountID: credential.AccountID,
		Token:     credential.Token,
		ExpiresAt: credential.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}
	_, err := s.db.NewInsert().
		Model(record).
		Column("id", "server_url", "account_id", "token", "expires_at").
		On("CONFLICT (id) DO UPDATE").
		Set("server_url = EXCLUDED.server_url").
		Set("account_id = EXCLUDED.account_id").
		Set("token = EXCLUDED.token").
		Set("expires_at = EXCLUDED.expires_at").
		Exec(ctx)
	return err
}

// DeleteClientSession 删除原生端当前登录凭据。
func (s *Store) DeleteClientSession(ctx context.Context) error {
	_, err := s.db.NewDelete().
		Model((*nativemodels.ClientSession)(nil)).
		Where("id = ?", currentClientSessionID).
		Exec(ctx)
	return err
}
