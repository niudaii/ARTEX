package db

import "testing"

func testDSN(t *testing.T) string {
	t.Helper()
	dsn, _, err := DSN()
	if err != nil {
		t.Skipf("no database config (%v) — skipping", err)
	}
	return dsn
}

func TestActivityPageReturnsAscendingWindowAndHasMore(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	expID, err := d.CreateExploration("test", "activity pagination")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM explorations WHERE id=$1`, expID)
	store := d.Exploration(expID)

	var mainIDs []int64
	appendActivity := func(activity Activity) int64 {
		id, err := store.AppendActivity(activity)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	for range 3 {
		mainIDs = append(mainIDs, appendActivity(Activity{Worker: "mainagent", Kind: "text", Summary: "main"}))
	}
	appendActivity(Activity{Worker: "planner", Kind: "text", Summary: "planner"})

	first, hasMore, err := store.ActivityPage(ActivitySessionFilter{Worker: "mainagent"}, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || !hasMore {
		t.Fatalf("first page = %d items, hasMore = %v; want 2 items, hasMore = true", len(first), hasMore)
	}
	if first[0].ID != mainIDs[1] || first[1].ID != mainIDs[2] {
		t.Fatalf("first page IDs = [%d, %d], want [%d, %d]", first[0].ID, first[1].ID, mainIDs[1], mainIDs[2])
	}

	second, hasMore, err := store.ActivityPage(ActivitySessionFilter{Worker: "mainagent"}, first[0].ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || hasMore {
		t.Fatalf("second page = %d items, hasMore = %v; want 1 item, hasMore = false", len(second), hasMore)
	}
	if second[0].ID != mainIDs[0] {
		t.Fatalf("second page ID = %d, want %d", second[0].ID, mainIDs[0])
	}
}

func TestActivityPageForTerminalIntentFiltersStateAndKind(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	expID, err := d.CreateExploration("test", "terminal activity pagination")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM explorations WHERE id=$1`, expID)
	store := d.Exploration(expID)

	intentID, err := store.AddIntent(map[string]any{"summary": "intent"}, 5, nil, "planner")
	if err != nil {
		t.Fatal(err)
	}
	var textIDs []int64
	appendActivity := func(kind string) int64 {
		id, err := store.AppendActivity(Activity{NodeID: &intentID, Worker: "worker", Kind: kind, Summary: kind})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	textIDs = append(textIDs, appendActivity("text"))
	appendActivity("thinking")
	textIDs = append(textIDs, appendActivity("text"))
	appendActivity("usage")
	textIDs = append(textIDs, appendActivity("text"))

	open, _, err := store.ActivityPageForTerminalIntent(intentID, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Fatalf("open intent returned %d activities, want 0", len(open))
	}

	if err := store.SetIntentState(intentID, "done"); err != nil {
		t.Fatal(err)
	}
	first, hasMore, err := store.ActivityPageForTerminalIntent(intentID, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || !hasMore {
		t.Fatalf("first page = %d items, hasMore = %v; want 2 items, hasMore = true", len(first), hasMore)
	}
	if first[0].ID != textIDs[1] || first[1].ID != textIDs[2] {
		t.Fatalf("first page IDs = [%d, %d], want [%d, %d]", first[0].ID, first[1].ID, textIDs[1], textIDs[2])
	}

	second, hasMore, err := store.ActivityPageForTerminalIntent(intentID, first[0].ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || hasMore {
		t.Fatalf("second page = %d items, hasMore = %v; want 1 item, hasMore = false", len(second), hasMore)
	}
	if second[0].ID != textIDs[0] {
		t.Fatalf("second page ID = %d, want %d", second[0].ID, textIDs[0])
	}
}
