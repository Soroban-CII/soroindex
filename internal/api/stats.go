package api

import "net/http"

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, 400, "bad_request", "stats takes no query parameters")
		return
	}
	b := backendFrom(r.Context())
	d, err := b.Store.Stats(r.Context(), b.Network, s.options.Rulesets)
	if detailError(w, err) {
		return
	}
	writeJSON(w, 200, d)
}
