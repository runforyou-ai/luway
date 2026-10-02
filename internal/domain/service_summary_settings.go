package domain

// ServiceSummarySettings 定义企业周期小结使用的判断模型编号、小结模型编号与小结语言；模型为空表示不使用。
type ServiceSummarySettings struct {
	DecisionModelID *string
	SummaryModelID  *string
	Locale          Locale
}
