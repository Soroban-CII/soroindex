package api

import (
	"errors"
	"net/http"

	"github.com/Soroban-CII/soroindex/internal/store"
)

// staleCursorMessage tells a client holding a cursor from an older format,
// schema or ruleset to start again; the cursor itself cannot be reused.
const staleCursorMessage = "cursor is from another schema or ruleset version; restart paging without a cursor"

func detailError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, store.ErrStaleCursor):
		writeError(w, 400, "bad_request", staleCursorMessage)
	case errors.Is(err, store.ErrInvalidCursor):
		writeError(w, 400, "bad_request", "invalid cursor")
	case errors.Is(err, store.ErrBadQuery):
		writeError(w, 400, "bad_request", "invalid identifier, filter or cursor")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "not_found", "not found")
	default:
		writeError(w, 500, "internal", "cannot read index")
	}
	return true
}
func (s *Server) contract(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, 400, "bad_request", "contract detail takes no query parameters")
		return
	}
	b := backendFrom(r.Context())
	d, err := b.Store.Contract(r.Context(), r.PathValue("id"), b.Network, s.options.Rulesets)
	if detailError(w, err) {
		return
	}
	if detailError(w, b.Store.AnnotateContract(r.Context(), &d, s.versions.Highest)) {
		return
	}
	writeJSON(w, 200, d)
}
func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	limit, cursor, err := pageParameters(r)
	if detailError(w, err) {
		return
	}
	b := backendFrom(r.Context())
	d, err := b.Store.HistoryPage(r.Context(), r.PathValue("id"), b.Network, s.options.Rulesets, limit, cursor)
	if detailError(w, err) {
		return
	}
	writeJSON(w, 200, d)
}
