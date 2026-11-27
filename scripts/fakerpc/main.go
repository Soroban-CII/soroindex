// Package main provides a mock RPC server responding to network and ledger queries for offline tests.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"
)

func marshalLedgerMeta(seq uint32) (string, error) {
	meta := xdr.LedgerCloseMeta{
		V: 0,
		V0: &xdr.LedgerCloseMetaV0{
			LedgerHeader: xdr.LedgerHeaderHistoryEntry{
				Header: xdr.LedgerHeader{
					LedgerSeq: xdr.Uint32(seq),
				},
			},
		},
	}
	return xdr.MarshalBase64(meta)
}

func main() {
	addr := flag.String("addr", ":8545", "listen address for recorded RPC")
	flag.Parse()

	const oldestLedger uint32 = 1
	const latestLedger uint32 = 10

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				StartLedger uint32 `json:"startLedger"`
				Pagination  *struct {
					Limit uint32 `json:"limit"`
				} `json:"pagination"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var result any
		switch req.Method {
		case "getNetwork":
			result = rpc.Network{Passphrase: config.Testnet.Passphrase}
		case "getLatestLedger":
			result = rpc.LatestLedger{Sequence: latestLedger}
		case "getLedgers":
			start := req.Params.StartLedger
			if start < oldestLedger {
				start = oldestLedger
			}
			limit := uint32(200)
			if req.Params.Pagination != nil && req.Params.Pagination.Limit > 0 {
				limit = req.Params.Pagination.Limit
			}
			var ledgers []rpc.Ledger
			for seq, count := start, uint32(0); seq <= latestLedger && count < limit; seq, count = seq+1, count+1 {
				encoded, err := marshalLedgerMeta(seq)
				if err != nil {
					http.Error(w, fmt.Sprintf("marshal meta %d: %v", seq, err), http.StatusInternalServerError)
					return
				}
				ledgers = append(ledgers, rpc.Ledger{Sequence: seq, MetadataXDR: encoded})
			}
			result = rpc.LedgersPage{
				OldestLedger: oldestLedger,
				LatestLedger: latestLedger,
				Ledgers:      ledgers,
			}
		default:
			http.Error(w, fmt.Sprintf("unsupported method %s", req.Method), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Result  any             `json:"result"`
		}{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  result,
		})
	})

	log.Printf("Fake RPC server listening on %s", *addr)
	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
