// Package api serves the read-only contract index over JSON HTTP endpoints.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/rules"
	"net/http"
	"strconv"
	"time"

	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/Soroban-CII/soroindex/internal/store"
)

// LatestLedger supplies live tip observations without writing the database.
type LatestLedger interface {
	GetLatestLedger(context.Context) (rpc.LatestLedger, error)
}

// Options binds a read-only database and network to a server.
type Options struct {
	Store     *store.Store
	Network   string
	RPC       LatestLedger
	RateLimit int
	Rulesets  map[int]string
}

// Server is an HTTP handler with bounded request contexts.
type Server struct {
	options Options
	mux     *http.ServeMux
}

// New refuses writable stores so serving cannot migrate or modify the index.
func New(o Options) (*Server, error) {
	if o.Store == nil || !o.Store.IsReadOnly() {
		return nil, errors.New("api: read-only store required")
	}
	if o.Network != "testnet" && o.Network != "mainnet" {
		return nil, errors.New("api: valid network required")
	}
	if o.RateLimit < 0 {
		return nil, errors.New("api: rate-limit must not be negative")
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
	s := &Server{options: o, mux: http.NewServeMux()}
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

// ServeHTTP adds GET CORS and a timeout before dispatching a request.
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
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	s.mux.ServeHTTP(w, r.WithContext(ctx))
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
	state, err := s.options.Store.State(r.Context(), store.KeyLastLedger)
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
	if s.options.RPC == nil {
		writeError(w, 503, "internal", "latest ledger unavailable")
		return
	}
	latest, err := s.options.RPC.GetLatestLedger(r.Context())
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
	writeJSON(w, 200, healthResponse{Status: status, Network: s.options.Network, LastLedger: uint32(last), LatestLedger: latest.Sequence, Lag: latest.Sequence - uint32(last)}) // #nosec G115 -- parsed with 32-bit bound
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
