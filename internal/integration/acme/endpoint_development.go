//go:build !production

package acme

// DirectoryURL 是开发构建使用的 Let's Encrypt 测试环境目录地址，签发的证书不受浏览器信任。
const DirectoryURL = "https://acme-staging-v02.api.letsencrypt.org/directory"
