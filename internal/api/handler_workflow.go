package api

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

var errWorkflowNotFound = errors.New("workflow not found")

type workflowSessionRef struct {
	bead        beads.Bead
	sessionName string
}

type workflowSnapshotResponse struct {
	WorkflowID        string                 `json:"workflow_id"`
	RootBeadID        string                 `json:"root_bead_id"`
	RootStoreRef      string                 `json:"root_store_ref"`
	ScopeKind         string                 `json:"scope_kind"`
	ScopeRef          string                 `json:"scope_ref"`
	Beads             []workflowBeadResponse `json:"beads"`
	Deps              []workflowDepResponse  `json:"deps"`
	LogicalNodes      []logicalNodeResponse  `json:"logical_nodes"`
	LogicalEdges      []workflowDepResponse  `json:"logical_edges"`
	ScopeGroups       []scopeGroupResponse   `json:"scope_groups"`
	Partial           bool                   `json:"partial"`
	ResolvedRootStore string                 `json:"resolved_root_store"`
	StoresScanned     []string               `json:"stores_scanned"`
	SnapshotVersion   uint64                 `json:"snapshot_version"`
	SnapshotEventSeq  *uint64                `json:"snapshot_event_seq,omitempty"`
}

type workflowBeadResponse struct {
	ID            string            `json:"id"`
	Title         string            `json:"title"`
	Status        string            `json:"status"`
	Kind          string            `json:"kind"`
	StepRef       string            `json:"step_ref,omitempty"`
	Attempt       *int              `json:"attempt,omitempty"`
	LogicalBeadID string            `json:"logical_bead_id,omitempty"`
	ScopeRef      string            `json:"scope_ref,omitempty"`
	Assignee      string            `json:"assignee,omitempty"`
	Metadata      map[string]string `json:"metadata"`
}

type workflowDepResponse struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind,omitempty"`
}

type logicalAttemptResponse struct {
	BeadID    string         `json:"bead_id"`
	Status    string         `json:"status"`
	Attempt   int            `json:"attempt"`
	StartedAt string         `json:"started_at,omitempty"`
	UpdatedAt string         `json:"updated_at,omitempty"`
	Summary   map[string]any `json:"summary,omitempty"`
}

type sessionLinkResponse struct {
	ProjectID   string `json:"project_id"`
	SessionID   string `json:"session_id"`
	SessionName string `json:"session_name"`
	Assignee    string `json:"assignee"`
}

type logicalNodeResponse struct {
	ID            string                   `json:"id"`
	Title         string                   `json:"title"`
	Kind          string                   `json:"kind"`
	Status        string                   `json:"status"`
	ScopeRef      string                   `json:"scope_ref,omitempty"`
	CurrentBeadID string                   `json:"current_bead_id,omitempty"`
	AttemptBadge  string                   `json:"attempt_badge,omitempty"`
	AttemptCount  *int                     `json:"attempt_count,omitempty"`
	ActiveAttempt *int                     `json:"active_attempt,omitempty"`
	Attempts      []logicalAttemptResponse `json:"attempts,omitempty"`
	Metadata      map[string]any           `json:"metadata"`
	SessionLink   *sessionLinkResponse     `json:"session_link,omitempty"`
}

type scopeGroupResponse struct {
	ScopeRef             string   `json:"scope_ref"`
	Label                string   `json:"label"`
	MemberLogicalNodeIDs []string `json:"member_logical_node_ids"`
}

type workflowStoreInfo struct {
	ref       string
	scopeKind string
	scopeRef  string
	store     beads.Store
}

type workflowRootMatch struct {
	info workflowStoreInfo
	root beads.Bead
}

type logicalGroup struct {
	id      string
	order   int
	base    *beads.Bead
	members []beads.Bead
}

func (s *Server) handleWorkflowGet(w http.ResponseWriter, r *http.Request) {
	workflowID := strings.TrimSpace(r.PathValue("workflow_id"))
	if workflowID == "" {
		writeError(w, http.StatusBadRequest, "invalid", "workflow_id is required")
		return
	}

	q := r.URL.Query()
	scopeKind, scopeRef, scopeErr := parseWorkflowRequestScope(q.Get("scope_kind"), q.Get("scope_ref"))
	if scopeErr != "" {
		writeError(w, http.StatusBadRequest, "invalid", scopeErr)
		return
	}
	index := s.latestIndex()

	snapshot, err := s.buildWorkflowSnapshot(workflowID, scopeKind, scopeRef, index)
	if err != nil {
		if errors.Is(err, errWorkflowNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "workflow "+workflowID+" not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", "workflow snapshot failed")
		return
	}

	writeIndexJSON(w, index, snapshot)
}

func (s *Server) handleWorkflowDelete(w http.ResponseWriter, r *http.Request) {
	workflowID := strings.TrimSpace(r.PathValue("workflow_id"))
	if workflowID == "" {
		writeError(w, http.StatusBadRequest, "invalid", "workflow_id is required")
		return
	}

	q := r.URL.Query()
	scopeKind := strings.TrimSpace(q.Get("scope_kind"))
	scopeRef := strings.TrimSpace(q.Get("scope_ref"))
	deleteFromStore := q.Get("delete") == "true"

	stores := s.workflowStores()

	closed := 0
	deleted := 0
	found := false

	for _, info := range stores {
		if info.store == nil {
			continue
		}
		// Skip stores that don't match the requested scope.
		if scopeKind != "" && info.scopeKind != scopeKind {
			continue
		}
		if scopeRef != "" && info.scopeRef != scopeRef {
			continue
		}

		all, err := info.store.List()
		if err != nil {
			continue
		}

		var ids []string
		for _, b := range all {
			if b.ID == workflowID || b.Metadata["gc.root_bead_id"] == workflowID {
				ids = append(ids, b.ID)
			}
		}
		if len(ids) == 0 {
			continue
		}
		found = true

		// Phase 1: Batch close all open beads.
		n, _ := info.store.CloseAll(ids, map[string]string{"gc.outcome": "skipped"})
		closed += n

		// Phase 2: Delete if requested.
		if deleteFromStore {
			for _, id := range ids {
				// Remove deps before delete.
				if deps, err := info.store.DepList(id, "down"); err == nil {
					for _, dep := range deps {
						_ = info.store.DepRemove(id, dep.DependsOnID)
					}
				}
				if deps, err := info.store.DepList(id, "up"); err == nil {
					for _, dep := range deps {
						_ = info.store.DepRemove(dep.IssueID, id)
					}
				}
				deleted++
			}
		}
	}

	if !found {
		writeError(w, http.StatusNotFound, "not_found", "workflow "+workflowID+" not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"workflow_id": workflowID,
		"closed":      closed,
		"deleted":     deleted,
	})
}

func (s *Server) buildWorkflowSnapshot(workflowID, fallbackScopeKind, fallbackScopeRef string, snapshotIndex uint64) (*workflowSnapshotResponse, error) {
	// Fast SQL currently resolves only against the city Dolt store. Rig-scoped
	// workflows still live in rig stores, so routing them through the city SQL
	// path can degenerate into a long scan before we ever reach the real store.
	if fallbackScopeKind != "rig" {
		// Fast path: try direct SQL for the entire snapshot (root discovery + beads + deps)
		if snap, err := s.tryFullWorkflowSQL(workflowID, fallbackScopeKind, fallbackScopeRef, snapshotIndex); err == nil {
			return snap, nil
		}
	}

	// Slow path: bd subprocess N+1
	stores := s.workflowStores()
	storesScanned := make([]string, 0, len(stores))
	seenStoreRefs := make(map[string]bool, len(stores))
	matches := make([]workflowRootMatch, 0)
	listPartial := false
	var firstListErr error
	cityScopeRef := ""

	for _, info := range stores {
		if info.store == nil {
			continue
		}
		if cityScopeRef == "" && info.scopeKind == "city" {
			cityScopeRef = info.scopeRef
		}
		if !seenStoreRefs[info.ref] {
			storesScanned = append(storesScanned, info.ref)
			seenStoreRefs[info.ref] = true
		}

		all, err := info.store.List()
		if err != nil {
			if firstListErr == nil {
				firstListErr = err
			}
			listPartial = true
			continue
		}
		for _, bead := range all {
			if !isWorkflowRoot(bead) || !matchesWorkflowID(bead, workflowID) {
				continue
			}
			matches = append(matches, workflowRootMatch{info: info, root: bead})
		}
	}

	match, ok := selectWorkflowRootMatch(matches, fallbackScopeKind, fallbackScopeRef, cityScopeRef)
	if !ok {
		if firstListErr != nil {
			return nil, firstListErr
		}
		return nil, errWorkflowNotFound
	}

	return s.snapshotFromStore(match.info, match.root, fallbackScopeKind, fallbackScopeRef, cityScopeRef, storesScanned, listPartial, snapshotIndex)
}

func (s *Server) snapshotFromStore(info workflowStoreInfo, root beads.Bead, fallbackScopeKind, fallbackScopeRef, cityScopeRef string, storesScanned []string, listPartial bool, snapshotIndex uint64) (*workflowSnapshotResponse, error) {
	// Try direct SQL path — ~500x faster than N+1 bd subprocess calls.
	var (
		workflowBeads []beads.Bead
		beadIndex     map[string]beads.Bead
		depMap        map[string][]beads.Dep
		sqlErr        error
	)
	if info.scopeKind == "city" {
		workflowBeads, beadIndex, depMap, sqlErr = s.tryWorkflowSQL(root.ID)
	} else {
		sqlErr = errors.New("sql fast path unsupported for rig-scoped workflows")
	}
	usedSQL := sqlErr == nil && len(workflowBeads) > 0

	if !usedSQL {
		// Fall back to bd subprocess path.
		all, err := info.store.List()
		if err != nil {
			return nil, err
		}

		workflowBeads = make([]beads.Bead, 0, len(all))
		for _, bead := range all {
			if bead.ID == root.ID || bead.Metadata["gc.root_bead_id"] == root.ID {
				workflowBeads = append(workflowBeads, bead)
			}
		}

		beadIndex = make(map[string]beads.Bead, len(workflowBeads))
		for _, bead := range workflowBeads {
			beadIndex[bead.ID] = bead
		}
	}

	if len(workflowBeads) == 0 {
		return nil, errWorkflowNotFound
	}

	// Update root from the fetched data (SQL path may have richer data)
	if updated, ok := beadIndex[root.ID]; ok {
		root = updated
	}

	var store beads.Store
	if usedSQL {
		store = &prefetchedDepStore{deps: depMap}
	} else {
		if prefetchedDeps, ok := prefetchedDepsForWorkflowBeads(workflowBeads); ok {
			store = &prefetchedDepStore{deps: prefetchedDeps}
		} else {
			store = info.store
		}
	}

	sessionIndex := s.workflowSessionIndex()
	workflowDeps, logicalDeps, logicalNodes, scopeGroups, partial := buildWorkflowGraph(root, workflowBeads, beadIndex, store, sessionIndex)
	partial = partial || listPartial
	scopeKind, scopeRef := workflowSnapshotScope(info, root, fallbackScopeKind, fallbackScopeRef, cityScopeRef)

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
		RootStoreRef:      info.ref,
		ScopeKind:         scopeKind,
		ScopeRef:          scopeRef,
		Beads:             beadResponses,
		Deps:              workflowDeps,
		LogicalNodes:      logicalNodes,
		LogicalEdges:      logicalDeps,
		ScopeGroups:       scopeGroups,
		Partial:           partial,
		ResolvedRootStore: info.ref,
		StoresScanned:     storesScanned,
		SnapshotVersion:   snapshotIndex,
	}
	if snapshotIndex > 0 {
		snapshot.SnapshotEventSeq = &snapshotIndex
	}
	return snapshot, nil
}

func isWorkflowRoot(bead beads.Bead) bool {
	return strings.TrimSpace(bead.Metadata["gc.kind"]) == "workflow"
}

func resolvedWorkflowID(root beads.Bead) string {
	if workflowID := strings.TrimSpace(root.Metadata["gc.workflow_id"]); workflowID != "" {
		return workflowID
	}
	return root.ID
}

func matchesWorkflowID(root beads.Bead, workflowID string) bool {
	workflowID = strings.TrimSpace(workflowID)
	if workflowID == "" {
		return false
	}
	return root.ID == workflowID || resolvedWorkflowID(root) == workflowID
}

func selectWorkflowRootMatch(matches []workflowRootMatch, requestedScopeKind, requestedScopeRef, cityScopeRef string) (workflowRootMatch, bool) {
	if len(matches) == 0 {
		return workflowRootMatch{}, false
	}
	if requestedScopeKind == "" || requestedScopeRef == "" {
		return matches[0], true
	}

	filtered := make([]workflowRootMatch, 0, len(matches))
	for _, match := range matches {
		if workflowScopeMatches(match.info, match.root, requestedScopeKind, requestedScopeRef) {
			filtered = append(filtered, match)
		}
	}
	switch len(filtered) {
	case 0:
		// Older workflows may not stamp logical scope on the root, and city-
		// scoped workflows can still live in a rig store. Preserve the caller
		// scope only for that legacy city-on-rig case when the workflow ID is
		// unique across scanned stores.
		if len(matches) == 1 && preserveRequestedWorkflowScope(matches[0].info, matches[0].root, requestedScopeKind, requestedScopeRef, cityScopeRef) {
			return matches[0], true
		}
		return workflowRootMatch{}, false
	default:
		return filtered[0], true
	}
}

func workflowScopeMatches(info workflowStoreInfo, root beads.Bead, requestedScopeKind, requestedScopeRef string) bool {
	scopeKind, scopeRef := workflowSelectionScope(info, root)
	return scopeKind == requestedScopeKind && scopeRef == requestedScopeRef
}

func workflowSelectionScope(info workflowStoreInfo, root beads.Bead) (string, string) {
	if scopeKind, scopeRef := workflowRootScope(root); scopeKind != "" && scopeRef != "" {
		return scopeKind, scopeRef
	}
	return info.scopeKind, info.scopeRef
}

func workflowEventScope(info workflowStoreInfo, root beads.Bead, cityScopeRef string) (string, string) {
	if scopeKind, scopeRef := workflowRootScope(root); scopeKind != "" && scopeRef != "" {
		return scopeKind, scopeRef
	}
	// Event projections favor the logical city scope for legacy rig-stored
	// workflows whose roots predate explicit scope stamping. That keeps live
	// event scopes aligned with the snapshot API's preserved city-scope reads
	// for those legacy workflows, while root_store_ref still exposes the
	// physical store for callers that need it.
	if info.scopeKind == "rig" {
		return "city", workflowCityScopeRef(cityScopeRef)
	}
	return info.scopeKind, info.scopeRef
}

func workflowSnapshotScope(info workflowStoreInfo, root beads.Bead, requestedScopeKind, requestedScopeRef, cityScopeRef string) (string, string) {
	if scopeKind, scopeRef := workflowRootScope(root); scopeKind != "" && scopeRef != "" {
		return scopeKind, scopeRef
	}
	if preserveRequestedWorkflowScope(info, root, requestedScopeKind, requestedScopeRef, cityScopeRef) {
		return requestedScopeKind, requestedScopeRef
	}
	return info.scopeKind, info.scopeRef
}

func preserveRequestedWorkflowScope(info workflowStoreInfo, root beads.Bead, requestedScopeKind, requestedScopeRef, cityScopeRef string) bool {
	if requestedScopeKind != "city" || requestedScopeRef == "" {
		return false
	}
	if info.scopeKind != "rig" {
		return false
	}
	if strings.TrimSpace(cityScopeRef) == "" || requestedScopeRef != strings.TrimSpace(cityScopeRef) {
		return false
	}
	scopeKind, scopeRef := workflowRootScope(root)
	return scopeKind == "" || scopeRef == ""
}

func parseWorkflowRequestScope(rawScopeKind, rawScopeRef string) (string, string, string) {
	scopeKind := strings.TrimSpace(rawScopeKind)
	scopeRef := strings.TrimSpace(rawScopeRef)
	if scopeKind == "" && scopeRef == "" {
		return "", "", "scope_kind and scope_ref are required"
	}
	if scopeKind == "" || scopeRef == "" {
		return "", "", "scope_kind and scope_ref must be provided together"
	}
	switch scopeKind {
	case "city", "rig":
		return scopeKind, scopeRef, ""
	default:
		return "", "", "scope_kind must be 'city' or 'rig'"
	}
}

func workflowRootScope(root beads.Bead) (string, string) {
	scopeKind := strings.TrimSpace(root.Metadata["gc.scope_kind"])
	scopeRef := strings.TrimSpace(root.Metadata["gc.scope_ref"])
	if scopeKind == "" || scopeRef == "" {
		return "", ""
	}
	return scopeKind, scopeRef
}

func buildWorkflowGraph(
	root beads.Bead,
	workflowBeads []beads.Bead,
	beadIndex map[string]beads.Bead,
	store beads.Store,
	sessionIndex map[string]workflowSessionRef,
) ([]workflowDepResponse, []workflowDepResponse, []logicalNodeResponse, []scopeGroupResponse, bool) {
	logicalGroups := make(map[string]*logicalGroup, len(workflowBeads))
	logicalIDForBead := make(map[string]string, len(workflowBeads))

	for idx, bead := range workflowBeads {
		key := logicalGroupID(workflowBeads, bead)
		logicalIDForBead[bead.ID] = key
		group := logicalGroups[key]
		if group == nil {
			group = &logicalGroup{id: key, order: idx}
			logicalGroups[key] = group
		}
		group.members = append(group.members, bead)
		if bead.ID == key {
			beadCopy := bead
			group.base = &beadCopy
		}
	}

	workflowDeps, logicalDeps, partial := collectWorkflowDeps(store, beadIndex, logicalIDForBead)
	nodes := buildLogicalNodes(root, workflowBeads, logicalGroups, sessionIndex)
	scopeGroups := buildScopeGroups(nodes, workflowBeads)

	return workflowDeps, logicalDeps, nodes, scopeGroups, partial
}

func collectWorkflowDeps(store beads.Store, beadIndex map[string]beads.Bead, logicalIDForBead map[string]string) ([]workflowDepResponse, []workflowDepResponse, bool) {
	workflowDeps := make([]workflowDepResponse, 0)
	logicalDeps := make([]workflowDepResponse, 0)
	seenWorkflow := map[string]bool{}
	seenLogical := map[string]bool{}
	partial := false

	for beadID := range beadIndex {
		deps, err := store.DepList(beadID, "down")
		if err != nil {
			partial = true
			continue
		}
		for _, dep := range deps {
			if _, ok := beadIndex[dep.DependsOnID]; !ok {
				continue
			}
			edge := workflowDepResponse{
				From: dep.DependsOnID,
				To:   dep.IssueID,
				Kind: dep.Type,
			}
			workflowKey := edge.From + "|" + edge.To + "|" + edge.Kind
			if !seenWorkflow[workflowKey] {
				workflowDeps = append(workflowDeps, edge)
				seenWorkflow[workflowKey] = true
			}

			fromLogical := logicalIDForBead[dep.DependsOnID]
			toLogical := logicalIDForBead[dep.IssueID]
			if fromLogical == "" || toLogical == "" || fromLogical == toLogical {
				continue
			}
			logicalEdge := workflowDepResponse{
				From: fromLogical,
				To:   toLogical,
				Kind: dep.Type,
			}
			logicalKey := logicalEdge.From + "|" + logicalEdge.To + "|" + logicalEdge.Kind
			if !seenLogical[logicalKey] {
				logicalDeps = append(logicalDeps, logicalEdge)
				seenLogical[logicalKey] = true
			}
		}
	}

	return workflowDeps, logicalDeps, partial
}

func prefetchedDepsForWorkflowBeads(workflowBeads []beads.Bead) (map[string][]beads.Dep, bool) {
	depMap := make(map[string][]beads.Dep)
	hasPrefetchedDeps := false

	for _, bead := range workflowBeads {
		if bead.Dependencies == nil {
			continue
		}
		hasPrefetchedDeps = true
		if len(bead.Dependencies) == 0 {
			depMap[bead.ID] = nil
			continue
		}
		depMap[bead.ID] = append([]beads.Dep(nil), bead.Dependencies...)
	}

	return depMap, hasPrefetchedDeps
}

func buildLogicalNodes(root beads.Bead, workflowBeads []beads.Bead, groups map[string]*logicalGroup, sessionIndex map[string]workflowSessionRef) []logicalNodeResponse {
	ordered := make([]*logicalGroup, 0, len(groups))
	for _, group := range groups {
		ordered = append(ordered, group)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].order < ordered[j].order
	})

	nodes := make([]logicalNodeResponse, 0, len(ordered))
	for _, group := range ordered {
		base := logicalBaseBead(group)
		current := logicalCurrentBead(group, base)
		scopeRef := logicalScopeRef(workflowBeads, root.ID, base, current)
		attempts, attemptCount, activeAttempt, attemptBadge := buildLogicalAttempts(group, base)
		metaSource := current
		if base.ID != "" {
			metaSource = base
		}

		node := logicalNodeResponse{
			ID:            group.id,
			Title:         logicalTitle(base, current),
			Kind:          logicalKind(base, current),
			Status:        logicalStatus(base, current),
			ScopeRef:      scopeRef,
			CurrentBeadID: current.ID,
			AttemptBadge:  attemptBadge,
			Attempts:      attempts,
			Metadata:      stringMapToAny(metaSource.Metadata),
			SessionLink:   sessionLinkFor(current, sessionIndex),
		}
		if current.ID == "" && base.ID == root.ID {
			node.CurrentBeadID = root.ID
		}
		if attemptCount > 0 {
			node.AttemptCount = &attemptCount
		}
		if activeAttempt > 0 {
			node.ActiveAttempt = &activeAttempt
		}
		nodes = append(nodes, node)
	}

	return nodes
}

func buildLogicalAttempts(group *logicalGroup, base beads.Bead) ([]logicalAttemptResponse, int, int, string) {
	attemptBeads := make(map[int][]beads.Bead)
	maxAttempts := 0
	for _, bead := range group.members {
		if attempt := workflowAttemptValue(bead); attempt > 0 {
			attemptBeads[attempt] = append(attemptBeads[attempt], bead)
		}
		if value := metadataInt(bead.Metadata, "gc.max_attempts"); value > maxAttempts {
			maxAttempts = value
		}
	}
	if len(attemptBeads) == 0 {
		return nil, 0, 0, ""
	}

	attemptNums := make([]int, 0, len(attemptBeads))
	for attempt := range attemptBeads {
		attemptNums = append(attemptNums, attempt)
	}
	sort.Ints(attemptNums)

	attempts := make([]logicalAttemptResponse, 0, len(attemptNums))
	baseStatus := workflowStatus(base)
	baseTerminal := isTerminalWorkflowStatus(baseStatus)
	activeAttempt := 0
	displayAttempt := 0
	for idx, attempt := range attemptNums {
		beadsForAttempt := attemptBeads[attempt]
		primary := preferredAttemptBead(beadsForAttempt)
		status := aggregateAttemptStatus(beadsForAttempt)
		if baseTerminal && idx == len(attemptNums)-1 {
			status = baseStatus
		}
		if !baseTerminal && (status == "active" || status == "pending") {
			activeAttempt = attempt
		}
		displayAttempt = attempt

		summary := map[string]any{
			"bead_ids": attemptBeadIDs(beadsForAttempt),
			"kinds":    attemptKinds(beadsForAttempt),
		}
		entry := logicalAttemptResponse{
			BeadID:  primary.ID,
			Status:  status,
			Attempt: attempt,
			Summary: summary,
		}
		if !primary.CreatedAt.IsZero() {
			entry.StartedAt = primary.CreatedAt.Format(time.RFC3339)
		}
		attempts = append(attempts, entry)
	}

	if displayAttempt == 0 {
		return attempts, len(attemptNums), 0, ""
	}
	if maxAttempts > 0 {
		return attempts, len(attemptNums), activeAttempt, strconv.Itoa(displayAttempt) + "/" + strconv.Itoa(maxAttempts)
	}
	return attempts, len(attemptNums), activeAttempt, strconv.Itoa(displayAttempt)
}

func buildScopeGroups(nodes []logicalNodeResponse, workflowBeads []beads.Bead) []scopeGroupResponse {
	membersByScope := make(map[string][]string)
	for _, node := range nodes {
		scopeRef := strings.TrimSpace(node.ScopeRef)
		if scopeRef == "" {
			continue
		}
		members := membersByScope[scopeRef]
		if !containsString(members, node.ID) {
			membersByScope[scopeRef] = append(members, node.ID)
		}
	}

	scopeRefs := make([]string, 0, len(membersByScope))
	for scopeRef := range membersByScope {
		scopeRefs = append(scopeRefs, scopeRef)
	}
	sort.Strings(scopeRefs)

	scopeGroups := make([]scopeGroupResponse, 0, len(scopeRefs))
	for _, scopeRef := range scopeRefs {
		scopeGroups = append(scopeGroups, scopeGroupResponse{
			ScopeRef:             scopeRef,
			Label:                scopeGroupLabel(scopeRef, workflowBeads),
			MemberLogicalNodeIDs: membersByScope[scopeRef],
		})
	}
	return scopeGroups
}

func scopeGroupLabel(scopeRef string, workflowBeads []beads.Bead) string {
	for _, bead := range workflowBeads {
		if strings.TrimSpace(bead.Metadata["gc.kind"]) != "scope" {
			continue
		}
		if matchesScopeRef(bead, scopeRef) {
			return bead.Title
		}
	}
	return scopeRef
}

func logicalBaseBead(group *logicalGroup) beads.Bead {
	if group.base != nil {
		return *group.base
	}
	if len(group.members) == 0 {
		return beads.Bead{}
	}
	return group.members[0]
}

func logicalCurrentBead(group *logicalGroup, base beads.Bead) beads.Bead {
	if isTerminalWorkflowStatus(workflowStatus(base)) {
		return base
	}

	candidates := make([]beads.Bead, 0, len(group.members))
	for _, bead := range group.members {
		if bead.ID == base.ID {
			continue
		}
		candidates = append(candidates, bead)
	}
	if len(candidates) == 0 {
		return base
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		iAttempt := workflowAttemptValue(candidates[i])
		jAttempt := workflowAttemptValue(candidates[j])
		if iAttempt != jAttempt {
			return iAttempt > jAttempt
		}
		iRank := statusRank(workflowStatus(candidates[i]))
		jRank := statusRank(workflowStatus(candidates[j]))
		if iRank != jRank {
			return iRank > jRank
		}
		return preferredKindRank(candidates[i]) < preferredKindRank(candidates[j])
	})
	return candidates[0]
}

func logicalGroupID(workflowBeads []beads.Bead, bead beads.Bead) string {
	if logicalID := strings.TrimSpace(bead.Metadata["gc.logical_bead_id"]); logicalID != "" {
		return logicalID
	}
	switch strings.TrimSpace(bead.Metadata["gc.kind"]) {
	case "ralph", "retry":
		return bead.ID
	}
	for _, logicalStepRef := range logicalStepRefCandidatesForBead(bead) {
		for _, candidate := range workflowBeads {
			switch strings.TrimSpace(candidate.Metadata["gc.kind"]) {
			case "ralph", "retry":
			default:
				continue
			}
			candidateRef := strings.TrimSpace(candidate.Metadata["gc.step_ref"])
			if candidateRef == "" {
				candidateRef = strings.TrimSpace(candidate.Ref)
			}
			if candidateRef == logicalStepRef {
				return candidate.ID
			}
		}
	}
	return bead.ID
}

func logicalTitle(base, current beads.Bead) string {
	if strings.TrimSpace(base.Title) != "" {
		return base.Title
	}
	return current.Title
}

func logicalKind(base, current beads.Bead) string {
	if original := strings.TrimSpace(base.Metadata["gc.original_kind"]); original != "" {
		return original
	}
	if kind := workflowKind(base); kind != "" {
		return kind
	}
	return workflowKind(current)
}

func logicalScopeRef(workflowBeads []beads.Bead, rootID string, base, current beads.Bead) string {
	scopeRef := strings.TrimSpace(base.Metadata["gc.scope_ref"])
	if scopeRef == "" {
		scopeRef = strings.TrimSpace(current.Metadata["gc.scope_ref"])
	}
	if scopeRef == "" {
		return ""
	}
	if body, ok := findScopeBody(workflowBeads, rootID, scopeRef); ok {
		if stepRef := strings.TrimSpace(body.Metadata["gc.step_ref"]); stepRef != "" {
			return stepRef
		}
		if ref := strings.TrimSpace(body.Ref); ref != "" {
			return ref
		}
	}
	return scopeRef
}

func logicalStatus(base, current beads.Bead) string {
	if status := workflowStatus(base); isTerminalWorkflowStatus(status) {
		return status
	}
	if status := workflowStatus(current); status != "" {
		return status
	}
	return workflowStatus(base)
}

func aggregateAttemptStatus(beadsForAttempt []beads.Bead) string {
	best := ""
	bestRank := -1
	for _, bead := range beadsForAttempt {
		status := workflowStatus(bead)
		rank := statusRank(status)
		if rank > bestRank {
			best = status
			bestRank = rank
		}
	}
	return best
}

func statusRank(status string) int {
	switch status {
	case "active":
		return 5
	case "pending":
		return 4
	case "failed":
		return 3
	case "skipped":
		return 2
	case "completed":
		return 1
	default:
		return 0
	}
}

func isTerminalWorkflowStatus(status string) bool {
	switch status {
	case "completed", "skipped", "failed":
		return true
	default:
		return false
	}
}

func preferredAttemptBead(beadsForAttempt []beads.Bead) beads.Bead {
	if len(beadsForAttempt) == 0 {
		return beads.Bead{}
	}
	sorted := append([]beads.Bead(nil), beadsForAttempt...)
	sort.SliceStable(sorted, func(i, j int) bool {
		iRank := preferredKindRank(sorted[i])
		jRank := preferredKindRank(sorted[j])
		if iRank != jRank {
			return iRank < jRank
		}
		return sorted[i].CreatedAt.Before(sorted[j].CreatedAt)
	})
	return sorted[0]
}

func preferredKindRank(bead beads.Bead) int {
	switch strings.TrimSpace(bead.Metadata["gc.kind"]) {
	case "run", "retry-run":
		return 0
	case "scope":
		return 1
	case "check", "retry-eval":
		return 2
	case "scope-check":
		return 3
	default:
		return 4
	}
}

func attemptBeadIDs(beadsForAttempt []beads.Bead) []string {
	result := make([]string, 0, len(beadsForAttempt))
	for _, bead := range beadsForAttempt {
		result = append(result, bead.ID)
	}
	sort.Strings(result)
	return result
}

func attemptKinds(beadsForAttempt []beads.Bead) []string {
	result := make([]string, 0, len(beadsForAttempt))
	for _, bead := range beadsForAttempt {
		kind := workflowKind(bead)
		if kind == "" || containsString(result, kind) {
			continue
		}
		result = append(result, kind)
	}
	sort.Strings(result)
	return result
}

func workflowAttempt(bead beads.Bead) *int {
	if attempt := workflowAttemptValue(bead); attempt > 0 {
		return &attempt
	}
	return nil
}

func workflowAttemptValue(bead beads.Bead) int {
	return metadataInt(bead.Metadata, "gc.attempt")
}

func logicalStepRefForAttemptBead(bead beads.Bead) string {
	stepRef := strings.TrimSpace(bead.Metadata["gc.step_ref"])
	if stepRef == "" {
		stepRef = strings.TrimSpace(bead.Ref)
	}
	if stepRef == "" {
		return ""
	}
	kind := strings.TrimSpace(bead.Metadata["gc.kind"])
	normalized := stepRef
	if kind == "scope-check" && strings.HasSuffix(normalized, "-scope-check") {
		normalized = strings.TrimSuffix(normalized, "-scope-check")
	}
	if trimmed, ok := trimAttemptStepRefForKind(normalized, kind, strings.TrimSpace(bead.Metadata["gc.attempt"])); ok {
		return trimmed
	}
	if trimmed, ok := trimRightmostAttemptStepRef(normalized); ok {
		return trimmed
	}
	if normalized != stepRef || kind == "scope-check" {
		return normalized
	}
	return ""
}

func logicalStepRefCandidatesForBead(bead beads.Bead) []string {
	candidates := make([]string, 0, 2)
	if target := scopeCheckControlledStepRef(bead); target != "" {
		candidates = append(candidates, target)
	}
	if logicalStepRef := logicalStepRefForAttemptBead(bead); logicalStepRef != "" && !containsString(candidates, logicalStepRef) {
		candidates = append(candidates, logicalStepRef)
	}
	return candidates
}

func scopeCheckControlledStepRef(bead beads.Bead) string {
	if strings.TrimSpace(bead.Metadata["gc.kind"]) != "scope-check" {
		return ""
	}
	stepRef := strings.TrimSpace(bead.Metadata["gc.step_ref"])
	if stepRef == "" {
		stepRef = strings.TrimSpace(bead.Ref)
	}
	if stepRef == "" || !strings.HasSuffix(stepRef, "-scope-check") {
		return ""
	}
	return strings.TrimSuffix(stepRef, "-scope-check")
}

func trimAttemptStepRefSuffix(stepRef, suffix string) (string, bool) {
	if suffix == "" || !strings.HasSuffix(stepRef, suffix) {
		return "", false
	}
	return strings.TrimSuffix(stepRef, suffix), true
}

func trimAttemptStepRefForKind(stepRef, kind, attempt string) (string, bool) {
	if attempt == "" {
		return "", false
	}
	switch kind {
	case "run", "scope", "retry-run":
		return trimAttemptStepRefSuffix(stepRef, ".run."+attempt)
	case "check":
		return trimAttemptStepRefSuffix(stepRef, ".check."+attempt)
	case "retry-eval":
		return trimAttemptStepRefSuffix(stepRef, ".eval."+attempt)
	default:
		return "", false
	}
}

func trimRightmostAttemptStepRef(stepRef string) (string, bool) {
	best := -1
	for _, prefix := range []string{".run.", ".check.", ".eval."} {
		if idx := strings.LastIndex(stepRef, prefix); idx > best {
			best = idx
		}
	}
	if best <= 0 {
		return "", false
	}
	return stepRef[:best], true
}

func metadataInt(meta map[string]string, key string) int {
	if meta == nil {
		return 0
	}
	value := strings.TrimSpace(meta[key])
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}

func workflowKind(bead beads.Bead) string {
	if bead.Metadata != nil {
		if kind := strings.TrimSpace(bead.Metadata["gc.kind"]); kind != "" {
			return kind
		}
	}
	return strings.TrimSpace(bead.Type)
}

func workflowStatus(bead beads.Bead) string {
	outcome := strings.TrimSpace(bead.Metadata["gc.outcome"])
	hasAssignment := strings.TrimSpace(bead.Assignee) != ""
	switch strings.TrimSpace(bead.Status) {
	case "closed":
		switch outcome {
		case "fail":
			return "failed"
		case "skipped":
			return "skipped"
		}
		return "completed"
	case "in_progress":
		if hasAssignment {
			return "active"
		}
		return "pending"
	case "open":
		return "pending"
	default:
		switch outcome {
		case "fail":
			return "failed"
		case "skipped":
			return "skipped"
		}
		return strings.TrimSpace(bead.Status)
	}
}

func sessionLinkFor(bead beads.Bead, sessionIndex map[string]workflowSessionRef) *sessionLinkResponse {
	sessionName := strings.TrimSpace(bead.Assignee)
	if sessionName == "" {
		return nil
	}

	sessionID := sessionName
	projectID := "city"
	for _, key := range []string{sessionName} {
		if key == "" {
			continue
		}
		sessionRef, ok := sessionIndex[key]
		if !ok {
			continue
		}
		if sessionRef.sessionName != "" {
			sessionName = sessionRef.sessionName
		}
		sessionID = sessionRef.bead.ID
		if value := strings.TrimSpace(sessionRef.bead.Metadata["mc_project_id"]); value != "" {
			projectID = value
		}
		break
	}
	if sessionID == "" {
		sessionID = sessionName
	}

	return &sessionLinkResponse{
		ProjectID:   projectID,
		SessionID:   sessionID,
		SessionName: sessionName,
		Assignee:    strings.TrimSpace(bead.Assignee),
	}
}

func (s *Server) workflowSessionIndex() map[string]workflowSessionRef {
	index := make(map[string]workflowSessionRef)
	store := s.state.CityBeadStore()
	if store == nil {
		return index
	}

	sessions, err := store.ListByLabel("gc:session", 0)
	if err != nil {
		sessions, err = store.List()
		if err != nil {
			return index
		}
	}

	for _, bead := range sessions {
		sessionName := strings.TrimSpace(bead.Metadata["session_name"])
		if sessionName == "" {
			continue
		}
		addWorkflowSessionRef(index, sessionName, bead)
		addWorkflowSessionRef(index, bead.Metadata["agent_name"], bead)
		addWorkflowSessionRef(index, bead.Metadata["alias"], bead)
	}
	return index
}

func addWorkflowSessionRef(index map[string]workflowSessionRef, key string, bead beads.Bead) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	existing, ok := index[key]
	if ok && !(existing.bead.Status == "closed" && bead.Status != "closed") {
		return
	}
	index[key] = workflowSessionRef{
		bead:        bead,
		sessionName: strings.TrimSpace(bead.Metadata["session_name"]),
	}
}

func workflowStores(state State) []workflowStoreInfo {
	beadStores := state.BeadStores()
	stores := make([]workflowStoreInfo, 0, len(beadStores)+1)
	cityName := workflowCityScopeRef(state.CityName())

	if cityStore := state.CityBeadStore(); cityStore != nil {
		stores = append(stores, workflowStoreInfo{
			ref:       "city:" + cityName,
			scopeKind: "city",
			scopeRef:  cityName,
			store:     cityStore,
		})
	}

	for _, rigName := range sortedRigNames(beadStores) {
		if rigName == cityName {
			continue
		}
		store := state.BeadStore(rigName)
		if store == nil {
			continue
		}
		stores = append(stores, workflowStoreInfo{
			ref:       "rig:" + rigName,
			scopeKind: "rig",
			scopeRef:  rigName,
			store:     store,
		})
	}

	return stores
}

func workflowStoreByRef(state State, ref string) (workflowStoreInfo, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return workflowStoreInfo{}, false
	}

	kind, scopeRef, ok := strings.Cut(ref, ":")
	if !ok {
		return workflowStoreInfo{}, false
	}
	scopeRef = strings.TrimSpace(scopeRef)
	if scopeRef == "" {
		return workflowStoreInfo{}, false
	}

	switch strings.TrimSpace(kind) {
	case "city":
		cityStore := state.CityBeadStore()
		cityName := workflowCityScopeRef(state.CityName())
		if cityStore == nil || scopeRef != cityName {
			return workflowStoreInfo{}, false
		}
		return workflowStoreInfo{
			ref:       "city:" + cityName,
			scopeKind: "city",
			scopeRef:  cityName,
			store:     cityStore,
		}, true
	case "rig":
		store := state.BeadStore(scopeRef)
		if store == nil {
			return workflowStoreInfo{}, false
		}
		return workflowStoreInfo{
			ref:       "rig:" + scopeRef,
			scopeKind: "rig",
			scopeRef:  scopeRef,
			store:     store,
		}, true
	}
	return workflowStoreInfo{}, false
}

func (s *Server) workflowStores() []workflowStoreInfo {
	return workflowStores(s.state)
}

func workflowCityScopeRef(cityName string) string {
	cityName = strings.TrimSpace(cityName)
	if cityName == "" {
		return "city"
	}
	return cityName
}

func matchesScopeRef(bead beads.Bead, scopeRef string) bool {
	if scopeRef == "" {
		return false
	}
	if bead.Metadata["gc.scope_ref"] == scopeRef {
		return true
	}
	stepRef := bead.Metadata["gc.step_ref"]
	return stepRef == scopeRef || strings.HasSuffix(stepRef, "."+scopeRef)
}

func findScopeBody(all []beads.Bead, rootID, scopeRef string) (beads.Bead, bool) {
	for _, bead := range all {
		if bead.Metadata["gc.root_bead_id"] != rootID {
			continue
		}
		if strings.TrimSpace(bead.Metadata["gc.kind"]) != "scope" {
			continue
		}
		if matchesScopeRef(bead, scopeRef) {
			return bead, true
		}
	}
	return beads.Bead{}, false
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func stringMapToAny(src map[string]string) map[string]any {
	if len(src) == 0 {
		return map[string]any{}
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
