package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
)

// TestWrToolRecoversPanic: 编排工具在 SDK 的独立 goroutine 里执行,未捕获的 panic
// 会打死整个进程(所有在跑任务陪葬,调用方表现为永久阻塞)。wrTool 必须把 panic
// 兜底成工具错误结果。
func TestWrToolRecoversPanic(t *testing.T) {
	tool := wrTool("boom", "test tool", map[string]any{"type": "object"},
		func(context.Context, json.RawMessage) (actool.Result, error) {
			panic("kaboom")
		})
	res, err := tool.Call(context.Background(), []byte(`{}`), nil)
	if err != nil {
		t.Fatalf("panic must be converted to a result, got err=%v", err)
	}
	if !res.IsError || !strings.Contains(fmt.Sprintf("%v", res.Content), "kaboom") {
		t.Fatalf("expected error result carrying the panic, got %+v", res)
	}
}

// TestSpawnTaskNilProfileNoPanic 回归线上事故:spawn_task 不带 llm_profile_id 且
// 无父任务 pin 时,旧代码对 nil *pin 解引用 → SIGSEGV 打死进程(chat 会话卡死)。
// 修复后该输入必须得到 graceful 结果而非 panic。
func TestSpawnTaskNilProfileNoPanic(t *testing.T) {
	tool := (&Server{}).toolSpawnTask()
	in, _ := json.Marshal(map[string]any{
		"description":       "回归测试",
		"goal":              "无 profile/parent 的 spawn_task 不应 panic",
		"seed_first_intent": true,
		"timeout_seconds":   3600,
	})
	res, err := tool.Call(context.Background(), in, nil)
	if err != nil {
		t.Fatalf("spawn_task must not surface a transport error, got %v", err)
	}
	// 裸 Server 无 Manager,建任务必然失败——但必须是 graceful 的工具错误。
	if !res.IsError {
		t.Fatalf("bare server cannot create tasks; expected error result, got %+v", res)
	}
}
