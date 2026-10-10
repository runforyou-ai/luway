// applybrand 把构建品牌同步到各平台的打包元数据；传入品牌目录时先用其中的品牌文件和图标替换仓库中的构建品牌。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/image/draw"

	"github.com/runforyou-ai/luway/internal/common/brand"
)

// brandFile 是仓库中的构建品牌文件。
const brandFile = "internal/common/brand/brand.json"

// fileReplacement 是一条改写文件字段的规则：把 pattern 两个捕获组之间的内容替换为 value，匹配次数必须等于 count。
type fileReplacement struct {
	path    string
	pattern string
	value   string
	count   int
}

// main 替换品牌目录中的品牌文件和图标，校验构建品牌并改写各平台打包元数据。
func main() {
	if len(os.Args) > 2 {
		fmt.Fprintln(os.Stderr, "用法: go run ./build/scripts/applybrand [品牌目录]")
		os.Exit(2)
	}
	if len(os.Args) == 2 {
		if err := importProfile(os.Args[1]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	data, err := os.ReadFile(brandFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取 %s: %v\n", brandFile, err)
		os.Exit(1)
	}
	value, err := parseBrand(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, replacement := range replacements(value) {
		if err := replaceInFile(replacement); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Printf("已应用品牌 %s（%s）\n", value.DisplayName(), value.Slug)
}

// parseBrand 解析并校验品牌文件。
func parseBrand(data []byte) (brand.Brand, error) {
	var value brand.Brand
	if err := json.Unmarshal(data, &value); err != nil {
		return brand.Brand{}, fmt.Errorf("解析品牌文件: %w", err)
	}
	if err := value.Validate(); err != nil {
		return brand.Brand{}, fmt.Errorf("品牌无效: %w", err)
	}
	return value, nil
}

// importProfile 校验品牌目录后，用其中的 brand.json、appicon.png 和 appicon.icon 替换仓库中的对应文件，并按 appicon.png 生成 Android 启动图标；图标缺省时保留现有图标。
func importProfile(dir string) error {
	data, err := os.ReadFile(filepath.Join(dir, "brand.json"))
	if err != nil {
		return fmt.Errorf("读取品牌目录的 brand.json: %w", err)
	}
	if _, err := parseBrand(data); err != nil {
		return err
	}
	icon, err := os.ReadFile(filepath.Join(dir, "appicon.png"))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("读取品牌目录的 appicon.png: %w", err)
	}
	var androidIcons map[string][]byte
	if icon != nil {
		if androidIcons, err = renderAndroidIcons(icon); err != nil {
			return err
		}
	}
	iconPackage := filepath.Join(dir, "appicon.icon")
	info, err := os.Stat(iconPackage)
	hasIconPackage := err == nil && info.IsDir()

	if err := os.WriteFile(brandFile, data, 0o644); err != nil {
		return fmt.Errorf("写入 %s: %w", brandFile, err)
	}
	if icon != nil {
		if err := os.WriteFile("build/appicon.png", icon, 0o644); err != nil {
			return fmt.Errorf("写入 build/appicon.png: %w", err)
		}
		for path, content := range androidIcons {
			if err := os.WriteFile(path, content, 0o644); err != nil {
				return fmt.Errorf("写入 %s: %w", path, err)
			}
		}
	}
	if hasIconPackage {
		if err := os.RemoveAll("build/appicon.icon"); err != nil {
			return fmt.Errorf("删除 build/appicon.icon: %w", err)
		}
		if err := os.CopyFS("build/appicon.icon", os.DirFS(iconPackage)); err != nil {
			return fmt.Errorf("复制 appicon.icon: %w", err)
		}
	}
	return nil
}

// androidIconSizes 是 Android 各屏幕密度的启动图标边长。
var androidIconSizes = map[string]int{"mdpi": 48, "hdpi": 72, "xhdpi": 96, "xxhdpi": 144, "xxxhdpi": 192}

// renderAndroidIcons 把应用图标缩放为 Android 各密度的方形与圆形启动图标，返回文件路径到 PNG 内容的映射。
func renderAndroidIcons(source []byte) (map[string][]byte, error) {
	decoded, err := png.Decode(bytes.NewReader(source))
	if err != nil {
		return nil, fmt.Errorf("解析 appicon.png: %w", err)
	}
	icons := make(map[string][]byte, len(androidIconSizes)*2)
	for density, size := range androidIconSizes {
		square := image.NewNRGBA(image.Rect(0, 0, size, size))
		draw.CatmullRom.Scale(square, square.Bounds(), decoded, decoded.Bounds(), draw.Src, nil)
		// 圆形图标把内切圆以外的像素设为透明。
		round := image.NewNRGBA(square.Bounds())
		radius := float64(size) / 2
		for y := range size {
			for x := range size {
				dx, dy := float64(x)+0.5-radius, float64(y)+0.5-radius
				if dx*dx+dy*dy <= radius*radius {
					round.Set(x, y, square.At(x, y))
				}
			}
		}
		for name, img := range map[string]image.Image{"ic_launcher.png": square, "ic_launcher_round.png": round} {
			var encoded bytes.Buffer
			if err := png.Encode(&encoded, img); err != nil {
				return nil, fmt.Errorf("生成 Android 启动图标: %w", err)
			}
			icons["build/android/app/src/main/res/mipmap-"+density+"/"+name] = encoded.Bytes()
		}
	}
	return icons, nil
}

// replacements 返回把品牌写入各平台打包元数据的改写规则。
func replacements(value brand.Brand) []fileReplacement {
	name := value.DisplayName()
	chineseName := value.Name("zh-CN")
	// 各格式的字段值转义：XML 与 plist 转义实体，其余带引号的格式转义引号和反斜杠。
	xml := func(text string) string { return html.EscapeString(text) }
	quoted := func(text string) string {
		return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(text)
	}
	plistString := func(key string, text string) fileReplacement {
		return fileReplacement{pattern: `(<key>` + key + `</key>\s*<string>)[^<]*(</string>)`, value: xml(text), count: 1}
	}
	var rules []fileReplacement
	add := func(path string, items ...fileReplacement) {
		for _, item := range items {
			item.path = path
			rules = append(rules, item)
		}
	}
	line := func(pattern string, text string) fileReplacement {
		return fileReplacement{pattern: pattern, value: text, count: 1}
	}
	// 唤起客户端的链接协议使用品牌标识。
	urlScheme := fileReplacement{pattern: `(<key>CFBundleURLSchemes</key>\s*<array>\s*<string>)[^<]*(</string>)`, value: value.Slug, count: 1}

	add("Taskfile.yml",
		line(`(?m)(^  APP_NAME: ")[^"]*(")`, value.Slug),
		line(`(?m)(^  APP_DISPLAY_NAME: ")[^"]*(")`, quoted(name)),
		line(`(?m)(^  APP_DESCRIPTION: ")[^"]*(")`, quoted(value.Description)),
	)
	add("build/config.yml",
		line(`(?m)(^  companyName: ")[^"]*(")`, quoted(value.Company)),
		line(`(?m)(^  productName: ")[^"]*(")`, quoted(name)),
		line(`(?m)(^  productIdentifier: ")[^"]*(")`, value.Identifier),
		line(`(?m)(^  description: ")[^"]*(")`, quoted(value.Description)),
		line(`(?m)(^  copyright: ")[^"]*(")`, quoted(value.Copyright)),
		line(`(?m)(^  comments: ")[^"]*(")`, quoted(value.Website)),
		line(`(?m)(^  - scheme: ")[^"]*(")`, value.Slug),
		line(`(?m)(^    description: ")[^"]*(")`, quoted(name)),
	)
	for _, path := range []string{"build/darwin/Info.plist", "build/darwin/Info.dev.plist", "build/ios/Info.plist"} {
		add(path,
			plistString("CFBundleDisplayName", name),
			plistString("CFBundleExecutable", value.Slug),
			plistString("CFBundleGetInfoString", value.Website),
			plistString("CFBundleIdentifier", value.Identifier),
			plistString("CFBundleName", name),
			plistString("NSHumanReadableCopyright", value.Copyright),
			plistString("CFBundleURLName", value.Identifier),
			urlScheme,
		)
	}
	add("build/ios/Info.dev.plist",
		plistString("CFBundleDisplayName", name+" (Dev)"),
		plistString("CFBundleExecutable", value.Slug),
		plistString("CFBundleGetInfoString", value.Website),
		plistString("CFBundleIdentifier", value.Identifier+".dev"),
		plistString("CFBundleName", name+" (Dev)"),
		plistString("NSHumanReadableCopyright", value.Copyright),
		plistString("CFBundleURLName", value.Identifier),
		urlScheme,
	)
	for path, localized := range map[string]string{"build/darwin/en.lproj/InfoPlist.strings": name, "build/darwin/zh-Hans.lproj/InfoPlist.strings": chineseName} {
		add(path,
			line(`(?m)(^"CFBundleDisplayName" = ")[^"]*(";)`, quoted(localized)),
			line(`(?m)(^"CFBundleName" = ")[^"]*(";)`, quoted(localized)),
		)
	}
	// iOS 工程的产物名与静态库名使用品牌标识，与 Info.plist 的可执行文件名和 ios Task 的产物一致。
	add("build/ios/project.pbxproj",
		fileReplacement{pattern: `(PRODUCT_BUNDLE_IDENTIFIER = ")[^"]*(";)`, value: value.Identifier, count: 2},
		line(`(ORGANIZATIONNAME = ")[^"]*(";)`, quoted(value.Company)),
		fileReplacement{pattern: `(PRODUCT_NAME = ")[^"]*(";)`, value: value.Slug, count: 2},
		fileReplacement{pattern: `(/\* )[a-zA-Z0-9-]+(\.a(?: in Frameworks)? \*/)`, value: value.Slug, count: 5},
		fileReplacement{pattern: `(/\* )[a-zA-Z0-9-]+(\.app \*/)`, value: value.Slug, count: 3},
		line(`(path = ")[^"]*(\.app"; sourceTree = BUILT_PRODUCTS_DIR)`, value.Slug),
		line(`(name = ")[^"]*(\.a"; path = "\.\./\.\./\.\./bin/)`, value.Slug),
		line(`(path = "\.\./\.\./\.\./bin/)[^"]*(\.a";)`, value.Slug),
		line(`(-o \\"bin/)[^"\\]*(\.a\\")`, value.Slug),
	)
	add("build/ios/LaunchScreen.storyboard",
		line(`(<label [^>]*text=")[^"]*(" [^>]*id="GJd-Yh-RWb">)`, xml(name)),
		line(`(<label [^>]*text=")[^"]*(" [^>]*id="MN2-I3-ftu">)`, xml(value.Description)),
	)
	add("build/android/Taskfile.yml", line(`(APP_ID: '\{\{\.APP_ID \| default ")[^"]*("\}\}')`, value.Identifier))
	add("build/android/app/src/main/AndroidManifest.xml", line(`(<data android:scheme=")[^"]*(" android:host="connect" />)`, value.Slug))
	add("build/android/app/build.gradle", line(`(applicationId ")[^"]*(")`, value.Identifier))
	add("build/android/app/src/main/res/values/strings.xml", line(`(<string name="app_name">)[^<]*(</string>)`, xml(name)))
	add("build/android/app/src/main/res/values-zh-rCN/strings.xml", line(`(<string name="app_name">)[^<]*(</string>)`, xml(chineseName)))
	add("build/linux/nfpm/nfpm.yaml",
		line(`(?m)(^name: ")[^"]*(")`, value.Slug),
		line(`(?m)(^description: ")[^"]*(")`, quoted(value.Description)),
		line(`(?m)(^vendor: ")[^"]*(")`, quoted(value.Company)),
		line(`(?m)(^homepage: ")[^"]*(")`, quoted(value.Website)),
		line(`(dst: "/usr/local/bin/)[^"]*(")`, value.Slug),
		line(`(dst: "/usr/share/icons/hicolor/128x128/apps/)[^"]*(\.png")`, value.Slug),
		line(`(src: "\./build/linux/)[^"]*(\.desktop")`, value.Slug),
		line(`(dst: "/usr/share/applications/)[^"]*(\.desktop")`, value.Slug),
	)
	add("build/windows/info.json",
		line(`("CompanyName": ")[^"]*(")`, quoted(value.Company)),
		line(`("FileDescription": ")[^"]*(")`, quoted(value.Description)),
		line(`("LegalCopyright": ")[^"]*(")`, quoted(value.Copyright)),
		line(`("ProductName": ")[^"]*(")`, quoted(name)),
		line(`("Comments": ")[^"]*(")`, quoted(value.Website)),
	)
	add("build/windows/nsis/wails_tools.nsh",
		line(`(!define INFO_PROJECTNAME ")[^"]*(")`, value.Slug),
		line(`(!define INFO_COMPANYNAME ")[^"]*(")`, quoted(value.Company)),
		line(`(!define INFO_PRODUCTNAME ")[^"]*(")`, quoted(name)),
		line(`(!define INFO_COPYRIGHT ")[^"]*(")`, quoted(value.Copyright)),
		line(`(!insertmacro CUSTOM_PROTOCOL_ASSOCIATE ")[^"]*(")`, value.Slug),
		line(`(!insertmacro CUSTOM_PROTOCOL_UNASSOCIATE ")[^"]*(")`, value.Slug),
	)
	add("build/windows/wails.exe.manifest", line(`(<assemblyIdentity type="win32" name=")[^"]*(" version="[^"]*" processorArchitecture="\*"/>)`, value.Identifier))
	return rules
}

// replaceInFile 在指定文件中执行一次匹配次数可验证的正则替换。
func replaceInFile(replacement fileReplacement) error {
	content, err := os.ReadFile(replacement.path)
	if err != nil {
		return fmt.Errorf("读取 %s: %w", replacement.path, err)
	}
	pattern, err := regexp.Compile(replacement.pattern)
	if err != nil {
		return fmt.Errorf("解析 %s 的替换规则: %w", replacement.path, err)
	}
	if matches := pattern.FindAllIndex(content, -1); len(matches) != replacement.count {
		return fmt.Errorf("%s 中规则 %s 应匹配 %d 处，实际 %d 处", replacement.path, replacement.pattern, replacement.count, len(matches))
	}
	// 保留两个捕获组，字段值按字面写入。
	updated := pattern.ReplaceAll(content, []byte("${1}"+strings.ReplaceAll(replacement.value, "$", "$$")+"${2}"))
	if err := os.WriteFile(replacement.path, updated, 0o644); err != nil {
		return fmt.Errorf("写入 %s: %w", replacement.path, err)
	}
	return nil
}
