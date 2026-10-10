// Package brand 提供产品品牌：构建品牌来自随程序嵌入的 brand.json，服务端在授权允许期间按部署品牌覆盖展示名称与嵌入脚本对象名。
package brand

import (
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"

	"golang.org/x/text/language"
)

// DefaultLocale 是产品名称的回退语言，每个品牌都必须提供该语言的名称。
const DefaultLocale = "en-US"

// nameMaxLength 是产品名称的最大字符数。
const nameMaxLength = 64

// slugPattern、identifierPattern 与 sdkNamePattern 是品牌标识、应用标识与嵌入脚本对象名的格式。
var (
	slugPattern       = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9]*)+$`)
	sdkNamePattern    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)
)

// buildJSON 是随程序嵌入的构建品牌文件。
//
//go:embed brand.json
var buildJSON []byte

// Brand 定义产品品牌。
type Brand struct {
	// Slug 是可执行文件、数据目录、安装包和镜像使用的小写标识。
	Slug string `json:"slug"`
	// Names 是按界面语言标签给出的产品名称，必须包含 DefaultLocale。
	Names map[string]string `json:"names"`
	// Identifier 是各平台原生应用的应用标识。
	Identifier  string `json:"identifier"`
	Company     string `json:"company"`
	Copyright   string `json:"copyright"`
	Description string `json:"description"`
	Website     string `json:"website"`
	// SDKName 是网站嵌入脚本在宿主页注册的全局对象名。
	SDKName string `json:"sdkName"`
	// ServerURL 是原生端内置的部署地址，本机没有保存过服务器地址时直接使用该地址；为空时进入连接页。
	ServerURL string `json:"serverURL,omitempty"`
	// UpdatePublicKey 是验证桌面端更新包签名的 Ed25519 公钥，取原始公钥的标准 Base64 编码。
	UpdatePublicKey string `json:"updatePublicKey"`
	// ReleaseURL 是一个版本的发布文件所在地址，{version} 替换为版本号；为空时服务端只能从本地目录读取客户端文件。
	ReleaseURL string `json:"releaseURL,omitempty"`
}

// Override 定义部署级品牌覆盖，留空的字段沿用构建品牌。
type Override struct {
	Names   map[string]string
	SDKName string
}

// OverrideSource 返回当前的部署级覆盖及其是否生效。
type OverrideSource func() (Override, bool)

// build 是随程序嵌入并校验通过的构建品牌。
var build = func() Brand {
	var value Brand
	if err := json.Unmarshal(buildJSON, &value); err != nil {
		panic(fmt.Errorf("解析构建品牌: %w", err))
	}
	if err := value.Validate(); err != nil {
		panic(fmt.Errorf("构建品牌无效: %w", err))
	}
	return value
}()

// source 是部署级覆盖的来源，未设置时不应用覆盖。
var source atomic.Pointer[OverrideSource]

// Build 返回随程序构建的品牌。
func Build() Brand {
	return build.clone()
}

// Current 返回当前进程使用的品牌：部署级覆盖生效时返回在构建品牌上应用覆盖后的品牌，否则返回构建品牌。
func Current() Brand {
	if override, active := currentOverride(); active {
		return build.WithOverride(override)
	}
	return build.clone()
}

// UseOverride 设置部署级覆盖的来源，nil 表示不应用覆盖；来源给出的覆盖由调用方保证有效。
func UseOverride(overrides OverrideSource) {
	if overrides == nil {
		source.Store(nil)
		return
	}
	source.Store(&overrides)
}

// OverrideActive 判断部署级覆盖当前是否生效。
func OverrideActive() bool {
	_, active := currentOverride()
	return active
}

// currentOverride 返回来源给出的覆盖及其是否生效。
func currentOverride() (Override, bool) {
	overrides := source.Load()
	if overrides == nil {
		return Override{}, false
	}
	return (*overrides)()
}

// WithOverride 返回应用覆盖后的品牌副本。
func (b Brand) WithOverride(override Override) Brand {
	value := b.clone()
	for locale, name := range override.Names {
		value.Names[locale] = name
	}
	if override.SDKName != "" {
		value.SDKName = override.SDKName
	}
	return value
}

// Validate 校验品牌字段。
func (b Brand) Validate() error {
	if !slugPattern.MatchString(b.Slug) {
		return fmt.Errorf("slug 必须以小写字母开头，只含小写字母、数字和连字符")
	}
	if !identifierPattern.MatchString(b.Identifier) {
		return fmt.Errorf("identifier 必须是小写的反向域名，如 net.example.app")
	}
	if b.Names[DefaultLocale] == "" {
		return fmt.Errorf("names 必须包含 %s 名称", DefaultLocale)
	}
	for locale, name := range b.Names {
		if _, err := language.Parse(locale); err != nil {
			return fmt.Errorf("names 的语言标签 %q 无效", locale)
		}
		if name != strings.TrimSpace(name) || name == "" || len([]rune(name)) > nameMaxLength {
			return fmt.Errorf("names 中 %s 的名称不能为空、不能以空白开头或结尾，且不超过 %d 个字符", locale, nameMaxLength)
		}
	}
	if !sdkNamePattern.MatchString(b.SDKName) {
		return fmt.Errorf("sdkName 必须以字母开头，只含字母和数字")
	}
	if b.ServerURL != "" {
		// 内置部署地址与服务端部署地址规则一致：不带路径、查询、片段和凭据的完整 HTTP 地址。
		parsed, err := url.Parse(strings.TrimRight(b.ServerURL, "/"))
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("serverURL 必须是不带路径的完整 HTTP 地址")
		}
	}
	if parsed, err := url.Parse(b.ReleaseURL); b.ReleaseURL != "" && (err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https")) {
		return fmt.Errorf("releaseURL 必须是完整的 HTTP 地址")
	}
	if b.ReleaseURL != "" && !strings.Contains(b.ReleaseURL, "{version}") {
		return fmt.Errorf("releaseURL 必须包含 {version}")
	}
	if key, err := base64.StdEncoding.DecodeString(b.UpdatePublicKey); err != nil || len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("updatePublicKey 必须是 Ed25519 公钥的标准 Base64 编码")
	}
	return nil
}

// UpdateKey 返回验证桌面端更新包签名的 Ed25519 公钥。
func (b Brand) UpdateKey() ed25519.PublicKey {
	key, _ := base64.StdEncoding.DecodeString(b.UpdatePublicKey)
	return key
}

// Name 按界面语言返回产品名称：先精确匹配语言标签，再匹配主语言，都没有时使用 DefaultLocale 名称。
func (b Brand) Name(locale string) string {
	if name := b.Names[locale]; name != "" {
		return name
	}
	base, _ := language.Make(locale).Base()
	for _, candidate := range slices.Sorted(maps.Keys(b.Names)) {
		if candidateBase, _ := language.Make(candidate).Base(); candidateBase == base {
			return b.Names[candidate]
		}
	}
	return b.Names[DefaultLocale]
}

// DisplayName 返回用于文件夹名、User-Agent 等不区分语言场景的产品名称。
func (b Brand) DisplayName() string {
	return b.Names[DefaultLocale]
}

// clone 返回不共享名称表的品牌副本。
func (b Brand) clone() Brand {
	value := b
	value.Names = make(map[string]string, len(b.Names))
	for locale, name := range b.Names {
		value.Names[locale] = name
	}
	return value
}
