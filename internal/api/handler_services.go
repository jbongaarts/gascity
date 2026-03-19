package api

import (
	"net"
	"net/http"
	"strings"

	"github.com/gastownhall/gascity/internal/workspacesvc"
)

func (s *Server) handleServiceList(w http.ResponseWriter, _ *http.Request) {
	reg := s.state.ServiceRegistry()
	if reg == nil {
		writeListJSON(w, s.latestIndex(), []any{}, 0)
		return
	}
	items := reg.List()
	writeListJSON(w, s.latestIndex(), items, len(items))
}

func (s *Server) handleServiceGet(w http.ResponseWriter, r *http.Request) {
	reg := s.state.ServiceRegistry()
	if reg == nil {
		writeError(w, http.StatusNotFound, "not_found", "service "+r.PathValue("name")+" not found")
		return
	}
	item, ok := reg.Get(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "service "+r.PathValue("name")+" not found")
		return
	}
	writeIndexJSON(w, s.latestIndex(), item)
}

func (s *Server) handleServiceRestart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	reg := s.state.ServiceRegistry()
	if reg == nil {
		writeError(w, http.StatusNotFound, "not_found", "service "+name+" not found")
		return
	}
	if err := reg.Restart(name); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "action": "restart", "service": name})
}

func (s *Server) handleServiceProxy(w http.ResponseWriter, r *http.Request) {
	reg := s.state.ServiceRegistry()
	if reg == nil {
		writeError(w, http.StatusNotFound, "not_found", "service route not found")
		return
	}
	name := serviceNameFromPath(r.URL.Path)
	if name == "" {
		writeError(w, http.StatusNotFound, "not_found", "service route not found")
		return
	}
	if !reg.AuthorizeAndServeHTTP(name, w, r, func(status workspacesvc.Status) bool {
		return serviceRequestAllowed(w, status, r, s.readOnly)
	}) {
		writeError(w, http.StatusNotFound, "not_found", "service route not found")
	}
}

func serviceNameFromPath(path string) string {
	path = strings.TrimPrefix(path, "/svc/")
	if path == "" {
		return ""
	}
	if i := strings.IndexByte(path, '/'); i >= 0 {
		return path[:i]
	}
	return path
}

func serviceRequestAllowed(w http.ResponseWriter, status workspacesvc.Status, r *http.Request, apiReadOnly bool) bool {
	if status.PublishMode == "" {
		status.PublishMode = "private"
	}
	// The raw controller listener only relaxes ingress guards for legacy
	// direct publication. Hosted/publication routes use a separate edge and
	// should not become public merely because a status projection synthesized a
	// published URL.
	directPublished := status.PublishMode == "direct"
	if apiReadOnly && !directPublished && isMutationMethod(r.Method) {
		writeError(w, http.StatusForbidden, "read_only", "service mutations are disabled for unpublished services")
		return false
	}
	if !directPublished {
		internalProxyRequest := r.Header.Get("X-GC-Request") != ""
		if !isLoopbackRemoteAddr(r.RemoteAddr) && !internalProxyRequest {
			writeError(w, http.StatusNotFound, "not_found", "service route not found")
			return false
		}
		if isMutationMethod(r.Method) && !internalProxyRequest {
			writeError(w, http.StatusForbidden, "csrf", "X-GC-Request header required on private service mutation endpoints")
			return false
		}
	}
	return true
}

func isLoopbackRemoteAddr(remoteAddr string) bool {
	if remoteAddr == "" {
		return false
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
