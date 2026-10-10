//go:build server

package customerchat

import "errors"

var (
	// ErrChannelNotFound 表示网站渠道不存在或不可用。
	ErrChannelNotFound = errors.New("website channel not found")
	// ErrCustomerIdentityInvalid 表示网站登录用户签名身份无效、过期或企业尚未生成客户身份密钥。
	ErrCustomerIdentityInvalid = errors.New("website customer identity invalid")
)
