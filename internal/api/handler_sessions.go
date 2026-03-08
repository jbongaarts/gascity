package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/session"
)

// sessionResponse is the JSON representation of a chat session.
type sessionResponse struct {
	ID          string `json:"id"`
	Template    string `json:"template"`
	State       string `json:"state"`
	Reason      string `json:"reason,omitempty"`
	Title       string `json:"title"`
	Provider    string `json:"provider"`
	SessionName string `json:"session_name"`
	CreatedAt   string `json:"created_at"`
	LastActive  string `json:"last_active,omitempty"`
	Attached    bool   `json:"attached"`
}

func sessionToResponse(info session.Info) sessionResponse {
	r := sessionResponse{
		ID:          info.ID,
		Template:    info.Template,
		State:       string(info.State),
		Title:       info.Title,
		Provider:    info.Provider,
		SessionName: info.SessionName,
		CreatedAt:   info.CreatedAt.Format(time.RFC3339),
		Attached:    info.Attached,
	}
	if !info.LastActive.IsZero() {
		r.LastActive = info.LastActive.Format(time.RFC3339)
	}
	return r
}

// sessionResponseWithReason builds a session response that includes the
// reason field derived from bead metadata. If the bead is nil (not found
// in the index), the reason is omitted.
func sessionResponseWithReason(info session.Info, b *beads.Bead) sessionResponse {
	r := sessionToResponse(info)
	if b == nil || info.State == "" {
		return r
	}
	// Surface bead-persisted sleep/hold/quarantine reason.
	if sr := b.Metadata["sleep_reason"]; sr != "" {
		r.Reason = sr
	} else if b.Metadata["quarantined_until"] != "" {
		r.Reason = "quarantine"
	} else if b.Metadata["held_until"] != "" {
		r.Reason = "user-hold"
	}
	return r
}

// writeResolveError maps session.ResolveSessionID errors to HTTP responses.
func writeResolveError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrAmbiguous):
		writeError(w, http.StatusConflict, "ambiguous", err.Error())
	case errors.Is(err, session.ErrSessionNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

func (s *Server) handleSessionList(w http.ResponseWriter, r *http.Request) {
	store := s.state.CityBeadStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "no bead store configured")
		return
	}
	sp := s.state.SessionProvider()
	mgr := session.NewManager(store, sp)

	q := r.URL.Query()
	stateFilter := q.Get("state")
	templateFilter := q.Get("template")

	sessions, err := mgr.List(stateFilter, templateFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	// Build bead index for reason enrichment.
	beadIndex := make(map[string]*beads.Bead)
	if all, err := store.ListByLabel(session.LabelSession, 0); err == nil {
		for i := range all {
			beadIndex[all[i].ID] = &all[i]
		}
	}

	items := make([]sessionResponse, len(sessions))
	for i, sess := range sessions {
		items[i] = sessionResponseWithReason(sess, beadIndex[sess.ID])
	}
	writeJSON(w, http.StatusOK, listResponse{Items: items, Total: len(items)})
}

func (s *Server) handleSessionGet(w http.ResponseWriter, r *http.Request) {
	store := s.state.CityBeadStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "no bead store configured")
		return
	}
	sp := s.state.SessionProvider()
	mgr := session.NewManager(store, sp)

	id, err := session.ResolveSessionID(store, r.PathValue("id"))
	if err != nil {
		writeResolveError(w, err)
		return
	}
	info, err := mgr.Get(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	b, _ := store.Get(id)
	writeJSON(w, http.StatusOK, sessionResponseWithReason(info, &b))
}

func (s *Server) handleSessionSuspend(w http.ResponseWriter, r *http.Request) {
	store := s.state.CityBeadStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "no bead store configured")
		return
	}
	sp := s.state.SessionProvider()
	mgr := session.NewManager(store, sp)

	id, err := session.ResolveSessionID(store, r.PathValue("id"))
	if err != nil {
		writeResolveError(w, err)
		return
	}
	if err := mgr.Suspend(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSessionClose(w http.ResponseWriter, r *http.Request) {
	store := s.state.CityBeadStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "no bead store configured")
		return
	}
	sp := s.state.SessionProvider()
	mgr := session.NewManager(store, sp)

	id, err := session.ResolveSessionID(store, r.PathValue("id"))
	if err != nil {
		writeResolveError(w, err)
		return
	}
	if err := mgr.Close(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleSessionWake clears hold and quarantine on a session.
func (s *Server) handleSessionWake(w http.ResponseWriter, r *http.Request) {
	store := s.state.CityBeadStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "no bead store configured")
		return
	}

	id, err := session.ResolveSessionID(store, r.PathValue("id"))
	if err != nil {
		writeResolveError(w, err)
		return
	}

	b, err := store.Get(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if b.Type != session.BeadType {
		writeError(w, http.StatusBadRequest, "invalid", id+" is not a session")
		return
	}
	if b.Status == "closed" {
		writeError(w, http.StatusConflict, "conflict", "session "+id+" is closed")
		return
	}

	batch := map[string]string{
		"held_until":        "",
		"quarantined_until": "",
		"wake_attempts":     "0",
	}
	sr := b.Metadata["sleep_reason"]
	if sr == "user-hold" || sr == "quarantine" {
		batch["sleep_reason"] = ""
	}

	if err := store.SetMetadataBatch(id, batch); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "id": id})
}

// handleSessionRename updates a session's title.
func (s *Server) handleSessionRename(w http.ResponseWriter, r *http.Request) {
	store := s.state.CityBeadStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "no bead store configured")
		return
	}

	id, err := session.ResolveSessionID(store, r.PathValue("id"))
	if err != nil {
		writeResolveError(w, err)
		return
	}

	var body struct {
		Title string `json:"title"`
	}
	if decErr := decodeBody(r, &body); decErr != nil {
		writeError(w, http.StatusBadRequest, "invalid", decErr.Error())
		return
	}
	if body.Title == "" {
		writeError(w, http.StatusBadRequest, "invalid", "title is required")
		return
	}

	b, err := store.Get(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if b.Type != session.BeadType {
		writeError(w, http.StatusBadRequest, "invalid", id+" is not a session")
		return
	}

	if err := store.Update(id, beads.UpdateOpts{Title: &body.Title}); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	// Re-fetch to return the updated session, consistent with PATCH.
	sp := s.state.SessionProvider()
	mgr := session.NewManager(store, sp)
	info, err := mgr.Get(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	updated, _ := store.Get(id)
	writeJSON(w, http.StatusOK, sessionResponseWithReason(info, &updated))
}

// handleSessionPatch handles PATCH /v0/session/{id}. Only title is mutable.
func (s *Server) handleSessionPatch(w http.ResponseWriter, r *http.Request) {
	store := s.state.CityBeadStore()
	if store == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "no bead store configured")
		return
	}

	id, err := session.ResolveSessionID(store, r.PathValue("id"))
	if err != nil {
		writeResolveError(w, err)
		return
	}

	var body map[string]any
	if decErr := decodeBody(r, &body); decErr != nil {
		writeError(w, http.StatusBadRequest, "invalid", decErr.Error())
		return
	}

	// Reject any field other than "title".
	for key := range body {
		if key != "title" {
			writeError(w, http.StatusForbidden, "forbidden",
				fmt.Sprintf("field %q is immutable on sessions; only 'title' can be patched", key))
			return
		}
	}

	title, ok := body["title"].(string)
	if !ok || title == "" {
		writeError(w, http.StatusBadRequest, "invalid", "title must be a non-empty string")
		return
	}

	b, err := store.Get(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if b.Type != session.BeadType {
		writeError(w, http.StatusBadRequest, "invalid", id+" is not a session")
		return
	}

	if err := store.Update(id, beads.UpdateOpts{Title: &title}); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	// Re-fetch to get updated state.
	sp := s.state.SessionProvider()
	mgr := session.NewManager(store, sp)
	info, err := mgr.Get(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	updated, _ := store.Get(id)
	writeJSON(w, http.StatusOK, sessionResponseWithReason(info, &updated))
}
