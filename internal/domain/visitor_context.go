package domain

// VisitorContext 表示网站访客在一个客服处理周期内的访问上下文，页面地址不含查询串与 hash。
type VisitorContext struct {
	ReferrerURL string `json:"referrerUrl,omitempty"`
	PageURL     string `json:"pageUrl,omitempty"`
	PageTitle   string `json:"pageTitle,omitempty"`
	Browser     string `json:"browser,omitempty"`
	OS          string `json:"os,omitempty"`
	DeviceType  string `json:"deviceType,omitempty"`
	Language    string `json:"language,omitempty"`
	TimeZone    string `json:"timeZone,omitempty"`
	Country     string `json:"country,omitempty"`
}

// VisitorDeviceType 定义访客设备类型。
type VisitorDeviceType string

const (
	VisitorDeviceDesktop VisitorDeviceType = "desktop"
	VisitorDeviceMobile  VisitorDeviceType = "mobile"
	VisitorDeviceTablet  VisitorDeviceType = "tablet"
)
