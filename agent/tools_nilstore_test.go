package agent

import (
	"context"
	"strings"
	"testing"
)

// TestNilStoreDomainToolsDoNotPanic is the regression test for the process-wide
// crash where a task-independent agent (chat page / Auto conversation) had an
// exploration-graph tool injected from the server-level registry (built with a
// nil ExplorationStore, see buildDomainReg) and calling it nil-deref'd →
// SIGSEGV took down the whole server (node_detail → GetNode on nil receiver).
// Every domain tool must degrade to a tool error on a store-less ToolSet.
func TestNilStoreDomainToolsDoNotPanic(t *testing.T) {
	ts := NewToolSet(nil, "")
	for _, tool := range ts.AllDomainTools() {
		res, err := tool.Call(context.Background(), []byte(`{}`), nil)
		if err != nil {
			t.Errorf("%s: unexpected call error: %v", tool.Name(), err)
			continue
		}
		if !res.IsError {
			t.Errorf("%s: expected a tool error without any store, got success: %s", tool.Name(), res.Flatten())
		}
	}
}

// TestNilStoreGraphToolMessage checks the degraded error is actionable: it tells
// the model the tool needs a task context, not a bare failure.
func TestNilStoreGraphToolMessage(t *testing.T) {
	ts := NewToolSet(nil, "")
	res, err := ts.nodeDetail().Call(context.Background(), []byte(`{"id": 1}`), nil)
	if err != nil {
		t.Fatalf("unexpected call error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Flatten(), "需要任务上下文") {
		t.Errorf("node_detail on nil store = %q (IsError=%v), want 需要任务上下文 error", res.Flatten(), res.IsError)
	}
}
