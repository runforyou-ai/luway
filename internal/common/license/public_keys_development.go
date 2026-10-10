//go:build !production

package license

// encodedPublicKeys 是本地开发 control 的签名公钥，按 kid 索引，值为 base64url 编码。
var encodedPublicKeys = map[string]string{
	"k20261002-ikqu": "DAtIkCFZWxKUgENyUszcGpLsiWBikadUsRCb1phDm6Y",
}
