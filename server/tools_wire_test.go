package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

func names(tools []actool.CoreTool) map[string]actool.CoreTool {
	m := map[string]actool.CoreTool{}
	for _, t := range tools {
		m[t.Name()] = t
	}
	return m
}

// TestWireTools verifies end-to-end against the live dev PG: wireTools seeds the
// catalog, ToolResolve keeps record_fact for worker but drops it for planner (not
// bound), and an edited description + injected default flow through.
func TestWireTools(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skipf("no database config (%v) — skipping", err)
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer pg.Close()

	wireTools(pg, nil) // nil domainReg: test only covers filter/decoration, not injection
	t.Cleanup(func() { agent.ToolResolve = nil })
	// Prevent interactive-shell Bash decoration: if the worker agent has
	// interactive_shell=true in the DB, ToolResolve would append a note to
	// Bash's description. Disable that for this test so the undecorated
	// pass-through assertion holds regardless of DB state.
	t.Setenv("AGENT_CORE_DISABLE_INTERACTIVE_SHELL", "1")

	// Seeding populated the catalog.
	rf, err := pg.GetTool("record_fact")
	if err != nil || rf == nil {
		t.Fatalf("record_fact not seeded: %v", err)
	}

	// Build a base as the worker does: its domain tools + defaults, resolved for
	// "worker" then for "planner".
	ts := agent.NewToolSet(nil, "")
	base := append(ts.WorkerTools(), actool.DefaultTools()...)
	ctx := context.Background()

	worker := names(agent.ToolResolve(ctx, "worker", base))
	if _, ok := worker["record_fact"]; !ok {
		t.Error("worker lost record_fact")
	}
	// SDK generic tools aren't seeded → ToolResolve passes them through unchanged
	// (same object, original description — not decorated). No startup prune touches
	// non-catalog rows, so future user-defined custom tools survive too.
	if bash, ok := worker["Bash"]; !ok {
		t.Error("worker lost Bash (should pass through)")
	} else if bash.Description() != actool.NewBash().Description() {
		t.Error("Bash should pass through undecorated, but description changed")
	}
	// planner is not bound to record_fact → resolving a base that contains it drops it.
	planner := names(agent.ToolResolve(ctx, "planner", base))
	if _, ok := planner["record_fact"]; ok {
		t.Error("planner should not get record_fact (not bound)")
	}

	// Edit description + add a default to a scalar param, then confirm the resolved
	// worker tool reflects both. Restore from code default on cleanup.
	t.Cleanup(func() { _ = pg.UpsertToolForce(rf.Key, rf.Description, rf.Schema, mustJSON(rf.Agents)) })
	var schema map[string]any
	_ = json.Unmarshal(rf.Schema, &schema)
	if props, ok := schema["properties"].(map[string]any); ok {
		if conf, ok := props["confidence"].(map[string]any); ok {
			conf["default"] = "inferred"
		}
	}
	edited := mustJSON(schema)
	if err := pg.UpdateTool(rf.Key, "EDITED DESC", edited, mustJSON([]string{"worker"}), true); err != nil {
		t.Fatal(err)
	}

	worker2 := names(agent.ToolResolve(ctx, "worker", base))
	got := worker2["record_fact"]
	if got == nil {
		t.Fatal("record_fact missing after edit")
	}
	if got.Description() != "EDITED DESC" {
		t.Errorf("description = %q, want EDITED DESC", got.Description())
	}
	// Default injection: omit confidence → handler input should carry the default.
	// We can't run the real handler (nil store), but InputSchema must show the default.
	sc := got.InputSchema()
	props := sc["properties"].(map[string]any)
	conf := props["confidence"].(map[string]any)
	if conf["default"] != "inferred" {
		t.Errorf("confidence.default = %v, want inferred", conf["default"])
	}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// TestBuildDomainRegOmitsTaskGraphTools locks in the fix for the
// "list_findings 需要任务上下文" / "report_finding 需要任务上下文" errors on
// remote: the task-independent (nil-store) registry must NOT expose per-task
// exploration-graph tools that can only errNoTaskGraph there — reads
// (list_findings / list_facts / graph_overview) AND report_finding (whose write
// path has no conversation fallback). Exposing them only makes the model waste a
// turn calling a tool that cannot succeed. Their cross-task host equivalents
// (list_task_findings / get_task_graph) are bound separately. node_detail stays
// (PG by-id fallback); list_assets stays (AssetStore-backed). Task agents are
// unaffected — they build a real-store ToolSet, not this registry.
func TestBuildDomainRegOmitsTaskGraphTools(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skipf("no database config (%v) — skipping", err)
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer pg.Close()

	reg := buildDomainReg(pg, pg.Assets())
	if reg == nil {
		t.Skip("no AssetStore → buildDomainReg returned nil (no injection)")
	}
	for _, name := range []string{"list_findings", "list_facts", "graph_overview", "report_finding"} {
		if _, ok := reg[name]; ok {
			t.Errorf("task-independent registry must not expose %q (can only error without a per-task store)", name)
		}
	}
	for _, name := range []string{"node_detail", "list_assets"} {
		if _, ok := reg[name]; !ok {
			t.Errorf("task-independent registry lost %q (has a cross-task fallback and must stay available)", name)
		}
	}
}
