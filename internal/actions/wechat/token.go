//go:build server

package wechat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

var (
	// ErrTokenFailed 表示获取微信接口调用凭据失败，原因已写入凭据行；具体原因由 TokenError 给出。
	ErrTokenFailed = errors.New("wechat access token failed")
	// ErrTokenSuperseded 表示刷新期间凭据所属配置已更换或租约已被其他服务器接手，本次结果未写回。
	ErrTokenSuperseded = errors.New("wechat access token superseded")
)

const (
	// tokenUseMargin 是使用接口调用凭据时要求的最短剩余有效期，不足时先刷新。
	tokenUseMargin = 5 * time.Minute
	// refreshLease 是一次刷新租约的时限，超过时限未结束的刷新视为中断，其他服务器可以重新认领。
	refreshLease = 30 * time.Second
	// refreshPollInterval 是等待其他服务器完成刷新时重新读取凭据的间隔。
	refreshPollInterval = 200 * time.Millisecond
)

// TokenError 是获取微信接口调用凭据失败的原因与详情，与 ErrTokenFailed 匹配。
type TokenError struct {
	Failure domain.WechatTokenFailure
	Detail  string
}

// Error 返回失败原因与详情。
func (e *TokenError) Error() string {
	return fmt.Sprintf("%s: %s %s", ErrTokenFailed, e.Failure, e.Detail)
}

// Is 让 TokenError 与 ErrTokenFailed 匹配。
func (e *TokenError) Is(target error) bool {
	return target == ErrTokenFailed
}

// tokenSource 定义一类接口调用凭据的获取方式。
type tokenSource interface {
	// prepare 在认领刷新的事务内读取并锁定凭据所属配置，返回获取新凭据的请求；配置已变化时返回错误且不认领租约。
	prepare(ctx context.Context, tx bun.Tx) (func(context.Context) (wechat.AccessToken, error), error)
	// commit 在写回结果的事务内锁定凭据所属配置，确认凭据仍属于当前配置并写回获取凭据时配置的变更。
	commit(ctx context.Context, tx bun.Tx) (bool, error)
}

// tokenKey 定义一份接口调用凭据的类别与所属 AppID。
type tokenKey struct {
	Credential domain.WechatCredential
	AppID      string
}

// accessToken 返回 key 剩余有效期不少于 margin 的接口调用凭据；force 为真时忽略已有凭据重新获取。
// 刷新时先在短事务内认领租约，请求微信不占用数据库连接，结束后写回结果。其他服务器持有租约时，非强制获取直接使用仍有效的凭据，否则等待对方写回并采用其结果：成功返回新凭据，失败返回 ErrTokenFailed；租约超时仍无结果时重新认领。获取失败时把原因写入凭据行并返回 ErrTokenFailed。
func accessToken(ctx context.Context, db *bun.DB, key tokenKey, margin time.Duration, force bool, source tokenSource) (string, error) {
	// waitedSince 是开始等待其他服务器刷新时凭据行的更新时间，未在等待时为空。
	var waitedSince *time.Time
	for {
		var token string
		var adoptedErr error
		var fetch func(context.Context) (wechat.AccessToken, error)
		var claimedAt time.Time
		err := serverstorage.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
			request, err := source.prepare(ctx, tx)
			if err != nil {
				return err
			}
			if _, err := tx.NewInsert().Model(&servermodels.WechatAccessToken{Credential: string(key.Credential), AppID: key.AppID}).
				Column("credential", "app_id").On("CONFLICT (credential, app_id) DO NOTHING").Exec(ctx); err != nil {
				return fmt.Errorf("ensure wechat access token: %w", err)
			}
			row := &servermodels.WechatAccessToken{}
			if err := tx.NewSelect().Model(row).Where("credential = ? AND app_id = ?", key.Credential, key.AppID).For("UPDATE").Scan(ctx); err != nil {
				return fmt.Errorf("lock wechat access token: %w", err)
			}
			// 租约与凭据剩余有效期按等待行锁之后的数据库时刻判断：valid 表示凭据仍有效，fresh 表示剩余有效期不少于 margin。
			var leased, valid, fresh bool
			if err := tx.NewSelect().Model((*servermodels.WechatAccessToken)(nil)).
				ColumnExpr("coalesce(refresh_started_at > clock_timestamp() - make_interval(secs => ?), false)", refreshLease.Seconds()).
				ColumnExpr("coalesce(access_token IS NOT NULL AND expires_at > clock_timestamp(), false)").
				ColumnExpr("coalesce(access_token IS NOT NULL AND expires_at > clock_timestamp() + make_interval(secs => ?), false)", margin.Seconds()).
				Where("credential = ? AND app_id = ?", key.Credential, key.AppID).Scan(ctx, &leased, &valid, &fresh); err != nil {
				return fmt.Errorf("read wechat access token lease: %w", err)
			}
			// 等待期间对方已写回结果时采用该结果。
			if waitedSince != nil && !leased && !row.UpdatedAt.Equal(*waitedSince) {
				if row.FailedAt != nil {
					adoptedErr = &TokenError{Failure: domain.WechatTokenFailure(row.Failure), Detail: row.FailureDetail}
					return nil
				}
				if valid {
					token = *row.AccessToken
					return nil
				}
			}
			if waitedSince == nil && !force && fresh {
				token = *row.AccessToken
				return nil
			}
			if leased {
				if !force && valid {
					token = *row.AccessToken
					return nil
				}
				if waitedSince == nil {
					waitedSince = &row.UpdatedAt
				}
				return nil
			}
			if err := tx.NewUpdate().Model((*servermodels.WechatAccessToken)(nil)).Where("credential = ? AND app_id = ?", key.Credential, key.AppID).
				Set("refresh_started_at = now()").Returning("refresh_started_at").Scan(ctx, &claimedAt); err != nil {
				return fmt.Errorf("claim wechat access token refresh: %w", err)
			}
			fetch = request
			return nil
		})
		switch {
		case err != nil:
			return "", err
		case adoptedErr != nil:
			return "", adoptedErr
		case token != "":
			return token, nil
		case fetch != nil:
			return finishRefresh(ctx, db, key, claimedAt, source, fetch)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(refreshPollInterval):
		}
	}
}

// finishRefresh 请求微信获取新凭据，在凭据仍属于当前配置且租约仍由本次持有时写回结果并释放租约；写回未生效时返回 ErrTokenSuperseded。
func finishRefresh(ctx context.Context, db *bun.DB, key tokenKey, claimedAt time.Time, source tokenSource, fetch func(context.Context) (wechat.AccessToken, error)) (string, error) {
	fetched, fetchErr := fetch(ctx)
	var saved bool
	err := serverstorage.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		valid, err := source.commit(ctx, tx)
		if err != nil || !valid {
			return err
		}
		update := tx.NewUpdate().Model((*servermodels.WechatAccessToken)(nil)).
			Where("credential = ? AND app_id = ?", key.Credential, key.AppID).
			Where("refresh_started_at = ?", claimedAt).
			Set("refresh_started_at = NULL")
		if fetchErr != nil {
			failure, detail := tokenFailure(fetchErr)
			update = update.Set("failed_at = now()").Set("failure = ?", failure).Set("failure_detail = ?", detail)
		} else {
			update = update.Set("access_token = ?", fetched.Value).
				Set("expires_at = now() + make_interval(secs => ?)", fetched.ExpiresIn.Seconds()).
				Set("failed_at = NULL").Set("failure = ''").Set("failure_detail = ''")
		}
		result, err := update.Exec(ctx)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		saved = rows > 0
		return err
	})
	if err != nil {
		return "", fmt.Errorf("save wechat access token: %w", err)
	}
	if !saved {
		return "", ErrTokenSuperseded
	}
	if fetchErr != nil {
		failure, detail := tokenFailure(fetchErr)
		return "", &TokenError{Failure: failure, Detail: detail}
	}
	slog.InfoContext(ctx, "已刷新微信接口调用凭据", "credential", key.Credential, "app_id", key.AppID, "expires_in", fetched.ExpiresIn)
	return fetched.Value, nil
}

// tokenFailure 把获取凭据的错误归类为失败原因与详情。
func tokenFailure(err error) (domain.WechatTokenFailure, string) {
	var apiError *wechat.APIError
	if errors.As(err, &apiError) {
		if apiError.IPNotWhitelisted() {
			return domain.WechatTokenFailureIPNotWhitelisted, apiError.RequestIP()
		}
		return domain.WechatTokenFailureRejected, fmt.Sprintf("%d %s", apiError.Code, apiError.Message)
	}
	return domain.WechatTokenFailureUnavailable, err.Error()
}

// deleteAccessToken 在事务内删除 key 的接口调用凭据。
func deleteAccessToken(ctx context.Context, tx bun.Tx, key tokenKey) error {
	if _, err := tx.NewDelete().Model((*servermodels.WechatAccessToken)(nil)).Where("credential = ? AND app_id = ?", key.Credential, key.AppID).Exec(ctx); err != nil {
		return fmt.Errorf("delete wechat access token: %w", err)
	}
	return nil
}

// saveAccessToken 在事务内写入 key 新获取的接口调用凭据，清除失败记录并结束进行中的刷新。
func saveAccessToken(ctx context.Context, tx bun.Tx, key tokenKey, token wechat.AccessToken) error {
	if _, err := tx.NewInsert().Model(&servermodels.WechatAccessToken{Credential: string(key.Credential), AppID: key.AppID, AccessToken: &token.Value}).
		Value("expires_at", "now() + make_interval(secs => ?)", token.ExpiresIn.Seconds()).
		On("CONFLICT (credential, app_id) DO UPDATE").
		Set("access_token = EXCLUDED.access_token").
		Set("expires_at = EXCLUDED.expires_at").
		Set("failed_at = NULL").Set("failure = ''").Set("failure_detail = ''").
		Set("refresh_started_at = NULL").
		Exec(ctx); err != nil {
		return fmt.Errorf("save wechat access token: %w", err)
	}
	return nil
}

// TokenState 定义接口调用凭据的到期时间与最近一次获取失败。
type TokenState struct {
	// AccessTokenExpiresAt 是接口调用凭据的到期时间，尚未成功获取时为空。
	AccessTokenExpiresAt *time.Time
	// TokenFailedAt 与 TokenFailure 是最近一次获取凭据失败的时间与原因，之后成功获取时为空。
	TokenFailedAt      *time.Time
	TokenFailure       *domain.WechatTokenFailure
	TokenFailureDetail string
}

// loadTokenState 读取 key 的接口调用凭据状态，凭据行不存在时返回空状态。
func loadTokenState(ctx context.Context, db bun.IDB, key tokenKey) (TokenState, error) {
	token := &servermodels.WechatAccessToken{}
	err := db.NewSelect().Model(token).Where("credential = ? AND app_id = ?", key.Credential, key.AppID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return TokenState{}, nil
	}
	if err != nil {
		return TokenState{}, fmt.Errorf("read wechat access token: %w", err)
	}
	state := TokenState{AccessTokenExpiresAt: token.ExpiresAt}
	if token.FailedAt != nil {
		state.TokenFailedAt, state.TokenFailure, state.TokenFailureDetail = token.FailedAt, new(domain.WechatTokenFailure(token.Failure)), token.FailureDetail
	}
	return state, nil
}
