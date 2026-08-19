package db

import "testing"

func TestActivityLatest(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	expID, err := d.CreateExploration("test", "latest window")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM explorations WHERE id=$1`, expID) // cascades nodes/edges/activity
	es := d.Exploration(expID)

	var ids []int64
	for i := 0; i < 12; i++ {
		id, err := es.AppendActivity(Activity{Worker: "worker-1", Kind: "text", Summary: "step"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}

	items, cursor, err := es.ActivityLatest(nil, 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 {
		t.Fatalf("want 5 items, got %d", len(items))
	}
	if cursor != ids[11] {
		t.Fatalf("cursor want %d, got %d", ids[11], cursor)
	}
	for i, a := range items { // oldest-first within the latest window
		if a.ID != ids[7+i] {
			t.Fatalf("item %d: want id %d, got %d", i, ids[7+i], a.ID)
		}
	}

	all, _, err := es.ActivityLatest(nil, 100, "")
	if err != nil || len(all) != 12 {
		t.Fatalf("all: items=%d err=%v", len(all), err)
	}
}
