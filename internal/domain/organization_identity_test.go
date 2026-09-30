package domain

import "testing"

// TestIdentityDisplayNameValid 验证企业成员与 AI 员工显示名的字符规则。
func TestIdentityDisplayNameValid(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		valid bool
	}{
		{"林晓", true},
		{"群协作助手 1", true},
		{"阿卜杜拉·买买提", true},
		{"Anne-Marie O_Neil Jr.", true},
		{"Dev @Ops", false},
		{"林晓（采购）", false},
		{"产品#经理", false},
		{"助手🤖", false},
		{"张三\n李四", false},
	} {
		if got := IdentityDisplayNameValid(scenario.name); got != scenario.valid {
			t.Errorf("IdentityDisplayNameValid(%q) = %v，期望 %v", scenario.name, got, scenario.valid)
		}
	}
}
