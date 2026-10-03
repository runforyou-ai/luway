// Package control 实现服务器与 control 的通信：登记服务器、用激活码换取授权码、拉取当前授权码和上报运行指标，每个请求都用服务器密钥签名。
package control

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/common/license"
	"github.com/runforyou-ai/luway/pkg/httpsig"
)

var (
	// ErrActivationCodeInvalid 表示激活码不存在、不属于本产品或授权已到期。
	ErrActivationCodeInvalid = errors.New("control: activation code invalid")
	// ErrServerMismatch 表示激活码已绑定其他服务器，或本服务器已绑定其他授权。
	ErrServerMismatch = errors.New("control: server mismatch")
	// ErrServerKeyMismatch 表示 control 登记的服务器公钥与本服务器密钥不一致。
	ErrServerKeyMismatch = errors.New("control: server key mismatch")
	// ErrLicenseNotFound 表示 control 中没有适用于本服务器的授权。
	ErrLicenseNotFound = errors.New("control: license not found")
	// ErrUnavailable 表示 control 无法连接、限流或暂时不可用。
	ErrUnavailable = errors.New("control: unavailable")
)

// requestTimeout 是单次请求 control 的超时时间。
const requestTimeout = 30 * time.Second

// Identity 是服务器在 control 中的身份：服务器标识与签名私钥。
type Identity struct {
	ServerID   string
	PrivateKey ed25519.PrivateKey
}

// IdentitySource 读取当前服务器身份，平台尚未完成首次安装时返回错误。
type IdentitySource func(context.Context) (Identity, error)

// ProblemError 是 control 返回的 RFC 9457 错误。
type ProblemError struct {
	Status int
	Code   string
	Detail string
}

// Error 返回包含状态码、错误码和说明的错误文本。
func (e *ProblemError) Error() string {
	return fmt.Sprintf("control: %d %s: %s", e.Status, e.Code, e.Detail)
}

// Client 是 control 服务器接口客户端。
type Client struct {
	baseURL  string
	version  string
	identity IdentitySource
	http     *http.Client
}

// New 创建 control 客户端，baseURL 是 control 服务地址，version 是上报的产品版本，identity 提供签名用的服务器身份。
func New(baseURL, version string, identity IdentitySource) *Client {
	transport := &httpsig.Transport{Signer: func(request *http.Request) (httpsig.Signer, error) {
		current, err := identity(request.Context())
		if err != nil {
			return httpsig.Signer{}, err
		}
		return httpsig.Signer{KeyID: current.ServerID, Key: current.PrivateKey}, nil
	}}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"), version: version, identity: identity,
		http: &http.Client{Transport: transport, Timeout: requestTimeout},
	}
}

// serverKey 是登记与激活请求体中的产品、服务器公钥和版本。
type serverKey struct {
	Product        string `json:"product"`
	PublicKey      string `json:"public_key"`
	Version        string `json:"version,omitempty"`
	ActivationCode string `json:"activation_code,omitempty"`
}

// Register 登记服务器与其公钥和当前版本，可重复调用。
func (c *Client) Register(ctx context.Context) error {
	body, err := c.serverKey(ctx, "")
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, "/api/v1/servers", body, nil)
}

// Activate 用激活码换取绑定本服务器的授权码，服务器未登记时同时完成登记。
func (c *Client) Activate(ctx context.Context, activationCode string) (string, error) {
	body, err := c.serverKey(ctx, activationCode)
	if err != nil {
		return "", err
	}
	var output struct {
		LicenseCode string `json:"license_code"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v1/activations", body, &output); err != nil {
		return "", err
	}
	return output.LicenseCode, nil
}

// License 返回 control 中本服务器当前的授权码；没有适用的授权时返回 ErrLicenseNotFound。
func (c *Client) License(ctx context.Context) (string, error) {
	var output struct {
		LicenseCode string `json:"license_code"`
	}
	err := c.do(ctx, http.MethodGet, "/api/v1/license", nil, &output)
	var problem *ProblemError
	if errors.As(err, &problem) && problem.Status == http.StatusNotFound {
		return "", fmt.Errorf("%w: %w", ErrLicenseNotFound, err)
	}
	if err != nil {
		return "", err
	}
	return output.LicenseCode, nil
}

// serverKey 读取服务器身份并编码登记或激活请求体。
func (c *Client) serverKey(ctx context.Context, activationCode string) ([]byte, error) {
	current, err := c.identity(ctx)
	if err != nil {
		return nil, err
	}
	publicKey := current.PrivateKey.Public().(ed25519.PublicKey)
	return json.Marshal(serverKey{
		Product: license.ProductID, PublicKey: base64.RawURLEncoding.EncodeToString(publicKey),
		Version: c.version, ActivationCode: activationCode,
	})
}

// do 发送签名请求，按 control 错误码返回对应错误，成功时把响应解码到 output。
func (c *Client) do(ctx context.Context, method, path string, body []byte, output any) error {
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		if output == nil {
			return nil
		}
		if err := json.Unmarshal(payload, output); err != nil {
			return fmt.Errorf("decode control response: %w", err)
		}
		return nil
	}
	problem := &ProblemError{Status: response.StatusCode}
	var decoded struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
		Title  string `json:"title"`
	}
	if json.Unmarshal(payload, &decoded) == nil {
		problem.Code, problem.Detail = decoded.Code, decoded.Detail
		if problem.Detail == "" {
			problem.Detail = decoded.Title
		}
	}
	// control 的错误码映射为客户端错误，限流与服务端错误视为暂时不可用。
	switch {
	case problem.Code == "activation_code_invalid":
		return fmt.Errorf("%w: %w", ErrActivationCodeInvalid, problem)
	case problem.Code == "server_mismatch":
		return fmt.Errorf("%w: %w", ErrServerMismatch, problem)
	case problem.Code == "server_key_mismatch":
		return fmt.Errorf("%w: %w", ErrServerKeyMismatch, problem)
	case response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= http.StatusInternalServerError:
		return fmt.Errorf("%w: %w", ErrUnavailable, problem)
	}
	return problem
}
