package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// PlatformAIProviderInput 定义创建平台供应商的名称与连接配置。
type PlatformAIProviderInput struct {
	Brand          AIProviderBrand          `json:"brand"`
	Name           string                   `json:"name"`
	CredentialType AIProviderCredentialType `json:"credentialType"`
	APIKey         string                   `json:"apiKey"`
	APIURL         string                   `json:"apiUrl"`
}

// PlatformAIProviderUpdateInput 定义修改平台供应商的字段，品牌沿用创建时的值。
type PlatformAIProviderUpdateInput struct {
	Name           string                   `json:"name"`
	CredentialType AIProviderCredentialType `json:"credentialType"`
	APIKey         string                   `json:"apiKey"`
	APIURL         string                   `json:"apiUrl"`
}

// PlatformAIProvider 定义平台供应商详情。
type PlatformAIProvider struct {
	ID             string                   `json:"id"`
	Brand          AIProviderBrand          `json:"brand"`
	Name           string                   `json:"name"`
	CredentialType AIProviderCredentialType `json:"credentialType"`
	APIKey         string                   `json:"apiKey"`
	APIURL         string                   `json:"apiUrl"`
}

// PlatformAIProviderSummary 定义平台供应商列表项，ModelCount 是以该供应商为来源的平台模型数；
// 近 24 小时内结束的上游尝试中，RecentAttempts 为成功、失败与超时的次数，RecentFailures 为失败与超时的次数，LastError 与 LastFailedAt 为最近一次失败或超时的原因与时间，没有时为空。
type PlatformAIProviderSummary struct {
	ID             string          `json:"id"`
	Brand          AIProviderBrand `json:"brand"`
	Name           string          `json:"name"`
	APIURL         string          `json:"apiUrl"`
	ModelCount     int             `json:"modelCount"`
	RecentAttempts int             `json:"recentAttempts"`
	RecentFailures int             `json:"recentFailures"`
	LastError      string          `json:"lastError"`
	LastFailedAt   *time.Time      `json:"lastFailedAt"`
}

// PlatformAIProviderList 定义平台供应商列表。
type PlatformAIProviderList struct {
	Providers []PlatformAIProviderSummary `json:"providers"`
}

// PlatformAIModelRouteInput 定义平台模型的一个来源，保存时编号为空表示新增来源。
type PlatformAIModelRouteInput struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerId"`
	Identifier string `json:"identifier"`
	Enabled    bool   `json:"enabled"`
}

// PlatformAIModelInput 定义平台模型的属性与按尝试顺序排列的来源。
type PlatformAIModelInput struct {
	Name            string                      `json:"name"`
	Type            AIModelType                 `json:"type"`
	InputModalities []AIModelInputModality      `json:"inputModalities"`
	ContextWindow   int64                       `json:"contextWindow"`
	MaxOutputTokens int64                       `json:"maxOutputTokens"`
	Routes          []PlatformAIModelRouteInput `json:"routes"`
}

// PlatformAIModelRoute 定义平台模型的一个来源及其供应商。
type PlatformAIModelRoute struct {
	ID            string          `json:"id"`
	ProviderID    string          `json:"providerId"`
	ProviderName  string          `json:"providerName"`
	ProviderBrand AIProviderBrand `json:"providerBrand"`
	Identifier    string          `json:"identifier"`
	Enabled       bool            `json:"enabled"`
}

// PlatformAIModel 定义平台模型的属性与按尝试顺序排列的来源。
type PlatformAIModel struct {
	ID              string                 `json:"id"`
	Name            string                 `json:"name"`
	Type            AIModelType            `json:"type"`
	InputModalities []AIModelInputModality `json:"inputModalities"`
	ContextWindow   int64                  `json:"contextWindow"`
	MaxOutputTokens int64                  `json:"maxOutputTokens"`
	Routes          []PlatformAIModelRoute `json:"routes"`
}

// PlatformAIModelList 定义平台模型目录。
type PlatformAIModelList struct {
	Models []PlatformAIModel `json:"models"`
}

// AIModelCallStatus 表示模型调用及其上游尝试的状态。
type AIModelCallStatus string

const (
	AIModelCallStatusRunning   AIModelCallStatus = AIModelCallStatus(domain.AIModelCallStatusRunning)
	AIModelCallStatusSucceeded AIModelCallStatus = AIModelCallStatus(domain.AIModelCallStatusSucceeded)
	AIModelCallStatusFailed    AIModelCallStatus = AIModelCallStatus(domain.AIModelCallStatusFailed)
	AIModelCallStatusCanceled  AIModelCallStatus = AIModelCallStatus(domain.AIModelCallStatusCanceled)
	AIModelCallStatusTimedOut  AIModelCallStatus = AIModelCallStatus(domain.AIModelCallStatusTimedOut)
)

// AIModelCallActor 表示模型调用的发起主体类型。
type AIModelCallActor string

const (
	AIModelCallActorMember AIModelCallActor = AIModelCallActor(domain.AIModelCallActorMember)
	AIModelCallActorAgent  AIModelCallActor = AIModelCallActor(domain.AIModelCallActorAgent)
	AIModelCallActorSystem AIModelCallActor = AIModelCallActor(domain.AIModelCallActorSystem)
)

// PlatformAIModelCallListInput 定义平台模型调用记录的筛选与分页条件：ModelID、Status 为空表示不限，Query 按工作区名称或标识匹配。
type PlatformAIModelCallListInput struct {
	ModelID  string            `json:"modelId" query:"modelId"`
	Status   AIModelCallStatus `json:"status" query:"status"`
	Query    string            `json:"query" query:"query"`
	Page     int               `json:"page" query:"page,default=1"`
	PageSize int               `json:"pageSize" query:"pageSize,default=50"`
}

// PlatformAIModelCall 定义一次平台模型调用及其归属工作区。
type PlatformAIModelCall struct {
	ID                string            `json:"id"`
	CreatedAt         time.Time         `json:"createdAt"`
	FinishedAt        *time.Time        `json:"finishedAt"`
	ModelID           string            `json:"modelId"`
	ModelName         string            `json:"modelName"`
	Usage             AIModelUsage      `json:"usage"`
	WorkspaceID       string            `json:"workspaceId"`
	WorkspaceName     string            `json:"workspaceName"`
	Actor             AIModelCallActor  `json:"actor"`
	Status            AIModelCallStatus `json:"status"`
	InputTokens       int64             `json:"inputTokens"`
	CachedInputTokens int64             `json:"cachedInputTokens"`
	OutputTokens      int64             `json:"outputTokens"`
	ErrorMessage      string            `json:"errorMessage"`
	AttemptCount      int               `json:"attemptCount"`
}

// PlatformAIModelCallList 定义平台模型调用记录分页结果。
type PlatformAIModelCallList struct {
	Calls []PlatformAIModelCall `json:"calls"`
	Page  PageInfo              `json:"page"`
}

// PlatformAIModelCallAttempt 定义平台模型调用的一次上游尝试。
type PlatformAIModelCallAttempt struct {
	ID                string            `json:"id"`
	CreatedAt         time.Time         `json:"createdAt"`
	FinishedAt        *time.Time        `json:"finishedAt"`
	ProviderName      string            `json:"providerName"`
	Identifier        string            `json:"identifier"`
	Status            AIModelCallStatus `json:"status"`
	InputTokens       int64             `json:"inputTokens"`
	CachedInputTokens int64             `json:"cachedInputTokens"`
	OutputTokens      int64             `json:"outputTokens"`
	ErrorMessage      string            `json:"errorMessage"`
}

// PlatformAIModelCallDetail 定义平台模型调用及其按顺序排列的上游尝试。
type PlatformAIModelCallDetail struct {
	Call     PlatformAIModelCall          `json:"call"`
	Attempts []PlatformAIModelCallAttempt `json:"attempts"`
}
