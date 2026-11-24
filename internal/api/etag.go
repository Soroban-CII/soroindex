package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// etagEligible reports whether a path is a cacheable index read. Health
// reports the live RPC tip and the upgrade stream never ends, so neither can
// be tagged from the index position alone.
func etagEligible(path string) bool {
	switch path {
	case "/v1/health", "/v1/events/upgrades":
		return false
	}
	return path == "/contracts" || strings.HasPrefix(path, "/v1/")
}

// responseETag is a strong validator for one request against one index
// position. It hashes the network, path and query with the last synced
// ledger, the parser version and every ruleset version, so it changes
// whenever the index advances or its rules do, and never needs the body.
func responseETag(network, path, rawQuery string, lastLedger uint64, parserVersion string, rulesets map[int]string) string {
	seps := make([]int, 0, len(rulesets))
	for sep := range rulesets {
		seps = append(seps, sep)
	}
	sort.Ints(seps)
	parts := []string{network, path, rawQuery, strconv.FormatUint(lastLedger, 10), parserVersion}
	for _, sep := range seps {
		parts = append(parts, strconv.Itoa(sep)+"="+rulesets[sep])
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// etagMatches applies the weak comparison If-None-Match requires: any listed
// tag equal to etag once a W/ prefix is dropped, or the wildcard. Anything
// that is not a quoted tag never matches, so a malformed header gets a full
// response.
func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" {
			return true
		}
		candidate = strings.TrimPrefix(candidate, "W/")
		if len(candidate) >= 2 && candidate[0] == '"' && candidate == etag {
			return true
		}
	}
	return false
}

// bufferedResponse holds a handler's response so the tag can be attached, or
// the body dropped for a 304, only after the handler has shown it succeeded.
type bufferedResponse struct {
	header http.Header
	status int
	body   []byte
}

func (b *bufferedResponse) Header() http.Header { return b.header }

func (b *bufferedResponse) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}

func (b *bufferedResponse) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	b.body = append(b.body, p...)
	return len(p), nil
}

// serveTagged runs next and, for a 200 response, sets a strong ETag and
// answers 304 with no body when If-None-Match already names it. Error
// responses are passed through untagged. The last ledger is read before the
// handler runs, so a body can only be newer than its tag, never older.
func (s *Server) serveTagged(w http.ResponseWriter, r *http.Request, b Backend, next http.Handler) {
	state, err := b.Store.State(r.Context(), store.KeyLastLedger)
	var last uint64
	if err == nil {
		last, err = strconv.ParseUint(state, 10, 32)
	}
	if errors.Is(err, store.ErrNotFound) {
		last, err = 0, nil
	}
	if err != nil {
		next.ServeHTTP(w, r)
		return
	}
	buf := &bufferedResponse{header: http.Header{}}
	next.ServeHTTP(buf, r)
	if buf.status == 0 {
		buf.status = http.StatusOK
	}
	out := w.Header()
	for k, v := range buf.header {
		out[k] = v
	}
	if buf.status != http.StatusOK {
		w.WriteHeader(buf.status)
		_, _ = w.Write(buf.body)
		return
	}
	etag := responseETag(b.Network, r.URL.Path, r.URL.RawQuery, last, sepmeta.ParserVersion, s.options.Rulesets)
	out.Set("ETag", etag)
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		out.Del("Content-Type")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.body)
}
