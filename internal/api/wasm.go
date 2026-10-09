package api

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/Soroban-CII/soroindex/internal/store"
)

func parseQuery(r *http.Request, allowed ...string) (url.Values, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, store.ErrBadQuery
	}
	for key, values := range q {
		found := false
		for _, a := range allowed {
			if a == key {
				found = true
			}
		}
		if !found || len(values) != 1 || values[0] == "" {
			return nil, store.ErrBadQuery
		}
	}
	return q, nil
}

func pageParameters(r *http.Request) (int, string, error) {
	q, err := parseQuery(r, "limit", "cursor")
	if err != nil {
		return 0, "", err
	}
	limit := 50
	if q.Get("limit") != "" {
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil || limit < 1 || limit > 500 {
			return 0, "", store.ErrBadQuery
		}
	}
	return limit, q.Get("cursor"), nil
}

func (s *Server) wasm(w http.ResponseWriter, r *http.Request) {
	limit, cursor, err := pageParameters(r)
	if detailError(w, err) {
		return
	}
	d, err := s.options.Store.Wasm(r.Context(), r.PathValue("hash"), limit, cursor, s.options.Rulesets)
	if detailError(w, err) {
		return
	}
	writeJSON(w, 200, d)
}
