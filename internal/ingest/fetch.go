package ingest

import (
	"context"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"

	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// RPC is the subset of the RPC client the indexer and phase0 use; tests
// substitute a fake.
type RPC interface {
	GetNetwork(ctx context.Context) (rpc.Network, error)
	GetLatestLedger(ctx context.Context) (rpc.LatestLedger, error)
	GetLedgers(ctx context.Context, r rpc.LedgersRequest) (rpc.LedgersPage, error)
	GetLedgerEntries(ctx context.Context, keys []string) (rpc.LedgerEntries, error)
}

// Code is a fetched Wasm, or the reason it could not be fetched.
type Code struct {
	Bytes    []byte
	Archived bool // absent from RPC, or its TTL has passed
}

// codeLimits bounds a ContractCode entry: Soroban caps code at 128 KiB, so
// 2 MiB of base64 leaves ample room while rejecting anything absurd.
var codeLimits = XDRLimits{MaxBase64Len: 2 << 20}

// FetchCode fetches every hash's code with getLedgerEntries, 200 keys per
// call, with calls in parallel up to the client's concurrency limit. A
// hash the node does not return, or whose liveUntilLedgerSeq is below the
// latest ledger, is Archived.
func FetchCode(ctx context.Context, c RPC, hashes []string) (map[string]Code, error) {
	keys := make([]string, 0, len(hashes))
	keyToHash := map[string]string{}
	for _, h := range hashes {
		k, err := ContractCodeKey(h)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
		keyToHash[k] = h
	}
	out := make(map[string]Code, len(hashes))
	for _, h := range hashes {
		out[h] = Code{Archived: true} // until the node returns it live
	}
	var mu sync.Mutex
	var firstErr error
	var wg sync.WaitGroup
	for start := 0; start < len(keys); start += rpc.MaxLedgerEntryKeys {
		batch := keys[start:min(start+rpc.MaxLedgerEntryKeys, len(keys))]
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := c.GetLedgerEntries(ctx, batch)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("fetch code batch at %d: %w", start, err)
				}
				return
			}
			for _, e := range res.Entries {
				data, err := DecodeLedgerEntryDataBase64(e.XDR, codeLimits)
				if err != nil || data.Type != xdr.LedgerEntryTypeContractCode || data.ContractCode == nil {
					if firstErr == nil {
						firstErr = fmt.Errorf("code entry for key %s: unexpected or undecodable (%v)", e.Key, err)
					}
					continue
				}
				h := hex.EncodeToString(data.ContractCode.Hash[:])
				if keyToHash[e.Key] != h {
					if firstErr == nil {
						firstErr = fmt.Errorf("node returned code %s for key %s", h, e.Key)
					}
					continue
				}
				if e.LiveUntilLedgerSeq != nil && *e.LiveUntilLedgerSeq < res.LatestLedger {
					continue // archived: TTL passed, stays Archived
				}
				out[h] = Code{Bytes: data.ContractCode.Code}
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// SortedKeys returns a map's keys in order, for deterministic output.
func SortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
