// Package frontend 内置 Web 前端的生产构建产物。
package frontend

import (
	"embed"
	"io/fs"
)

// Assets 是服务端与原生端内置的 Web 前端构建产物，位于 dist 目录下。
//
//go:embed all:dist
var Assets embed.FS

// Dist 返回 Web 前端构建产物的根目录。
func Dist() fs.FS {
	dist, err := fs.Sub(Assets, "dist")
	if err != nil {
		panic(err)
	}
	return dist
}
