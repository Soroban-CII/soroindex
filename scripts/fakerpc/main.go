package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"
)

func main() {
	addr := flag.String("addr", ":8545", "listen address for recorded RPC")
	flag.Parse()

	meta := xdr.LedgerCloseMeta{
		V: 0,
		V0: &xdr.LedgerCloseMetaV0{
			LedgerHeader: xdr.LedgerHeaderHistoryEntry{
				Header: xdr.LedgerHeader{
					LedgerSeq: 10,
				},
			},
		},
	}
	encoded, err := xdr.MarshalBase64(meta)
	if err != nil {
		log.Fatalf("marshal meta: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
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
			result = rpc.LatestLedger{Sequence: 10}
		case "getLedgers":
			result = rpc.LedgersPage{
				OldestLedger: 1,
				LatestLedger: 10,
				Ledgers: []rpc.Ledger{
					{Sequence: 10, MetadataXDR: encoded},
				},
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
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}
