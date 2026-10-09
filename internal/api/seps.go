package api

import "net/http"

func (s *Server) seps(w http.ResponseWriter, r *http.Request) {
	limit, cursor, err := pageParameters(r)
	if detailError(w, err) {
		return
	}
	d, err := s.options.Store.SEPs(r.Context(), limit, cursor, s.options.Rulesets)
	if detailError(w, err) {
		return
	}
	writeJSON(w, 200, d)
}
