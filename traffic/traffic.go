// Package traffic implements the request-recording subsystem (docs §10): an
// embedded go-mitmproxy proxy whose addon writes every target HTTP exchange into
// a human-browsable file tree, with a sidecar SQLite index for paged queries.
// Full capture, plaintext (no redaction), target HTTP only.
package traffic

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	mproxy "github.com/lqqyt2423/go-mitmproxy/proxy"
	"github.com/sirupsen/logrus"
	_ "modernc.org/sqlite"
)

// mitmNoiseFormatter wraps the logrus standard logger's formatter (go-mitmproxy
// logs through it) to drop the recurring "tls: unknown certificate" errors from
// Proxy.attacker.httpsTlsDial. These are expected when scanning self-signed HTTPS
// targets whose MITM handshake the connecting tool rejects — the request still
// fails-open (target unreachable or passthrough), so the log line is pure noise.
type mitmNoiseFormatter struct{ inner logrus.Formatter }

func (f *mitmNoiseFormatter) Format(e *logrus.Entry) ([]byte, error) {
	if e.Data["in"] == "Proxy.attacker.httpsTlsDial" {
		m := strings.ToLower(e.Message)
		if strings.Contains(m, "unknown certificate") || strings.Contains(m, "tls:") {
			return nil, nil // suppress — see comment above
		}
	}
	return f.inner.Format(e)
}

func init() {
	logrus.SetFormatter(&mitmNoiseFormatter{inner: logrus.StandardLogger().Formatter})
}

// indexSchema is applied statement-by-statement in Open (triggers contain
// semicolons, so the whole block can't go through one Exec safely).
//
// host_stats is a per-host summary (row count + last activity) maintained by
// the ex_ins/ex_del triggers, so the UI's count/hosts/host-filter queries read
// a few thousand rows instead of scanning millions of exchanges.
var indexSchema = []string{
	`CREATE TABLE IF NOT EXISTS exchanges (
  id           TEXT PRIMARY KEY,
  ts           INTEGER,
  host         TEXT,
  method       TEXT,
  url_template TEXT,
  url          TEXT,
  status       INTEGER,
  content_type TEXT,
  req_len      INTEGER,
  resp_len     INTEGER,
  path         TEXT
)`,
	`CREATE INDEX IF NOT EXISTS idx_ex_ts      ON exchanges(ts)`,
	`CREATE INDEX IF NOT EXISTS idx_ex_host_ts ON exchanges(host, ts)`,
	// superseded: idx_ex_host is covered by idx_ex_host_ts; idx_ex_tmpl has no
	// query that could use it (url_template is only matched with LIKE '%...%').
	`DROP INDEX IF EXISTS idx_ex_host`,
	`DROP INDEX IF EXISTS idx_ex_tmpl`,
	`CREATE TABLE IF NOT EXISTS host_stats (
  host    TEXT PRIMARY KEY,
  n       INTEGER NOT NULL DEFAULT 0,
  last_ts INTEGER NOT NULL DEFAULT 0
)`,
	`CREATE TRIGGER IF NOT EXISTS ex_ins AFTER INSERT ON exchanges BEGIN
  INSERT INTO host_stats(host, n, last_ts) VALUES (new.host, 1, new.ts)
  ON CONFLICT(host) DO UPDATE SET n = n + 1, last_ts = MAX(last_ts, excluded.last_ts);
END`,
	`CREATE TRIGGER IF NOT EXISTS ex_del AFTER DELETE ON exchanges BEGIN
  UPDATE host_stats SET n = n - 1 WHERE host = old.host;
  DELETE FROM host_stats WHERE host = old.host AND n <= 0;
END`,
}

const maxInlineBody = 256 * 1024

// Traffic runs the recording proxy and owns the file tree + index.
type Traffic struct {
	dir   string
	addr  string
	db    *sql.DB
	wmu   sync.Mutex // serializes record() vs DeleteHost (incl. blob GC)
	seq   atomic.Int64
	proxy *mproxy.Proxy
	// pass is the set of hosts whose MITM interception failed for a proxy/protocol
	// reason; connections to them are tunneled transparently (fail-open) so the
	// request still reaches the target — unrecorded — instead of being killed.
	pass sync.Map // hostname(string) -> struct{}
}

// Open initializes the traffic tree, blob store and SQLite index under dir.
func Open(dir, addr string) (*Traffic, error) {
	for _, d := range []string{dir, filepath.Join(dir, "_index"), filepath.Join(dir, "_blobs"), filepath.Join(dir, "_ca")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	dbPath := filepath.Join(dir, "_index", "index.sqlite")
	// Stat BEFORE opening: sql.Open + the WAL pragma below already write bytes
	// to a fresh file, which would defeat a post-open size check.
	preexisting := false
	if fi, err := os.Stat(dbPath); err == nil && fi.Size() > 0 {
		preexisting = true
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	for _, p := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000"} {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, err
		}
	}
	// user_version 0 = store created before host_stats existed (or brand new).
	var uv int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&uv); err != nil {
		db.Close()
		return nil, err
	}
	if uv == 0 && preexisting {
		log.Printf("[traffic] 首次升级流量索引：重建索引并生成 host 汇总（一次性，数据多时可能需要一分钟）…")
	}
	for _, stmt := range indexSchema {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, err
		}
	}
	if uv == 0 {
		if err := backfillHostStats(db); err != nil {
			db.Close()
			return nil, err
		}
		if _, err := db.Exec(`PRAGMA user_version = 1`); err != nil {
			db.Close()
			return nil, err
		}
	}
	t := &Traffic{dir: dir, addr: addr, db: db}

	p, err := mproxy.NewProxy(&mproxy.Options{
		Addr:        addr,
		SslInsecure: true,
		CaRootPath:  filepath.Join(dir, "_ca"),
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	// Dial targets DIRECTLY. go-mitmproxy's default upstream uses
	// http.ProxyFromEnvironment, so an HTTP_PROXY/HTTPS_PROXY in the environment
	// (a VPN/system proxy) would make it forward target requests through that
	// external proxy — which can't reach the target → 502. We capture target
	// traffic directly, never via the host's proxy.
	p.SetUpstreamProxy(func(*http.Request) (*url.URL, error) { return nil, nil })
	// Fail-open: MITM every host by default, EXCEPT ones a prior request proved we
	// can't intercept without breaking (see maybePassthrough). Those are tunneled
	// transparently so the request still reaches the target instead of being killed.
	p.SetShouldInterceptRule(func(req *http.Request) bool {
		_, tunnel := t.pass.Load(hostOnly(req.Host))
		return !tunnel
	})
	p.AddAddon(&sink{t: t})
	t.proxy = p
	return t, nil
}

// backfillHostStats rebuilds the per-host summary from exchanges for stores
// created before host_stats existed. Idempotent (wipe-then-insert inside one
// transaction), so a crash mid-upgrade just redoes it on the next start.
func backfillHostStats(db *sql.DB) error {
	var hasRows bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM exchanges)`).Scan(&hasRows); err != nil {
		return err
	}
	if !hasRows {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM host_stats`); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`INSERT INTO host_stats(host, n, last_ts)
SELECT host, COUNT(*), MAX(ts) FROM exchanges GROUP BY host`); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// hostOnly strips an optional :port, so passthrough keys match whether the host
// arrives as "example.com:443" (CONNECT) or "example.com" (request URL).
func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

// ProxyAddr returns the address workers should set as HTTP(S)_PROXY.
func (t *Traffic) ProxyAddr() string { return "http://127.0.0.1" + t.addr }

// CACertPath returns the PEM CA cert clients must trust to verify HTTPS through
// the MITM proxy (go-mitmproxy writes it here on first start).
func (t *Traffic) CACertPath() string {
	return filepath.Join(t.dir, "_ca", "mitmproxy-ca-cert.pem")
}

// Start runs the proxy (blocking); run in a goroutine.
func (t *Traffic) Start() error { return t.proxy.Start() }

func (t *Traffic) Close() error { return t.db.Close() }
func (t *Traffic) DB() *sql.DB  { return t.db }

// sink is the go-mitmproxy addon that records completed exchanges.
type sink struct {
	mproxy.BaseAddon
	t *Traffic
}

func (s *sink) Response(f *mproxy.Flow) {
	if f.Request == nil || f.Response == nil {
		return
	}
	s.t.record(f)
}

// RequestError fires when a request through an established MITM tunnel fails. If
// the failure looks proxy/protocol-caused (h2 quirks, HEAD-with-body, protocol
// errors) — not a plain target-unreachable error — we flag the host for
// transparent passthrough so future requests to it succeed instead of dying.
func (s *sink) RequestError(f *mproxy.Flow, err error) { s.t.maybePassthrough(f, err) }

// maybePassthrough marks a host to be tunneled transparently on the next
// connection, but only for errors the proxy itself caused — a target that is
// simply down/filtered would fail without us too, and must stay MITM'd+recorded.
func (t *Traffic) maybePassthrough(f *mproxy.Flow, err error) {
	if err == nil || f == nil || f.Request == nil || f.Request.URL == nil || !proxyCausedErr(err) {
		return
	}
	host := f.Request.URL.Hostname()
	if host == "" {
		return
	}
	if _, loaded := t.pass.LoadOrStore(host, struct{}{}); !loaded {
		log.Printf("[traffic] 与 %s 的 MITM 出错，改为透传（该 host 后续直连目标、不再记录，但请求照常）：%v", host, err)
	}
}

// proxyCausedErr reports whether err indicates the interception layer (not the
// target) is at fault — HTTP/2 handling, HEAD-with-body, or a protocol violation.
func proxyCausedErr(err error) bool {
	s := strings.ToLower(err.Error())
	for _, p := range []string{"head request", "http2", "http/2", "protocol error", "protocol_error", "malformed"} {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func (t *Traffic) record(f *mproxy.Flow) {
	// The write lock covers blob + tree + index writes, so DeleteHost (and its
	// blob GC) can run under the same lock without racing a concurrent record.
	t.wmu.Lock()
	defer t.wmu.Unlock()
	host := f.Request.URL.Hostname()
	method := f.Request.Method
	tmpl := db.TemplatePath(f.Request.URL.EscapedPath())
	n := t.seq.Add(1)
	now := time.Now()
	id := fmt.Sprintf("%d-%04d", now.Unix(), n%10000)

	// dir mirrors the URL path as a browsable file tree:
	//   <host>/<seg1>/<seg2>/.../<METHOD>/<id>/{request.http,response.http,meta.json}
	// e.g. http://h/api/v1/login → traffic/h/api/v1/login/GET/<id>/
	parts := []string{t.dir, sanitize(host)}
	for _, seg := range strings.Split(strings.Trim(tmpl, "/"), "/") {
		if seg != "" {
			parts = append(parts, sanitize(seg))
		}
	}
	parts = append(parts, method, id)
	exDir := filepath.Join(parts...)
	if rel, err := filepath.Rel(t.dir, exDir); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		log.Printf("[traffic] 写入失败：路径 %s 逃逸出流量根目录 %s（该条流量丢弃）", exDir, t.dir)
		return
	}
	if err := os.MkdirAll(exDir, 0o755); err != nil {
		log.Printf("[traffic] 写入失败：创建目录 %s 出错（磁盘满或权限问题，该条流量丢弃）：%v", exDir, err)
		return
	}

	reqBody := t.bodyOrBlob(f.Request.Body)
	respBody := t.bodyOrBlob(f.Response.Body)
	ct := f.Response.Header.Get("Content-Type")

	reqTxt := fmt.Sprintf("%s %s %s\n%s\n%s", method, f.Request.URL.RequestURI(), f.Request.Proto, headerLines(f.Request.Header), reqBody)
	respTxt := fmt.Sprintf("HTTP %d\n%s\n%s", f.Response.StatusCode, headerLines(f.Response.Header), respBody)
	if err := os.WriteFile(filepath.Join(exDir, "request.http"), []byte(reqTxt), 0o644); err != nil {
		log.Printf("[traffic] 写入失败：request.http 出错 %s：%v", exDir, err)
	}
	if err := os.WriteFile(filepath.Join(exDir, "response.http"), []byte(respTxt), 0o644); err != nil {
		log.Printf("[traffic] 写入失败：response.http 出错 %s：%v", exDir, err)
	}
	meta := fmt.Sprintf(`{"id":%q,"host":%q,"method":%q,"url":%q,"template":%q,"status":%d,"content_type":%q}`,
		id, host, method, f.Request.URL.String(), tmpl, f.Response.StatusCode, ct)
	if err := os.WriteFile(filepath.Join(exDir, "meta.json"), []byte(meta), 0o644); err != nil {
		log.Printf("[traffic] 写入失败：meta.json 出错 %s：%v", exDir, err)
	}

	rel, _ := filepath.Rel(t.dir, exDir)
	if _, err := t.db.Exec(`INSERT OR REPLACE INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		id, now.Unix(), host, method, tmpl, f.Request.URL.String(), f.Response.StatusCode, ct,
		len(f.Request.Body), len(f.Response.Body), rel); err != nil {
		log.Printf("[traffic] 索引失败：写入 SQLite exchanges 出错（%s %s）：%v", host, f.Request.URL.String(), err)
	}
}

// bodyOrBlob returns the body inline if small, else stores it content-addressed
// and returns a human-readable pointer.
func (t *Traffic) bodyOrBlob(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	if len(body) <= maxInlineBody {
		return string(body)
	}
	sum := sha256.Sum256(body)
	h := hex.EncodeToString(sum[:])
	blobDir := filepath.Join(t.dir, "_blobs", "sha256", h[:2], h[2:4])
	_ = os.MkdirAll(blobDir, 0o755)
	blobPath := filepath.Join(blobDir, h+".bin")
	if _, err := os.Stat(blobPath); os.IsNotExist(err) {
		_ = os.WriteFile(blobPath, body, 0o644)
	}
	return fmt.Sprintf("@blob sha256:%s (len=%d)", h, len(body))
}

func headerLines(h map[string][]string) string {
	var b strings.Builder
	for k, vs := range h {
		for _, v := range vs {
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func sanitize(s string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "?", "_", "*", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	out := r.Replace(s)
	if out == "" || out == "_" || out == "." || out == ".." {
		return "root"
	}
	if len(out) > 120 {
		out = out[:120]
	}
	return out
}

// ExchangeMeta is one row of the index (returned by Search).
type ExchangeMeta struct {
	ID          string `json:"id"`
	TS          int64  `json:"ts"`
	Host        string `json:"host"`
	Method      string `json:"method"`
	URLTemplate string `json:"url_template"`
	URL         string `json:"url"`
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
	RespLen     int    `json:"resp_len"`
	Path        string `json:"path"`
}

// Search returns paged exchange metadata (never bodies).
func (t *Traffic) Search(host string, page, size int) ([]ExchangeMeta, error) {
	if size <= 0 || size > 500 {
		size = 100
	}
	q := `SELECT id,ts,host,method,url_template,url,status,content_type,resp_len,path FROM exchanges`
	args := []any{}
	if host != "" {
		q += ` WHERE host=?`
		args = append(args, host)
	}
	q += ` ORDER BY ts DESC LIMIT ? OFFSET ?`
	args = append(args, size, page*size)
	rows, err := t.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExchangeMeta
	for rows.Next() {
		var m ExchangeMeta
		if err := rows.Scan(&m.ID, &m.TS, &m.Host, &m.Method, &m.URLTemplate, &m.URL, &m.Status, &m.ContentType, &m.RespLen, &m.Path); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// countCap bounds filtered totals: an exact COUNT(*) must visit every matching
// row (seconds on a multi-million-row store), while the UI only needs "many".
// Totals above the cap are reported as countCap with capped=true.
const countCap = 10000

// hostInLimit caps the resolved-host IN(...) list; broader substrings fall back
// to a LIKE predicate instead of a thousands-long parameter list.
const hostInLimit = 256

// Page returns one page of exchange metadata filtered by an optional host
// substring, exact method, and a free-text query q that fuzzy-matches across all
// indexed metadata columns (host/url/method/content-type/status), together with
// the total number of rows matching that filter (for the UI's pagination —
// capped at countCap, see capped return). Newest first. Bodies are never
// included.
func (t *Traffic) Page(host, method, q string, page, size int) (rows []ExchangeMeta, total int, capped bool, err error) {
	if size <= 0 || size > 500 {
		size = 100
	}
	if page < 0 {
		page = 0
	}

	hostSet, hostLike, ok, err := t.resolveHosts(host)
	if err != nil || !ok {
		return nil, 0, false, err // ok=false: no recorded host matches the filter
	}
	where, args := filterWhere(hostSet, hostLike, method, q)

	// Total: prefer the small host summary (exact, O(hosts)); only count
	// exchanges rows when method/q force a per-row filter — capped so a rare
	// search term can't stall the page for seconds.
	switch {
	case where == "":
		err = t.db.QueryRow(`SELECT COALESCE(SUM(n),0) FROM host_stats`).Scan(&total)
	case len(hostSet) > 0 && len(hostSet) <= hostInLimit && strings.TrimSpace(method) == "" && strings.TrimSpace(q) == "":
		err = t.db.QueryRow(`SELECT COALESCE(SUM(n),0) FROM host_stats WHERE host IN (`+placeholders(len(hostSet))+`)`, anyList(hostSet)...).Scan(&total)
	default:
		var n int
		if err = t.db.QueryRow(`SELECT COUNT(*) FROM (SELECT 1 FROM exchanges`+where+` LIMIT ?)`,
			append(append([]any{}, args...), countCap+1)...).Scan(&n); err == nil {
			if n > countCap {
				n = countCap
				capped = true
			}
			total = n
		}
	}
	if err != nil {
		return nil, 0, false, err
	}

	sel := `SELECT id,ts,host,method,url_template,url,status,content_type,resp_len,path FROM exchanges` +
		where + ` ORDER BY ts DESC LIMIT ? OFFSET ?`
	qargs := append(append([]any{}, args...), size, page*size)
	rs, err := t.db.Query(sel, qargs...)
	if err != nil {
		return nil, 0, false, err
	}
	defer rs.Close()
	for rs.Next() {
		var m ExchangeMeta
		if err := rs.Scan(&m.ID, &m.TS, &m.Host, &m.Method, &m.URLTemplate, &m.URL, &m.Status, &m.ContentType, &m.RespLen, &m.Path); err != nil {
			return nil, 0, false, err
		}
		rows = append(rows, m)
	}
	return rows, total, capped, rs.Err()
}

// resolveHosts resolves a host substring filter against the small host_stats
// summary (thousands of rows) instead of scanning every exchange. An empty
// filter returns ok=true with no hosts; ok=false means nothing matches.
func (t *Traffic) resolveHosts(host string) (hostSet []string, hostLike string, ok bool, err error) {
	h := strings.TrimSpace(host)
	if h == "" {
		return nil, "", true, nil
	}
	hostLike = "%" + h + "%"
	rs, err := t.db.Query(`SELECT host FROM host_stats WHERE host LIKE ?`, hostLike)
	if err != nil {
		return nil, "", false, err
	}
	defer rs.Close()
	for rs.Next() {
		var x string
		if err := rs.Scan(&x); err != nil {
			return nil, "", false, err
		}
		hostSet = append(hostSet, x)
	}
	return hostSet, hostLike, len(hostSet) > 0, rs.Err()
}

// filterWhere builds the exchanges WHERE clause shared by Page and Export:
// exact hosts from resolveHosts, method exact-match, and free-text LIKE across
// the indexed text columns (bodies aren't indexed). Cheapest columns first —
// OR short-circuits per row, so common terms hit method/status/host before
// touching the long url strings.
func filterWhere(hostSet []string, hostLike, method, q string) (where string, args []any) {
	add := func(cond string, vs ...any) {
		if where == "" {
			where = " WHERE "
		} else {
			where += " AND "
		}
		where += cond
		args = append(args, vs...)
	}
	if hostLike != "" {
		if len(hostSet) <= hostInLimit {
			add(`host IN (`+placeholders(len(hostSet))+`)`, anyList(hostSet)...)
		} else {
			add(`host LIKE ?`, hostLike)
		}
	}
	if m := strings.TrimSpace(method); m != "" {
		add("method=?", strings.ToUpper(m))
	}
	if s := strings.TrimSpace(q); s != "" {
		like := "%" + s + "%"
		add("(method LIKE ? OR CAST(status AS TEXT) LIKE ? OR host LIKE ? OR content_type LIKE ? OR url LIKE ? OR url_template LIKE ?)",
			like, like, like, like, like, like)
	}
	return where, args
}

// exportCap bounds one export so an unfiltered export can't try to
// dump millions of exchanges in a single download.
const exportCap = 5000

var exportBlobRef = regexp.MustCompile(`@blob sha256:([0-9a-f]{64}) \(len=\d+\)`)

// exportRecord is one exchange in a JSON export.
type exportRecord struct {
	Time     string `json:"time"`
	Host     string `json:"host"`
	Method   string `json:"method"`
	URL      string `json:"url"`
	Status   int    `json:"status"`
	Request  string `json:"request"`
	Response string `json:"response"`
}

// Export streams the exchanges matching the same filters as Page (host
// substring / method / free-text q), newest first, capped at limit (0 or
// out-of-range → exportCap). format selects the shape:
//   - "json": a JSON array of {time,host,method,url,status,request,response}
//   - "raw" (default): one text block per exchange —
//     ---
//     <time> <host> <method> <url> <status>
//     <request>
//     <blank line>
//     <response>
//     ---
//
// Bodies are included with blob pointers resolved inline. Returns the number
// of exchanges exported.
func (t *Traffic) Export(host, method, q string, limit int, format string, w io.Writer) (int, error) {
	if limit <= 0 || limit > exportCap {
		limit = exportCap
	}
	hostSet, hostLike, ok, err := t.resolveHosts(host)
	if err != nil || !ok {
		return 0, err
	}
	where, args := filterWhere(hostSet, hostLike, method, q)
	rs, err := t.db.Query(`SELECT ts,host,method,url,status,path FROM exchanges`+where+` ORDER BY ts DESC LIMIT ?`,
		append(append([]any{}, args...), limit)...)
	if err != nil {
		return 0, err
	}
	type exRow struct {
		ts                     int64
		host, method, url, rel string
		status                 int
	}
	var rows []exRow
	for rs.Next() {
		var r exRow
		if err := rs.Scan(&r.ts, &r.host, &r.method, &r.url, &r.status, &r.rel); err != nil {
			rs.Close()
			return 0, err
		}
		rows = append(rows, r)
	}
	err = rs.Err()
	rs.Close()
	if err != nil {
		return 0, err
	}

	jsonMode := strings.EqualFold(strings.TrimSpace(format), "json")
	bw := bufio.NewWriter(w)
	if jsonMode {
		bw.WriteByte('[')
	}
	n := 0
	for _, r := range rows {
		base := filepath.Join(t.dir, r.rel)
		reqB, rerr := os.ReadFile(filepath.Join(base, "request.http"))
		respB, serr := os.ReadFile(filepath.Join(base, "response.http"))
		if rerr != nil && serr != nil {
			continue // tree files gone; skip this exchange
		}
		req := strings.TrimRight(string(t.inlineBlobs(reqB)), "\n")
		resp := strings.TrimRight(string(t.inlineBlobs(respB)), "\n")
		ts := time.Unix(r.ts, 0).Format("2006-01-02 15:04:05")
		if jsonMode {
			if n > 0 {
				bw.WriteByte(',')
			}
			bw.WriteByte('\n')
			enc := json.NewEncoder(bw)
			enc.SetEscapeHTML(false)
			rec := exportRecord{Time: ts, Host: r.host, Method: r.method, URL: r.url, Status: r.status, Request: req, Response: resp}
			if err := enc.Encode(rec); err != nil {
				return n, err
			}
		} else {
			fmt.Fprintf(bw, "---\n%s %s %s %s %d\n%s\n\n%s\n---\n", ts, r.host, r.method, r.url, r.status, req, resp)
		}
		n++
	}
	if jsonMode {
		bw.WriteString("]\n")
	}
	return n, bw.Flush()
}

// inlineBlobs replaces "@blob sha256:<h> (len=N)" body pointers with the
// referenced blob bytes so exported files are self-contained; a missing blob
// keeps its pointer.
func (t *Traffic) inlineBlobs(b []byte) []byte {
	return exportBlobRef.ReplaceAllFunc(b, func(m []byte) []byte {
		h := string(exportBlobRef.FindSubmatch(m)[1])
		blob, err := os.ReadFile(filepath.Join(t.dir, "_blobs", "sha256", h[:2], h[2:4], h+".bin"))
		if err != nil {
			return m
		}
		return blob
	})
}

// placeholders returns "?,?,?" for n parameters.
func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

// anyList converts a string slice to driver arguments.
func anyList(ss []string) []any {
	out := make([]any, len(ss))
	for i, x := range ss {
		out[i] = x
	}
	return out
}

func (t *Traffic) Get(id string) (req, resp string, err error) {
	var rel string
	if err = t.db.QueryRow(`SELECT path FROM exchanges WHERE id=?`, id).Scan(&rel); err != nil {
		return "", "", err
	}
	respPath := filepath.Join(t.dir, rel, "response.http")
	pb, err := os.ReadFile(respPath)
	if err != nil {
		return "", "", fmt.Errorf("流量响应文件缺失（%s，可能被删除或磁盘异常）：%w", rel, err)
	}
	rb, _ := os.ReadFile(filepath.Join(t.dir, rel, "request.http"))
	return string(rb), string(pb), nil
}

// HostCount is one distinct recorded host plus its exchange count.
type HostCount struct {
	Host  string `json:"host"`
	Count int    `json:"count"`
}

// Hosts returns distinct recorded hosts with exchange counts, most recent
// activity first — powers the page's target picker.
func (t *Traffic) Hosts() ([]HostCount, error) {
	rows, err := t.db.Query(`SELECT host, n FROM host_stats ORDER BY last_ts DESC, host`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HostCount
	for rows.Next() {
		var h HostCount
		if err := rows.Scan(&h.Host, &h.Count); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// Count returns total recorded exchanges.
func (t *Traffic) Count() (int, error) {
	var n int
	err := t.db.QueryRow(`SELECT COALESCE(SUM(n),0) FROM host_stats`).Scan(&n)
	return n, err
}

// DeleteHost removes every recorded exchange whose host contains the given
// substring — mirroring the UI's host filter, so what you filtered is what gets
// deleted: the index rows plus each matched host's file tree (<dir>/<host>/…).
// Content-addressed blobs in _blobs are shared across hosts, so instead of
// deleting by host they are garbage-collected afterwards: any blob no longer
// referenced by a remaining exchange tree is removed (frees the data volume).
// Returns rows deleted.
func (t *Traffic) DeleteHost(host string) (int64, error) {
	like := "%" + host + "%"
	t.wmu.Lock()
	defer t.wmu.Unlock()
	// Collect distinct matched hosts first from the small host summary (the
	// source of truth for tree names) so the trees are removed even if the
	// DELETE row count is 0.
	rows, err := t.db.Query(`SELECT host FROM host_stats WHERE host LIKE ?`, like)
	if err != nil {
		return 0, err
	}
	var hosts []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			rows.Close()
			return 0, err
		}
		hosts = append(hosts, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	res, err := t.db.Exec(`DELETE FROM exchanges WHERE host LIKE ?`, like)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	for _, h := range hosts {
		os.RemoveAll(filepath.Join(t.dir, sanitize(h)))
	}
	if n > 0 {
		_ = t.gcBlobs()
	}
	return n, nil
}

// DeleteHostsExact removes recorded exchanges for a set of EXACT hosts (the
// batch path for the page's multi-select delete): index rows + each host's file
// tree, then one blob-GC pass. Exact match — unlike DeleteHost's substring —
// so picking "api.example.com" never sweeps "api.example.com.cn". Returns rows
// deleted. Duplicate hosts are harmless (idempotent deletes, single tree pass).
func (t *Traffic) DeleteHostsExact(hosts []string) (int64, error) {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	var deleted int64
	removed := make(map[string]bool)
	for _, h := range hosts {
		res, err := t.db.Exec(`DELETE FROM exchanges WHERE host=?`, h)
		if err != nil {
			return deleted, err
		}
		n, _ := res.RowsAffected()
		deleted += n
		if !removed[h] {
			os.RemoveAll(filepath.Join(t.dir, sanitize(h)))
			removed[h] = true
		}
	}
	if deleted > 0 {
		_ = t.gcBlobs()
	}
	return deleted, nil
}

// DeleteHostsExactAsync is the same as DeleteHostsExact but defers the file-tree
// removal and blob GC to a background goroutine. The SQLite index rows are
// deleted synchronously (fast) so the caller sees the traffic as gone
// immediately; the potentially slow file walk (gcBlobs scans every exchange
// tree) runs without blocking the HTTP request. Used by task deletion where a
// large traffic volume would otherwise time out the DELETE response.
func (t *Traffic) DeleteHostsExactAsync(hosts []string) (int64, error) {
	t.wmu.Lock()
	var deleted int64
	removed := make(map[string]bool)
	for _, h := range hosts {
		res, err := t.db.Exec(`DELETE FROM exchanges WHERE host=?`, h)
		if err != nil {
			t.wmu.Unlock()
			return deleted, err
		}
		n, _ := res.RowsAffected()
		deleted += n
		removed[h] = true
	}
	t.wmu.Unlock()

	if deleted > 0 {
		hostsToRemove := make([]string, 0, len(removed))
		for h := range removed {
			hostsToRemove = append(hostsToRemove, h)
		}
		go func() {
			for _, h := range hostsToRemove {
				os.RemoveAll(filepath.Join(t.dir, sanitize(h)))
			}
			t.wmu.Lock()
			_ = t.gcBlobs()
			t.wmu.Unlock()
		}()
	}
	return deleted, nil
}

// gcBlobs removes content-addressed blobs in _blobs that no remaining exchange
// tree references. References appear as "@blob sha256:<hex>" lines inside the
// tree's request.http/response.http files (see bodyOrBlob). Best-effort: walk
// errors only skip cleanup of the affected files. Callers must hold wmu so this
// never races a concurrent record() writing a fresh blob + tree.
func (t *Traffic) gcBlobs() error {
	refs := make(map[string]struct{})
	blobRe := regexp.MustCompile(`@blob sha256:([0-9a-f]{64})`)
	err := filepath.WalkDir(t.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			// _blobs/_index/_ca never contain references; skip their subtrees.
			if p != t.dir && strings.HasPrefix(d.Name(), "_") {
				return filepath.SkipDir
			}
			return nil
		}
		if n := d.Name(); n != "request.http" && n != "response.http" {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		for _, m := range blobRe.FindAllSubmatch(b, -1) {
			refs[string(m[1])] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return filepath.WalkDir(filepath.Join(t.dir, "_blobs", "sha256"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		h := strings.TrimSuffix(d.Name(), ".bin")
		if _, ok := refs[h]; !ok {
			os.Remove(p)
		}
		return nil
	})
}

// query returns one page of exchange metadata filtered by host and/or a url
// substring. Default page size is intentionally small (3) to keep tool results
// lightweight and capped at 10; page is 0-based (page*limit offset).
func (t *Traffic) query(host, contains string, statusMin, page, limit int) ([]ExchangeMeta, error) {
	if limit <= 0 {
		limit = 3
	}
	if limit > 10 {
		limit = 10
	}
	if page < 0 {
		page = 0
	}
	q := `SELECT id,ts,host,method,url_template,url,status,content_type,resp_len,path FROM exchanges WHERE 1=1`
	args := []any{}
	if host != "" {
		q += ` AND host=?`
		args = append(args, host)
	}
	if statusMin > 0 {
		q += ` AND status >= ?`
		args = append(args, statusMin)
	}
	if contains != "" {
		q += ` AND (url LIKE ? OR url_template LIKE ? OR content_type LIKE ?)`
		args = append(args, "%"+contains+"%", "%"+contains+"%", "%"+contains+"%")
	}
	q += ` ORDER BY ts DESC LIMIT ? OFFSET ?`
	args = append(args, limit, page*limit)
	rows, err := t.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExchangeMeta
	for rows.Next() {
		var m ExchangeMeta
		if err := rows.Scan(&m.ID, &m.TS, &m.Host, &m.Method, &m.URLTemplate, &m.URL, &m.Status, &m.ContentType, &m.RespLen, &m.Path); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Tools exposes traffic lookup to work agents so they query already-captured
// traffic instead of re-curling the same resource (token + dedup win).
func (t *Traffic) Tools() []actool.CoreTool {
	allow := func(context.Context, json.RawMessage, permission.Context) permission.Decision {
		return permission.Allowed()
	}
	ro := func(json.RawMessage) bool { return true }

	search := actool.Build(actool.Spec{
		Name:        "traffic_search",
		Description: "查询记录代理已抓取的目标流量（必须指定 host，可再按 URL 子串过滤）。仅返回极轻量索引(id/method/url/status/resp_len)，不含任何响应内容。默认只返回 3 条、每页最多 10 条；结果多时用 page 翻页（page=0 起）；要看某条的请求/响应原文用 traffic_get(id)。回看已访问资源、找端点先用它，避免重复 curl 同一 URL。排障时可用 status_min 过滤错误码（如 500 取所有 5xx，即环境/服务端报错）。",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"host":       map[string]any{"type": "string", "description": "按主机过滤（必填，如 '107.172.96.177:8082'）"},
				"contains":   map[string]any{"type": "string", "description": "URL 子串过滤（可选，如 'api' / 'login'）"},
				"status_min": map[string]any{"type": "integer", "description": "按最小 HTTP 状态码过滤（可选，如 500 只看 5xx 报错、400 只看 4xx）"},
				"limit":      map[string]any{"type": "integer", "description": "每页条数，默认 3，最大 10"},
				"page":       map[string]any{"type": "integer", "description": "页码，从 0 开始，默认 0（按 ts 倒序分页）"},
			},
			"required": []any{"host"},
		},
		ReadOnly:    ro,
		Permissions: allow,
		Run: func(_ context.Context, in json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			var a struct {
				Host, Contains string
				StatusMin      int
				Limit          int
				Page           int
			}
			_ = json.Unmarshal(in, &a)
			if strings.TrimSpace(a.Host) == "" {
				return actool.Errorf("host 为必填参数：请指定要查询的主机（如 '107.172.96.177:8082'），避免全库扫描。"), nil
			}
			rows, err := t.query(a.Host, a.Contains, a.StatusMin, a.Page, a.Limit)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if len(rows) == 0 {
				return actool.Text("无匹配流量。"), nil
			}
			// 精简为最小索引：仅保留定位所需字段 + 响应码/长度，不带任何响应内容。
			type liteRow struct {
				ID      string `json:"id"`
				Method  string `json:"method"`
				URL     string `json:"url"`
				Status  int    `json:"status"`
				RespLen int    `json:"resp_len"`
			}
			lite := make([]liteRow, 0, len(rows))
			for _, r := range rows {
				lite = append(lite, liteRow{ID: r.ID, Method: r.Method, URL: r.URL, Status: r.Status, RespLen: r.RespLen})
			}
			b, _ := json.Marshal(lite)
			return actool.Text(string(b)), nil
		},
	})

	get := actool.Build(actool.Spec{
		Name:        "traffic_get",
		Description: "按 id 取一条已抓流量的请求/响应原文（过大会截断）。配合 traffic_search 用，避免重复 curl。",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": map[string]any{"type": "string", "description": "traffic_search 返回的 id"}},
			"required":   []any{"id"},
		},
		ReadOnly:    ro,
		Permissions: allow,
		Run: func(_ context.Context, in json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			var a struct{ ID string }
			_ = json.Unmarshal(in, &a)
			req, resp, err := t.Get(a.ID)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text("=== REQUEST ===\n" + clip(req, 2500) + "\n\n=== RESPONSE ===\n" + clip(resp, 4000)), nil
		},
	})
	return []actool.CoreTool{search, get}
}

// SeedToolMetas returns the traffic tools built on a ZERO receiver, for seeding the
// tools catalog (metadata only — Name/Description/InputSchema). The handlers close
// over the nil receiver but are never invoked on this instance, so it is safe.
func SeedToolMetas() []actool.CoreTool { return (&Traffic{}).Tools() }

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n... [截断，共 %d 字节；完整在流量文件树] ...", len(s))
}
