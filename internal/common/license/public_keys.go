package license

// PublicKeys 返回内置的 control 签名公钥。
func PublicKeys() Keys {
	keys, err := DecodeKeys(encodedPublicKeys)
	if err != nil {
		panic(err)
	}
	return keys
}
