//go:build production

package license

// encodedPublicKeys 是正式 control 的签名公钥，按 kid 索引，值为 base64url 编码；轮换签名密钥后保留旧公钥。
var encodedPublicKeys = map[string]string{
	"k20261002-clwx": "L7glrNbRPk8OSdA8sCyxir8niADkdaxD3csQFATYSTU",
}
