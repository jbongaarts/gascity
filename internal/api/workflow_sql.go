package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql" // MySQL driver for dolt

	"github.com/gastownhall/gascity/internal/beads"
)

// workflowSQLSnapshot fetches all workflow beads and deps via direct SQL,
// bypassing the N+1 bd subprocess calls. Returns beads, a bead index, and
// a pre-fetched dep map. Connects to the dolt server on the given port
// using the given database name.
func workflowSQLSnapshot(host string, port int, database, rootID string) ([]beads.Bead, map[string]beads.Bead, map[string][]beads.Dep, error) {
	dsn := fmt.Sprintf("root@tcp(%s:%d)/%s?parseTime=true&timeout=10s", host, port, database)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("sql open: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(30 * time.Second)

	// Query 1: All workflow beads (root + children by gc.root_bead_id metadata)
	beadRows, err := db.Query(`
		SELECT
			i.id, i.title, i.status, i.issue_type, i.assignee,
			i.description, i.created_at, i.updated_at,
			i.metadata
		FROM issues i
		WHERE i.id = ?
		   OR JSON_UNQUOTE(JSON_EXTRACT(i.metadata, '$."gc.root_bead_id"')) = ?
		ORDER BY i.created_at
	`, rootID, rootID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("beads query: %w", err)
	}
	defer beadRows.Close()

	var workflowBeads []beads.Bead
	beadIndex := make(map[string]beads.Bead)
	beadIDs := make([]string, 0, 100)

	for beadRows.Next() {
		var b beads.Bead
		var assignee, description sql.NullString
		var metadataJSON []byte
		var createdAt, updatedAt time.Time

		if err := beadRows.Scan(
			&b.ID, &b.Title, &b.Status, &b.Type, &assignee,
			&description, &createdAt, &updatedAt,
			&metadataJSON,
		); err != nil {
			return nil, nil, nil, fmt.Errorf("bead scan: %w", err)
		}

		b.Assignee = assignee.String
		b.Description = description.String
		b.CreatedAt = createdAt

		// Parse JSON metadata
		if len(metadataJSON) > 0 {
			b.Metadata = make(map[string]string)
			var raw map[string]interface{}
			if json.Unmarshal(metadataJSON, &raw) == nil {
				for k, v := range raw {
					if s, ok := v.(string); ok {
						b.Metadata[k] = s
					} else {
						// Non-string values: marshal back to string
						if encoded, err := json.Marshal(v); err == nil {
							b.Metadata[k] = string(encoded)
						}
					}
				}
			}
		}

		workflowBeads = append(workflowBeads, b)
		beadIndex[b.ID] = b
		beadIDs = append(beadIDs, b.ID)
	}
	if err := beadRows.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("bead rows: %w", err)
	}

	if len(beadIDs) == 0 {
		return nil, nil, nil, fmt.Errorf("no beads found for workflow %s", rootID)
	}

	// Query 2: All deps between workflow beads
	// Use subquery instead of IN (?,?,...) — dolt handles subqueries much
	// faster than large parameter lists (13s vs 46ms for 95 IDs).
	depRows, err := db.Query(`
		SELECT d.issue_id, d.depends_on_id, d.type
		FROM dependencies d
		WHERE d.issue_id IN (
			SELECT i.id FROM issues i
			WHERE i.id = ? OR JSON_UNQUOTE(JSON_EXTRACT(i.metadata, '$."gc.root_bead_id"')) = ?
		)
		AND d.depends_on_id IN (
			SELECT i.id FROM issues i
			WHERE i.id = ? OR JSON_UNQUOTE(JSON_EXTRACT(i.metadata, '$."gc.root_bead_id"')) = ?
		)
	`, rootID, rootID, rootID, rootID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("deps query: %w", err)
	}
	defer depRows.Close()

	depMap := make(map[string][]beads.Dep)
	for depRows.Next() {
		var d beads.Dep
		if err := depRows.Scan(&d.IssueID, &d.DependsOnID, &d.Type); err != nil {
			return nil, nil, nil, fmt.Errorf("dep scan: %w", err)
		}
		depMap[d.IssueID] = append(depMap[d.IssueID], d)
	}
	if err := depRows.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("dep rows: %w", err)
	}

	// Query 3: Labels for workflow beads
	labelRows, err := db.Query(`
		SELECT l.issue_id, l.label
		FROM labels l
		WHERE l.issue_id IN (
			SELECT i.id FROM issues i
			WHERE i.id = ? OR JSON_UNQUOTE(JSON_EXTRACT(i.metadata, '$."gc.root_bead_id"')) = ?
		)
	`, rootID, rootID)
	if err != nil {
		// Non-fatal — labels are optional
		return workflowBeads, beadIndex, depMap, nil
	}
	defer labelRows.Close()

	labelMap := make(map[string][]string)
	for labelRows.Next() {
		var issueID, label string
		if err := labelRows.Scan(&issueID, &label); err != nil {
			continue
		}
		labelMap[issueID] = append(labelMap[issueID], label)
	}

	// Attach labels to beads
	for i := range workflowBeads {
		if labels, ok := labelMap[workflowBeads[i].ID]; ok {
			workflowBeads[i].Labels = labels
			beadIndex[workflowBeads[i].ID] = workflowBeads[i]
		}
	}

	return workflowBeads, beadIndex, depMap, nil
}

// tryFullWorkflowSQL does the entire workflow snapshot via SQL — root
// discovery, bead fetch, dep fetch, and graph build. Falls back to nil
// error only on full success so the caller can use the slow path on any failure.
func (s *Server) tryFullWorkflowSQL(workflowID, fallbackScopeKind, fallbackScopeRef string, snapshotIndex uint64) (*workflowSnapshotResponse, error) {
	cityPath := s.state.CityPath()
	if cityPath == "" {
		return nil, fmt.Errorf("no city path")
	}

	port, database, err := resolveDoltConnection(cityPath)
	if err != nil {
		return nil, err
	}

	workflowBeads, beadIndex, depMap, err := workflowSQLSnapshot("127.0.0.1", port, database, workflowID)
	if err != nil {
		return nil, err
	}
	if len(workflowBeads) == 0 {
		return nil, fmt.Errorf("no beads found")
	}

	root, ok := beadIndex[workflowID]
	if !ok {
		return nil, fmt.Errorf("root bead not found in SQL results")
	}

	store := &prefetchedDepStore{deps: depMap}

	// Collect physical deps only — logical nodes are computed by MC.
	workflowDeps, partial := collectWorkflowDeps(store, beadIndex)

	scopeKind := fallbackScopeKind
	scopeRef := fallbackScopeRef
	if sk := strings.TrimSpace(root.Metadata["gc.scope_kind"]); sk != "" {
		scopeKind = sk
	}
	if sr := strings.TrimSpace(root.Metadata["gc.scope_ref"]); sr != "" {
		scopeRef = sr
	}

	storeRef := "city:" + s.state.CityName()
	beadResponses := make([]workflowBeadResponse, 0, len(workflowBeads))
	for _, bead := range workflowBeads {
		beadResponses = append(beadResponses, workflowBeadResponse{
			ID:            bead.ID,
			Title:         bead.Title,
			Status:        workflowStatus(bead),
			Kind:          workflowKind(bead),
			StepRef:       strings.TrimSpace(bead.Metadata["gc.step_ref"]),
			Attempt:       workflowAttempt(bead),
			LogicalBeadID: strings.TrimSpace(bead.Metadata["gc.logical_bead_id"]),
			ScopeRef:      strings.TrimSpace(bead.Metadata["gc.scope_ref"]),
			Assignee:      strings.TrimSpace(bead.Assignee),
			Metadata:      cloneStringMap(bead.Metadata),
		})
	}

	snapshot := &workflowSnapshotResponse{
		WorkflowID:        resolvedWorkflowID(root),
		RootBeadID:        root.ID,
		RootStoreRef:      storeRef,
		ScopeKind:         scopeKind,
		ScopeRef:          scopeRef,
		Beads:             beadResponses,
		Deps:              workflowDeps,
		LogicalNodes:      []logicalNodeResponse{},
		LogicalEdges:      []workflowDepResponse{},
		ScopeGroups:       []scopeGroupResponse{},
		Partial:           partial,
		ResolvedRootStore: storeRef,
		StoresScanned:     []string{storeRef},
		SnapshotVersion:   snapshotIndex,
	}
	if snapshotIndex > 0 {
		snapshot.SnapshotEventSeq = &snapshotIndex
	}
	return snapshot, nil
}

// tryWorkflowSQL attempts to resolve the dolt port and database for the
// city and fetch the workflow snapshot via direct SQL. Returns a non-nil
// error if SQL is not available (caller should fall back to bd subprocess).
func (s *Server) tryWorkflowSQL(rootID string) ([]beads.Bead, map[string]beads.Bead, map[string][]beads.Dep, error) {
	cityPath := s.state.CityPath()
	if cityPath == "" {
		return nil, nil, nil, fmt.Errorf("no city path")
	}

	port, database, err := resolveDoltConnection(cityPath)
	if err != nil {
		return nil, nil, nil, err
	}

	return workflowSQLSnapshot("127.0.0.1", port, database, rootID)
}

// resolveDoltConnection reads the dolt port from the runtime state file and
// the database name from the beads metadata. Returns (port, database, error).
func resolveDoltConnection(cityPath string) (int, string, error) {
	// Read port from dolt-state.json (managed by gc runtime packs)
	stateData, err := os.ReadFile(cityPath + "/.gc/runtime/packs/dolt/dolt-state.json")
	if err != nil {
		// Try legacy port file
		portData, err2 := os.ReadFile(cityPath + "/.beads/dolt-server.port")
		if err2 != nil {
			return 0, "", fmt.Errorf("no dolt state: %w", err)
		}
		port := 0
		fmt.Sscanf(strings.TrimSpace(string(portData)), "%d", &port)
		if port == 0 {
			return 0, "", fmt.Errorf("invalid port in port file")
		}
		// Get database from config
		db := resolveDoltDatabase(cityPath)
		return port, db, nil
	}

	var state struct {
		Running bool   `json:"running"`
		Port    int    `json:"port"`
		DataDir string `json:"data_dir"`
	}
	if err := json.Unmarshal(stateData, &state); err != nil {
		return 0, "", fmt.Errorf("parse dolt state: %w", err)
	}
	if !state.Running || state.Port == 0 {
		return 0, "", fmt.Errorf("dolt not running")
	}

	db := resolveDoltDatabase(cityPath)
	return state.Port, db, nil
}

// resolveDoltDatabase reads the database name from beads metadata.json.
func resolveDoltDatabase(cityPath string) string {
	data, err := os.ReadFile(cityPath + "/.beads/metadata.json")
	if err != nil {
		return "beads" // default
	}
	var meta struct {
		DoltDatabase string `json:"dolt_database"`
	}
	if json.Unmarshal(data, &meta) == nil && meta.DoltDatabase != "" {
		return meta.DoltDatabase
	}
	return "beads"
}

// prefetchedDepStore wraps a pre-fetched dep map to satisfy the beads.Store
// interface for collectWorkflowDeps, which calls store.DepList().
type prefetchedDepStore struct {
	beads.Store // embed nil Store — only DepList is called
	deps        map[string][]beads.Dep
}

func (s *prefetchedDepStore) DepList(id, direction string) ([]beads.Dep, error) {
	if direction == "down" {
		return s.deps[id], nil
	}
	// "up" direction — reverse lookup
	var result []beads.Dep
	for _, deps := range s.deps {
		for _, d := range deps {
			if d.DependsOnID == id {
				result = append(result, d)
			}
		}
	}
	return result, nil
}
