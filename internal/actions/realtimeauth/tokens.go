//go:build server

// Package realtimeauth 签发并复核访客与电脑的短期实时凭据。
package realtimeauth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/runforyou-ai/luway/internal/common/customeridentity"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// Lifetime 是短期实时凭据的有效期上限。
const Lifetime = 5 * time.Minute

// ErrInvalid 表示实时凭据或其业务身份失效。
var ErrInvalid = errors.New("invalid realtime credential")

// Identity 是复核后的精确业务主体与频道范围。
type Identity struct {
	ID          string
	WorkspaceID string
	ChannelID   string
	ExternalID  string
	Secret      string
	ExpiresAt   time.Time
}

// Claims 仅携带主体编号、用途、执行器版本和有效期。
type Claims struct {
	jwt.RegisteredClaims
	Version string `json:"version,omitempty"`
}

// Visitor 读取有效网站渠道中的指定身份及当前签名密钥。
func Visitor(ctx context.Context, db bun.IDB, id string) (Identity, error) {
	var identity Identity
	if !str.IsUUID(id) {
		return identity, ErrInvalid
	}
	err := db.NewRaw(`SELECT ci.id::text, ci.workspace_id::text, ci.channel_id::text, ci.external_id,
 COALESCE(css.customer_identity_secret, '') AS secret
 FROM channel_identities ci JOIN channels c ON c.id = ci.channel_id AND c.workspace_id = ci.workspace_id
 JOIN workspaces w ON w.id = ci.workspace_id
 JOIN customer_service_settings css ON css.workspace_id = ci.workspace_id
 WHERE ci.id = ? AND c.enabled AND c.type = ? AND w.lifecycle_status = ?`, id, domain.ChannelTypeWebsite, domain.WorkspaceLifecycleActive).Scan(ctx, &identity)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, ErrInvalid
	}
	if err != nil {
		return Identity{}, err
	}
	kind, key, ok := customeridentity.ParseExternalID(identity.ExternalID)
	if !ok {
		return Identity{}, ErrInvalid
	}
	if kind == customeridentity.ExternalIDAnonymous {
		identity.Secret = key
	}
	if identity.Secret == "" {
		return Identity{}, ErrInvalid
	}
	return identity, nil
}

// Computer 读取仍有访问资格的电脑与当前凭据摘要。
func Computer(ctx context.Context, db bun.IDB, id string) (Identity, error) {
	var identity Identity
	if !str.IsUUID(id) {
		return identity, ErrInvalid
	}
	err := db.NewRaw(`SELECT c.id::text, c.workspace_id::text, c.credential_hash AS secret
 FROM computers c JOIN workspaces w ON w.id = c.workspace_id
 LEFT JOIN users u ON u.id = c.owner_user_id AND u.workspace_id = c.workspace_id
 LEFT JOIN accounts a ON a.id = u.account_id
 WHERE c.id = ? AND c.revoked_at IS NULL AND w.lifecycle_status = ?
 AND (c.kind = ? OR (u.status = ? AND a.status = ?))`, id, domain.WorkspaceLifecycleActive,
		domain.ComputerKindWorkspace, domain.IdentityStatusActive, domain.AccountStatusActive).Scan(ctx, &identity)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, ErrInvalid
	}
	return identity, err
}

// Issue 按数据库时钟签发用途隔离的凭据，客户身份的有效期构成额外上限。
func Issue(ctx context.Context, db bun.IDB, kind string, identity Identity, customerToken string) (string, error) {
	now, err := serverstorage.ClockNow(ctx, db)
	if err != nil {
		return "", err
	}
	expiry := now.Add(Lifetime)
	if kind == "v" {
		identityKind, key, ok := customeridentity.ParseExternalID(identity.ExternalID)
		if !ok {
			return "", ErrInvalid
		}
		if identityKind == customeridentity.ExternalIDCustomer {
			customer, err := customeridentity.Verify(identity.Secret, customerToken, now)
			if err != nil || customer.UserID != key {
				return "", ErrInvalid
			}
			if customer.ExpiresAt.Before(expiry) {
				expiry = customer.ExpiresAt
			}
		}
	}
	claims := Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: identity.ID, Audience: []string{"realtime-" + kind}, ExpiresAt: jwt.NewNumericDate(expiry)}}
	if kind == "c" {
		claims.Version = domain.ExecutorVersion
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("realtime-" + kind + ":" + identity.Secret))
	return "rt_" + kind + "." + token, err
}

// Authenticate 以当前业务密钥复核短期凭据和数据库状态。
func Authenticate(ctx context.Context, db bun.IDB, token string) (string, Identity, error) {
	prefix, raw, ok := strings.Cut(token, ".")
	if !ok || (prefix != "rt_v" && prefix != "rt_c") {
		return "", Identity{}, ErrInvalid
	}
	kind := strings.TrimPrefix(prefix, "rt_")
	now, err := serverstorage.ClockNow(ctx, db)
	if err != nil {
		return "", Identity{}, err
	}
	claims := &Claims{}
	var identity Identity
	_, err = jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) {
		var err error
		if kind == "v" {
			identity, err = Visitor(ctx, db, claims.Subject)
		} else {
			identity, err = Computer(ctx, db, claims.Subject)
		}
		return []byte("realtime-" + kind + ":" + identity.Secret), err
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience("realtime-"+kind), jwt.WithExpirationRequired(), jwt.WithTimeFunc(func() time.Time { return now }))
	if err != nil {
		return "", Identity{}, err
	}
	if claims.ExpiresAt.Time.After(now.Add(Lifetime)) || (kind == "c" && claims.Version != domain.ExecutorVersion) {
		return "", Identity{}, ErrInvalid
	}
	identity.ExpiresAt = claims.ExpiresAt.Time
	identity.Secret, identity.ExternalID = "", ""
	return kind, identity, nil
}
