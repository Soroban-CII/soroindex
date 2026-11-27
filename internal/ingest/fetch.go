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

// DefaultConcurrency is how many getLedgerEntries calls, and how many Wasm
// analyses, run at once when the caller does not say. It matches the RPC
// client's default --rpc-concurrency.
const DefaultConcurrency = 4

// FetchCode fetches every hash's code with getLedgerEntries, 200 keys per
// call, with up to DefaultConcurrency calls in flight. See FetchCodeWith.
func FetchCode(ctx context.Context, c RPC, hashes []string, b XDRBudget) (map[string]Code, error) {
	return FetchCodeWith(ctx, c, hashes, DefaultConcurrency, b)
}

// FetchCodeWith fetches every hash's code with getLedgerEntries, 200 keys
// per call, using exactly min(workers, calls) goroutines, so the number of
// goroutines never grows with the input. A hash the node does not return,
// or whose liveUntilLedgerSeq is below the latest ledger, is Archived. The
// first error cancels the calls still waiting and is returned. The RPC
// client already retries with backoff, so this adds no retry layer.
//
// b bounds each entry's base64 XDR before it is decoded; a zero XDRBudget is
// the shipped default.
func FetchCodeWith(ctx context.Context, c RPC, hashes []string, workers int, b XDRBudget) (map[string]Code, error) {
	codeLimits := b.Code()
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
	var batches [][]string
	for start := 0; start < len(keys); start += rpc.MaxLedgerEntryKeys {
		batches = append(batches, keys[start:min(start+rpc.MaxLedgerEntryKeys, len(keys))])
	}
	var mu sync.Mutex
	err := forEach(ctx, len(batches), workers, func(ctx context.Context, i int) error {
		res, err := c.GetLedgerEntries(ctx, batches[i])
		if err != nil {
			return fmt.Errorf("fetch code batch at %d: %w", i*rpc.MaxLedgerEntryKeys, err)
		}
		got := map[string]Code{}
		for _, e := range res.Entries {
			data, err := DecodeLedgerEntryDataBase64(e.XDR, codeLimits)
			if err != nil || data.Type != xdr.LedgerEntryTypeContractCode || data.ContractCode == nil {
				return fmt.Errorf("code entry for key %s: unexpected or undecodable (%v)", e.Key, err)
			}
			h := hex.EncodeToString(data.ContractCode.Hash[:])
			if keyToHash[e.Key] != h {
				return fmt.Errorf("node returned code %s for key %s", h, e.Key)
			}
			if e.LiveUntilLedgerSeq != nil && *e.LiveUntilLedgerSeq < res.LatestLedger {
				continue // archived: TTL passed, stays Archived
			}
			got[h] = Code{Bytes: data.ContractCode.Code}
		}
		mu.Lock()
		defer mu.Unlock()
		for h, c := range got {
			out[h] = c
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// forEach runs fn(ctx, i) for i in [0, n) on min(workers, n) goroutines
// (DefaultConcurrency when workers <= 0). The first error cancels the
// context passed to the remaining calls, stops handing out work, and is
// returned after every started call has returned. It never leaves a
// goroutine running after it returns.
func forEach(ctx context.Context, n, workers int, fn func(context.Context, int) error) error {
	if n == 0 {
		return ctx.Err()
	}
	if workers <= 0 {
		workers = DefaultConcurrency
	}
	workers = min(workers, n)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		mu       sync.Mutex
		firstErr error
		next     int
		wg       sync.WaitGroup
	)
	take := func() (int, bool) {
		mu.Lock()
		defer mu.Unlock()
		if firstErr != nil || next >= n {
			return 0, false
		}
		i := next
		next++
		return i, true
	}
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i, ok := take()
				if !ok {
					return
				}
				if err := ctx.Err(); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					return
				}
				if err := fn(ctx, i); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					cancel()
					return
				}
			}
		}()
	}
	wg.Wait()
	return firstErr
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
