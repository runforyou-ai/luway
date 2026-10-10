// Package acme 经 ACME 协议以 HTTP-01 质询为域名签发证书，质询应答由调用方发布，部署中的服务器按令牌应答。
package acme

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/runforyou-ai/support/arr"
	"golang.org/x/crypto/acme"
)

// ChallengePath 是 HTTP-01 质询请求的路径前缀，令牌紧随其后。
const ChallengePath = "/.well-known/acme-challenge/"

// Challenges 发布与撤销 HTTP-01 质询应答。
type Challenges interface {
	// Put 发布令牌对应的密钥授权，发布后部署中的服务器都能按令牌应答。
	Put(ctx context.Context, token, keyAuthorization string) error
	// Delete 撤销令牌的应答。
	Delete(ctx context.Context, token string) error
}

// Certificate 是一张签发完成的证书：PEM 证书链、PEM 私钥与到期时间。
type Certificate struct {
	Chain      string
	PrivateKey string
	ExpiresAt  time.Time
}

// Issuer 向 ACME 服务申请证书。
type Issuer struct {
	directoryURL string
	challenges   Challenges
}

// NewIssuer 创建使用 directoryURL 目录的证书签发器，challenges 发布质询应答。
func NewIssuer(directoryURL string, challenges Challenges) *Issuer {
	return &Issuer{directoryURL: directoryURL, challenges: challenges}
}

// NewAccountKey 生成 PEM 编码的 ACME 账号私钥。
func NewAccountKey() (string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generate ACME account key: %w", err)
	}
	return encodePrivateKey(key)
}

// Issue 以 accountKey 对应的 ACME 账号为 domain 签发证书，账号未注册时先注册并接受服务条款。
func (i *Issuer) Issue(ctx context.Context, accountKey, domain string) (Certificate, error) {
	signer, err := decodePrivateKey(accountKey)
	if err != nil {
		return Certificate{}, err
	}
	client := &acme.Client{Key: signer, DirectoryURL: i.directoryURL}
	if _, err := client.Register(ctx, &acme.Account{}, acme.AcceptTOS); err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
		return Certificate{}, fmt.Errorf("register ACME account: %w", err)
	}
	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(domain))
	if err != nil {
		return Certificate{}, fmt.Errorf("create ACME order: %w", err)
	}
	for _, authorizationURL := range order.AuthzURLs {
		if err := i.authorize(ctx, client, authorizationURL); err != nil {
			return Certificate{}, err
		}
	}
	if order, err = client.WaitOrder(ctx, order.URI); err != nil {
		return Certificate{}, fmt.Errorf("wait ACME order: %w", err)
	}

	// 为证书生成新私钥，以只含该域名的证书请求完成订单。
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Certificate{}, fmt.Errorf("generate certificate key: %w", err)
	}
	request, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: []string{domain}}, key)
	if err != nil {
		return Certificate{}, fmt.Errorf("create certificate request: %w", err)
	}
	chain, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, request, true)
	if err != nil {
		return Certificate{}, fmt.Errorf("finalize ACME order: %w", err)
	}
	if len(chain) == 0 {
		return Certificate{}, errors.New("ACME order returned no certificate")
	}
	leaf, err := x509.ParseCertificate(chain[0])
	if err != nil {
		return Certificate{}, fmt.Errorf("parse issued certificate: %w", err)
	}
	privateKey, err := encodePrivateKey(key)
	if err != nil {
		return Certificate{}, err
	}
	var encoded []byte
	for _, der := range chain {
		encoded = append(encoded, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	return Certificate{Chain: string(encoded), PrivateKey: privateKey, ExpiresAt: leaf.NotAfter}, nil
}

// authorize 以 HTTP-01 质询完成一项待验证的域名授权，结束后撤销发布的应答。
func (i *Issuer) authorize(ctx context.Context, client *acme.Client, authorizationURL string) error {
	authorization, err := client.GetAuthorization(ctx, authorizationURL)
	if err != nil {
		return fmt.Errorf("read ACME authorization: %w", err)
	}
	if authorization.Status == acme.StatusValid {
		return nil
	}
	var challenge *acme.Challenge
	for _, candidate := range authorization.Challenges {
		if candidate.Type == "http-01" {
			challenge = candidate
			break
		}
	}
	if challenge == nil {
		return errors.New("ACME authorization offers no http-01 challenge")
	}
	keyAuthorization, err := client.HTTP01ChallengeResponse(challenge.Token)
	if err != nil {
		return fmt.Errorf("compute ACME challenge response: %w", err)
	}
	if err := i.challenges.Put(ctx, challenge.Token, keyAuthorization); err != nil {
		return fmt.Errorf("publish ACME challenge: %w", err)
	}
	defer func() {
		if err := i.challenges.Delete(context.WithoutCancel(ctx), challenge.Token); err != nil {
			slog.WarnContext(ctx, "撤销 ACME 质询应答失败", "error", err)
		}
	}()
	if _, err := client.Accept(ctx, challenge); err != nil {
		return fmt.Errorf("accept ACME challenge: %w", err)
	}
	if _, err := client.WaitAuthorization(ctx, authorization.URI); err != nil {
		return fmt.Errorf("wait ACME authorization: %w", err)
	}
	return nil
}

// Reason 返回 ACME 服务给出的失败说明，不是 ACME 服务的错误时为空。
func Reason(err error) string {
	var authorizationErr *acme.AuthorizationError
	if errors.As(err, &authorizationErr) {
		return strings.Join(arr.Map(authorizationErr.Errors, Reason), "; ")
	}
	var orderErr *acme.OrderError
	if errors.As(err, &orderErr) {
		return "ACME order " + orderErr.Status
	}
	var acmeErr *acme.Error
	if errors.As(err, &acmeErr) {
		return acmeErr.Detail
	}
	return ""
}

// encodePrivateKey 把私钥编码为 PKCS #8 PEM。
func encodePrivateKey(key crypto.Signer) (string, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", fmt.Errorf("encode private key: %w", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
}

// decodePrivateKey 解析 PKCS #8 PEM 私钥。
func decodePrivateKey(encoded string) (crypto.Signer, error) {
	block, _ := pem.Decode([]byte(encoded))
	if block == nil {
		return nil, errors.New("ACME account key is not PEM encoded")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse ACME account key: %w", err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, errors.New("ACME account key cannot sign")
	}
	return signer, nil
}
