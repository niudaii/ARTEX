package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/popo"
	actool "github.com/Autumn-27/norma/tool"
)

// 平台操作工具(给内置 Auto agent 用):建/改 skill、自定义工具、MCP。都是 host 工具,
// seed 进 tools 表、默认绑定 auto,经 hostTools 注入。复用现有 db/文件系统逻辑。

func (s *Server) platformTools() []actool.CoreTool {
	return []actool.CoreTool{
		s.toolCreateSkill(),
		s.toolUpdateSkillFile(),
		s.toolCreateCustomTool(),
		s.toolUpdateCustomTool(),
		s.toolCreateMCP(),
		s.toolUpdateMCP(),
		s.toolDeleteAssetsByHost(),
		s.toolSendMe(),
		s.toolArchiveTaskReport(),
	}
}

// platformToolKeys are the tool keys the Auto agent gets bound by default.
var platformToolKeys = []string{
	"create_skill", "update_skill_file",
	"create_custom_tool", "update_custom_tool",
	"create_mcp", "update_mcp",
	"delete_assets_by_host",
	"send_me",
	"archive_task_report",
}

// ---- assets ----

// toolDeleteAssetsByHost hard-deletes every asset tied to one host (exact match).
// Platform-level (not a per-task tool): operates on the global, cross-task asset库.
func (s *Server) toolDeleteAssetsByHost() actool.CoreTool {
	return wrTool("delete_assets_by_host",
		"按 host 精确删除资产：删掉该 host 的域名/子域名，以及其下的服务(service)、接口(endpoint)。\n"+
			"host 完全匹配(小写、去空格)，不是模糊/通配。\n"+
			"传根域名(如 example.com)会连带删除它的子域名及其服务/接口；传子域名(如 a.example.com)或 IP 只删该 host 自身及其服务/接口。\n"+
			"⚠️ 硬删除、作用于全局资产库(跨任务共享)、不可撤销。",
		objSchema(map[string]any{
			"host": strParam("要删除的 host：域名/子域名/IP。完全匹配，如 example.com 或 a.example.com 或 1.2.3.4"),
		}, "host"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			as := s.assetStore()
			if as == nil {
				return actool.Errorf("资产库未初始化"), nil
			}
			var a struct {
				Host string `json:"host"`
			}
			_ = json.Unmarshal(in, &a)
			if strings.TrimSpace(a.Host) == "" {
				return actool.Errorf("host 不能为空"), nil
			}
			counts, err := as.DeleteByHost(a.Host)
			if err != nil {
				return actool.Errorf("删除失败: " + err.Error()), nil
			}
			var total int64
			for _, n := range counts {
				total += n
			}
			return jsonResult(map[string]any{
				"host":            a.Host,
				"deleted":         total,
				"deleted_by_type": counts,
			})
		})
}

// ---- report ----

// toolArchiveTaskReport writes the agent-assembled archive report back to a task
// (overwrites the task's persisted report). It is the closing action of the
// 「报告归档」 flow driven by the Auto agent from the report tab.
func (s *Server) toolArchiveTaskReport() actool.CoreTool {
	return wrTool("archive_task_report",
		"把归档好的渗透测试报告(Markdown)写回指定任务，覆盖该任务已保存的报告。\n"+
			"「报告归档」的收尾动作：报告按归档要求组装完成后调用本工具；写回成功后简要回复归档结果即可。",
		objSchema(map[string]any{
			"task_id":  strParam("要归档的任务 id"),
			"markdown": strParam("完整归档报告 Markdown(含漏洞总结表 + 按域名分组的漏洞正文)"),
		}, "task_id", "markdown"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				TaskID   string `json:"task_id"`
				Markdown string `json:"markdown"`
			}
			_ = json.Unmarshal(in, &a)
			t := s.m.ResolveTask(strings.TrimSpace(a.TaskID))
			if t == nil {
				return actool.Errorf("task 不存在: " + a.TaskID), nil
			}
			md := strings.TrimSpace(a.Markdown)
			if len([]rune(md)) < 200 {
				return actool.Errorf("报告内容过短(<200字)，拒绝写回；先组装完整归档报告"), nil
			}
			taskIDInt, err := strconv.ParseInt(t.ID, 10, 64)
			if err != nil {
				return actool.Errorf("task id 非法: " + t.ID), nil
			}
			if err := s.m.PG().SaveReport(taskIDInt, md); err != nil {
				return actool.Errorf("归档写回失败: " + err.Error()), nil
			}
			log.Printf("[report/archive] task %s: agent archived report (%d chars)", t.ID, len(md))
			return jsonResult(map[string]any{"task_id": t.ID, "saved_chars": len(md)})
		})
}

// ---- skills ----

func (s *Server) toolCreateSkill() actool.CoreTool {
	return wrTool("create_skill",
		"创建一个新 skill(写 SKILL.md，agentskills.io 规范)。name 小写字母/数字/连字符。",
		objSchema(map[string]any{
			"name":         strParam("skill 名(小写字母开头，字母/数字/连字符)"),
			"description":  strParam("skill 描述(必填，说明它做什么/何时用)"),
			"instructions": strParam("Markdown 正文说明(可选)"),
		}, "name", "description"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct{ Name, Description, Instructions string }
			_ = json.Unmarshal(in, &a)
			if !validSkillName(a.Name) {
				return actool.Errorf("skill 名不合法(小写字母开头，仅字母/数字/连字符，≤64)"), nil
			}
			if strings.TrimSpace(a.Description) == "" {
				return actool.Errorf("description 必填"), nil
			}
			path := filepath.Join(s.skillDir, a.Name)
			if _, err := os.Stat(path); err == nil {
				return actool.Errorf("skill 已存在: " + a.Name), nil
			}
			if err := os.MkdirAll(path, 0o755); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			var b strings.Builder
			b.WriteString("---\n")
			fmt.Fprintf(&b, "name: %s\n", a.Name)
			fmt.Fprintf(&b, "description: %s\n", a.Description)
			b.WriteString("---\n")
			if strings.TrimSpace(a.Instructions) != "" {
				b.WriteString(a.Instructions)
			} else {
				fmt.Fprintf(&b, "## %s\n\n1. \n2. \n3. \n", a.Name)
			}
			if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(b.String()), 0o644); err != nil {
				_ = os.RemoveAll(path)
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text("skill created: " + a.Name), nil
		})
}

func (s *Server) toolUpdateSkillFile() actool.CoreTool {
	return wrTool("update_skill_file",
		"写/覆盖某个 skill 内的一个文件(默认 SKILL.md)。用于修改技能内容或加脚本/引用。",
		objSchema(map[string]any{
			"name":    strParam("skill 名"),
			"file":    strParam("相对路径(可选，默认 SKILL.md，如 scripts/run.py)"),
			"content": strParam("文件完整内容"),
		}, "name", "content"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct{ Name, File, Content string }
			_ = json.Unmarshal(in, &a)
			if !validSkillName(a.Name) {
				return actool.Errorf("skill 名不合法"), nil
			}
			skillPath := filepath.Join(s.skillDir, a.Name)
			if _, err := os.Stat(skillPath); os.IsNotExist(err) {
				return actool.Errorf("skill 不存在: " + a.Name), nil
			}
			rel := strings.TrimSpace(a.File)
			if rel == "" {
				rel = "SKILL.md"
			}
			clean, msg := skillRelPath(rel)
			if msg != "" {
				return actool.Errorf("非法路径: " + msg), nil
			}
			full := filepath.Join(skillPath, clean)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if err := os.WriteFile(full, []byte(a.Content), 0o644); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text("skill file written: " + a.Name + "/" + clean), nil
		})
}

// ---- custom tools ----

type customToolToolInput struct {
	Key         string          `json:"key"`
	Description string          `json:"description"`
	Kind        string          `json:"kind"`
	Exec        json.RawMessage `json:"exec"`
	Schema      json.RawMessage `json:"schema"`
	Agents      []string        `json:"agents"`
	Deferred    bool            `json:"deferred"`
	Enabled     *bool           `json:"enabled"`
}

func customToolSchema(keyDesc string) map[string]any {
	return objSchema(map[string]any{
		"key":         strParam(keyDesc),
		"description": strParam("发给模型的描述"),
		"kind":        strParam("shell | command | script(仅Python) | http。shell=bash 环境声明(仅告知模型该工具可在 bash 中直接调用，无需 exec/schema)；其余三种需提供 exec"),
		"exec":        map[string]any{"type": "object", "description": "执行规格(shell 类型不需要): command→{command}; script→{code}; http→{method,url,headers,body,proxy,use_recording_proxy}"},
		"schema":      map[string]any{"type": "object", "description": "参数 JSON-Schema(shell/command/script 可留空; http 必填且需含 properties)"},
		"agents":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "绑定的 agent key(可选)"},
		"deferred":    map[string]any{"type": "boolean", "description": "是否延迟(shell 类型无效；仅 command/script/http 的不常用工具才开)"},
		"enabled":     map[string]any{"type": "boolean", "description": "是否启用(默认 true)"},
	}, "key", "kind")
}

func toDBTool(a customToolToolInput) *db.Tool {
	enabled := true
	if a.Enabled != nil {
		enabled = *a.Enabled
	}
	return &db.Tool{
		Key: a.Key, Description: a.Description, Schema: a.Schema, Agents: a.Agents,
		Enabled: enabled, Kind: a.Kind, Exec: a.Exec, Deferred: a.Deferred,
	}
}

func (s *Server) toolCreateCustomTool() actool.CoreTool {
	return wrTool("create_custom_tool", "【重要】当安装一些平台没有的工具时，调用该工具将安装的工具放入平台中，让平台可以调用！创建一个自定义工具(shell/command/script/http)。shell=bash 环境声明，只需 key+description+agents，无需 exec/schema。",
		customToolSchema("工具 key(小写字母开头，字母/数字/下划线)"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a customToolToolInput
			_ = json.Unmarshal(in, &a)
			a.Key = strings.TrimSpace(a.Key)
			if !reToolKey.MatchString(a.Key) {
				return actool.Errorf("key 需小写字母开头，仅含小写字母/数字/下划线"), nil
			}
			if a.Kind != "shell" && a.Kind != "command" && a.Kind != "script" && a.Kind != "http" {
				return actool.Errorf("kind 需为 shell / command / script / http"), nil
			}
			if a.Kind == "http" && !hasSchemaProps(a.Schema) {
				return actool.Errorf("http 工具必须提供参数 JSON Schema(不能留空)"), nil
			}
			if exist, _ := s.m.pg.GetTool(a.Key); exist != nil {
				return actool.Errorf("该 key 已存在: " + a.Key), nil
			}
			if err := s.m.pg.CreateCustomTool(toDBTool(a)); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text("custom tool created: " + a.Key), nil
		})
}

func (s *Server) toolUpdateCustomTool() actool.CoreTool {
	return wrTool("update_custom_tool", "修改一个已有的自定义工具(按 key)。",
		customToolSchema("要修改的自定义工具 key"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a customToolToolInput
			_ = json.Unmarshal(in, &a)
			existing, _ := s.m.pg.GetTool(a.Key)
			if existing == nil || existing.System {
				return actool.Errorf("只能修改自定义工具: " + a.Key), nil
			}
			if a.Kind != "shell" && a.Kind != "command" && a.Kind != "script" && a.Kind != "http" {
				return actool.Errorf("kind 需为 shell / command / script / http"), nil
			}
			if a.Kind == "http" && !hasSchemaProps(a.Schema) {
				return actool.Errorf("http 工具必须提供参数 JSON Schema(不能留空)"), nil
			}
			if err := s.m.pg.UpdateCustomTool(toDBTool(a)); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text("custom tool updated: " + a.Key), nil
		})
}

// ---- MCP ----

type mcpToolInput struct {
	ID        int64           `json:"id"`
	Name      string          `json:"name"`
	Transport string          `json:"transport"`
	Command   string          `json:"command"`
	Args      json.RawMessage `json:"args"`
	Env       json.RawMessage `json:"env"`
	URL       string          `json:"url"`
	Enabled   *bool           `json:"enabled"`
}

func mcpSchema(withID bool) map[string]any {
	props := map[string]any{
		"name":      strParam("MCP 服务器名"),
		"transport": strParam("stdio | http / sse"),
		"command":   strParam("stdio 的启动命令(如 npx)"),
		"args":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "命令参数数组"},
		"env":       map[string]any{"type": "object", "description": "环境变量 {KEY:VALUE}"},
		"url":       strParam("http/sse 的 URL"),
		"enabled":   map[string]any{"type": "boolean", "description": "是否启用(默认 true)"},
	}
	required := []string{"name", "transport"}
	if withID {
		props["id"] = map[string]any{"type": "integer", "description": "要修改的 MCP 服务器 id"}
		required = []string{"id", "name", "transport"}
	}
	return objSchema(props, required...)
}

func (a mcpToolInput) toDB() *db.MCPServer {
	enabled := true
	if a.Enabled != nil {
		enabled = *a.Enabled
	}
	return &db.MCPServer{
		ID: a.ID, Name: a.Name, Transport: a.Transport, Command: a.Command,
		Args: a.Args, Env: a.Env, URL: a.URL, Enabled: enabled,
	}
}

func (s *Server) toolCreateMCP() actool.CoreTool {
	return wrTool("create_mcp", "创建一个 MCP 服务器(stdio/http)。创建后其工具需按 agent 可见性授权。",
		mcpSchema(false),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a mcpToolInput
			_ = json.Unmarshal(in, &a)
			a.ID = 0
			if strings.TrimSpace(a.Name) == "" || strings.TrimSpace(a.Transport) == "" {
				return actool.Errorf("name / transport 必填"), nil
			}
			id, err := s.m.pg.SaveMCP(a.toDB())
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text(fmt.Sprintf("mcp created: id=%d name=%s", id, a.Name)), nil
		})
}

func (s *Server) toolUpdateMCP() actool.CoreTool {
	return wrTool("update_mcp", "修改一个已有的 MCP 服务器(按 id)。",
		mcpSchema(true),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a mcpToolInput
			_ = json.Unmarshal(in, &a)
			if a.ID == 0 {
				return actool.Errorf("id 必填"), nil
			}
			if _, err := s.m.pg.SaveMCP(a.toDB()); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text(fmt.Sprintf("mcp updated: id=%d", a.ID)), nil
		})
}

// ---- notify ----

// popoRobot is a lazily-initialized singleton; the token is cached inside.
const settingTaskURL = "task_url"

var (
	popoOnce   sync.Once
	popoRobot  *popo.Robot
	popoConfig popoCfg
)

type popoCfg struct {
	appKey    string
	appSecret string
	notifyTo  string
}

// loadPopoConfig reads POPO robot credentials from env vars.
func loadPopoConfig() popoCfg {
	appKey := os.Getenv("ARTEX_POPO_APP_KEY")
	appSecret := os.Getenv("ARTEX_POPO_APP_SECRET")
	notifyTo := os.Getenv("ARTEX_POPO_NOTIFY_TO")
	return popoCfg{appKey: appKey, appSecret: appSecret, notifyTo: notifyTo}
}

// popoRobotSingleton returns the cached robot, creating it on first use.
func popoRobotSingleton() *popo.Robot {
	popoOnce.Do(func() {
		popoConfig = loadPopoConfig()
		popoRobot = popo.NewRobot(popoConfig.appKey, popoConfig.appSecret)
	})
	return popoRobot
}

// popoConfigured reports whether the POPO robot credentials AND the notify
// target are present, via the singleton's lazy config load. When false,
// send_me fails fast with a clear message and notifyTaskDone skips silently,
// instead of surfacing an opaque token-endpoint error.
func popoConfigured() bool {
	popoRobotSingleton()
	return popoConfig.appKey != "" && popoConfig.appSecret != "" && popoConfig.notifyTo != ""
}

// toolSendMe sends a POPO message to notify me (the operator). Designed for
// important events such as a failed vulnerability retest: the agent calls it with
// a formatted message including the vulnerability title, link, and task link.
func (s *Server) toolSendMe() actool.CoreTool {
	return wrTool("send_me",
		"通过 POPO 机器人给主人发一条通知消息。\n"+
			"典型场景：漏洞复测未通过时，发送漏洞标题、漏洞链接和 ARTEX 任务链接。\n"+
			"消息内容由调用方组织，直接原样发送。",
		objSchema(map[string]any{
			"message": strParam("要发送的消息正文。例如：漏洞复测未通过 — 漏洞标题：XXX，漏洞链接：https://...，ARTEX 任务链接：https://..."),
		}, "message"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(in, &a)
			if strings.TrimSpace(a.Message) == "" {
				return actool.Errorf("message 不能为空"), nil
			}
			if !popoConfigured() {
				return actool.Errorf("POPO 机器人未配置：需设置 ARTEX_POPO_APP_KEY / ARTEX_POPO_APP_SECRET / ARTEX_POPO_NOTIFY_TO 环境变量"), nil
			}
			robot := popoRobotSingleton()
			if err := robot.SendMessage(popoConfig.notifyTo, a.Message); err != nil {
				log.Printf("[send_me] 发送失败: %v", err)
				return actool.Errorf("发送失败: " + err.Error()), nil
			}
			return actool.Text("消息已发送"), nil
		})
}

// notifyTaskDone sends a POPO notification when a task reaches a terminal state.
// Best-effort: errors are logged but never propagated, so a POPO outage cannot
// block the task-completion path.
func (s *Server) notifyTaskDone(taskID string) {
	t := s.m.ResolveTask(taskID)
	if t == nil {
		return
	}
	desc := t.Description
	if len([]rune(desc)) > 80 {
		desc = string([]rune(desc)[:80]) + "…"
	}
	msg := fmt.Sprintf("✅ ARTEX 任务完成\n任务ID: %s\n描述: %s\n状态: %s", taskID, desc, t.Status)
	msg += "\n执行时长: " + taskRunDuration(t)
	msg += "\n" + findingSummaryLine(t)
	if base := s.taskURLBase(); base != "" {
		url := fmt.Sprintf("%s/function/tasks/detail?id=%s", strings.TrimRight(base, "/"), taskID)
		msg += fmt.Sprintf("\n链接: %s", url)
	}
	if !popoConfigured() {
		return // best-effort 通知：未配置 POPO 时静默跳过
	}
	robot := popoRobotSingleton()
	if err := robot.SendMessage(popoConfig.notifyTo, msg); err != nil {
		log.Printf("[notify] task %s: POPO 发送失败: %v", taskID, err)
	}
}

// taskRunDuration renders the task's run duration (created → terminal state,
// mirroring the web task list) as a compact human string like "2h 15m".
func taskRunDuration(t *Task) string {
	// 定时任务:从 ScheduledStartAt(开始时间)算,而非 CreatedAt(创建时间);
	// 非定时任务保持从 CreatedAt 起。
	start := t.CreatedAt
	if t.ScheduledStartAt > 0 {
		start = t.ScheduledStartAt
	}
	if start <= 0 {
		return "—"
	}
	end := t.CompletedAt
	if end <= 0 {
		end = time.Now().Unix()
	}
	sec := end - start
	if sec <= 0 {
		return "—"
	}
	d, h := sec/86400, (sec%86400)/3600
	m, s := (sec%3600)/60, sec%60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd %dh", d, h)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// findingSummaryLine counts the task's confirmed findings by severity and
// renders the overview line, e.g. "确认漏洞: 5（高 2 / 中 2 / 低 1）".
func findingSummaryLine(t *Task) string {
	if t.Store == nil {
		return "确认漏洞: 0"
	}
	findings, _ := t.Store.ListByKind(db.KindFinding, 1000)
	var high, medium, low, other int
	for _, n := range findings {
		var p struct {
			Severity string `json:"severity"`
		}
		_ = json.Unmarshal(n.Payload, &p)
		switch strings.ToLower(strings.TrimSpace(p.Severity)) {
		case "high":
			high++
		case "medium":
			medium++
		case "low":
			low++
		default:
			other++
		}
	}
	parts := fmt.Sprintf("高 %d / 中 %d / 低 %d", high, medium, low)
	if other > 0 {
		parts += fmt.Sprintf(" / 其他 %d", other)
	}
	return fmt.Sprintf("确认漏洞: %d（%s）", len(findings), parts)
}

// taskURLBase returns the base URL for task detail links. It reads from the DB
// settings table first, then falls back to the ARTEX_TASK_URL env var.
func (s *Server) taskURLBase() string {
	if v, ok, _ := s.m.pg.GetSetting(settingTaskURL); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return os.Getenv("ARTEX_TASK_URL")
}
