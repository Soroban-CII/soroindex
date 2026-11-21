// Package api serves the read-only contract index over JSON HTTP endpoints.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
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
	Store     *store.Store
	Network   string
	RPC       LatestLedger
	Networks  map[string]Backend
	RateLimit int
	Rulesets  map[int]string
}

// Server is an HTTP handler with bounded request contexts.
type Server struct {
	options  Options
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
	s := &Server{options: o, mux: http.NewServeMux(), backends: o.Networks}
	s.mux.HandleFunc("GET /v1/health", s.health)
	s.mux.HandleFunc("GET /v1/contracts", s.contracts)
	s.mux.HandleFunc("GET /contracts", s.contracts)
	s.mux.HandleFunc("GET /v1/contracts/{id}", s.contract)
	s.mux.HandleFunc("GET /v1/contracts/{id}/history", s.history)
	s.mux.HandleFunc("GET /v1/wasm/{hash}", s.wasm)
	s.mux.HandleFunc("GET /v1/seps", s.seps)
	s.mux.HandleFunc("GET /v1/stats", s.stats)
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
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	if r.Method == http.MethodOptions {
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
	if !s.limiter.allow(r.RemoteAddr, s.options.RateLimit, time.Now()) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "rate_limited", "request limit exceeded")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, backendKey{}, b)
	s.mux.ServeHTTP(w, r.WithContext(ctx))
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
	writeJSON(w, 200, healthResponse{Status: status, Network: b.Network, LastLedger: uint32(last), LatestLedger: latest.Sequence, Lag: latest.Sequence - uint32(last)}) // #nosec G115 -- parsed with 32-bit bound
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
