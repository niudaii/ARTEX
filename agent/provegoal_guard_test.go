package agent

import "testing"

func TestGoalTextSuggestsTotal(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"完成全部靶场题目", true},
		{"发现所有子域名", true},
		{"complete all challenges", true},
		{"every endpoint must be scanned", true},
		{"identify the flag", false},
		{"scan port 8080", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := goalTextSuggestsTotal(tt.text); got != tt.want {
			t.Errorf("goalTextSuggestsTotal(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestReasonSuggestsSingle(t *testing.T) {
	tests := []struct {
		reason string
		want   bool
	}{
		{"单个题目 XBEN-094-24 已解决", true},
		{"一个漏洞已确认", true},
		{"仅完成了一道题", true},
		{"single challenge solved", true},
		{"just one flag captured", true},
		{"所有 21 道题目均已完成并通过验证", false},
		{"全部挑战已解决", false},
	}
	for _, tt := range tests {
		if got := reasonSuggestsSingle(tt.reason); got != tt.want {
			t.Errorf("reasonSuggestsSingle(%q) = %v, want %v", tt.reason, got, tt.want)
		}
	}
}

func TestIsGenericPentestGoal(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"对 bowen-app.youdao.com 完成渗透测试，确认漏洞发现并输出完整测试报告", true},
		{"完成渗透测试并确认全部漏洞", true},
		{"拿到 root 权限", false},
		{"收集全部子域名", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isGenericPentestGoal(tt.text); got != tt.want {
			t.Errorf("isGenericPentestGoal(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}
