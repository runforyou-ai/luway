package appservice

import "time"

// KnowledgeQASimilarQuestion 定义带稳定编号的相似问题，新问题的编号为空。
type KnowledgeQASimilarQuestion struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

// KnowledgeQAInput 定义整条问答的编辑字段。
type KnowledgeQAInput struct {
	Question         string                       `json:"question"`
	SimilarQuestions []KnowledgeQASimilarQuestion `json:"similarQuestions"`
	Answer           string                       `json:"answer"`
}

// KnowledgeQAListInput 定义知识库的问答查询条件。
type KnowledgeQAListInput struct {
	Keyword  string `json:"keyword" query:"keyword"`
	Page     int    `json:"page" query:"page,default=1"`
	PageSize int    `json:"pageSize" query:"pageSize,default=20"`
}

// KnowledgeQASummary 定义问答列表项及其索引状态。
type KnowledgeQASummary struct {
	ID               string               `json:"id"`
	Question         string               `json:"question"`
	SimilarQuestions []string             `json:"similarQuestions"`
	Answer           string               `json:"answer"`
	Status           KnowledgeIndexStatus `json:"status"`
	FailureMessage   string               `json:"failureMessage"`
	UpdatedAt        time.Time            `json:"updatedAt"`
}

// KnowledgeQAEntry 定义完整问答详情。
type KnowledgeQAEntry struct {
	ID               string                       `json:"id"`
	Question         string                       `json:"question"`
	SimilarQuestions []KnowledgeQASimilarQuestion `json:"similarQuestions"`
	Answer           string                       `json:"answer"`
	CreatedAt        time.Time                    `json:"createdAt"`
	UpdatedAt        time.Time                    `json:"updatedAt"`
}

// KnowledgeQAList 定义问答分页结果。
type KnowledgeQAList struct {
	Entries []KnowledgeQASummary `json:"entries"`
	Page    PageInfo             `json:"page"`
}
