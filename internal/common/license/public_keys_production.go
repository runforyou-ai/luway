//go:build production

package license

// encodedPublicKeys 是正式 control 的签名公钥，按 kid 索引，值为 base64url 编码；轮换签名密钥后保留旧公钥。
var encodedPublicKeys = map[string]string{
	"k20261004-g65v": "mPjlxquju1o0z_ZimmZEKO1IwBv0A_D1v5dSmgYePAY",
}
