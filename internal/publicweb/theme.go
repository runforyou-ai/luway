//go:build server

package publicweb

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	channelaction "github.com/runforyou-ai/cervi/internal/actions/channel"
)

var themeColorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

const (
	paintLight = "#FFFFFF"
	paintDark  = "#1C1917"
)

// theme 保存访客页主题颜色。
type theme struct {
	Color          string
	OnColor        string
	Text           string
	Focus          string
	LauncherShadow string
}

// defaultTheme 返回默认主题。
func defaultTheme() theme {
	return parseTheme(channelaction.DefaultWebsiteChannelThemeColor)
}

// parseTheme 计算访客页颜色并过滤非法主题色。
func parseTheme(value string) theme {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	if !themeColorPattern.MatchString(normalized) {
		normalized = channelaction.DefaultWebsiteChannelThemeColor
	}
	// 解析已校验的六位十六进制颜色。
	parsedColor, _ := strconv.ParseUint(normalized[1:], 16, 32)
	color := int(parsedColor)
	r, g, b := (color>>16)&0xFF, (color>>8)&0xFF, color&0xFF
	themeL := relativeLuminance(r, g, b)
	whiteC := contrastRatio(themeL, relativeLuminance(255, 255, 255))
	darkC := contrastRatio(themeL, relativeLuminance(0x1c, 0x19, 0x17))
	onColor := paintLight
	if darkC > whiteC {
		onColor = paintDark
	}
	// 浅色主题色与深色按 45:55 混合后作为白底上的主题文字色。
	text := normalized
	if onColor == paintDark {
		text = fmt.Sprintf("#%02X%02X%02X", (r*45+0x1c*55+50)/100, (g*45+0x19*55+50)/100, (b*45+0x17*55+50)/100)
	}
	focus := fmt.Sprintf("rgba(%d, %d, %d, 0.40)", r, g, b)
	shadow := fmt.Sprintf("0 10px 28px rgba(%d, %d, %d, 0.42)", r, g, b)
	if whiteC < 3 {
		focus = "rgba(28, 25, 23, 0.35)"
		shadow = "0 8px 24px rgba(28, 25, 23, 0.12)"
	}
	return theme{
		Color:          normalized,
		OnColor:        onColor,
		Text:           text,
		Focus:          focus,
		LauncherShadow: shadow,
	}
}

// rootCSS 返回聊天页主题变量。
func (t theme) rootCSS() string {
	page := fmt.Sprintf(
		"linear-gradient(160deg, color-mix(in srgb, %s 20%%, #ffffff), color-mix(in srgb, %s 7%%, #ffffff) 45%%, #ffffff 100%%)",
		t.Color,
		t.Color,
	)
	return fmt.Sprintf(
		":root{--cv-theme:%s;--cv-on-theme:%s;--cv-theme-text:%s;--cv-focus:%s;--cv-page:%s;color-scheme:light}",
		t.Color,
		t.OnColor,
		t.Text,
		t.Focus,
		page,
	)
}

// relativeLuminance 计算 sRGB 相对亮度。
func relativeLuminance(r, g, b int) float64 {
	return 0.2126*linearChannel(r) + 0.7152*linearChannel(g) + 0.0722*linearChannel(b)
}

// linearChannel 把 sRGB 通道转成线性光。
func linearChannel(value int) float64 {
	channel := float64(value) / 255
	if channel <= 0.04045 {
		return channel / 12.92
	}
	return math.Pow((channel+0.055)/1.055, 2.4)
}

// contrastRatio 计算两色对比度。
func contrastRatio(l1, l2 float64) float64 {
	lighter := math.Max(l1, l2)
	darker := math.Min(l1, l2)
	return (lighter + 0.05) / (darker + 0.05)
}
