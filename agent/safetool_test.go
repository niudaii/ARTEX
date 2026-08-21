package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
)

type panickingTool struct {
	actool.CoreTool
}

func (panickingTool) Name() string { return "boom_tool" }
func (panickingTool) Call(context.Context, json.RawMessage, *actool.ToolContext) (actool.Result, error) {
	panic("tool exploded")
}

// TestSafeToolRecovers: 工具 panic 必须被兜底为错误结果,而不是打死进程
// (线上真实事故:spawn_task nil 解引用 → 整个 ARTEX 进程崩溃重启)。
func TestSafeToolRecovers(t *testing.T) {
	wrapped := wrapSafe([]actool.CoreTool{panickingTool{}})
	if len(wrapped) != 1 {
		t.Fatalf("wrapSafe length = %d, want 1", len(wrapped))
	}
	if wrapped[0].Name() != "boom_tool" {
		t.Fatalf("wrapper must delegate Name(), got %s", wrapped[0].Name())
	}
	res, err := wrapped[0].Call(context.Background(), []byte(`{}`), nil)
	if err != nil {
		t.Fatalf("panic must become a result, got err=%v", err)
	}
	if !res.IsError || !strings.Contains(fmt.Sprintf("%v", res.Content), "tool exploded") {
		t.Fatalf("expected error result carrying the panic, got %+v", res)
	}
}

// TestAugmentToolsWrapsAllTools: ToolAugment/ToolResolve 均未接线时,base 工具
// 也必须全部被 safeTool 包裹。
func TestAugmentToolsWrapsAllTools(t *testing.T) {
	oldAugment, oldResolve := ToolAugment, ToolResolve
	ToolAugment, ToolResolve = nil, nil
	defer func() { ToolAugment, ToolResolve = oldAugment, oldResolve }()

	tools, _, cleanup := AugmentTools(context.Background(), "worker", []actool.CoreTool{panickingTool{}})
	defer cleanup()
	if len(tools) != 1 {
		t.Fatalf("AugmentTools length = %d, want 1", len(tools))
	}
	if _, ok := tools[0].(safeTool); !ok {
		t.Fatalf("tool not wrapped by safeTool: %T", tools[0])
	}
	res, err := tools[0].Call(context.Background(), []byte(`{}`), nil)
	if err != nil || !res.IsError {
		t.Fatalf("expected graceful error result, got res=%+v err=%v", res, err)
	}
}
