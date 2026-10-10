package customeridentity

import "strings"

// ExternalIDKind 定义网站访客渠道外部编号所属的访客类型。
type ExternalIDKind int

const (
	// ExternalIDAnonymous 表示以访客令牌识别的匿名访客。
	ExternalIDAnonymous ExternalIDKind = iota + 1
	// ExternalIDCustomer 表示验签通过的企业网站登录用户。
	ExternalIDCustomer
)

const (
	// anonymousExternalIDPrefix 是匿名访客渠道外部编号的前缀，其后为访客令牌。
	anonymousExternalIDPrefix = "web-session:"
	// customerExternalIDPrefix 是登录用户渠道外部编号的前缀，其后为企业用户编号。
	customerExternalIDPrefix = "web-user:"
	// anonymousTokenLength 是匿名访客令牌的十六进制字符数。
	anonymousTokenLength = 32
)

// AnonymousExternalID 返回匿名访客令牌对应的渠道外部编号。
func AnonymousExternalID(token string) string { return anonymousExternalIDPrefix + token }

// CustomerExternalID 返回企业用户编号对应的渠道外部编号。
func CustomerExternalID(userID string) string { return customerExternalIDPrefix + userID }

// ParseExternalID 解析网站访客渠道外部编号，返回访客类型与访客令牌或企业用户编号；匿名访客令牌须为 32 位小写十六进制，企业用户编号须合法，不合法时类型为零值且 ok 为假。
func ParseExternalID(value string) (kind ExternalIDKind, id string, ok bool) {
	if userID, customer := strings.CutPrefix(value, customerExternalIDPrefix); customer {
		if !ValidUserID(userID) {
			return 0, "", false
		}
		return ExternalIDCustomer, userID, true
	}
	token, anonymous := strings.CutPrefix(value, anonymousExternalIDPrefix)
	if !anonymous || !ValidAnonymousToken(token) {
		return 0, "", false
	}
	return ExternalIDAnonymous, token, true
}

// ValidAnonymousToken 判断匿名访客令牌为 32 位小写十六进制。
func ValidAnonymousToken(token string) bool {
	if len(token) != anonymousTokenLength {
		return false
	}
	for _, character := range token {
		if (character < 'a' || character > 'f') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

// ValidExternalID 判断是否为合法的网站访客渠道外部编号。
func ValidExternalID(value string) bool {
	_, _, ok := ParseExternalID(value)
	return ok
}
