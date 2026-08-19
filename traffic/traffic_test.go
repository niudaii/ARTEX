package traffic

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDeleteHost verifies the delete contract: rows for hosts containing the
// substring are removed together with their file trees, non-matching hosts are
// untouched, and the count is right.
func TestDeleteHost(t *testing.T) {
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	// Seed two hosts' index rows + trees directly (record() needs a live Flow).
	for i, h := range []string{"a.example.com", "b.example.com"} {
		id := fmt.Sprintf("1-%04d", i+1)
		exDir := filepath.Join(dir, h, "GET", id)
		if err := os.MkdirAll(exDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(exDir, "meta.json"), []byte(fmt.Sprintf(`{"id":%q,"host":%q}`, id, h)), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := tr.DB().Exec(`INSERT INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			id, i+1, h, "GET", "/", "http://"+h+"/", 200, "text/html", 0, 0, h+"/GET/"+id); err != nil {
			t.Fatal(err)
		}
	}

	// Substring: "a.example" matches a.example.com only, leaves b.example.com.
	n, err := tr.DeleteHost("a.example")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("deleted=%d, want 1", n)
	}
	// Tree removed for the target, intact for the other host.
	if _, err := os.Stat(filepath.Join(dir, "a.example.com")); !os.IsNotExist(err) {
		t.Fatalf("a.example.com tree still exists (stat err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.example.com")); err != nil {
		t.Fatalf("b.example.com tree removed: %v", err)
	}
	// Index reduced to the other host's single row.
	var c int
	if err := tr.DB().QueryRow(`SELECT COUNT(*) FROM exchanges`).Scan(&c); err != nil {
		t.Fatal(err)
	}
	if c != 1 {
		t.Fatalf("rows=%d, want 1", c)
	}
	// A substring matching nothing is a no-op, not an error.
	n, err = tr.DeleteHost("nope.example")
	if err != nil || n != 0 {
		t.Fatalf("DeleteHost(missing)=%d, err=%v; want 0, nil", n, err)
	}
	// A broader substring sweeps the remaining host too.
	if n, err = tr.DeleteHost("example.com"); err != nil || n != 1 {
		t.Fatalf("DeleteHost(example.com)=%d, err=%v; want 1, nil", n, err)
	}
	c = 0
	if err := tr.DB().QueryRow(`SELECT COUNT(*) FROM exchanges`).Scan(&c); err == nil && c != 0 {
		t.Fatalf("rows=%d, want 0 after full sweep", c)
	}
}

// TestHosts verifies the target picker contract: distinct hosts with counts,
// most recent activity first.
func TestHosts(t *testing.T) {
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	for i, row := range []struct {
		host string
		ts   int64
	}{{"old.example.com", 1}, {"new.example.com", 3}, {"old.example.com", 2}} {
		id := fmt.Sprintf("1-%04d", i+1)
		if _, err := tr.DB().Exec(`INSERT INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			id, row.ts, row.host, "GET", "/", "http://"+row.host+"/", 200, "text/html", 0, 0, row.host+"/GET/"+id); err != nil {
			t.Fatal(err)
		}
	}
	hosts, err := tr.Hosts()
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 2 {
		t.Fatalf("hosts=%d, want 2", len(hosts))
	}
	// newest activity (ts=3) first
	if hosts[0].Host != "new.example.com" || hosts[0].Count != 1 {
		t.Fatalf("hosts[0]=%+v, want new.example.com/1", hosts[0])
	}
	if hosts[1].Host != "old.example.com" || hosts[1].Count != 2 {
		t.Fatalf("hosts[1]=%+v, want old.example.com/2", hosts[1])
	}
}

// TestDeleteHostsExact verifies the batch delete: exact host match only — a
// host whose name contains another as a substring is untouched — duplicates in
// the batch are harmless, and the per-host trees are removed.
func TestDeleteHostsExact(t *testing.T) {
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	seed := func(id, h string) {
		exDir := filepath.Join(dir, h, "GET", id)
		if err := os.MkdirAll(exDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := tr.DB().Exec(`INSERT INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			id, 1, h, "GET", "/", "http://"+h+"/", 200, "text/html", 0, 0, h+"/GET/"+id); err != nil {
			t.Fatal(err)
		}
	}
	// "api.example.com" is a substring of "api.example.com.cn".
	seed("1-0001", "api.example.com")
	seed("1-0002", "api.example.com.cn")
	seed("1-0003", "shop.example.com")

	// Duplicate entry in the batch must not double-delete or error.
	n, err := tr.DeleteHostsExact([]string{"api.example.com", "api.example.com", "shop.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("deleted=%d, want 2", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "api.example.com")); !os.IsNotExist(err) {
		t.Fatalf("api.example.com tree still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "shop.example.com")); !os.IsNotExist(err) {
		t.Fatalf("shop.example.com tree still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "api.example.com.cn")); err != nil {
		t.Fatalf("api.example.com.cn removed by an exact delete that shouldn't match: %v", err)
	}
	var c int
	if err := tr.DB().QueryRow(`SELECT COUNT(*) FROM exchanges`).Scan(&c); err != nil {
		t.Fatal(err)
	}
	if c != 1 {
		t.Fatalf("rows=%d, want 1 (api.example.com.cn only)", c)
	}
}

// TestDeleteHostGCBlobs verifies blob garbage collection: after a host's trees
// are removed, blobs referenced by no remaining exchange are deleted, while
// blobs still referenced (including shared ones) survive.
func TestDeleteHostGCBlobs(t *testing.T) {
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	// Two distinct blobs + one shared blob (referenced by two hosts).
	blobA := filepath.Join(dir, "_blobs", "sha256", "aa", "aa", strings.Repeat("a", 64)+".bin")
	blobB := filepath.Join(dir, "_blobs", "sha256", "bb", "bb", strings.Repeat("b", 64)+".bin")
	blobC := filepath.Join(dir, "_blobs", "sha256", "cc", "cc", strings.Repeat("c", 64)+".bin")
	for _, b := range []string{blobA, blobB, blobC} {
		if err := os.MkdirAll(filepath.Dir(b), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(b, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seed := func(id, h, ref string) {
		exDir := filepath.Join(dir, h, "GET", id)
		if err := os.MkdirAll(exDir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "no blob"
		if ref != "" {
			body = "@blob sha256:" + ref + " (len=1)"
		}
		if err := os.WriteFile(filepath.Join(exDir, "request.http"), []byte("GET / HTTP/1.1\n\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(exDir, "response.http"), []byte("HTTP 200 OK\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := tr.DB().Exec(`INSERT INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			id, 1, h, "GET", "/", "http://"+h+"/", 200, "text/html", 0, 0, h+"/GET/"+id); err != nil {
			t.Fatal(err)
		}
	}
	ha := strings.Repeat("a", 64)
	hb := strings.Repeat("b", 64)
	hc := strings.Repeat("c", 64)
	seed("1-0001", "a.example.com", ha) // sole reference to blobA
	seed("1-0002", "b.example.com", hb) // sole reference to blobB
	seed("1-0003", "c.example.com", hc) // shares blobC with d
	seed("1-0004", "d.example.com", hc)

	// Delete a: blobA orphaned → removed; blobB/blobC still referenced → kept.
	if n, err := tr.DeleteHost("a.example"); err != nil || n != 1 {
		t.Fatalf("DeleteHost(a.example)=%d, err=%v; want 1, nil", n, err)
	}
	if _, err := os.Stat(blobA); !os.IsNotExist(err) {
		t.Fatalf("orphaned blobA still exists: %v", err)
	}
	if _, err := os.Stat(blobB); err != nil {
		t.Fatalf("referenced blobB removed: %v", err)
	}
	if _, err := os.Stat(blobC); err != nil {
		t.Fatalf("shared blobC removed while d still references it: %v", err)
	}

	// Delete c (shares blobC with d): blobC must survive.
	if n, err := tr.DeleteHost("c.example"); err != nil || n != 1 {
		t.Fatalf("DeleteHost(c.example)=%d, err=%v; want 1, nil", n, err)
	}
	if _, err := os.Stat(blobC); err != nil {
		t.Fatalf("shared blobC removed after deleting one sharer: %v", err)
	}

	// Delete d: last reference gone → blobC collected.
	if n, err := tr.DeleteHost("d.example"); err != nil || n != 1 {
		t.Fatalf("DeleteHost(d.example)=%d, err=%v; want 1, nil", n, err)
	}
	if _, err := os.Stat(blobC); !os.IsNotExist(err) {
		t.Fatalf("blobC still exists after last reference removed: %v", err)
	}
}

// TestPageCounts verifies the pagination contract on the host-summary fast
// path: unfiltered and host-filtered totals are exact (from host_stats, kept
// in sync by the ex_ins/ex_del triggers), an unknown host filter yields an
// empty page, and deletes update the summary.
func TestPageCounts(t *testing.T) {
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	seed := func(id, h string, ts int64) {
		if _, err := tr.DB().Exec(`INSERT INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			id, ts, h, "GET", "/", "http://"+h+"/", 200, "text/html", 0, 0, h+"/GET/"+id); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		seed(fmt.Sprintf("1-%04d", i+1), "a.example.com", int64(i+1))
	}
	seed("1-0010", "b.example.com", 10)
	seed("1-0011", "b.example.com", 11)

	// Unfiltered: all 5 rows, newest first, uncapped.
	rows, total, capped, err := tr.Page("", "", "", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || capped || len(rows) != 5 {
		t.Fatalf("unfiltered: total=%d capped=%v rows=%d; want 5/false/5", total, capped, len(rows))
	}
	if rows[0].Host != "b.example.com" {
		t.Fatalf("rows[0]=%s, want newest host b.example.com", rows[0].Host)
	}

	// Host substring resolves through the summary to exact hosts.
	rows, total, capped, err = tr.Page("a.example", "", "", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || capped || len(rows) != 3 {
		t.Fatalf("host filter: total=%d capped=%v rows=%d; want 3/false/3", total, capped, len(rows))
	}
	for _, r := range rows {
		if r.Host != "a.example.com" {
			t.Fatalf("row host=%s leaked past the host filter", r.Host)
		}
	}

	// Unknown host: empty page, zero total, no error.
	rows, total, _, err = tr.Page("nope", "", "", 0, 10)
	if err != nil || total != 0 || len(rows) != 0 {
		t.Fatalf("unknown host: total=%d rows=%d err=%v; want 0/0/nil", total, len(rows), err)
	}

	// Global count agrees with the summary.
	if c, err := tr.Count(); err != nil || c != 5 {
		t.Fatalf("Count()=%d, err=%v; want 5", c, err)
	}

	// Deletes keep the summary in sync.
	if _, err := tr.DeleteHost("a.example"); err != nil {
		t.Fatal(err)
	}
	if c, err := tr.Count(); err != nil || c != 2 {
		t.Fatalf("Count() after delete=%d, err=%v; want 2", c, err)
	}
	hosts, err := tr.Hosts()
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0].Host != "b.example.com" || hosts[0].Count != 2 {
		t.Fatalf("Hosts() after delete=%+v; want [b.example.com/2]", hosts)
	}
}

// TestQueryStatusMin verifies the statusMin filter in query(): only exchanges
// whose status >= statusMin are returned. Also checks that contains now
// matches content_type, and that Get surfaces a missing-file error instead of
// returning empty strings silently.
func TestQueryStatusMin(t *testing.T) {
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	type seedRow struct {
		id, host, url, ct string
		status            int
	}
	seeds := []seedRow{
		{"1-0001", "t.example.com", "http://t.example.com/", "text/html", 200},
		{"1-0002", "t.example.com", "http://t.example.com/api/login", "application/json", 401},
		{"1-0003", "t.example.com", "http://t.example.com/api/crash", "application/json", 500},
		{"1-0004", "t.example.com", "http://t.example.com/api/gateway", "text/html", 502},
		{"1-0005", "t.example.com", "http://t.example.com/api/svc", "text/html", 503},
	}
	for i, s := range seeds {
		if _, err := tr.DB().Exec(`INSERT INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			s.id, i+1, s.host, "GET", "/", s.url, s.status, s.ct, 0, 0,
			s.host+"/GET/"+s.id); err != nil {
			t.Fatal(err)
		}
	}

	// statusMin=500 → only 3 rows (500, 502, 503).
	rows, err := tr.query("t.example.com", "", 500, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("statusMin=500: got %d rows, want 3", len(rows))
	}
	for _, r := range rows {
		if r.Status < 500 {
			t.Fatalf("status=%d leaked past statusMin=500", r.Status)
		}
	}

	// statusMin=400 → 4 rows (401, 500, 502, 503).
	rows, err = tr.query("t.example.com", "", 400, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("statusMin=400: got %d rows, want 4", len(rows))
	}

	// statusMin=0 → all 5 rows (filter disabled).
	rows, err = tr.query("t.example.com", "", 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("statusMin=0: got %d rows, want 5", len(rows))
	}

	// contains now also matches content_type: "json" → 2 rows (401, 500).
	rows, err = tr.query("t.example.com", "json", 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("contains=json: got %d rows, want 2", len(rows))
	}

	// Get on a row whose files were never written → error, not silent empty.
	_, _, err = tr.Get("1-0003")
	if err == nil {
		t.Fatal("Get on missing files: got nil error, want non-nil")
	}
}

// TestGetMissingFile ensures Get returns an error when the response file is
// absent (e.g. deleted on disk), so the agent doesn't mistake it for empty.
func TestGetMissingFile(t *testing.T) {
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	// Seed a row pointing at a directory we never create.
	if _, err := tr.DB().Exec(`INSERT INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		"1-0001", 1, "t.example.com", "GET", "/", "http://t.example.com/", 200, "text/html", 0, 0,
		"t.example.com/GET/1-0001"); err != nil {
		t.Fatal(err)
	}

	_, _, err = tr.Get("1-0001")
	if err == nil {
		t.Fatal("Get on non-existent file: expected error, got nil")
	}
}
