//go:build !server && !production

package main

import (
	"os"
	"strings"

	"github.com/runforyou-ai/luway/internal/common/brand"
)

// defaultServerURL 返回原生端内置的部署地址：开发构建优先使用运行环境的 PUBLIC_URL，连接当前工作树的服务端，未设置时使用构建品牌的部署地址。
func defaultServerURL() string {
	if publicURL := strings.TrimSpace(os.Getenv("PUBLIC_URL")); publicURL != "" {
		return publicURL
	}
	return brand.Build().ServerURL
}
