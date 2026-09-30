// Package buildinfo 提供构建时注入的程序信息。
package buildinfo

// Version 是程序版本号，发布构建通过 -ldflags "-X" 注入，未注入时为 dev。
var Version = "dev"
