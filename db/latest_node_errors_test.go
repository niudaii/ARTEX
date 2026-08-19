package db

import "testing"

func TestLatestNodeErrors(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	expID, err := d.CreateExploration("test", "latest-node-errors")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM explorations WHERE id=$1`, expID) // cascades nodes/activity
	es := d.Exploration(expID)

	i1, err := es.AddIntent(map[string]any{"summary": "intent one"}, 5, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	i2, err := es.AddIntent(map[string]any{"summary": "intent two"}, 5, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}

	mk := func(nodeID int64, kind string, isErr bool, summary, detail string) {
		t.Helper()
		if _, err := es.AppendActivity(Activity{
			NodeID: &nodeID, Kind: kind, IsError: isErr, Summary: summary, Detail: detail,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// i1: an early error superseded by a later one; only the latest must survive.
	mk(i1, "result", true, "执行出错: first failure", "first failure")
	mk(i1, "result", false, "（正常总结）", "ok") // non-error must be ignored
	mk(i1, "result", true, "执行出错: second failure", "second failure")
	// i2: error row with empty detail → falls back to summary.
	mk(i2, "result", true, "执行出错: summary only", "")

	out, err := es.LatestNodeErrors([]int64{i1, i2})
	if err != nil {
		t.Fatal(err)
	}
	if got := out[i1]; got != "second failure" {
		t.Errorf("i1: want latest error %q, got %q", "second failure", got)
	}
	if got := out[i2]; got != "执行出错: summary only" {
		t.Errorf("i2: want summary fallback, got %q", got)
	}

	// empty input short-circuits without error.
	if empty, err := es.LatestNodeErrors(nil); err != nil || len(empty) != 0 {
		t.Errorf("nil input: want empty map + no error, got %v / %v", empty, err)
	}
	// a node without any error activity stays absent from the map.
	i3, err := es.AddIntent(map[string]any{"summary": "intent three"}, 5, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	mk(i3, "result", false, "（正常总结）", "ok")
	out2, err := es.LatestNodeErrors([]int64{i3})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out2[i3]; ok {
		t.Errorf("i3 has no error activity, want absent, got %q", out2[i3])
	}
}
