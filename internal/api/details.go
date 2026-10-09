package api

import (
	"errors"
	"net/http"

	"github.com/Soroban-CII/soroindex/internal/store"
)

func detailError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, store.ErrBadQuery):
		writeError(w, 400, "bad_request", "invalid contract ID or Wasm hash")
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
	d, err := s.options.Store.Contract(r.Context(), r.PathValue("id"), s.options.Network, s.options.Rulesets)
	if detailError(w, err) {
		return
	}
	writeJSON(w, 200, d)
}
func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, 400, "bad_request", "history returns all versions and takes no query parameters")
		return
	}
	d, err := s.options.Store.History(r.Context(), r.PathValue("id"), s.options.Network, s.options.Rulesets)
	if detailError(w, err) {
		return
	}
	writeJSON(w, 200, d)
}
