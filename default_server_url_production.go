//go:build !server && production

package main

import "github.com/runforyou-ai/cervi/internal/common/brand"

// defaultServerURL 返回原生端内置的部署地址，即构建品牌的部署地址。
func defaultServerURL() string {
	return brand.Build().ServerURL
}
