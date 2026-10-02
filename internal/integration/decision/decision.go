//go:build server

// Package decision 调用 System One 判断接口，在固定选项内给出带概率的判断。
package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/pkg/connectiontest"
)

// Kind 定义判断题型。
type Kind string

const (
	// KindYesNo 判断陈述是否成立。
	KindYesNo Kind = "noul"
	// KindChoice 从给定选项中选出一项。
	KindChoice Kind = "choice"
	// KindScore 在从低到高的有序等级上评分。
	KindScore Kind = "score"
)

const (
	// maxChoiceOptions 是单选题的最多选项数。
	maxChoiceOptions = 255
	// minScoreLevels 是评分题的最少等级数。
	minScoreLevels = 2
	// maxScoreLevels 是评分题的最多等级数。
	maxScoreLevels = 10
	// maxResponseBytes 是判定响应允许读取的最大字节数。
	maxResponseBytes = 1 << 20
)

// Credential 提供访问判断模型所需的接口地址和密钥，地址为 systemone 接口所在的基础路径。
type Credential struct {
	BaseURL string
	APIKey  string
}

// Option 定义单选题的一个选项，Key 在题目内唯一。
type Option struct {
	Key         string
	Description string
}

// Question 定义一道判断题：是否题只用 Instructions，单选题使用 Options，评分题使用从低到高排列的 Levels。
type Question struct {
	Kind         Kind
	Instructions string
	Options      []Option
	Levels       []string
}

// Answer 是一道判断题的结果。
type Answer struct {
	Kind Kind
	// Probability 是是否题判断为成立的概率。
	Probability float64
	// Choice 是单选题概率最高的选项键。
	Choice string
	// Score 是评分题按概率加权的等级下标，取值范围为 0 到等级数减一。
	Score float64
	// Probabilities 是单选题各选项键的概率。
	Probabilities map[string]float64
	// LevelProbabilities 是评分题各等级的概率，下标与 Levels 一致。
	LevelProbabilities []float64
	// Confidence 是单选题和评分题概率分布的集中程度，取值 0 到 1。
	Confidence float64
}

// Error 定义判断调用的语言无关失败原因码。
type Error struct {
	Code string
}

// Error 返回语言无关的失败原因。
func (e *Error) Error() string { return "decision: " + e.Code }

// Client 通过 System One 判断接口给出判断。
type Client struct{ http *http.Client }

// NewClient 创建判断客户端，重定向响应按失败处理。
func NewClient() *Client {
	return &Client{http: &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

// Decide 把状态和一组判断题一次提交给判断模型，返回按题目键索引的结果；state 为文本或可序列化为 JSON 的结构。
func (c *Client) Decide(ctx context.Context, credential Credential, model string, state any, questions map[string]Question) (map[string]Answer, error) {
	payloadQuestions := make(map[string]any, len(questions))
	for key, question := range questions {
		encoded, err := encodeQuestion(question)
		if err != nil {
			return nil, fmt.Errorf("question %q: %w", key, err)
		}
		payloadQuestions[key] = encoded
	}
	body, err := json.Marshal(map[string]any{"model": model, "state": state, "questions": payloadQuestions})
	if err != nil {
		return nil, err
	}
	endpoint, err := connectiontest.AppendPath(strings.TrimSpace(credential.BaseURL), "systemone")
	if err != nil {
		return nil, &Error{Code: "decision_model_unavailable"}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, &Error{Code: "decision_model_unavailable"}
	}
	request.Header.Set("Content-Type", "application/json")
	// 无凭据的自建服务不携带鉴权头。
	if apiKey := strings.TrimSpace(credential.APIKey); apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	response, err := c.http.Do(request)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, &Error{Code: "decision_timeout"}
		}
		return nil, &Error{Code: "decision_model_unavailable"}
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, &Error{Code: "decision_model_unavailable"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &Error{Code: "decision_failed"}
	}
	var decoded struct {
		Answers map[string]struct {
			Type          string             `json:"type"`
			Noul          *float64           `json:"noul"`
			Choice        string             `json:"choice"`
			Score         *float64           `json:"score"`
			Probabilities map[string]float64 `json:"probabilities"`
			Confidence    float64            `json:"confidence"`
		} `json:"answers"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&decoded); err != nil {
		return nil, &Error{Code: "decision_failed"}
	}
	answers := make(map[string]Answer, len(questions))
	for key, question := range questions {
		raw, ok := decoded.Answers[key]
		if !ok || Kind(raw.Type) != question.Kind {
			return nil, &Error{Code: "decision_failed"}
		}
		answer := Answer{Kind: question.Kind, Confidence: raw.Confidence}
		switch question.Kind {
		case KindYesNo:
			if raw.Noul == nil || *raw.Noul < 0 || *raw.Noul > 1 {
				return nil, &Error{Code: "decision_failed"}
			}
			answer.Probability = *raw.Noul
		case KindChoice:
			// 返回的选项必须是题目给出的选项之一。
			valid := false
			for _, option := range question.Options {
				valid = valid || option.Key == raw.Choice
			}
			if !valid {
				return nil, &Error{Code: "decision_failed"}
			}
			answer.Choice = raw.Choice
			answer.Probabilities = raw.Probabilities
		case KindScore:
			if raw.Score == nil || *raw.Score < 0 || *raw.Score > float64(len(question.Levels)-1) {
				return nil, &Error{Code: "decision_failed"}
			}
			answer.Score = *raw.Score
			// 评分概率按等级下标返回，转换为与 Levels 对齐的切片。
			answer.LevelProbabilities = make([]float64, len(question.Levels))
			for index := range question.Levels {
				answer.LevelProbabilities[index] = raw.Probabilities[strconv.Itoa(index)]
			}
		}
		answers[key] = answer
	}
	return answers, nil
}

// encodeQuestion 校验题型约束并转换为接口请求格式。
func encodeQuestion(question Question) (map[string]any, error) {
	if strings.TrimSpace(question.Instructions) == "" {
		return nil, errors.New("instructions are required")
	}
	encoded := map[string]any{"type": string(question.Kind), "instructions": question.Instructions}
	switch question.Kind {
	case KindYesNo:
	case KindChoice:
		if len(question.Options) == 0 || len(question.Options) > maxChoiceOptions {
			return nil, fmt.Errorf("choice question requires 1 to %d options", maxChoiceOptions)
		}
		criteria := make(map[string]string, len(question.Options))
		for _, option := range question.Options {
			if _, exists := criteria[option.Key]; exists || option.Key == "" {
				return nil, fmt.Errorf("option key %q is empty or duplicated", option.Key)
			}
			criteria[option.Key] = option.Description
		}
		encoded["criteria"] = criteria
	case KindScore:
		if len(question.Levels) < minScoreLevels || len(question.Levels) > maxScoreLevels {
			return nil, fmt.Errorf("score question requires %d to %d levels", minScoreLevels, maxScoreLevels)
		}
		encoded["criteria"] = question.Levels
	default:
		return nil, fmt.Errorf("unsupported question kind %q", question.Kind)
	}
	return encoded, nil
}
