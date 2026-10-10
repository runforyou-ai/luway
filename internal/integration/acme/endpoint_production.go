//go:build production

package acme

import "golang.org/x/crypto/acme"

// DirectoryURL 是正式构建使用的 Let's Encrypt 目录地址。
const DirectoryURL = acme.LetsEncryptURL
