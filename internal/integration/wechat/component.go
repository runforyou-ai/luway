package wechat

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/runforyou-ai/support/arr"
)

// componentLoginPageURL 是公众号管理员扫码授权第三方平台的页面地址。
const componentLoginPageURL = "https://mp.weixin.qq.com/cgi-bin/componentloginpage"

// 公众号的账号类型与认证类型编号。
const (
	serviceTypeServiceAccount = 2
	verifyTypeUnverified      = -1
)

// PreAuthCode 定义发起授权使用的预授权码与有效时长。
type PreAuthCode struct {
	Value     string
	ExpiresIn time.Duration
}

// Authorization 定义用授权码换取的公众号授权信息。
type Authorization struct {
	AppID         string
	AccessToken   AccessToken
	RefreshToken  string
	PermissionIDs []int
}

// AuthorizerProfile 定义授权公众号的资料、账号类型与认证类型。
type AuthorizerProfile struct {
	NickName      string
	HeadImageURL  string
	PrincipalName string
	UserName      string
	ServiceType   int
	VerifyType    int
}

// VerifiedServiceAccount 判断公众号是已认证的服务号。
func (p AuthorizerProfile) VerifiedServiceAccount() bool {
	return p.ServiceType == serviceTypeServiceAccount && p.VerifyType != verifyTypeUnverified
}

// AuthorizerToken 定义授权方接口调用凭据与微信返回的刷新令牌。
type AuthorizerToken struct {
	AccessToken  AccessToken
	RefreshToken string
}

// permissionInfo 定义授权信息中的一项权限集。
type permissionInfo struct {
	Category struct {
		ID int `json:"id"`
	} `json:"funcscope_category"`
}

// permissionIDs 返回权限集编号列表。
func permissionIDs(items []permissionInfo) []int {
	return arr.OrEmpty(arr.Map(items, func(item permissionInfo) int { return item.Category.ID }))
}

// componentPath 返回带平台接口调用凭据的开放平台接口路径。
func componentPath(path, componentToken string) string {
	return path + "?component_access_token=" + url.QueryEscape(componentToken)
}

// CreatePreAuthCode 用平台接口调用凭据获取预授权码。
func (c *Client) CreatePreAuthCode(ctx context.Context, componentToken, componentAppID string) (PreAuthCode, error) {
	var response struct {
		Code      string `json:"pre_auth_code"`
		ExpiresIn int64  `json:"expires_in"`
	}
	if err := c.post(ctx, componentPath("/cgi-bin/component/api_create_preauthcode", componentToken), map[string]string{
		"component_appid": componentAppID,
	}, &response); err != nil {
		return PreAuthCode{}, err
	}
	if response.Code == "" || response.ExpiresIn <= 0 {
		return PreAuthCode{}, fmt.Errorf("%w: pre auth code missing", ErrUnavailable)
	}
	return PreAuthCode{Value: response.Code, ExpiresIn: time.Duration(response.ExpiresIn) * time.Second}, nil
}

// AuthorizationURL 返回公众号管理员授权第三方平台的页面地址；bizAppID 非空时只允许该公众号授权。
func AuthorizationURL(componentAppID, preAuthCode, redirectURI, bizAppID string) string {
	query := url.Values{}
	query.Set("component_appid", componentAppID)
	query.Set("pre_auth_code", preAuthCode)
	query.Set("redirect_uri", redirectURI)
	query.Set("auth_type", "1")
	if bizAppID != "" {
		query.Set("biz_appid", bizAppID)
	}
	return componentLoginPageURL + "?" + query.Encode()
}

// QueryAuthorization 用授权码换取公众号授权信息与授权方接口调用凭据。
func (c *Client) QueryAuthorization(ctx context.Context, componentToken, componentAppID, authorizationCode string) (Authorization, error) {
	var response struct {
		Info struct {
			AppID        string           `json:"authorizer_appid"`
			AccessToken  string           `json:"authorizer_access_token"`
			ExpiresIn    int64            `json:"expires_in"`
			RefreshToken string           `json:"authorizer_refresh_token"`
			Permissions  []permissionInfo `json:"func_info"`
		} `json:"authorization_info"`
	}
	if err := c.post(ctx, componentPath("/cgi-bin/component/api_query_auth", componentToken), map[string]string{
		"component_appid": componentAppID, "authorization_code": authorizationCode,
	}, &response); err != nil {
		return Authorization{}, err
	}
	info := response.Info
	if info.AppID == "" || info.AccessToken == "" || info.ExpiresIn <= 0 || info.RefreshToken == "" {
		return Authorization{}, fmt.Errorf("%w: authorization info missing", ErrUnavailable)
	}
	return Authorization{
		AppID:         info.AppID,
		AccessToken:   AccessToken{Value: info.AccessToken, ExpiresIn: time.Duration(info.ExpiresIn) * time.Second},
		RefreshToken:  info.RefreshToken,
		PermissionIDs: permissionIDs(info.Permissions),
	}, nil
}

// AuthorizerProfile 读取授权公众号的资料、账号类型与认证类型。
func (c *Client) AuthorizerProfile(ctx context.Context, componentToken, componentAppID, appID string) (AuthorizerProfile, error) {
	var response struct {
		Info struct {
			NickName        string `json:"nick_name"`
			HeadImage       string `json:"head_img"`
			PrincipalName   string `json:"principal_name"`
			UserName        string `json:"user_name"`
			ServiceTypeInfo struct {
				ID int `json:"id"`
			} `json:"service_type_info"`
			VerifyTypeInfo struct {
				ID int `json:"id"`
			} `json:"verify_type_info"`
		} `json:"authorizer_info"`
	}
	if err := c.post(ctx, componentPath("/cgi-bin/component/api_get_authorizer_info", componentToken), map[string]string{
		"component_appid": componentAppID, "authorizer_appid": appID,
	}, &response); err != nil {
		return AuthorizerProfile{}, err
	}
	info := response.Info
	return AuthorizerProfile{
		NickName: info.NickName, HeadImageURL: info.HeadImage, PrincipalName: info.PrincipalName, UserName: info.UserName,
		ServiceType: info.ServiceTypeInfo.ID, VerifyType: info.VerifyTypeInfo.ID,
	}, nil
}

// AuthorizerAccessToken 用授权方刷新令牌获取授权公众号的接口调用凭据。
func (c *Client) AuthorizerAccessToken(ctx context.Context, componentToken, componentAppID, appID, refreshToken string) (AuthorizerToken, error) {
	var response struct {
		AccessToken  string `json:"authorizer_access_token"`
		ExpiresIn    int64  `json:"expires_in"`
		RefreshToken string `json:"authorizer_refresh_token"`
	}
	if err := c.post(ctx, componentPath("/cgi-bin/component/api_authorizer_token", componentToken), map[string]string{
		"component_appid": componentAppID, "authorizer_appid": appID, "authorizer_refresh_token": refreshToken,
	}, &response); err != nil {
		return AuthorizerToken{}, err
	}
	if response.AccessToken == "" || response.ExpiresIn <= 0 {
		return AuthorizerToken{}, fmt.Errorf("%w: authorizer access token missing", ErrUnavailable)
	}
	return AuthorizerToken{
		AccessToken:  AccessToken{Value: response.AccessToken, ExpiresIn: time.Duration(response.ExpiresIn) * time.Second},
		RefreshToken: response.RefreshToken,
	}, nil
}
