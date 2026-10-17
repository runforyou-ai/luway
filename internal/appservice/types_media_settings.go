package appservice

// MediaUnderstandingSettings 定义工作区媒体理解兜底模型；为空表示不启用对应投影。
type MediaUnderstandingSettings struct {
	OCRModelID           *string `json:"ocrModelId"`
	TranscriptionModelID *string `json:"transcriptionModelId"`
}
