package appservice

import (
	"net/http"
	"time"
)

// AdminSessionCookieName 是平台后台会话的 HttpOnly Cookie 名。
const AdminSessionCookieName = "luway_admin"

// AdminTokenFromRequest 从请求 Cookie 读取平台后台会话令牌。
func AdminTokenFromRequest(request *http.Request) string {
	cookie, err := request.Cookie(AdminSessionCookieName)
	if err != nil || cookie == nil {
		return ""
	}
	return cookie.Value
}

// SetAdminSessionCookie 写入平台后台会话 Cookie。
func SetAdminSessionCookie(writer http.ResponseWriter, token string, expiresAt time.Time, secure bool) {
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	sameSite := http.SameSiteLaxMode
	if secure {
		sameSite = http.SameSiteStrictMode
	}
	http.SetCookie(writer, &http.Cookie{
		Name:     AdminSessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: sameSite,
		Expires:  expiresAt,
		MaxAge:   maxAge,
	})
}

// ClearAdminSessionCookie 清除平台后台会话 Cookie。
func ClearAdminSessionCookie(writer http.ResponseWriter, secure bool) {
	sameSite := http.SameSiteLaxMode
	if secure {
		sameSite = http.SameSiteStrictMode
	}
	http.SetCookie(writer, &http.Cookie{
		Name:     AdminSessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: sameSite,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}
