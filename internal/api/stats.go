package api

import "net/http"

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, 400, "bad_request", "stats takes no query parameters")
		return
	}
	d, err := s.options.Store.Stats(r.Context(), s.options.Network, s.options.Rulesets)
	if detailError(w, err) {
		return
	}
	writeJSON(w, 200, d)
}
