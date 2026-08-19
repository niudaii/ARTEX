package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
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

// TestNodeDetailCrossTaskFallback verifies the task-less path: a ToolSet with no
// ExplorationStore but a server-level PG handle (the buildDomainReg shape)
// resolves node_detail by the node's globally unique id — the 需要任务上下文
// error only remains when neither store nor DB handle is wired (covered above).
func TestNodeDetailCrossTaskFallback(t *testing.T) {
	d := testDB(t)
	defer d.Close()

	expID, err := d.CreateExploration("node_detail fallback", "goal")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM explorations WHERE id=$1`, expID)
	nodeID, err := d.Exploration(expID).AddNode(db.KindFact, map[string]any{"summary": "cross-task fact", "detail": "global read"}, 5, "confirmed", "test", nil)
	if err != nil {
		t.Fatal(err)
	}

	ts := NewToolSet(nil, "")
	ts.SetExplorationDB(d)
	res, err := ts.nodeDetail().Call(context.Background(), []byte(fmt.Sprintf(`{"id": %d}`, nodeID)), nil)
	if err != nil {
		t.Fatalf("nodeDetail Call error: %v", err)
	}
	if res.IsError || !strings.Contains(res.Flatten(), "cross-task fact") {
		t.Errorf("node_detail via expDB = %q (IsError=%v), want the node payload", res.Flatten(), res.IsError)
	}
}
