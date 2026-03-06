package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

// APIFetcher implements ConvoyFetcher by calling the GC API server.
type APIFetcher struct {
	baseURL  string       // e.g. "http://127.0.0.1:8080"
	cityPath string       // city directory path
	cityName string       // for display
	client   *http.Client // shared client with timeout
}

// NewAPIFetcher creates a new API-backed fetcher.
func NewAPIFetcher(baseURL, cityPath, cityName string) *APIFetcher {
	return &APIFetcher{
		baseURL:  strings.TrimRight(baseURL, "/"),
		cityPath: cityPath,
		cityName: cityName,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// --- API response types (matching internal/api JSON shapes) ---

// apiListResponse wraps list endpoint responses: {"items": [...], "total": N}
type apiListResponse struct {
	Items json.RawMessage `json:"items"`
	Total int             `json:"total"`
}

type apiAgentResponse struct {
	Name       string          `json:"name"`
	Running    bool            `json:"running"`
	Suspended  bool            `json:"suspended"`
	Rig        string          `json:"rig,omitempty"`
	Pool       string          `json:"pool,omitempty"`
	Session    *apiSessionInfo `json:"session,omitempty"`
	ActiveBead string          `json:"active_bead,omitempty"`
}

type apiSessionInfo struct {
	Name         string     `json:"name"`
	LastActivity *time.Time `json:"last_activity,omitempty"`
	Attached     bool       `json:"attached"`
}

type apiRigResponse struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Suspended bool   `json:"suspended"`
	Prefix    string `json:"prefix,omitempty"`
}

type apiConvoyDetail struct {
	Convoy   apiBead   `json:"convoy"`
	Children []apiBead `json:"children"`
	Progress struct {
		Total  int `json:"total"`
		Closed int `json:"closed"`
	} `json:"progress"`
}

type apiBead struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Status      string    `json:"status"`
	Type        string    `json:"type"`
	CreatedAt   time.Time `json:"created_at"`
	Assignee    string    `json:"assignee,omitempty"`
	From        string    `json:"from,omitempty"`
	ParentID    string    `json:"parent_id,omitempty"`
	Description string    `json:"description,omitempty"`
	Labels      []string  `json:"labels,omitempty"`
}

type apiEvent struct {
	Seq     uint64          `json:"seq"`
	Type    string          `json:"type"`
	Ts      time.Time       `json:"ts"`
	Actor   string          `json:"actor"`
	Subject string          `json:"subject,omitempty"`
	Message string          `json:"message,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type apiMailMessage struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Subject   string    `json:"subject"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	Read      bool      `json:"read"`
	ThreadID  string    `json:"thread_id,omitempty"`
	ReplyTo   string    `json:"reply_to,omitempty"`
	Priority  int       `json:"priority,omitempty"`
}

type apiStatusResponse struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	AgentCount int    `json:"agent_count"`
	RigCount   int    `json:"rig_count"`
	Running    int    `json:"running"`
}

// --- HTTP helpers ---

// get performs a GET request and decodes the JSON response into result.
func (f *APIFetcher) get(path string, result any) error {
	resp, err := f.client.Get(f.baseURL + path)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET %s: status %d: %s", path, resp.StatusCode, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("GET %s: decode: %w", path, err)
	}
	return nil
}

// getList performs a GET request and unwraps the {"items": [...], "total": N} envelope.
func (f *APIFetcher) getList(path string, items any) error {
	var wrapper apiListResponse
	if err := f.get(path, &wrapper); err != nil {
		return err
	}
	if len(wrapper.Items) == 0 || string(wrapper.Items) == "null" {
		return nil
	}
	return json.Unmarshal(wrapper.Items, items)
}

// post performs a POST with JSON body and decodes the response.
func (f *APIFetcher) post(path string, body, result any) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("POST %s: marshal: %w", path, err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(http.MethodPost, f.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "1") // CSRF header required by API server

	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST %s: status %d: %s", path, resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("POST %s: decode: %w", path, err)
		}
	}
	return nil
}

// --- ConvoyFetcher implementation ---

// FetchRigs returns all registered rigs from the API.
func (f *APIFetcher) FetchRigs() ([]RigRow, error) {
	var rigs []apiRigResponse
	if err := f.getList("/v0/rigs", &rigs); err != nil {
		return nil, fmt.Errorf("fetching rigs: %w", err)
	}

	rows := make([]RigRow, 0, len(rigs))
	for _, r := range rigs {
		rows = append(rows, RigRow{
			Name: r.Name,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Name < rows[j].Name
	})
	return rows, nil
}

// FetchWorkers returns all running worker agents with activity data.
func (f *APIFetcher) FetchWorkers() ([]WorkerRow, error) {
	var agents []apiAgentResponse
	if err := f.getList("/v0/agents", &agents); err != nil {
		return nil, fmt.Errorf("fetching agents: %w", err)
	}

	var workers []WorkerRow
	for _, agent := range agents {
		if !agent.Running {
			continue
		}
		// Only show polecats in the workers panel.
		if !strings.Contains(agent.Name, "polecat") {
			continue
		}

		var lastActivity time.Time
		sessionName := agent.Name
		if agent.Session != nil {
			sessionName = agent.Session.Name
			if agent.Session.LastActivity != nil {
				lastActivity = *agent.Session.LastActivity
			}
		}

		activityAge := time.Duration(0)
		if !lastActivity.IsZero() {
			activityAge = time.Since(lastActivity)
		}

		issueID := agent.ActiveBead
		var issueTitle string
		if issueID != "" {
			// Try to get bead title
			var bead apiBead
			if err := f.get("/v0/bead/"+issueID, &bead); err == nil {
				issueTitle = bead.Title
			}
		}

		workStatus := calculateWorkerWorkStatus(activityAge, issueID, agent.Name,
			5*time.Minute, defaultGUPPViolationTimeout)

		// Get status hint via peek API.
		statusHint := f.getStatusHint(agent.Name)

		workers = append(workers, WorkerRow{
			Name:         agent.Name,
			Rig:          agent.Rig,
			SessionID:    sessionName,
			LastActivity: calculateActivity(lastActivity),
			IssueID:      issueID,
			IssueTitle:   issueTitle,
			WorkStatus:   workStatus,
			AgentType:    "agent",
			StatusHint:   statusHint,
		})
	}

	return workers, nil
}

// FetchDogs returns city-scoped pool agents (rig == "").
func (f *APIFetcher) FetchDogs() ([]DogRow, error) {
	var agents []apiAgentResponse
	if err := f.getList("/v0/agents", &agents); err != nil {
		return nil, nil
	}

	var rows []DogRow
	for _, agent := range agents {
		if agent.Rig != "" || agent.Pool == "" {
			continue
		}

		state := "idle"
		if agent.ActiveBead != "" {
			state = "working"
		}

		rows = append(rows, DogRow{
			Name:  agent.Name,
			State: state,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Name < rows[j].Name
	})
	return rows, nil
}

// FetchMayor returns the coordinator agent's status.
func (f *APIFetcher) FetchMayor() (*MayorStatus, error) {
	status := &MayorStatus{IsAttached: false}

	var agents []apiAgentResponse
	if err := f.getList("/v0/agents", &agents); err != nil {
		return status, nil
	}

	// Find city-scoped non-pool agent (the coordinator).
	var mayor *apiAgentResponse
	for i := range agents {
		if agents[i].Pool == "" && agents[i].Rig == "" {
			mayor = &agents[i]
			break
		}
	}
	if mayor == nil {
		return status, nil
	}

	if mayor.Session != nil {
		status.IsAttached = mayor.Session.Attached
		status.SessionName = mayor.Session.Name
		if mayor.Session.LastActivity != nil {
			age := time.Since(*mayor.Session.LastActivity)
			status.LastActivity = formatTimestamp(*mayor.Session.LastActivity)
			status.IsActive = age < 5*time.Minute
		}
	}
	if status.SessionName == "" {
		status.SessionName = mayor.Name
	}

	return status, nil
}

// FetchConvoys fetches all open convoys with progress data.
func (f *APIFetcher) FetchConvoys() ([]ConvoyRow, error) {
	// List convoys (they're beads with type=convoy)
	var convoys []apiBead
	if err := f.getList("/v0/convoys", &convoys); err != nil {
		return nil, fmt.Errorf("listing convoys: %w", err)
	}

	rows := make([]ConvoyRow, 0, len(convoys))
	for _, c := range convoys {
		if c.Status == "closed" {
			continue
		}

		// Get convoy detail with children and progress.
		var detail apiConvoyDetail
		if err := f.get("/v0/convoy/"+c.ID, &detail); err != nil {
			log.Printf("warning: skipping convoy %s: %v", c.ID, err)
			continue
		}

		row := ConvoyRow{
			ID:        c.ID,
			Title:     c.Title,
			Status:    c.Status,
			Total:     detail.Progress.Total,
			Completed: detail.Progress.Closed,
			Progress:  fmt.Sprintf("%d/%d", detail.Progress.Closed, detail.Progress.Total),
		}

		// Build tracked issues and find most recent activity.
		var mostRecentUpdated time.Time
		tracked := make([]TrackedIssue, 0, len(detail.Children))
		for _, child := range detail.Children {
			tracked = append(tracked, TrackedIssue{
				ID:       child.ID,
				Title:    child.Title,
				Status:   child.Status,
				Assignee: child.Assignee,
			})
			if child.CreatedAt.After(mostRecentUpdated) {
				mostRecentUpdated = child.CreatedAt
			}
		}
		row.TrackedIssues = tracked

		if !mostRecentUpdated.IsZero() {
			row.LastActivity = calculateActivity(mostRecentUpdated)
		} else {
			row.LastActivity = ActivityInfo{
				Display:    "idle",
				ColorClass: colorUnknown,
			}
		}

		row.WorkStatus = calculateWorkStatus(row.Completed, row.Total, row.LastActivity.ColorClass)
		rows = append(rows, row)
	}

	return rows, nil
}

// FetchMail fetches recent mail messages from the API.
func (f *APIFetcher) FetchMail() ([]MailRow, error) {
	var messages []apiMailMessage
	if err := f.getList("/v0/mail", &messages); err != nil {
		return nil, fmt.Errorf("fetching mail: %w", err)
	}

	rows := make([]MailRow, 0, len(messages))
	for _, m := range messages {
		var age string
		var sortKey int64
		if !m.CreatedAt.IsZero() {
			age = formatTimestamp(m.CreatedAt)
			sortKey = m.CreatedAt.Unix()
		}

		priorityStr := "normal"
		switch m.Priority {
		case 0:
			priorityStr = "urgent"
		case 1:
			priorityStr = "high"
		case 2:
			priorityStr = "normal"
		case 3, 4:
			priorityStr = "low"
		}

		rows = append(rows, MailRow{
			ID:        m.ID,
			From:      formatAgentAddress(m.From),
			FromRaw:   m.From,
			To:        formatAgentAddress(m.To),
			Subject:   m.Subject,
			Timestamp: m.CreatedAt.Format("15:04"),
			Age:       age,
			Priority:  priorityStr,
			Type:      "notification",
			Read:      m.Read,
			SortKey:   sortKey,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].SortKey > rows[j].SortKey
	})
	return rows, nil
}

// FetchHooks returns hooked beads (work pinned to agents).
func (f *APIFetcher) FetchHooks() ([]HookRow, error) {
	var beadList []apiBead
	if err := f.getList("/v0/beads?status=hooked&limit=1000", &beadList); err != nil {
		return nil, nil
	}

	var rows []HookRow
	for _, b := range beadList {
		row := HookRow{
			ID:       b.ID,
			Title:    b.Title,
			Assignee: b.Assignee,
			Agent:    formatAgentAddress(b.Assignee),
		}

		if !b.CreatedAt.IsZero() {
			age := time.Since(b.CreatedAt)
			row.Age = formatTimestamp(b.CreatedAt)
			row.IsStale = age > time.Hour
		}

		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].IsStale != rows[j].IsStale {
			return rows[i].IsStale
		}
		return rows[i].Age > rows[j].Age
	})
	return rows, nil
}

// FetchIssues returns open and hooked issues (the backlog).
func (f *APIFetcher) FetchIssues() ([]IssueRow, error) {
	var allBeads []apiBead

	// Fetch open issues.
	var openBeads []apiBead
	if err := f.getList("/v0/beads?status=open&limit=50", &openBeads); err == nil {
		allBeads = append(allBeads, openBeads...)
	}

	// Fetch hooked issues (in progress).
	var hookedBeads []apiBead
	if err := f.getList("/v0/beads?status=hooked&limit=50", &hookedBeads); err == nil {
		allBeads = append(allBeads, hookedBeads...)
	}

	var rows []IssueRow
	for _, b := range allBeads {
		// Skip internal types.
		isInternal := false
		switch b.Type {
		case "message", "convoy", "queue", "merge-request", "wisp", "agent":
			isInternal = true
		}
		for _, l := range b.Labels {
			switch l {
			case "gc:message", "gc:convoy", "gc:queue", "gc:merge-request", "gc:wisp", "gc:agent":
				isInternal = true
			}
		}
		if isInternal {
			continue
		}

		row := IssueRow{
			ID:    b.ID,
			Title: b.Title,
			Type:  b.Type,
		}

		var displayLabels []string
		for _, label := range b.Labels {
			if !strings.HasPrefix(label, "gc:") && !strings.HasPrefix(label, "internal:") {
				displayLabels = append(displayLabels, label)
			}
		}
		if len(displayLabels) > 0 {
			row.Labels = strings.Join(displayLabels, ", ")
			if len(row.Labels) > 25 {
				row.Labels = row.Labels[:22] + "..."
			}
		}

		if !b.CreatedAt.IsZero() {
			row.Age = formatTimestamp(b.CreatedAt)
		}

		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		pi, pj := rows[i].Priority, rows[j].Priority
		if pi == 0 {
			pi = 5
		}
		if pj == 0 {
			pj = 5
		}
		if pi != pj {
			return pi < pj
		}
		return rows[i].Age > rows[j].Age
	})
	return rows, nil
}

// FetchEscalations returns open escalations needing attention.
func (f *APIFetcher) FetchEscalations() ([]EscalationRow, error) {
	var beadList []apiBead
	if err := f.getList("/v0/beads?label=gc:escalation&status=open", &beadList); err != nil {
		return nil, nil
	}

	var rows []EscalationRow
	for _, b := range beadList {
		row := EscalationRow{
			ID:          b.ID,
			Title:       b.Title,
			EscalatedBy: formatAgentAddress(b.From),
			Severity:    "medium",
		}

		for _, label := range b.Labels {
			if strings.HasPrefix(label, "severity:") {
				row.Severity = strings.TrimPrefix(label, "severity:")
			}
			if label == "acked" {
				row.Acked = true
			}
		}

		if !b.CreatedAt.IsZero() {
			row.Age = formatTimestamp(b.CreatedAt)
		}

		rows = append(rows, row)
	}

	severityOrder := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
	sort.Slice(rows, func(i, j int) bool {
		si, sj := severityOrder[rows[i].Severity], severityOrder[rows[j].Severity]
		return si < sj
	})
	return rows, nil
}

// FetchHealth returns system health from the API.
func (f *APIFetcher) FetchHealth() (*HealthRow, error) {
	row := &HealthRow{}

	var status apiStatusResponse
	if err := f.get("/v0/status", &status); err != nil {
		row.DeaconHeartbeat = "no heartbeat"
		return row, nil
	}

	// Count healthy/unhealthy agents.
	var agents []apiAgentResponse
	if err := f.getList("/v0/agents", &agents); err == nil {
		for _, agent := range agents {
			if agent.Running {
				row.HealthyAgents++
			} else {
				row.UnhealthyAgents++
			}
		}
	}

	row.DeaconHeartbeat = "active"
	row.HeartbeatFresh = true
	return row, nil
}

// FetchQueues returns work queues.
func (f *APIFetcher) FetchQueues() ([]QueueRow, error) {
	var beadList []apiBead
	if err := f.getList("/v0/beads?label=gc:queue", &beadList); err != nil {
		return nil, nil
	}

	var rows []QueueRow
	for _, b := range beadList {
		row := QueueRow{
			Name:   b.Title,
			Status: b.Status,
		}

		// Parse counts from description.
		for _, line := range strings.Split(b.Description, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "available_count:") {
				_, _ = fmt.Sscanf(line, "available_count: %d", &row.Available)
			} else if strings.HasPrefix(line, "processing_count:") {
				_, _ = fmt.Sscanf(line, "processing_count: %d", &row.Processing)
			} else if strings.HasPrefix(line, "completed_count:") {
				_, _ = fmt.Sscanf(line, "completed_count: %d", &row.Completed)
			} else if strings.HasPrefix(line, "failed_count:") {
				_, _ = fmt.Sscanf(line, "failed_count: %d", &row.Failed)
			} else if strings.HasPrefix(line, "status:") {
				var s string
				_, _ = fmt.Sscanf(line, "status: %s", &s)
				if s != "" {
					row.Status = s
				}
			}
		}

		rows = append(rows, row)
	}
	return rows, nil
}

// FetchActivity returns recent events from the API.
func (f *APIFetcher) FetchActivity() ([]ActivityRow, error) {
	var events []apiEvent
	if err := f.getList("/v0/events?since=1h", &events); err != nil {
		return nil, nil
	}

	// Take last 50 events.
	start := 0
	if len(events) > 50 {
		start = len(events) - 50
	}

	var rows []ActivityRow
	for i := len(events) - 1; i >= start; i-- {
		event := events[i]

		// Parse payload for event summary.
		var payload map[string]interface{}
		if len(event.Payload) > 0 {
			_ = json.Unmarshal(event.Payload, &payload)
		}

		row := ActivityRow{
			Type:         event.Type,
			Category:     eventCategory(event.Type),
			Actor:        formatAgentAddress(event.Actor),
			Rig:          extractRig(event.Actor),
			Icon:         eventIcon(event.Type),
			RawTimestamp: event.Ts.Format(time.RFC3339),
		}

		if !event.Ts.IsZero() {
			row.Time = formatTimestamp(event.Ts)
		}

		row.Summary = eventSummary(event.Type, event.Actor, payload)
		rows = append(rows, row)
	}

	return rows, nil
}

// FetchMergeQueue fetches open PRs from registered rigs via the API + gh CLI.
func (f *APIFetcher) FetchMergeQueue() ([]MergeQueueRow, error) {
	// Get rig paths from the API.
	var rigs []apiRigResponse
	if err := f.getList("/v0/rigs", &rigs); err != nil {
		return nil, fmt.Errorf("fetching rigs for merge queue: %w", err)
	}

	ghTimeout := 10 * time.Second

	var result []MergeQueueRow
	for _, rig := range rigs {
		if rig.Path == "" {
			continue
		}
		repoPath := detectRepoFromPath(rig.Path, ghTimeout)
		if repoPath == "" {
			continue
		}

		prs, err := fetchPRsForRepo(repoPath, rig.Name, ghTimeout)
		if err != nil {
			continue
		}
		result = append(result, prs...)
	}

	return result, nil
}

// getStatusHint fetches the last non-empty line from an agent's peek output.
func (f *APIFetcher) getStatusHint(agentName string) string {
	var peekResp struct {
		Output string `json:"output"`
	}
	if err := f.get("/v0/agent/"+agentName+"/peek", &peekResp); err != nil {
		return ""
	}
	if peekResp.Output == "" {
		return ""
	}

	lines := strings.Split(peekResp.Output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			if len(line) > 60 {
				line = line[:57] + "..."
			}
			return line
		}
	}
	return ""
}

// detectRepoFromPath tries to extract owner/repo from a git working directory.
func detectRepoFromPath(path string, timeout time.Duration) string {
	stdout, err := runCmd(timeout, "git", "-C", path, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return gitURLToRepoPath(strings.TrimSpace(stdout.String()))
}

// fetchPRsForRepo fetches open PRs for a single repo via gh CLI.
func fetchPRsForRepo(repoFull, repoShort string, timeout time.Duration) ([]MergeQueueRow, error) {
	stdout, err := runCmd(timeout, "gh", "pr", "list",
		"--repo", repoFull,
		"--state", "open",
		"--json", "number,title,url,mergeable,statusCheckRollup")
	if err != nil {
		return nil, fmt.Errorf("fetching PRs for %s: %w", repoFull, err)
	}

	var prs []prResponse
	if err := json.Unmarshal(stdout.Bytes(), &prs); err != nil {
		return nil, fmt.Errorf("parsing PRs for %s: %w", repoFull, err)
	}

	result := make([]MergeQueueRow, 0, len(prs))
	for _, pr := range prs {
		row := MergeQueueRow{
			Number: pr.Number,
			Repo:   repoShort,
			Title:  pr.Title,
			URL:    pr.URL,
		}
		row.CIStatus = determineCIStatus(pr.StatusCheckRollup)
		row.Mergeable = determineMergeableStatus(pr.Mergeable)
		row.ColorClass = determineColorClass(row.CIStatus, row.Mergeable)
		result = append(result, row)
	}

	return result, nil
}
