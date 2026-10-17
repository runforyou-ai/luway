// Package supportplane 实现部署登记、在线激活、主动诊断上传和手机推送中继。
package supportplane

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/common/license"
	"github.com/runforyou-ai/luway/pkg/httpjson"
	"github.com/runforyou-ai/luway/pkg/httpsig"
)

var (
	// ErrActivationCodeInvalid 表示激活码不存在、不属于本产品或授权已到期。
	ErrActivationCodeInvalid = errors.New("supportplane: activation code invalid")
	// ErrDeploymentMismatch 表示激活码已绑定其他部署，或本部署已绑定其他授权。
	ErrDeploymentMismatch = errors.New("supportplane: deployment mismatch")
	// ErrDeploymentKeyMismatch 表示支持面登记的部署公钥与本部署密钥不一致。
	ErrDeploymentKeyMismatch = errors.New("supportplane: deployment key mismatch")
	// ErrPushUndeliverable 表示本部署的授权不含推送中继、支持面没有该应用与平台的推送配置，或推送服务拒绝了这次推送；重试不会成功。
	ErrPushUndeliverable = errors.New("supportplane: push undeliverable")
	// ErrUnavailable 表示支持面无法连接、限流或暂时不可用。
	ErrUnavailable = errors.New("supportplane: unavailable")
)

// requestTimeout 是单次请求支持面的超时时间。
const requestTimeout = 30 * time.Second

// Identity 是部署在支持面中的身份：部署标识与签名私钥。
type Identity struct {
	DeploymentID string
	PrivateKey   ed25519.PrivateKey
}

// IdentitySource 读取当前部署身份，平台尚未完成首次安装时返回错误。
type IdentitySource func(context.Context) (Identity, error)

// ProblemError 是支持面返回的 RFC 9457 错误。
type ProblemError struct {
	Status int
	Code   string
	Detail string
}

// Error 返回包含状态码、错误码和说明的错误文本。
func (e *ProblemError) Error() string {
	return fmt.Sprintf("supportplane: %d %s: %s", e.Status, e.Code, e.Detail)
}

// Client 是支持面部署接口客户端。
type Client struct {
	baseURL  string
	version  string
	identity IdentitySource
	http     *http.Client
}

// New 创建支持面客户端，baseURL 是支持面服务地址，version 是上报的产品版本，identity 提供签名用的部署身份。
func New(baseURL, version string, identity IdentitySource) *Client {
	transport := &httpsig.Transport{Signer: func(request *http.Request) (httpsig.Signer, error) {
		current, err := identity(request.Context())
		if err != nil {
			return httpsig.Signer{}, err
		}
		return httpsig.Signer{KeyID: current.DeploymentID, Key: current.PrivateKey}, nil
	}}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"), version: version, identity: identity,
		http: &http.Client{Transport: transport, Timeout: requestTimeout},
	}
}

// deploymentKey 是登记与激活请求体中的产品、部署公钥和版本。
type deploymentKey struct {
	Product        string `json:"product"`
	PublicKey      string `json:"public_key"`
	Version        string `json:"version,omitempty"`
	ActivationCode string `json:"activation_code,omitempty"`
}

// Register 登记部署与其公钥和当前版本，可重复调用。
func (c *Client) Register(ctx context.Context) error {
	body, err := c.deploymentKey(ctx, "")
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, "/api/support/v1/deployments", body, nil)
}

// Activate 用激活码换取绑定本部署的授权码，部署未登记时同时完成登记。
func (c *Client) Activate(ctx context.Context, activationCode string) (string, error) {
	body, err := c.deploymentKey(ctx, activationCode)
	if err != nil {
		return "", err
	}
	var output struct {
		LicenseCode string `json:"license_code"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/support/v1/activations", body, &output); err != nil {
		return "", err
	}
	return output.LicenseCode, nil
}

// PushRequest 是一次离线推送：同一应用与平台的一批设备收到同一条通知，Data 随通知交给客户端。
type PushRequest struct {
	NotificationID string            `json:"notification_id"`
	App            string            `json:"app"`
	Platform       string            `json:"platform"`
	DeviceIDs      []string          `json:"device_ids"`
	Title          string            `json:"title"`
	Body           string            `json:"body"`
	Data           map[string]string `json:"data,omitempty"`
}

// Push 经官方推送中继向一批设备发送通知；授权不含推送中继、应用与平台没有推送配置或推送被拒绝时返回 ErrPushUndeliverable。
func (c *Client) Push(ctx context.Context, input PushRequest) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	err = c.do(ctx, http.MethodPost, "/api/support/v1/push", body, nil)
	var problem *ProblemError
	if errors.As(err, &problem) && (problem.Code == "license_required" || problem.Code == "not_found" || problem.Code == "push_rejected") {
		return fmt.Errorf("%w: %w", ErrPushUndeliverable, err)
	}
	return err
}

// deploymentKey 读取部署身份并编码登记或激活请求体。
func (c *Client) deploymentKey(ctx context.Context, activationCode string) ([]byte, error) {
	current, err := c.identity(ctx)
	if err != nil {
		return nil, err
	}
	publicKey := current.PrivateKey.Public().(ed25519.PublicKey)
	return json.Marshal(deploymentKey{
		Product: license.ProductID, PublicKey: base64.RawURLEncoding.EncodeToString(publicKey),
		Version: c.version, ActivationCode: activationCode,
	})
}

// do 发送签名请求，按支持面错误码返回对应错误，成功时把响应解码到 output。
func (c *Client) do(ctx context.Context, method, path string, body []byte, output any) error {
	input := httpjson.Request{Method: method, URL: c.baseURL + path, MaxResponseBytes: 1 << 20}
	if body != nil {
		input.Body, input.ContentType = bytes.NewReader(body), "application/json"
	}
	return c.send(ctx, input, output)
}

// send 发送签名请求并统一解析支持面的响应与错误。
func (c *Client) send(ctx context.Context, input httpjson.Request, output any) error {
	response, err := httpjson.Do(ctx, c.http, input)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if response.OK() {
		if output == nil {
			return nil
		}
		if err := httpjson.Decode(response.Body, output); err != nil {
			return fmt.Errorf("decode support plane response: %w", err)
		}
		return nil
	}
	problem := &ProblemError{Status: response.StatusCode}
	var decoded struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
		Title  string `json:"title"`
	}
	if json.Unmarshal(response.Body, &decoded) == nil {
		problem.Code, problem.Detail = decoded.Code, decoded.Detail
		if problem.Detail == "" {
			problem.Detail = decoded.Title
		}
	}
	// 支持面的错误码映射为客户端错误，限流与服务端错误视为暂时不可用。
	switch {
	case problem.Code == "activation_code_invalid":
		return fmt.Errorf("%w: %w", ErrActivationCodeInvalid, problem)
	case problem.Code == "deployment_mismatch":
		return fmt.Errorf("%w: %w", ErrDeploymentMismatch, problem)
	case problem.Code == "deployment_key_mismatch":
		return fmt.Errorf("%w: %w", ErrDeploymentKeyMismatch, problem)
	case response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= http.StatusInternalServerError:
		return fmt.Errorf("%w: %w", ErrUnavailable, problem)
	}
	return problem
}

// UploadDiagnostic 把主动生成的诊断包上传到支持面。
func (c *Client) UploadDiagnostic(ctx context.Context, bundle []byte) error {
	input := httpjson.Request{Method: http.MethodPost, URL: c.baseURL + "/api/support/v1/diagnostics", Body: bytes.NewReader(bundle), ContentType: "application/zip", MaxResponseBytes: 1 << 20}
	return c.send(ctx, input, nil)
}
