//go:build server

package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/officialidentity"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/runforyou-ai/cervi/internal/storage/server/pgerr"
	commonemail "github.com/runforyou-ai/cervi/pkg/email"
	"github.com/uptrace/bun"
)

var (
	// ErrOfficialLoginInputInvalid 表示客户端提交的 state、nonce 或 PKCE 参数格式无效。
	ErrOfficialLoginInputInvalid = errors.New("official login input invalid")
	// ErrLoginAttemptInvalid 表示登录尝试不存在、已使用、已过期或 PKCE verifier 不匹配。
	ErrLoginAttemptInvalid = errors.New("login attempt invalid")
	// ErrOfficialAccountUnavailable 表示官方账号绑定的本地账号已停用，或 ID Token 缺少可建立账号的已验证邮箱。
	ErrOfficialAccountUnavailable = errors.New("official account cannot sign in")
)

// officialLoginAttemptTTL 是登录尝试从发起到完成授权码交换的有效期。
const officialLoginAttemptTTL = 10 * time.Minute

// defaultAccountTimeZone 是官方账号首次登录新建账号时使用的时区。
const defaultAccountTimeZone = "Asia/Shanghai"

// OfficialLoginCallbackPath 是部署地址下接收授权码的固定路径。
const OfficialLoginCallbackPath = "/auth/callback"

// officialLoginOpaquePattern 限定客户端生成的 state 与 nonce 为 16 到 128 位 URL 安全字符。
var officialLoginOpaquePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

// pkcePattern 限定 PKCE verifier 与 S256 challenge 为 RFC 7636 规定的字符与长度。
var pkcePattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

// OfficialIdentityProvider 定义官方账号登录使用的身份服务能力。
type OfficialIdentityProvider interface {
	Issuer() string
	AuthorizationURL(ctx context.Context, request officialidentity.AuthorizationRequest) (string, error)
	Exchange(ctx context.Context, request officialidentity.ExchangeRequest) (officialidentity.Claims, error)
}

// StartOfficialLoginAction 登记官方账号登录尝试并生成授权地址。
type StartOfficialLoginAction struct {
	db          *bun.DB
	provider    OfficialIdentityProvider
	redirectURI string
}

// StartOfficialLoginInput 定义发起官方账号登录的输入，state、nonce 与 PKCE verifier 由客户端生成并保存。
type StartOfficialLoginInput struct {
	State         string
	Nonce         string
	CodeChallenge string
}

// StartOfficialLoginOutput 返回登录尝试编号和授权地址。
type StartOfficialLoginOutput struct {
	AttemptID        string
	AuthorizationURL string
}

// NewStartOfficialLoginAction 创建发起官方账号登录操作，redirectURI 为部署地址下的回调地址。
func NewStartOfficialLoginAction(db *bun.DB, provider OfficialIdentityProvider, redirectURI string) *StartOfficialLoginAction {
	return &StartOfficialLoginAction{db: db, provider: provider, redirectURI: redirectURI}
}

// Execute 生成授权地址后登记 Web 端登录尝试。
func (a *StartOfficialLoginAction) Execute(ctx context.Context, input StartOfficialLoginInput) (StartOfficialLoginOutput, error) {
	if !officialLoginOpaquePattern.MatchString(input.State) || !officialLoginOpaquePattern.MatchString(input.Nonce) || !pkcePattern.MatchString(input.CodeChallenge) {
		return StartOfficialLoginOutput{}, ErrOfficialLoginInputInvalid
	}
	redirectURI := a.redirectURI
	authorizationURL, err := a.provider.AuthorizationURL(ctx, officialidentity.AuthorizationRequest{
		RedirectURI:   redirectURI,
		State:         input.State,
		Nonce:         input.Nonce,
		CodeChallenge: input.CodeChallenge,
	})
	if err != nil {
		return StartOfficialLoginOutput{}, err
	}
	attempt := &servermodels.LoginAttempt{
		Purpose:       string(domain.LoginAttemptPurposeLogin),
		ClientType:    string(domain.OfficialLoginClientWeb),
		RedirectURI:   redirectURI,
		CodeChallenge: input.CodeChallenge,
		Nonce:         input.Nonce,
		ExpiresAt:     time.Now().Add(officialLoginAttemptTTL),
	}
	if _, err := a.db.NewInsert().Model(attempt).
		Column("purpose", "client_type", "redirect_uri", "code_challenge", "nonce", "expires_at").
		Returning("id::text").
		Exec(ctx); err != nil {
		return StartOfficialLoginOutput{}, fmt.Errorf("save login attempt: %w", err)
	}
	return StartOfficialLoginOutput{AttemptID: attempt.ID, AuthorizationURL: authorizationURL}, nil
}

// CompleteOfficialLoginAction 用授权码完成官方账号登录并签发登录令牌。
type CompleteOfficialLoginAction struct {
	db       *bun.DB
	provider OfficialIdentityProvider
}

// CompleteOfficialLoginInput 定义完成官方账号登录的输入；Locale 用作首次登录时新建账号的界面语言。
type CompleteOfficialLoginInput struct {
	AttemptID    string
	Code         string
	CodeVerifier string
	Locale       domain.Locale
}

// NewCompleteOfficialLoginAction 创建完成官方账号登录操作。
func NewCompleteOfficialLoginAction(db *bun.DB, provider OfficialIdentityProvider) *CompleteOfficialLoginAction {
	return &CompleteOfficialLoginAction{db: db, provider: provider}
}

// Execute 一次性消费登录尝试，交换授权码并校验 ID Token，再按外部身份绑定找到或建立账号并签发登录会话。
func (a *CompleteOfficialLoginAction) Execute(ctx context.Context, input CompleteOfficialLoginInput) (SessionOutput, error) {
	if input.Code == "" || !pkcePattern.MatchString(input.CodeVerifier) {
		return SessionOutput{}, ErrLoginAttemptInvalid
	}
	attempt, err := a.consumeAttempt(ctx, input)
	if err != nil {
		return SessionOutput{}, err
	}
	claims, err := a.provider.Exchange(ctx, officialidentity.ExchangeRequest{
		Code:         input.Code,
		RedirectURI:  attempt.RedirectURI,
		CodeVerifier: input.CodeVerifier,
		Nonce:        attempt.Nonce,
	})
	if err != nil {
		return SessionOutput{}, err
	}

	var output SessionOutput
	err = a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		account, err := a.bindAccount(ctx, tx, claims, input.Locale)
		if err != nil {
			return err
		}
		if account.Status != string(domain.AccountStatusActive) {
			return ErrOfficialAccountUnavailable
		}
		output, err = IssueSession(ctx, tx, account)
		return err
	})
	if err != nil {
		return SessionOutput{}, err
	}
	return output, nil
}

// bindAccount 按可信 issuer 与稳定 subject 找到已绑定账号；尚未绑定时按已验证邮箱关联已有账号或新建账号并登记绑定。
func (a *CompleteOfficialLoginAction) bindAccount(ctx context.Context, tx bun.Tx, claims officialidentity.Claims, locale domain.Locale) (*servermodels.Account, error) {
	// 同一官方身份的并发首次登录按 issuer 与 subject 串行，后到的一方读到先完成的绑定。
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "official-identity:"+a.provider.Issuer()+"\n"+claims.Subject); err != nil {
		return nil, fmt.Errorf("lock official identity binding: %w", err)
	}
	account := &servermodels.Account{}
	err := tx.NewSelect().Model(account).
		Join("JOIN account_external_identities AS aei ON aei.account_id = acc.id").
		Where("aei.issuer = ?", a.provider.Issuer()).
		Where("aei.subject = ?", claims.Subject).
		Scan(ctx)
	if err == nil {
		return account, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("find official account binding: %w", err)
	}
	email := commonemail.Normalize(claims.Email)
	if !claims.EmailVerified || !commonemail.Valid(email) {
		return nil, ErrOfficialAccountUnavailable
	}
	err = tx.NewSelect().Model(account).Where("lower(acc.email) = lower(?)", email).For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		// 首次登录的官方账号新建本地账号，名称缺失或不合规时使用邮箱前缀。
		displayName := strings.TrimSpace(claims.Name)
		if !domain.IdentityDisplayNameValid(displayName) {
			displayName, _, _ = strings.Cut(email, "@")
		}
		if locale != domain.LocaleEnglishUnitedStates {
			locale = domain.LocaleChineseSimplified
		}
		account, err = identityaction.CreateAccount(ctx, tx, identityaction.NewAccount{
			Email: email, EmailVerified: true, DisplayName: displayName, Locale: locale, TimeZone: defaultAccountTimeZone,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("prepare official account: %w", err)
	}
	binding := &servermodels.AccountExternalIdentity{AccountID: account.ID, Issuer: a.provider.Issuer(), Subject: claims.Subject}
	if _, err := tx.NewInsert().Model(binding).Column("account_id", "issuer", "subject").Exec(ctx); err != nil {
		if pgerr.UniqueViolationOn(err, "account_external_identities_account_issuer_unique") {
			return nil, ErrOfficialAccountUnavailable
		}
		return nil, fmt.Errorf("bind official account: %w", err)
	}
	return account, nil
}

// consumeAttempt 锁定并消费仍有效的 Web 登录尝试，并校验 PKCE verifier 与登记的 challenge 一致。
func (a *CompleteOfficialLoginAction) consumeAttempt(ctx context.Context, input CompleteOfficialLoginInput) (*servermodels.LoginAttempt, error) {
	if !common.ValidUUID(input.AttemptID) {
		return nil, ErrLoginAttemptInvalid
	}
	attempt := &servermodels.LoginAttempt{}
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewSelect().Model(attempt).
			Where("la.id = ?", input.AttemptID).
			Where("la.purpose = ?", domain.LoginAttemptPurposeLogin).
			Where("la.client_type = ?", domain.OfficialLoginClientWeb).
			Where("la.consumed_at IS NULL").
			Where("la.expires_at > now()").
			For("UPDATE").
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLoginAttemptInvalid
		}
		if err != nil {
			return fmt.Errorf("find login attempt: %w", err)
		}
		if _, err := tx.NewUpdate().Model(attempt).
			Set("consumed_at = now()").
			Set("updated_at = now()").
			WherePK().
			Exec(ctx); err != nil {
			return fmt.Errorf("consume login attempt: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// S256 challenge 为 verifier 的 SHA-256 摘要的 base64url 编码（无填充）。
	digest := sha256.Sum256([]byte(input.CodeVerifier))
	if base64.RawURLEncoding.EncodeToString(digest[:]) != attempt.CodeChallenge {
		return nil, ErrLoginAttemptInvalid
	}
	return attempt, nil
}
