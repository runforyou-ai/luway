// set_release_version 将发布版本同步到各平台的打包元数据。
package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
)

// fileReplacement 是一条改写版本字段的规则：把 path 中匹配 pattern 的内容替换为 replacement。
type fileReplacement struct {
	path        string
	pattern     string
	replacement string
}

// main 校验参数并更新各平台的发布版本与构建号。
func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "用法: go run build/scripts/set_release_version.go <version> <build-number>")
		os.Exit(2)
	}

	versionPattern := regexp.MustCompile(`^([0-9]+\.[0-9]+\.[0-9]+)(?:-[0-9A-Za-z.-]+)?$`)
	versionMatch := versionPattern.FindStringSubmatch(os.Args[1])
	if versionMatch == nil {
		fmt.Fprintf(os.Stderr, "无效的发布版本: %s\n", os.Args[1])
		os.Exit(2)
	}

	buildNumber, err := strconv.ParseUint(os.Args[2], 10, 31)
	if err != nil || buildNumber == 0 {
		fmt.Fprintf(os.Stderr, "无效的构建号: %s\n", os.Args[2])
		os.Exit(2)
	}

	version := versionMatch[1]
	build := strconv.FormatUint(buildNumber, 10)
	replacements := []fileReplacement{
		{"build/config.yml", `(?m)(^  version: ")[^"]+(")`, `${1}` + version + `${2}`},
		{"build/windows/wails.exe.manifest", `(<assemblyIdentity type="win32" name="[^"]+" version=")[^"]+(" processorArchitecture="\*"/>)`, `${1}` + version + `${2}`},
		{"build/windows/info.json", `("file_version": ")[^"]+(")`, `${1}` + version + `${2}`},
		{"build/windows/info.json", `("ProductVersion": ")[^"]+(")`, `${1}` + version + `${2}`},
		{"build/windows/nsis/wails_tools.nsh", `(!define INFO_PRODUCTVERSION ")[^"]+(")`, `${1}` + version + `${2}`},
		{"build/darwin/Info.plist", `(<key>CFBundleShortVersionString</key>\s*<string>)[^<]+(</string>)`, `${1}` + version + `${2}`},
		{"build/darwin/Info.plist", `(<key>CFBundleVersion</key>\s*<string>)[^<]+(</string>)`, `${1}` + build + `${2}`},
		{"build/ios/Info.plist", `(<key>CFBundleShortVersionString</key>\s*<string>)[^<]+(</string>)`, `${1}` + version + `${2}`},
		{"build/ios/Info.plist", `(<key>CFBundleVersion</key>\s*<string>)[^<]+(</string>)`, `${1}` + build + `${2}`},
		{"build/android/app/build.gradle", `(?m)(^\s*versionCode\s+)[0-9]+`, `${1}` + build},
		{"build/android/app/build.gradle", `(?m)(^\s*versionName\s+")[^"]+(")`, `${1}` + version + `${2}`},
		{"build/linux/nfpm/nfpm.yaml", `(?m)(^version: ")[^"]+(")`, `${1}` + version + `${2}`},
	}

	for _, replacement := range replacements {
		if err := replaceInFile(replacement); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	fmt.Printf("已将打包版本更新为 %s，构建号更新为 %s\n", version, build)
}

// replaceInFile 在指定文件中执行一次可验证的正则替换。
func replaceInFile(replacement fileReplacement) error {
	content, err := os.ReadFile(replacement.path)
	if err != nil {
		return fmt.Errorf("读取 %s: %w", replacement.path, err)
	}

	pattern, err := regexp.Compile(replacement.pattern)
	if err != nil {
		return fmt.Errorf("解析 %s 的替换规则: %w", replacement.path, err)
	}
	if matches := pattern.FindAllIndex(content, -1); len(matches) != 1 {
		return fmt.Errorf("%s 中应有一个版本字段，实际找到 %d 个", replacement.path, len(matches))
	}

	updated := pattern.ReplaceAllString(string(content), replacement.replacement)
	if err := os.WriteFile(replacement.path, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("写入 %s: %w", replacement.path, err)
	}
	return nil
}
