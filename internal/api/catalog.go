package api

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Soroban-CII/soroindex/internal/store"
)

func (s *Server) contracts(w http.ResponseWriter, r *http.Request) {
	q, err := parseQuery(r, "ids", "implements", "implements_any", "tier", "kind", "archived", "exec_ref_owner", "limit", "cursor")
	if detailError(w, err) {
		return
	}
	if _, ok := q["ids"]; ok {
		s.lookup(w, r, q)
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
	var archived *bool
	if v := q.Get("archived"); v != "" {
		switch v {
		case "true":
			b := true
			archived = &b
		case "false":
			b := false
			archived = &b
		default:
			writeError(w, 400, "bad_request", "archived must be true or false")
			return
		}
	}
	execRefOwner := q.Get("exec_ref_owner")
	if execRefOwner != "" {
		if err := store.ValidateContractID(execRefOwner); err != nil {
			writeError(w, 400, "bad_request", "invalid exec_ref_owner")
			return
		}
	}
	limit := 50
	if _, ok := q["limit"]; ok {
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil || limit < 1 || limit > 500 {
			writeError(w, 400, "bad_request", "limit must be 1..500")
			return
		}
	}
	b := backendFrom(r.Context())
	result, err := b.Store.Contracts(r.Context(), store.Filter{Implements: all, ImplementsAny: anySEPs, Tier: q.Get("tier"), Kind: q.Get("kind"), Archived: archived, ExecRefOwner: execRefOwner, Limit: limit, Cursor: q.Get("cursor"), Rulesets: s.options.Rulesets})
	if errors.Is(err, store.ErrBadQuery) {
		if errors.Is(err, store.ErrStaleCursor) || errors.Is(err, store.ErrInvalidCursor) {
			detailError(w, err)
			return
		}
		writeError(w, 400, "bad_request", "invalid filter or cursor")
		return
	}
	if err != nil {
		writeError(w, 500, "internal", "cannot query contracts")
		return
	}
	writeJSON(w, 200, result)
}

// idsPerCharge is how many looked-up IDs count as one request against the
// per-IP rate limit. A full 100-ID lookup costs 10, so bulk is cheaper than
// one request per contract but not free.
const idsPerCharge = 10

// lookupCost is the rate-limit charge for a request: 1, or one per started
// block of idsPerCharge IDs for a bulk lookup. It is computed from the raw
// list before validation so that invalid lists are charged too; it is
// capped at the charge for the largest allowed list.
func lookupCost(path string, q url.Values) int {
	if path != "/v1/contracts" && path != "/contracts" {
		return 1
	}
	values, ok := q["ids"]
	if !ok || len(values) != 1 {
		return 1
	}
	n := strings.Count(values[0], ",") + 1
	if n > store.MaxLookupIDs {
		n = store.MaxLookupIDs
	}
	return (n + idsPerCharge - 1) / idsPerCharge
}

// lookup answers ?ids=. The list is exclusive with filters and pagination:
// mixing them would leave it unclear whether an absent ID is unknown or only
// filtered out.
func (s *Server) lookup(w http.ResponseWriter, r *http.Request, q url.Values) {
	if len(q) != 1 {
		writeError(w, 400, "bad_request", "ids cannot be combined with other parameters")
		return
	}
	ids, err := store.ParseContractIDs(q.Get("ids"))
	if err != nil {
		// The message names a position and the cap, never the raw input.
		writeError(w, 400, "bad_request", err.Error())
		return
	}
	b := backendFrom(r.Context())
	result, err := b.Store.LookupContracts(r.Context(), ids)
	if detailError(w, err) {
		return
	}
	writeJSON(w, 200, result)
}
