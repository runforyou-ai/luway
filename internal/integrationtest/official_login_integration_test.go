//go:build server

package integrationtest

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/appservice/direct"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/i18n"
	"github.com/runforyou-ai/cervi/internal/integration/officialidentity"
	"github.com/runforyou-ai/cervi/internal/servertest"
	serverstorage "github.com/runforyou-ai/cervi/internal/storage/server"
	serverfilecontent "github.com/runforyou-ai/cervi/internal/storage/server/filecontent"
	"github.com/uptrace/bun"
)

const (
	testOfficialWebClientID     = "web-client"
	testOfficialWebClientSecret = "web-client-secret"
	testOfficialCodeVerifier    = "official-login-code-verifier-0123456789abcdefghijklmnop"
)

// fakeIdentityProvider 是签发 RS256 ID Token 的测试用 OIDC 提供方，令牌中的 subject 与 nonce 由测试指定。
type fakeIdentityProvider struct {
	server *httptest.Server
	key    *rsa.PrivateKey

	mu      sync.Mutex
	subject string
	nonce   string
	// tokenStatus 非零时令牌端点直接返回该状态码。
	tokenStatus int
	// email 与 emailVerified 是 ID Token 中 email 与 email_verified 声明的取值。
	email         string
	emailVerified any
	// authorizationEndpoint 非空时替换发现文档中的授权端点。
	authorizationEndpoint string
}

// newFakeIdentityProvider 启动提供发现文档、JWKS 与令牌端点的 HTTPS 测试服务。
func newFakeIdentityProvider(t *testing.T) *fakeIdentityProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	provider := &fakeIdentityProvider{key: key, email: uniqueEmail("official"), emailVerified: true}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(writer http.ResponseWriter, request *http.Request) {
		issuer := provider.server.URL
		provider.mu.Lock()
		authorizationEndpoint := provider.authorizationEndpoint
		provider.mu.Unlock()
		if authorizationEndpoint == "" {
			authorizationEndpoint = issuer + "/oauth/authorize"
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"issuer":                                issuer,
			"authorization_endpoint":                authorizationEndpoint,
			"token_endpoint":                        issuer + "/oauth/token",
			"jwks_uri":                              issuer + "/oauth/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/oauth/jwks", func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/oauth/token", func(writer http.ResponseWriter, request *http.Request) {
		provider.mu.Lock()
		tokenStatus := provider.tokenStatus
		provider.mu.Unlock()
		if tokenStatus != 0 {
			writer.WriteHeader(tokenStatus)
			return
		}
		// 令牌端点校验客户端凭据与 PKCE verifier，模拟身份服务对授权码的校验。
		clientID, clientSecret, _ := request.BasicAuth()
		if request.ParseForm() != nil || clientID != testOfficialWebClientID || clientSecret != testOfficialWebClientSecret ||
			request.PostForm.Get("code") != "valid-code" || request.PostForm.Get("code_verifier") != testOfficialCodeVerifier {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		provider.mu.Lock()
		claims := jwt.MapClaims{
			"iss": provider.server.URL, "sub": provider.subject, "aud": testOfficialWebClientID,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(), "nonce": provider.nonce,
			"email": provider.email, "email_verified": provider.emailVerified, "name": "官方成员",
		}
		provider.mu.Unlock()
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		token.Header["kid"] = "test-key"
		signed, err := token.SignedString(key)
		if err != nil {
			t.Error(err)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"access_token": "access", "token_type": "Bearer", "expires_in": 600, "id_token": signed})
	})
	provider.server = httptest.NewTLSServer(mux)
	t.Cleanup(provider.server.Close)
	return provider
}

// issue 指定下一次签发的 ID Token 中的 subject 与 nonce。
func (p *fakeIdentityProvider) issue(subject string, nonce string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.subject, p.nonce = subject, nonce
}

// officialLoginFixture 是官方账号登录测试使用的测试身份服务、托管后端和官方账号标识。
type officialLoginFixture struct {
	db       *bun.DB
	provider *fakeIdentityProvider
	backend  *direct.Backend
	ctx      context.Context
	subject  string
}

// newOfficialLoginFixture 创建使用测试身份服务的托管后端，官方账号标识在测试库内唯一。
func newOfficialLoginFixture(t *testing.T) officialLoginFixture {
	t.Helper()
	provider := newFakeIdentityProvider(t)
	store, err := serverstorage.Open(context.Background(), servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	client := officialidentity.NewClient(officialidentity.Config{
		Issuer: provider.server.URL, WebClientID: testOfficialWebClientID, WebClientSecret: testOfficialWebClientSecret,
		HTTPClient: provider.server.Client(),
	})
	backend := direct.New(db, direct.DeploymentConfig{Mode: domain.DeploymentModeManaged, PublicURL: testPublicURL, OfficialIdentity: client},
		nil, serverfilecontent.S3Config{}, nil, nil, nil, nil, nil)
	return officialLoginFixture{db: db, provider: provider, backend: backend, ctx: context.Background(), subject: "subject-" + uniqueEmail("official")}
}

// signIn 以指定官方账号标识完成一次登录并返回登录会话。
func (f officialLoginFixture) signIn(t *testing.T, subject string) appservice.Auth {
	t.Helper()
	nonce := "nonce-0123456789abcdef"
	attemptID, _ := f.start(t, nonce)
	f.provider.issue(subject, nonce)
	auth, err := f.complete(attemptID, testOfficialCodeVerifier)
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

// start 发起一次登录尝试，返回尝试编号和授权地址中的参数。
func (f officialLoginFixture) start(t *testing.T, nonce string) (string, url.Values) {
	t.Helper()
	digest := sha256.Sum256([]byte(testOfficialCodeVerifier))
	started, err := f.backend.StartOfficialLogin(f.ctx, appservice.RequestMeta{}, appservice.OfficialLoginInput{
		State: "state-0123456789abcdef", Nonce: nonce, CodeChallenge: base64.RawURLEncoding.EncodeToString(digest[:]),
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizationURL, err := url.Parse(started.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	return started.AttemptID, authorizationURL.Query()
}

// complete 用测试授权码完成登录尝试。
func (f officialLoginFixture) complete(attemptID string, verifier string) (appservice.Auth, error) {
	return f.completeWithCode(attemptID, "valid-code", verifier)
}

// completeWithCode 用指定授权码完成登录尝试。
func (f officialLoginFixture) completeWithCode(attemptID string, code string, verifier string) (appservice.Auth, error) {
	return f.backend.CompleteOfficialLogin(f.ctx, appservice.RequestMeta{Locale: appservice.LocaleEnglishUnitedStates}, appservice.OfficialLoginCompletion{AttemptID: attemptID, Code: code, CodeVerifier: verifier})
}

// configure 在持有锁时修改测试身份服务的行为。
func (p *fakeIdentityProvider) configure(change func(*fakeIdentityProvider)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	change(p)
}

// requireErrorKey 断言错误是使用指定文案键的业务错误。
func requireErrorKey(t *testing.T, err error, key i18n.Key) {
	t.Helper()
	expected, _ := i18n.Localize("", key)
	var appError *appservice.Error
	if !errors.As(err, &appError) || appError.Message != expected {
		t.Fatalf("错误应为 %s（%s），实际为 %v", key, expected, err)
	}
}

// TestOfficialLoginCreatesAndReusesAccount 验证首次官方账号登录按声明建立账号并绑定，再次登录复用同一账号，授权地址携带 PKCE、nonce 与部署地址回调。
func TestOfficialLoginCreatesAndReusesAccount(t *testing.T) {
	t.Parallel()
	f := newOfficialLoginFixture(t)
	nonce := "nonce-0123456789abcdef"
	attemptID, query := f.start(t, nonce)

	if query.Get("client_id") != testOfficialWebClientID || query.Get("code_challenge_method") != "S256" || query.Get("nonce") != nonce ||
		query.Get("state") != "state-0123456789abcdef" || query.Get("redirect_uri") != testPublicURL+"/auth/callback" ||
		!strings.Contains(query.Get("scope"), "openid") {
		t.Fatalf("授权地址参数不正确: %v", query)
	}

	f.provider.issue(f.subject, nonce)
	auth, err := f.complete(attemptID, testOfficialCodeVerifier)
	if err != nil {
		t.Fatal(err)
	}
	if auth.Token == "" || auth.Account.Email != f.provider.email || auth.Account.DisplayName != "官方成员" || auth.Account.Locale != appservice.LocaleEnglishUnitedStates {
		t.Fatalf("登录结果不正确: %+v", auth.Account)
	}
	meta := appservice.RequestMeta{Token: auth.Token}
	account, err := f.backend.LoadAccount(f.ctx, meta)
	if err != nil || account.ID != auth.Account.ID {
		t.Fatalf("签发的会话不可用: %v", err)
	}
	// 官方账号登录后可以创建并进入自己的工作区。
	slug := "official-" + strings.ReplaceAll(strings.Split(f.provider.email, "@")[0], ".", "-")
	workspace, err := f.backend.CreateWorkspace(f.ctx, meta, appservice.WorkspaceInput{Name: "官方工作区", Slug: slug})
	if err != nil {
		t.Fatal(err)
	}
	meta.WorkspaceID = workspace.ID
	if identity, err := f.backend.LoadIdentity(f.ctx, meta); err != nil || identity.User.Email != f.provider.email {
		t.Fatalf("工作区成员身份 = %+v, err = %v", identity, err)
	}

	// 登录尝试只能使用一次，再次登录复用已绑定的账号。
	_, err = f.complete(attemptID, testOfficialCodeVerifier)
	requireErrorKey(t, err, i18n.ErrorOfficialLoginExpired)
	if again := f.signIn(t, f.subject); again.Account.ID != auth.Account.ID {
		t.Fatalf("再次登录的账号 = %s, want %s", again.Account.ID, auth.Account.ID)
	}
}

// TestOfficialLoginConcurrentFirstLogin 验证同一官方账号并发首次登录时都成功，并绑定到同一个账号。
func TestOfficialLoginConcurrentFirstLogin(t *testing.T) {
	t.Parallel()
	f := newOfficialLoginFixture(t)
	nonce := "nonce-0123456789abcdef"
	attempts := []string{}
	for range 3 {
		attemptID, _ := f.start(t, nonce)
		attempts = append(attempts, attemptID)
	}
	f.provider.issue(f.subject, nonce)
	results := make([]appservice.Auth, len(attempts))
	errs := make([]error, len(attempts))
	var wg sync.WaitGroup
	for index, attemptID := range attempts {
		wg.Go(func() { results[index], errs[index] = f.complete(attemptID, testOfficialCodeVerifier) })
	}
	wg.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("第 %d 次登录失败: %v", index+1, err)
		}
		if results[index].Account.ID != results[0].Account.ID {
			t.Fatalf("并发登录绑定了不同账号: %s, %s", results[index].Account.ID, results[0].Account.ID)
		}
	}
}

// TestOfficialLoginBindsVerifiedEmailAccount 验证未绑定的官方账号按已验证邮箱关联已有账号，邮箱未验证时拒绝登录。
func TestOfficialLoginBindsVerifiedEmailAccount(t *testing.T) {
	t.Parallel()
	f := newOfficialLoginFixture(t)
	existing := installWorkspace(t, f.db, workspaceSpec{Name: "已有账号", DisplayName: "已有成员", Email: f.provider.email, Password: "password123"})

	f.provider.configure(func(p *fakeIdentityProvider) { p.emailVerified = false })
	nonce := "nonce-0123456789abcdef"
	attemptID, _ := f.start(t, nonce)
	f.provider.issue(f.subject, nonce)
	_, err := f.complete(attemptID, testOfficialCodeVerifier)
	requireErrorKey(t, err, i18n.ErrorOfficialAccountUnavailable)

	f.provider.configure(func(p *fakeIdentityProvider) { p.emailVerified = true })
	if auth := f.signIn(t, f.subject); auth.Account.ID != existing.Identity.Account.ID {
		t.Fatalf("关联的账号 = %s, want %s", auth.Account.ID, existing.Identity.Account.ID)
	}
}

// TestOfficialLoginRejectsInvalidAttempts 验证 verifier 不匹配、过期或用途不符的登录尝试被拒绝。
func TestOfficialLoginRejectsInvalidAttempts(t *testing.T) {
	t.Parallel()
	f := newOfficialLoginFixture(t)
	nonce := "nonce-0123456789abcdef"
	f.provider.issue(f.subject, nonce)

	wrongVerifier, _ := f.start(t, nonce)
	_, err := f.complete(wrongVerifier, strings.Repeat("x", 43))
	requireErrorKey(t, err, i18n.ErrorOfficialLoginExpired)

	expired, _ := f.start(t, nonce)
	if _, err := f.db.NewUpdate().Table("login_attempts").Set("expires_at = now() - interval '1 second'").Where("id = ?", expired).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err = f.complete(expired, testOfficialCodeVerifier)
	requireErrorKey(t, err, i18n.ErrorOfficialLoginExpired)

	// 其他用途的登录尝试不能由登录接口完成。
	otherPurpose, _ := f.start(t, nonce)
	if _, err := f.db.NewUpdate().Table("login_attempts").Set("purpose = ?", "accept_invitation").Where("id = ?", otherPurpose).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err = f.complete(otherPurpose, testOfficialCodeVerifier)
	requireErrorKey(t, err, i18n.ErrorOfficialLoginExpired)
}

// TestOfficialLoginClassifiesTokenEndpointFailures 验证令牌端点拒绝授权码时提示重新登录，服务端错误时提示服务暂时不可用。
func TestOfficialLoginClassifiesTokenEndpointFailures(t *testing.T) {
	t.Parallel()
	f := newOfficialLoginFixture(t)
	nonce := "nonce-0123456789abcdef"
	f.provider.issue(f.subject, nonce)

	rejected, _ := f.start(t, nonce)
	_, err := f.completeWithCode(rejected, "unknown-code", testOfficialCodeVerifier)
	requireErrorKey(t, err, i18n.ErrorOfficialLoginRejected)

	unavailable, _ := f.start(t, nonce)
	f.provider.configure(func(p *fakeIdentityProvider) { p.tokenStatus = http.StatusServiceUnavailable })
	_, err = f.complete(unavailable, testOfficialCodeVerifier)
	requireErrorKey(t, err, i18n.ErrorOfficialIdentityUnavailable)
}

// TestOfficialLoginIgnoresMalformedOptionalClaims 验证已绑定的官方账号在可选声明类型不符时仍按 subject 完成登录。
func TestOfficialLoginIgnoresMalformedOptionalClaims(t *testing.T) {
	t.Parallel()
	f := newOfficialLoginFixture(t)
	bound := f.signIn(t, f.subject)
	f.provider.configure(func(p *fakeIdentityProvider) { p.emailVerified = map[string]any{"unexpected": "shape"} })

	if auth := f.signIn(t, f.subject); auth.Account.ID != bound.Account.ID {
		t.Fatalf("可选声明异常时登录到了其他账号: %s", auth.Account.ID)
	}
}

// TestOfficialLoginRejectsForeignAuthorizationEndpoint 验证发现文档的授权端点不属于 issuer 主机时不生成授权地址。
func TestOfficialLoginRejectsForeignAuthorizationEndpoint(t *testing.T) {
	t.Parallel()
	f := newOfficialLoginFixture(t)
	f.provider.configure(func(p *fakeIdentityProvider) { p.authorizationEndpoint = "https://attacker.example/oauth/authorize" })
	digest := sha256.Sum256([]byte(testOfficialCodeVerifier))
	_, err := f.backend.StartOfficialLogin(f.ctx, appservice.RequestMeta{}, appservice.OfficialLoginInput{
		State: "state-0123456789abcdef", Nonce: "nonce-0123456789abcdef", CodeChallenge: base64.RawURLEncoding.EncodeToString(digest[:]),
	})
	requireErrorKey(t, err, i18n.ErrorOfficialIdentityUnavailable)
}

// TestOfficialLoginRejectsUntrustedIdentity 验证 nonce 不一致的令牌和已停用的账号不能登录。
func TestOfficialLoginRejectsUntrustedIdentity(t *testing.T) {
	t.Parallel()
	f := newOfficialLoginFixture(t)
	nonce := "nonce-0123456789abcdef"

	attemptID, _ := f.start(t, nonce)
	f.provider.issue(f.subject, "nonce-from-another-request")
	_, err := f.complete(attemptID, testOfficialCodeVerifier)
	requireErrorKey(t, err, i18n.ErrorOfficialLoginRejected)

	auth := f.signIn(t, f.subject)
	if _, err := f.db.NewUpdate().Table("accounts").Set("status = ?", domain.AccountStatusInactive).Where("id = ?", auth.Account.ID).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	attemptID, _ = f.start(t, nonce)
	f.provider.issue(f.subject, nonce)
	_, err = f.complete(attemptID, testOfficialCodeVerifier)
	requireErrorKey(t, err, i18n.ErrorOfficialAccountUnavailable)
	_, err = f.backend.LoadAccount(f.ctx, appservice.RequestMeta{Token: auth.Token})
	requireSessionState(t, err, appservice.SessionStateLogin)
}

// TestOfficialLoginAvailability 验证自托管部署不提供官方账号登录，身份服务不可用时返回对应错误。
func TestOfficialLoginAvailability(t *testing.T) {
	t.Parallel()
	f := newOfficialLoginFixture(t)
	selfHosted := direct.New(f.db, direct.DeploymentConfig{Mode: domain.DeploymentModeSelfHosted, PublicURL: testPublicURL},
		nil, serverfilecontent.S3Config{}, nil, nil, nil, nil, nil)
	_, err := selfHosted.StartOfficialLogin(f.ctx, appservice.RequestMeta{}, appservice.OfficialLoginInput{})
	requireErrorKey(t, err, i18n.ErrorOfficialLoginNotAvailable)

	f.provider.server.Close()
	digest := sha256.Sum256([]byte(testOfficialCodeVerifier))
	_, err = f.backend.StartOfficialLogin(f.ctx, appservice.RequestMeta{}, appservice.OfficialLoginInput{
		State: "state-0123456789abcdef", Nonce: "nonce-0123456789abcdef", CodeChallenge: base64.RawURLEncoding.EncodeToString(digest[:]),
	})
	requireErrorKey(t, err, i18n.ErrorOfficialIdentityUnavailable)
}
