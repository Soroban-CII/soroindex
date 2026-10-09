package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Soroban-CII/soroindex/internal/store"
)

func (s *Server) contracts(w http.ResponseWriter, r *http.Request) {
	q, err := parseQuery(r, "implements", "implements_any", "tier", "kind", "limit", "cursor")
	if detailError(w, err) {
		return
	}
	all, err := store.ParseSEPs(q.Get("implements"))
	if err != nil {
		writeError(w, 400, "bad_request", "invalid implements list")
		return
	}
	anySEPs, err := store.ParseSEPs(q.Get("implements_any"))
	if err != nil {
		writeError(w, 400, "bad_request", "invalid implements_any list")
		return
	}
	limit := 50
	if _, ok := q["limit"]; ok {
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil || limit < 1 || limit > 500 {
			writeError(w, 400, "bad_request", "limit must be 1..500")
			return
		}
	}
	result, err := s.options.Store.Contracts(r.Context(), store.Filter{Implements: all, ImplementsAny: anySEPs, Tier: q.Get("tier"), Kind: q.Get("kind"), Limit: limit, Cursor: q.Get("cursor"), Rulesets: s.options.Rulesets})
	if errors.Is(err, store.ErrBadQuery) {
		writeError(w, 400, "bad_request", "invalid filter or cursor")
		return
	}
	if err != nil {
		writeError(w, 500, "internal", "cannot query contracts")
		return
	}
	writeJSON(w, 200, result)
}
