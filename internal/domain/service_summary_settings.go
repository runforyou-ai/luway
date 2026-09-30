package domain

// ServiceSummarySettings 定义企业周期小结使用的判断模型、小结模型与小结语言；模型为空表示不使用。
type ServiceSummarySettings struct {
	Decision *AIModelReference
	Summary  *AIModelReference
	Locale   Locale
}
