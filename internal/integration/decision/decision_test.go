//go:build server

package decision

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDecide 验证三种题型的请求格式、鉴权和结果解析。
func TestDecide(t *testing.T) {
	var path string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body = map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"model":"jev","answers":{
			"resolved":{"type":"noul","noul":0.97},
			"sentiment":{"type":"choice","choice":"positive","probabilities":{"positive":0.9,"neutral":0.1,"negative":0},"confidence":0.8},
			"satisfaction":{"type":"score","score":1.8,"legend":{"0":"低","1":"中","2":"高"},"probabilities":{"0":0,"1":0.2,"2":0.8},"confidence":0.7}
		}}`))
	}))
	defer server.Close()
	questions := map[string]Question{
		"resolved":     {Kind: KindYesNo, Instructions: "问题是否已解决"},
		"sentiment":    {Kind: KindChoice, Instructions: "客户情绪", Options: []Option{{Key: "positive", Description: "积极"}, {Key: "neutral", Description: "中性"}, {Key: "negative", Description: "消极"}}},
		"satisfaction": {Kind: KindScore, Instructions: "满意度", Levels: []string{"低", "中", "高"}},
	}

	answers, err := NewClient().Decide(context.Background(), Credential{BaseURL: server.URL + "/api/v1", APIKey: "secret"}, "jev", "客户：已经好了，谢谢", questions)
	if err != nil || path != "/api/v1/systemone" {
		t.Fatalf("path=%s err=%v", path, err)
	}
	if body["model"] != "jev" || body["state"] != "客户：已经好了，谢谢" {
		t.Fatalf("body=%v", body)
	}
	sentiment := body["questions"].(map[string]any)["sentiment"].(map[string]any)
	if sentiment["type"] != "choice" || sentiment["criteria"].(map[string]any)["negative"] != "消极" {
		t.Fatalf("sentiment=%v", sentiment)
	}
	if answers["resolved"].Probability != 0.97 {
		t.Fatalf("resolved=%+v", answers["resolved"])
	}
	if answer := answers["sentiment"]; answer.Choice != "positive" || answer.Probabilities["neutral"] != 0.1 || answer.Confidence != 0.8 {
		t.Fatalf("sentiment=%+v", answer)
	}
	if answer := answers["satisfaction"]; answer.Score != 1.8 || len(answer.LevelProbabilities) != 3 || answer.LevelProbabilities[2] != 0.8 {
		t.Fatalf("satisfaction=%+v", answer)
	}

	_, err = NewClient().Decide(context.Background(), Credential{BaseURL: server.URL + "/api/v1", APIKey: "wrong"}, "jev", "x", questions)
	if failure, ok := err.(*Error); !ok || failure.Code != "decision_model_unavailable" {
		t.Fatalf("err=%v", err)
	}
}

// TestDecideRejectsInvalidQuestions 验证题型约束在发出请求前校验。
func TestDecideRejectsInvalidQuestions(t *testing.T) {
	levels := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"}
	for name, question := range map[string]Question{
		"缺少说明":   {Kind: KindYesNo},
		"单选无选项":  {Kind: KindChoice, Instructions: "x"},
		"选项重复":   {Kind: KindChoice, Instructions: "x", Options: []Option{{Key: "a"}, {Key: "a"}}},
		"评分等级过少": {Kind: KindScore, Instructions: "x", Levels: []string{"1"}},
		"评分等级过多": {Kind: KindScore, Instructions: "x", Levels: levels},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewClient().Decide(context.Background(), Credential{BaseURL: "http://127.0.0.1:1"}, "jev", "x", map[string]Question{"q": question})
			if _, ok := err.(*Error); ok || err == nil {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

// TestDecideRejectsUnknownChoice 验证接口返回题目之外的选项时视为失败。
func TestDecideRejectsUnknownChoice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"answers":{"q":{"type":"choice","choice":"other","probabilities":{"other":1}}}}`))
	}))
	defer server.Close()
	_, err := NewClient().Decide(context.Background(), Credential{BaseURL: server.URL}, "jev", "x", map[string]Question{
		"q": {Kind: KindChoice, Instructions: "x", Options: []Option{{Key: "a", Description: "甲"}}},
	})
	if failure, ok := err.(*Error); !ok || failure.Code != "decision_failed" {
		t.Fatalf("err=%v", err)
	}
}

// TestDecideRejectsMissingOrOutOfRangeValues 验证是否题缺少概率、评分题缺少或超出等级范围时视为失败。
func TestDecideRejectsMissingOrOutOfRangeValues(t *testing.T) {
	for name, test := range map[string]struct {
		response string
		question Question
	}{
		"是否题缺少概率": {`{"answers":{"q":{"type":"noul"}}}`, Question{Kind: KindYesNo, Instructions: "x"}},
		"评分题缺少分值": {`{"answers":{"q":{"type":"score","probabilities":{"0":1}}}}`, Question{Kind: KindScore, Instructions: "x", Levels: []string{"低", "高"}}},
		"评分超出等级":  {`{"answers":{"q":{"type":"score","score":2}}}`, Question{Kind: KindScore, Instructions: "x", Levels: []string{"低", "高"}}},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()
			_, err := NewClient().Decide(context.Background(), Credential{BaseURL: server.URL}, "jev", "x", map[string]Question{"q": test.question})
			if failure, ok := err.(*Error); !ok || failure.Code != "decision_failed" {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
