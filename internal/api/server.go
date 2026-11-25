// Package api serves the read-only contract index over JSON HTTP endpoints.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/rules"
)

// LatestLedger supplies live tip observations without writing the database.
type LatestLedger interface {
	GetLatestLedger(context.Context) (rpc.LatestLedger, error)
}

// Backend is one read-only network database and its live tip source.
type Backend struct {
	Store   *store.Store
	Network string
	RPC     LatestLedger
}

// Options binds one or more read-only networks to a server. Set Networks to
// serve several; Store, Network and RPC are the single-network shorthand.
type Options struct {
	Store              *store.Store
	Network            string
	RPC                LatestLedger
	Networks           map[string]Backend
	RateLimit          int
	Rulesets           map[int]string
	CORSAllowedOrigins []string
}

// Server is an HTTP handler with bounded request contexts.
type Server struct {
	options  Options
	versions match.Versions // current and historical rule files, for matched_version
	mux      *http.ServeMux
	limiter  ipLimiter
	backends map[string]Backend
}

type backendKey struct{}

func backendFrom(ctx context.Context) Backend {
	b, _ := ctx.Value(backendKey{}).(Backend)
	return b
}

// New refuses writable stores so serving cannot migrate or modify the index.
func New(o Options) (*Server, error) {
	if o.RateLimit < 0 {
		return nil, errors.New("api: rate-limit must not be negative")
	}
	for _, origin := range o.CORSAllowedOrigins {
		if err := validateCORSOrigin(origin); err != nil {
			return nil, err
		}
	}
	if len(o.Networks) == 0 {
		o.Networks = map[string]Backend{o.Network: {Store: o.Store, Network: o.Network, RPC: o.RPC}}
	}
	for name, b := range o.Networks {
		if err := validateBackend(name, b); err != nil {
			return nil, err
		}
	}
	if o.Rulesets == nil {
		o.Rulesets = map[int]string{}
		loaded, err := match.LoadRules(rules.FS)
		if err != nil {
			return nil, err
		}
		for _, rule := range loaded {
			o.Rulesets[rule.SEP] = rule.RulesetVersion
		}
	}
	current, err := match.LoadRules(rules.FS)
	if err != nil {
		return nil, err
	}
	versions, err := match.LoadVersions(rules.FS, current)
	if err != nil {
		return nil, err
	}
	s := &Server{options: o, versions: versions, mux: http.NewServeMux(), backends: o.Networks}
	s.mux.HandleFunc("GET /v1/health", s.health)
	s.mux.HandleFunc("GET /v1/contracts", s.contracts)
	s.mux.HandleFunc("GET /contracts", s.contracts)
	s.mux.HandleFunc("GET /v1/contracts/{id}", s.contract)
	s.mux.HandleFunc("GET /v1/contracts/{id}/history", s.history)
	s.mux.HandleFunc("GET /v1/wasm/{hash}", s.wasm)
	s.mux.HandleFunc("GET /v1/seps", s.seps)
	s.mux.HandleFunc("GET /v1/stats", s.stats)
	s.mux.HandleFunc("GET /v1/stats/toolchains", s.toolchains)
	s.mux.HandleFunc("GET /v1/events/upgrades", s.upgradesSSE)
	s.mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "endpoint not found")
	})
	return s, nil
}

func validateBackend(name string, b Backend) error {
	if name != "testnet" && name != "mainnet" {
		return errors.New("api: valid network required")
	}
	if b.Network != name {
		return errors.New("api: backend network does not match its key")
	}
	if b.Store == nil || !b.Store.IsReadOnly() {
		return errors.New("api: read-only store required")
	}
	return nil
}

// ServeHTTP adds GET CORS, selects the network and bounds the request context
// before dispatching. A network query parameter is removed before handlers
// see it, so handlers that reject unknown query parameters still work.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	corsAllowed := s.setCORSHeaders(w, r)
	if r.Method == http.MethodOptions {
		if len(s.options.CORSAllowedOrigins) > 0 {
			w.Header().Add("Vary", "Access-Control-Request-Method")
			w.Header().Add("Vary", "Access-Control-Request-Headers")
			if corsAllowed {
				if requestedHeaders := r.Header.Get("Access-Control-Request-Headers"); requestedHeaders != "" {
					w.Header().Set("Access-Control-Allow-Headers", requestedHeaders)
				}
			}
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, OPTIONS")
		writeError(w, http.StatusMethodNotAllowed, "bad_request", "only GET is supported")
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid query")
		return
	}
	network := ""
	if values, ok := q["network"]; ok {
		if len(values) != 1 || values[0] == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "network must be given once")
			return
		}
		network = values[0]
		q.Del("network")
		r.URL.RawQuery = q.Encode()
	}
	b, err := s.selectBackend(network)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if !s.limiter.allowN(r.RemoteAddr, s.options.RateLimit, lookupCost(r.URL.Path, q), time.Now()) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "rate_limited", "request limit exceeded")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, backendKey{}, b)
	s.mux.ServeHTTP(w, r.WithContext(ctx))
}

func (s *Server) setCORSHeaders(w http.ResponseWriter, r *http.Request) bool {
	if len(s.options.CORSAllowedOrigins) == 0 {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		return true
	}
	w.Header().Add("Vary", "Origin")
	origins := r.Header.Values("Origin")
	if len(origins) != 1 {
		return false
	}
	for _, allowed := range s.options.CORSAllowedOrigins {
		if origins[0] == allowed {
			w.Header().Set("Access-Control-Allow-Origin", origins[0])
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			return true
		}
	}
	return false
}

func validateCORSOrigin(origin string) error {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.Hostname() == "" || strings.TrimSpace(origin) != origin || u.User != nil ||
		u.Opaque != "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("api: invalid CORS origin %q: expected an http(s) origin without a path", origin)
	}
	return nil
}

func (s *Server) selectBackend(network string) (Backend, error) {
	if network == "" {
		if len(s.backends) == 1 {
			for _, b := range s.backends {
				return b, nil
			}
		}
		return Backend{}, errors.New("network is required when serving more than one network")
	}
	b, ok := s.backends[network]
	if !ok {
		return Backend{}, errors.New("unknown network")
	}
	return b, nil
}

type healthResponse struct {
	Status       string `json:"status"`
	Network      string `json:"network"`
	LastLedger   uint32 `json:"last_ledger"`
	LatestLedger uint32 `json:"latest_ledger"`
	Lag          uint32 `json:"lag"`
	// LastProtocol is the protocol version last_ledger closed under, or
	// null before the first sync batch records one. Added for issue #80;
	// the fields above keep their meaning.
	LastProtocol *uint32 `json:"last_protocol"`
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, 400, "bad_request", "health takes no query parameters")
		return
	}
	b := backendFrom(r.Context())
	state, err := b.Store.State(r.Context(), store.KeyLastLedger)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeError(w, 500, "internal", "cannot read sync progress")
		return
	}
	var last uint64
	if state != "" {
		last, err = strconv.ParseUint(state, 10, 32)
		if err != nil {
			writeError(w, 500, "internal", "invalid sync progress")
			return
		}
	}
	if b.RPC == nil {
		writeError(w, 503, "internal", "latest ledger unavailable")
		return
	}
	latest, err := b.RPC.GetLatestLedger(r.Context())
	if err != nil {
		writeError(w, 503, "internal", "latest ledger unavailable")
		return
	}
	if uint64(latest.Sequence) < last {
		writeError(w, 503, "internal", "RPC tip is behind the index")
		return
	}
	status := "ok"
	if last == 0 {
		status = "initializing"
	}
	resp := healthResponse{Status: status, Network: b.Network, LastLedger: uint32(last), LatestLedger: latest.Sequence, Lag: latest.Sequence - uint32(last)} // #nosec G115 -- parsed with 32-bit bound
	proto, ok, err := b.Store.LastProtocol(r.Context())
	if err != nil {
		writeError(w, 500, "internal", "cannot read sync progress")
		return
	}
	if ok {
		resp.LastProtocol = &proto
	}
	writeJSON(w, 200, resp)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		writeError(w, 500, "internal", "cannot encode response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n')) // connection errors cannot be reported after headers
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{Error: struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{code, message}})
}

// upgradesSSE streams contract version upgrades as Server-Sent Events.
// Query parameter: since (uint32 ledger number, optional) - resume from this ledger.
// Last-Event-ID header is also supported for automatic resume.
// The stream sends events with id=<ledger> and data=<JSON VersionChange>.
// Connection limits and idle timeouts are enforced by the HTTP server settings.
type testTickerIntervalKeyType struct{}

// TestTickerIntervalKey is a context key for overriding the SSE ticker interval in tests.
var TestTickerIntervalKey = testTickerIntervalKeyType{}

func (s *Server) upgradesSSE(w http.ResponseWriter, r *http.Request) {
	// Only allow GET.
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, http.StatusMethodNotAllowed, "bad_request", "only GET is supported")
		return
	}

	// Parse since parameter (from query or Last-Event-ID header).
	since := uint32(0)
	if v := r.URL.Query().Get("since"); v != "" {
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			writeError(w, 400, "bad_request", "invalid since parameter")
			return
		}
		since = uint32(n)
	}
	// Last-Event-ID header takes precedence over query parameter.
	if lei := r.Header.Get("Last-Event-ID"); lei != "" {
		n, err := strconv.ParseUint(lei, 10, 32)
		if err == nil {
			since = uint32(n)
		}
	}

	b := backendFrom(r.Context())

	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering.

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, 500, "internal", "streaming not supported")
		return
	}

	// Send initial retry comment to set reconnection delay.
	_, _ = fmt.Fprintf(w, "retry: 5000\n\n")
	flusher.Flush()

	// Stream version changes.
	ctx := r.Context()
	tickerInterval := 5 * time.Second
	if v := ctx.Value(TestTickerIntervalKey); v != nil {
		if d, ok := v.(time.Duration); ok && d > 0 {
			tickerInterval = d
		}
	}
	ticker := time.NewTicker(tickerInterval)
	defer ticker.Stop()

	lastSentLedger := since
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			changes, err := b.Store.VersionChanges(ctx, lastSentLedger, 1000)
			if err != nil {
				// Log error but keep connection alive.
				slog.ErrorContext(ctx, "SSE version changes query failed", "error", err)
				continue
			}
			for _, change := range changes {
				data, err := json.Marshal(change)
				if err != nil {
					slog.ErrorContext(ctx, "SSE marshal failed", "error", err)
					continue
				}
				// Use the ledger as event ID for resume.
				_, _ = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", change.Ledger, data)
				lastSentLedger = change.Ledger
			}
			flusher.Flush()
		}
	}
}
