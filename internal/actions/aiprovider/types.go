//go:build server

// Package aiprovider 实现模型服务供应商的查询与操作。
package aiprovider

import "github.com/runforyou-ai/cervi/internal/domain"

// Input 定义创建模型服务供应商的字段。
type Input struct {
	Brand          domain.AIProviderBrand
	Name           string
	CredentialType domain.AIProviderCredentialType
	APIKey         string
	APIURL         string
	Models         []Model
}

// UpdateInput 定义修改模型服务供应商的字段，品牌沿用创建时的值。
type UpdateInput struct {
	Name           string
	CredentialType domain.AIProviderCredentialType
	APIKey         string
	APIURL         string
	Models         []Model
}

// ConnectionInput 定义测试模型服务连接和发现模型需要的草稿配置。
type ConnectionInput struct {
	Brand          domain.AIProviderBrand
	CredentialType domain.AIProviderCredentialType
	APIKey         string
	APIURL         string
}

// Model 定义供应商模型目录项。
type Model struct {
	Identifier      string
	Name            string
	Type            domain.AIModelType
	InputModalities []domain.AIModelInputModality
	ContextWindow   int64
	MaxOutputTokens int64
}

// Record 定义模型服务供应商及其模型目录。
type Record struct {
	ID             string
	Brand          domain.AIProviderBrand
	Name           string
	CredentialType domain.AIProviderCredentialType
	APIKey         string
	APIURL         string
	Models         []Model
}

// ModelSummary 定义供应商列表中的模型目录摘要。
type ModelSummary struct {
	Identifier string
	Name       string
	Type       domain.AIModelType
}

// Summary 定义模型服务供应商列表项。
type Summary struct {
	ID     string
	Brand  domain.AIProviderBrand
	Name   string
	APIURL string
	Models []ModelSummary
}
