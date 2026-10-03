package brand

import (
	"testing"
	"time"
)

// TestNameMatchesLocale 验证产品名称先精确匹配语言标签，再匹配主语言，最后回退到默认语言。
func TestNameMatchesLocale(t *testing.T) {
	value := Brand{Names: map[string]string{"en-US": "Acme", "zh-CN": "艾克米"}}
	cases := map[string]string{"zh-CN": "艾克米", "zh-TW": "艾克米", "zh": "艾克米", "en-GB": "Acme", "fr-FR": "Acme", "": "Acme"}
	for locale, want := range cases {
		if got := value.Name(locale); got != want {
			t.Errorf("Name(%q) = %q，期望 %q", locale, got, want)
		}
	}
}

// TestBuildBrandIsValid 验证随程序嵌入的构建品牌有效。
func TestBuildBrandIsValid(t *testing.T) {
	if err := Build().Validate(); err != nil {
		t.Fatalf("构建品牌无效: %v", err)
	}
}

// TestConfigureAppliesOverride 验证部署覆盖只替换给出的字段，只在生效截止时间前应用，无效覆盖不改变当前品牌。
func TestConfigureAppliesOverride(t *testing.T) {
	t.Cleanup(func() {
		current.Store(nil)
		EnableOverrideUntil(time.Time{})
	})
	if err := Configure(Override{Names: map[string]string{"zh-CN": "艾克米"}, SDKName: "Acme"}); err != nil {
		t.Fatalf("应用覆盖失败: %v", err)
	}
	if Current().SDKName != Build().SDKName {
		t.Fatal("未设置生效截止时间时应使用构建品牌")
	}
	EnableOverrideUntil(time.Now().Add(-time.Second))
	if Current().SDKName != Build().SDKName {
		t.Fatal("超过生效截止时间后应使用构建品牌")
	}
	EnableOverrideUntil(time.Now().Add(time.Hour))
	value := Current()
	if value.Name("zh-CN") != "艾克米" || value.Name("en-US") != Build().Name("en-US") || value.SDKName != "Acme" || value.Slug != Build().Slug {
		t.Fatalf("覆盖后的品牌 = %#v", value)
	}
	if err := Configure(Override{SDKName: "acme-desk"}); err == nil {
		t.Fatal("应拒绝无效的嵌入脚本对象名")
	}
	if Current().SDKName != "Acme" {
		t.Fatal("无效覆盖不应改变当前品牌")
	}
}

// TestValidateServerURL 验证内置部署地址可以为空，填写时必须是不带路径、查询和凭据的完整 HTTP 地址。
func TestValidateServerURL(t *testing.T) {
	cases := map[string]bool{
		"":                             true,
		"https://app.example.com":      true,
		"http://example.com:8080/":     true,
		"http://example.com:8080/team": false,
		"app.example.com":              false,
		"ftp://app.example.com":        false,
		"https://app.example.com/?a=1": false,
		"https://user@app.example.com": false,
	}
	for serverURL, valid := range cases {
		value := Build()
		value.ServerURL = serverURL
		if err := value.Validate(); (err == nil) != valid {
			t.Errorf("serverURL %q 校验结果 = %v，期望有效 = %v", serverURL, err, valid)
		}
	}
}

// TestValidateUpdatePublicKey 验证更新公钥可以为空，填写时必须是 32 字节 Ed25519 公钥的 base64。
func TestValidateUpdatePublicKey(t *testing.T) {
	cases := map[string]bool{
		"": true,
		"MCowBQYDK2VwAyEAGb9ECWmEzf6FQbrBZ9w7lshQhqowtrbLDFw4rXAxZuE=": false,
		"GB9ECWmEzf6FQbrBZ9w7lshQhqowtrbLDFw4rXAxZuE=":                 true,
		"not base64": false,
	}
	for key, valid := range cases {
		value := Build()
		value.UpdatePublicKey = key
		if err := value.Validate(); (err == nil) != valid {
			t.Errorf("updatePublicKey %q 校验结果 = %v，期望有效 = %v", key, err, valid)
		}
		if valid && key != "" && len(value.UpdateKey()) != 32 {
			t.Errorf("updatePublicKey %q 解析后长度 = %d", key, len(value.UpdateKey()))
		}
	}
}
