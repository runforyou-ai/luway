package appservice

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/runforyou-ai/luway/internal/i18n"
)

// APIVersion 是当前代码的接口版本，服务端与原生端之间出现不兼容的接口变化时加 1。
const APIVersion = 1

// MinClientAPIVersion 是服务端接受的最低原生端接口版本。
const MinClientAPIVersion = 1

// MinServerAPIVersion 是原生端要求的最低服务端接口版本。
const MinServerAPIVersion = 1

// ClientAPIVersionHeader 是原生端请求携带本端接口版本的请求头。
const ClientAPIVersionHeader = "X-Client-API-Version"

// RequestClientOutdated 判断请求头声明的原生端接口版本是否低于服务端接受的最低版本或无法解析；未携带该请求头的请求放行。
func RequestClientOutdated(header http.Header) bool {
	value := strings.TrimSpace(header.Get(ClientAPIVersionHeader))
	if value == "" {
		return false
	}
	version, err := strconv.Atoi(value)
	return err != nil || version < MinClientAPIVersion
}

// ServerOutdated 判断服务器接口版本是否低于原生端要求的最低版本。
func (s InstallationStatus) ServerOutdated() bool {
	return s.APIVersion < MinServerAPIVersion
}

// ClientOutdated 判断本端接口版本是否低于服务器接受的最低版本。
func (s InstallationStatus) ClientOutdated() bool {
	return s.MinClientAPIVersion > APIVersion
}

// ClientUpgradeError 返回原生端需要升级的会话错误。
func ClientUpgradeError(meta RequestMeta) *Error {
	return SessionError(meta, SessionStateUpgrade, i18n.ErrorClientUpgradeRequired)
}
