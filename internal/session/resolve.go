package session

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gastownhall/gascity/internal/beads"
)

// Resolution errors returned by ResolveSessionID.
var (
	ErrSessionNotFound = errors.New("session not found")
	ErrAmbiguous       = errors.New("ambiguous session identifier")
)

// ResolveSessionID resolves a user-provided identifier to a bead ID.
// It first attempts a direct store lookup; if the identifier exists as
// a session bead, it is returned immediately. Otherwise, it falls back
// to template-name matching against open session beads.
//
// Returns ErrSessionNotFound if no match is found, or ErrAmbiguous
// (wrapped with details) if multiple sessions match the template name.
func ResolveSessionID(store beads.Store, identifier string) (string, error) {
	// Try direct store lookup first — works for any ID format.
	b, err := store.Get(identifier)
	if err == nil && b.Type == BeadType {
		return b.ID, nil
	}
	if err != nil && !errors.Is(err, beads.ErrNotFound) {
		return "", fmt.Errorf("looking up session %q: %w", identifier, err)
	}

	// Fall back to template-name resolution among open sessions.
	all, err := store.ListByLabel(LabelSession, 0)
	if err != nil {
		return "", fmt.Errorf("listing sessions: %w", err)
	}

	var matches []beads.Bead
	for _, b := range all {
		if b.Type != BeadType || b.Status == "closed" {
			continue
		}
		tmpl := b.Metadata["template"]
		if tmpl == identifier || strings.HasSuffix(tmpl, "/"+identifier) {
			matches = append(matches, b)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("%w: %q", ErrSessionNotFound, identifier)
	case 1:
		return matches[0].ID, nil
	default:
		var ids []string
		for _, m := range matches {
			ids = append(ids, fmt.Sprintf("%s (%s)", m.ID, m.Metadata["template"]))
		}
		return "", fmt.Errorf("%w: %q matches %d sessions: %s", ErrAmbiguous, identifier, len(matches), strings.Join(ids, ", "))
	}
}
