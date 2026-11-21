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

// toolchains reports the Rust, soroban-sdk and Stellar CLI versions found in
// stored Wasm meta, with declaration and SEP-41 match rates per version.
func (s *Server) toolchains(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, 400, "bad_request", "toolchain statistics take no query parameters")
		return
	}
	b := backendFrom(r.Context())
	d, err := b.Store.Toolchains(r.Context(), b.Network, s.options.Rulesets)
	if detailError(w, err) {
		return
	}
	writeJSON(w, 200, d)
}
