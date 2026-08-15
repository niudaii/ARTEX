// Finding filter: uses LLM judgment to remove low-value/ignorable vulnerabilities
// before report generation. Best-effort — returns all findings unchanged if the
// LLM is unavailable or any call fails.

package report

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
)

type filterFindingInput struct {
	Index     int    `json:"index"`
	VulnClass string `json:"vulnclass"`
	Severity  string `json:"severity"`
	Summary   string `json:"summary"`
	PoC       string `json:"poc,omitempty"`
}

type filterVerdict struct {
	Index  int    `json:"index"`
	Ignore bool   `json:"ignore"`
	Rule   string `json:"rule,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// FilterFindings removes low-value findings via LLM judgment.
// systemPrompt overrides the default filtering criteria (empty = use default).
// If prov is nil or the call fails, all findings are returned unchanged.
func FilterFindings(ctx context.Context, findings []*db.Node, prov llm.Provider, systemPrompt string) []*db.Node {
	if prov == nil || len(findings) == 0 {
		return findings
	}

	inputs := make([]filterFindingInput, len(findings))
	for i, n := range findings {
		var p struct {
			VulnClass string `json:"vulnclass"`
			Severity  string `json:"severity"`
			Summary   string `json:"summary"`
			Evidence  struct {
				PoC string `json:"poc"`
			} `json:"evidence"`
		}
		_ = json.Unmarshal(n.Payload, &p)
		inputs[i] = filterFindingInput{
			Index:     i,
			VulnClass: p.VulnClass,
			Severity:  p.Severity,
			Summary:   p.Summary,
			PoC:       p.Evidence.PoC,
		}
	}

	ignoreSet := make(map[int]bool)
	const chunkSize = 30
	for start := 0; start < len(inputs); start += chunkSize {
		end := start + chunkSize
		if end > len(inputs) {
			end = len(inputs)
		}
		verdicts, err := judgeChunk(ctx, prov, inputs[start:end], systemPrompt)
		if err != nil {
			log.Printf("[report/filter] chunk %d-%d error: %v (keeping all)", start, end, err)
			continue
		}
		for _, v := range verdicts {
			if v.Ignore {
				ignoreSet[v.Index] = true
			}
		}
	}

	var out []*db.Node
	for i, n := range findings {
		if !ignoreSet[i] {
			out = append(out, n)
		}
	}
	if removed := len(findings) - len(out); removed > 0 {
		log.Printf("[report/filter] removed %d/%d low-value findings", removed, len(findings))
	}
	return out
}

func judgeChunk(ctx context.Context, prov llm.Provider, inputs []filterFindingInput, systemPrompt string) ([]filterVerdict, error) {
	if systemPrompt == "" {
		systemPrompt = DefaultFilterSystemPrompt
	}
	inputsJSON, _ := json.Marshal(inputs)
	req := llm.CompletionRequest{
		System:    []string{systemPrompt},
		Messages:  []llm.Message{llm.UserText(fmt.Sprintf(filterUserPrompt, string(inputsJSON)))},
		MaxTokens: 4096,
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	acc := llm.NewAccumulator()
	for ev, err := range prov.Stream(ctx, req) {
		if err != nil {
			return nil, err
		}
		acc.Add(ev)
	}
	text := extractJSONArray(acc.Message().Text())
	if text == "" {
		return nil, fmt.Errorf("empty LLM response (stop=%s, thinking-only?)", acc.StopReason)
	}
	var verdicts []filterVerdict
	if err := json.Unmarshal([]byte(text), &verdicts); err != nil {
		return nil, fmt.Errorf("parse verdicts: %w (raw: %s)", err, truncateStr(text, 200))
	}
	return verdicts, nil
}

func extractJSONArray(s string) string {
	start := strings.Index(s, "[")
	if start < 0 {
		return s
	}
	end := strings.LastIndex(s, "]")
	if end <= start {
		return s
	}
	return s[start : end+1]
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

const DefaultFilterSystemPrompt = `你是安全漏洞过滤专家。判断每个漏洞是否应该从报告中忽略（不写入最终报告）。

核心原则：只忽略无法造成实际危害、不涉及敏感信息、或缺乏证据的漏洞。其余一律不忽略。

判断时关注三个维度：
1. 可利用性：漏洞能否被实际利用（不只是理论可行）
2. 危害后果：利用后能造成什么实际危害
3. 敏感信息：是否涉及敏感数据的泄露/篡改/删除

忽略标准：

A. 漏洞类型专项：
- A1 XSS: Self-XSS（控制台粘贴执行）、PDF-XSS、非自然流量地址存储型XSS、需点击触发且无进一步危害。不忽略：访问即触发、可窃取Cookie/Session、可控制他人账号。
- A2 CORS: 无法读取敏感数据、无法进行credential传输。credentials:true+任意Origin反射但PoC未演示实际跨域读取到敏感数据（如仅返回401/非敏感响应）→忽略。不忽略：PoC演示了实际跨域读取到敏感数据（如成功读取用户信息/业务数据）。
- A3 CSRF: POST型、无敏感操作。不忽略：GET型+密码修改/资金操作/权限变更。
- A4 SSRF: 无回显、仅dnslog无影响。不忽略：可访问内网、可读取文件、可泄露敏感数据。
- A5 URL重定向: 二维码形式、需点击触发且无进一步危害。不忽略：直接访问即触发跳转（302/meta refresh/JS window.location）。
- A6 AI安全: 未泄露数据/调用接口的Prompt注入、模型准确性问题。不忽略：导致后端数据泄露/接口调用/命令执行。

B. 无法利用或无利用价值（无敏感信息时忽略）：
无敏感信息的目录遍历/异常信息泄露(仅内网IP/hostname/端口/服务名/路径)/JSON劫持/前端打包文件/日志/401钓鱼/编码缺陷无法利用/未授权访问无利用点/验证码复用/六位验证码爆破/横向短信轰炸/API文档泄露/actuator泄露/非核心客户端本地DoS/安全响应头缺失(独立存在)/需MITM位置利用的凭据安全配置缺陷(Cookie缺Secure/明文HTTP传输/OAuth redirect_uri明文，未提供实际凭据窃取证明)。

C. 权限与业务影响不足：不可遍历的越权、非敏感信息越权、无实际危害(功能缺陷/乱码/报错)、不能直接体现漏洞(猜测/测试页面)。

D. 范围/重复/证据：非安全缺陷/业务预期/产品Bug/隐私合规建议/自我DoS/不可指定用户邮箱轰炸/纯扫描器结果/非接收范围/重复漏洞/已知公开CVE/专项治理已知/未提供PoC/仅理论可行/纯推测。

敏感信息（命中即不忽略）：
- 员工信息(姓名/邮箱/手机/工号/部门/职位/组织架构)
- 用户PII(手机/邮箱/身份证/地址/真实姓名)
- 认证凭据值被实际窃取(token/密码/session/API key/密钥的实际值泄露)
- 财务数据(订单/交易/余额/支付)
- 内部敏感数据(系统数据/配置/后台管理)
拿不准时按敏感处理，不忽略。
注意：凭据安全配置缺陷(Cookie缺Secure/OAuth redirect_uri明文等)未实际泄露凭据值时→B16忽略，不命中敏感信息门控。

关键边界：
- B4: 基础设施元数据(IP/hostname/端口/服务名/路径)可忽略；结构化配置数据(数据中心/集群架构/存储平台/网络拓扑/权限模型)不可忽略。
- B15: 仅HTTP安全响应头缺失可忽略。Cookie属性(Secure/HttpOnly)/OAuth配置(redirect_uri)→B16(需MITM且无实际窃取证明→忽略)。
- B3 vs D13/D14: 已确认漏洞存在但无可达敏感数据→B3忽略；利用依赖未来变更→D14忽略；仅理论未验证→D13忽略。
- 条件性利用: 利用需凭据→敏感信息门控不忽略；利用需MITM位置且无实际利用证明→B16忽略；依赖未来变更→D14忽略；当前无可达敏感数据→B3忽略。
- 安全头缺失+已确认攻击链(如缺CSP+已确认XSS)→复合漏洞，不忽略。但HSTS缺失与被B16忽略的MITM类缺陷不构成已确认攻击链。`

const filterUserPrompt = `根据上述标准，判断以下每个漏洞是否应该忽略。仔细阅读每个漏洞的类型、等级、描述和PoC证据，基于实际利用场景和危害后果推理判断。

漏洞列表(JSON):
%s

输出JSON数组，每个元素对应一个漏洞:
{"index": <原始序号>, "ignore": true/false, "rule": "<命中编号如A1a/B3/D12>", "reason": "<一句话理由>"}

只输出JSON数组，不要其他内容。`
