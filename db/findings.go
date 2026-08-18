package db

import (
	"database/sql"
	"encoding/json"
	"time"
)

// DBFinding is a row in the standalone findings table (persists across task deletion).
type DBFinding struct {
	ID              int64
	TaskID          *int64
	NodeID          *int64
	VulnClass       string
	Severity        string
	Summary         string
	Evidence        string
	SourceFile      string
	Harm            string
	Fix             string
	Request         string
	Response        string
	ReproCmd        string
	Worker          string
	AssetIDs        []int64
	CreatedAt       time.Time
	TaskDescription string // populated via LEFT JOIN on tasks
}

// AddFinding inserts a finding into the standalone findings table. taskID and
// nodeID may be 0 (stored as NULL). Returns the new finding id.
func (d *DB) AddFinding(taskID, nodeID int64, vulnclass, severity, summary, evidence, sourceFile, harm, fix, request, response, reproCmd, worker string, assetIDs []int64) (int64, error) {
	aidsJSON, _ := json.Marshal(assetIDs)
	if assetIDs == nil {
		aidsJSON = []byte("[]")
	}
	var tid, nid *int64
	if taskID > 0 {
		tid = &taskID
	}
	if nodeID > 0 {
		nid = &nodeID
	}
	var id int64
	err := d.QueryRow(
		`INSERT INTO findings (task_id, node_id, vulnclass, severity, summary, evidence, source_file, harm, fix, request, response, repro_cmd, worker, asset_ids)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14) RETURNING id`,
		tid, nid, vulnclass, severity, summary, evidence, sourceFile, harm, fix, request, response, reproCmd, worker, string(aidsJSON),
	).Scan(&id)
	return id, err
}

// ListFindings returns findings (newest first), joined with task description.
// limit <= 0 returns all rows.
func (d *DB) ListFindings(limit int) ([]*DBFinding, error) {
	const q = `
		SELECT f.id, f.task_id, f.node_id, f.vulnclass, f.severity, f.summary,
		       f.evidence, f.source_file, f.harm, f.fix, f.request, f.response, f.repro_cmd, f.worker, f.asset_ids, f.created_at,
		       COALESCE(t.description, '') AS task_description
		FROM findings f
		LEFT JOIN tasks t ON f.task_id = t.id
		ORDER BY f.created_at DESC`
	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = d.Query(q+` LIMIT $1`, limit)
	} else {
		rows, err = d.Query(q)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*DBFinding
	for rows.Next() {
		f := &DBFinding{}
		var aidsJSON string
		if err := rows.Scan(&f.ID, &f.TaskID, &f.NodeID, &f.VulnClass, &f.Severity,
			&f.Summary, &f.Evidence, &f.SourceFile, &f.Harm, &f.Fix, &f.Request, &f.Response, &f.ReproCmd, &f.Worker, &aidsJSON, &f.CreatedAt, &f.TaskDescription); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(aidsJSON), &f.AssetIDs)
		out = append(out, f)
	}
	return out, rows.Err()
}
