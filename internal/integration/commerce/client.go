// Package commerce 实现服务器与商业服务的通信：解析配对码、确认配对和读取变更源，每个请求都用服务器密钥签名。
package commerce

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
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/common/license"
	"github.com/runforyou-ai/luway/pkg/httpsig"
)

var (
	// ErrPairingCodeInvalid 表示配对码不是有效的 base64url JSON，或缺少地址、服务标识、公钥与令牌。
	ErrPairingCodeInvalid = errors.New("commerce: pairing code invalid")
	// ErrPairingRejected 表示商业服务拒绝配对，如令牌已使用、已过期或实例不匹配。
	ErrPairingRejected = errors.New("commerce: pairing rejected")
	// ErrNotPaired 表示商业服务中本服务器未配对或配对已解除。
	ErrNotPaired = errors.New("commerce: instance not paired")
	// ErrUnavailable 表示商业服务无法连接、限流或暂时不可用。
	ErrUnavailable = errors.New("commerce: unavailable")
	// ErrInvalidResponse 表示商业服务的响应不符合接口约定。
	ErrInvalidResponse = errors.New("commerce: invalid response")
)

// requestTimeout 是单次请求商业服务的超时时间。
const requestTimeout = 30 * time.Second

// 变更源中的变更类型。
const (
	ChangeEntitlement = "entitlement"
	ChangeCreditOrder = "credit_order"
)

// 积分订单状态。
const (
	CreditOrderPaid     = "paid"
	CreditOrderRefunded = "refunded"
)

// Identity 是服务器在商业服务中的身份：服务器标识与签名私钥。
type Identity struct {
	ServerID   string
	PrivateKey ed25519.PrivateKey
}

// IdentitySource 读取当前服务器身份，平台尚未完成首次安装时返回错误。
type IdentitySource func(context.Context) (Identity, error)

// PairingCode 是商业服务后台生成的配对码内容。
type PairingCode struct {
	URL       string
	ServiceID string
	PublicKey ed25519.PublicKey
	Token     string
}

// ParsePairingCode 解码 base64url 编码的配对码 JSON，并校验商业服务地址、服务标识、Ed25519 公钥与令牌。
func ParsePairingCode(code string) (PairingCode, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(code), "="))
	if err != nil {
		return PairingCode{}, fmt.Errorf("%w: %w", ErrPairingCodeInvalid, err)
	}
	var decoded struct {
		URL       string `json:"url"`
		ServiceID string `json:"service_id"`
		PublicKey string `json:"public_key"`
		Token     string `json:"token"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return PairingCode{}, fmt.Errorf("%w: %w", ErrPairingCodeInvalid, err)
	}
	publicKey, err := base64.RawURLEncoding.DecodeString(decoded.PublicKey)
	target, urlErr := url.Parse(decoded.URL)
	if err != nil || len(publicKey) != ed25519.PublicKeySize || urlErr != nil || target.Host == "" ||
		(target.Scheme != "https" && target.Scheme != "http") || decoded.ServiceID == "" || decoded.Token == "" {
		return PairingCode{}, fmt.Errorf("%w: missing or malformed fields", ErrPairingCodeInvalid)
	}
	return PairingCode{
		URL: strings.TrimRight(decoded.URL, "/"), ServiceID: decoded.ServiceID, PublicKey: publicKey, Token: decoded.Token,
	}, nil
}

// Change 是变更源中的一条变更，按 Type 携带对应的完整状态。
type Change struct {
	Sequence    int64        `json:"sequence"`
	Type        string       `json:"type"`
	WorkspaceID string       `json:"workspace_id"`
	OccurredAt  time.Time    `json:"occurred_at"`
	Entitlement *Entitlement `json:"entitlement"`
	CreditOrder *CreditOrder `json:"credit_order"`
}

// Entitlement 是工作区权益的完整快照。
type Entitlement struct {
	Revision int64 `json:"revision"`
	Plan     struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"plan"`
	SeatLimit int        `json:"seat_limit"`
	PeriodEnd *time.Time `json:"period_end"`
}

// CreditOrder 是积分充值订单的状态。
type CreditOrder struct {
	OrderID string `json:"order_id"`
	Credits int64  `json:"credits"`
	Status  string `json:"status"`
}

// ProblemError 是商业服务返回的 RFC 9457 错误。
type ProblemError struct {
	Status int
	Code   string
	Detail string
}

// Error 返回包含状态码、错误码和说明的错误文本。
func (e *ProblemError) Error() string {
	return fmt.Sprintf("commerce: %d %s: %s", e.Status, e.Code, e.Detail)
}

// Client 是商业服务接口客户端，商业服务地址由每次调用传入。
type Client struct {
	identity IdentitySource
	http     *http.Client
}

// New 创建商业服务客户端，identity 提供签名用的服务器身份。
func New(identity IdentitySource) *Client {
	transport := &httpsig.Transport{Signer: func(request *http.Request) (httpsig.Signer, error) {
		current, err := identity(request.Context())
		if err != nil {
			return httpsig.Signer{}, err
		}
		return httpsig.Signer{KeyID: current.ServerID, Key: current.PrivateKey}, nil
	}}
	// 商业服务接口不使用重定向，按收到的响应判断结果。
	checkRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{identity: identity, http: &http.Client{Transport: transport, Timeout: requestTimeout, CheckRedirect: checkRedirect}}
}

// ConfirmPairing 用配对码中的令牌向商业服务确认配对并登记服务器公钥；同一服务器重复确认同一令牌时商业服务返回成功。
func (c *Client) ConfirmPairing(ctx context.Context, code PairingCode) error {
	current, err := c.identity(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{
		"product": license.ProductID, "token": code.Token,
		"public_key": base64.RawURLEncoding.EncodeToString(current.PrivateKey.Public().(ed25519.PublicKey)),
	})
	if err != nil {
		return err
	}
	err = c.do(ctx, http.MethodPost, code.URL+"/api/v1/pairing/confirm", body, nil)
	var problem *ProblemError
	if errors.As(err, &problem) && !errors.Is(err, ErrUnavailable) {
		return fmt.Errorf("%w: %w", ErrPairingRejected, err)
	}
	return err
}

// Changes 读取商业服务变更源中序号大于 after 的至多 limit 条变更，按序号升序返回。
func (c *Client) Changes(ctx context.Context, baseURL string, after int64, limit int) ([]Change, error) {
	query := url.Values{"after": {strconv.FormatInt(after, 10)}, "limit": {strconv.Itoa(limit)}}
	var output struct {
		Data *[]Change `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, baseURL+"/api/v1/changes?"+query.Encode(), nil, &output); err != nil {
		return nil, err
	}
	if output.Data == nil {
		return nil, fmt.Errorf("%w: missing data", ErrInvalidResponse)
	}
	return *output.Data, nil
}

// do 发送签名请求，按商业服务错误码返回对应错误，成功时把响应解码到 output。
func (c *Client) do(ctx context.Context, method, target string, body []byte, output any) error {
	request, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
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
	payload, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		if output == nil {
			return nil
		}
		if err := json.Unmarshal(payload, output); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidResponse, err)
		}
		return nil
	}
	problem := &ProblemError{Status: response.StatusCode}
	var decoded struct {
		Code      string `json:"code"`
		Detail    string `json:"detail"`
		Title     string `json:"title"`
		Retryable *bool  `json:"retryable"`
	}
	if json.Unmarshal(payload, &decoded) == nil {
		problem.Code, problem.Detail = decoded.Code, decoded.Detail
		if problem.Detail == "" {
			problem.Detail = decoded.Title
		}
	}
	// 未配对单独识别，其余按 retryable 成员判断，缺少该成员时 429 与 5xx 视为暂时不可用。
	retryable := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= http.StatusInternalServerError
	if decoded.Retryable != nil {
		retryable = *decoded.Retryable
	}
	switch {
	case problem.Code == "instance_not_paired":
		return fmt.Errorf("%w: %w", ErrNotPaired, problem)
	case retryable:
		return fmt.Errorf("%w: %w", ErrUnavailable, problem)
	}
	return problem
}
