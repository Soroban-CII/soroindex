package ingest

import (
	"context"
	"encoding/hex"
	"errors"

	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// errNotAnalyzed marks a cache entry whose analysis never ran, because its
// context ended first.
var errNotAnalyzed = errors.New("wasm prefetch did not run")

// prefetched is one Wasm fetched and analyzed outside any transaction. A
// Wasm hash is the SHA-256 of its code, so an entry can never go stale for
// its parsed contents; only availability (archived, fetch failure) depends
// on when it was read, which is why the cache is dropped between batches.
type prefetched struct {
	done     chan struct{} // closed once the fields below are final
	a        analysis      // valid when !archived and both errors are nil
	archived bool          // the node did not return the code live
	fetchErr error         // the fetch failed after RPC retries: queue it
	err      error         // analysis failed: fail the batch, as writeAnalysis does
}

func (p *prefetched) ready() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// workers is the bounded pool size for fetching and analyzing Wasm.
func (ix *Indexer) workers() int {
	if ix.Concurrency > 0 {
		return ix.Concurrency
	}
	return DefaultConcurrency
}

// dropPrefetched forgets every cached analysis. Entries still being filled
// are only unlinked: whoever holds them keeps waiting on their own pointer.
func (ix *Indexer) dropPrefetched() {
	ix.cacheMu.Lock()
	ix.cache = nil
	ix.cacheMu.Unlock()
}

// takePrefetched returns a finished cache entry for hash and removes it, or
// nil. It never waits: an entry still in flight is ignored, so a caller
// holding the write transaction can never block on a worker that is itself
// waiting for the database connection.
func (ix *Indexer) takePrefetched(hash string) *prefetched {
	ix.cacheMu.Lock()
	defer ix.cacheMu.Unlock()
	p := ix.cache[hash]
	if p == nil || !p.ready() {
		return nil
	}
	delete(ix.cache, hash)
	return p
}

// peekPrefetched returns a finished, successful analysis for hash without
// removing it. writeAnalysis uses it for code whose bytes arrived in ledger
// metadata, so parsing happened in the pool rather than inside the
// transaction.
func (ix *Indexer) peekPrefetched(hash string) (analysis, bool) {
	ix.cacheMu.Lock()
	defer ix.cacheMu.Unlock()
	p := ix.cache[hash]
	if p == nil || !p.ready() || p.archived || p.fetchErr != nil || p.err != nil {
		return analysis{}, false
	}
	return p.a, true
}

// claim returns the entries this caller must fill (owned) and the entries
// another caller is already filling (waiting). Each hash is fetched by at
// most one caller, however many batches need it.
func (ix *Indexer) claim(hashes []string) (owned map[string]*prefetched, waiting []*prefetched) {
	ix.cacheMu.Lock()
	defer ix.cacheMu.Unlock()
	if ix.cache == nil {
		ix.cache = map[string]*prefetched{}
	}
	owned = map[string]*prefetched{}
	for _, h := range hashes {
		if p, ok := ix.cache[h]; ok {
			if _, mine := owned[h]; !mine {
				waiting = append(waiting, p)
			}
			continue
		}
		p := &prefetched{done: make(chan struct{})}
		ix.cache[h] = p
		owned[h] = p
	}
	return owned, waiting
}

// Prefetch fetches and analyzes, outside any transaction, every hash in
// hashes that is neither stored nor already cached, using a bounded pool of
// ix.Concurrency workers. Results wait in memory for EnsureWasm, which
// writes them inside the batch transaction in the usual order, so commit
// order and the one-transaction-per-batch rule are unchanged. A fetch error
// is not returned: EnsureWasm queues those hashes for retry exactly as it
// does when it fetches itself. Only cancellation is returned.
func (ix *Indexer) Prefetch(ctx context.Context, hashes []string) error {
	var need []string
	for _, h := range dedupe(hashes) {
		ok, err := ix.Store.HasWasm(ctx, h)
		if err != nil {
			return err
		}
		if !ok {
			need = append(need, h)
		}
	}
	owned, waiting := ix.claim(need)
	ix.fill(ctx, owned, nil)
	for _, p := range waiting {
		select {
		case <-p.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return ctx.Err()
}

// fill fetches the owned entries without code, analyzes all of them on the
// bounded pool, and closes each entry's done channel, always, so no waiter
// is left hanging even when ctx is cancelled.
func (ix *Indexer) fill(ctx context.Context, owned map[string]*prefetched, code map[string][]byte) {
	defer func() {
		for _, p := range owned {
			if p.ready() {
				continue
			}
			if p.fetchErr == nil && !p.archived {
				// Never analyzed (cancelled): never let a zero analysis
				// pass for a successful one.
				p.err = errors.Join(errNotAnalyzed, ctx.Err())
			}
			close(p.done)
		}
	}()
	if len(owned) == 0 {
		return
	}
	var fetch []string
	for _, h := range SortedKeys(owned) {
		if _, ok := code[h]; !ok {
			fetch = append(fetch, h)
		}
	}
	all := map[string][]byte{}
	for h, b := range code {
		if _, ok := owned[h]; ok {
			all[h] = b
		}
	}
	if len(fetch) > 0 {
		got, err := FetchCodeWith(ctx, ix.RPC, fetch, ix.workers())
		for _, h := range fetch {
			p := owned[h]
			switch {
			case err != nil:
				p.fetchErr = err
			case got[h].Archived:
				p.archived = true
			default:
				all[h] = got[h].Bytes
			}
		}
	}
	work := SortedKeys(all)
	// Analysis errors are recorded per entry, not returned, so one bad
	// Wasm does not cancel its neighbours. Only ctx stops the pool.
	_ = forEach(ctx, len(work), ix.workers(), func(ctx context.Context, i int) error { // per-entry errors live in the entries
		h := work[i]
		p := owned[h]
		p.a, p.err = ix.analyze(ctx, h, all[h], 0)
		close(p.done)
		return nil
	})
}

// PrefetchBatch prepares a ledger batch before its transaction opens: it
// fetches, on the bounded pool, every directly referenced Wasm hash that is
// not stored yet, and analyzes code that arrived in the metadata itself. It
// drops the previous batch's cache first. Decode problems are left for
// ApplyLedger, which reports them in ledger order.
func (ix *Indexer) PrefetchBatch(ctx context.Context, b Batch) error {
	ix.dropPrefetched()
	var hashes []string
	code := map[string][]byte{}
	for _, f := range b.Facts {
		for _, c := range f.Changes {
			if c.Entry == nil || (c.Kind != Created && c.Kind != Updated && c.Kind != Restored) {
				continue
			}
			d := c.Entry.Data
			switch {
			case d.Type == xdr.LedgerEntryTypeContractCode && d.ContractCode != nil:
				h := hex.EncodeToString(d.ContractCode.Hash[:])
				code[h] = d.ContractCode.Code
			case d.Type == xdr.LedgerEntryTypeContractData && d.ContractData != nil && IsInstanceKey(*d.ContractData):
				in, err := DecodeInstance(*d.ContractData)
				if err == nil && in.Kind == KindWasm && in.WasmHash != "" {
					hashes = append(hashes, in.WasmHash)
				}
			}
		}
	}
	// Code carried in the metadata is analyzed whether or not it is stored:
	// ApplyLedger re-analyzes restored code, and an unused entry is dropped
	// with the next batch.
	var fromMeta []string
	for h := range code {
		fromMeta = append(fromMeta, h)
	}
	owned, _ := ix.claim(fromMeta)
	ix.fill(ctx, owned, code)
	if err := ctx.Err(); err != nil {
		return err
	}
	var fetch []string
	for _, h := range hashes {
		if _, ok := code[h]; !ok {
			fetch = append(fetch, h)
		}
	}
	return ix.Prefetch(ctx, fetch)
}

// writeCached stores one prefetched entry inside the batch transaction,
// exactly as EnsureWasm would have after fetching it. It reports whether
// the Wasm was written (false when queued for retry).
func (ix *Indexer) writeCached(ctx context.Context, tx *store.Tx, h string, p *prefetched, seenLedger uint32) (bool, error) {
	switch {
	case p.err != nil:
		return false, p.err
	case p.fetchErr != nil:
		return false, ix.queueFetch(ctx, tx, []string{h}, p.fetchErr)
	case p.archived:
		return true, tx.UpsertWasm(ctx, store.WasmRow{Hash: h, FirstSeenLedger: seenLedger, ParseStatus: ParseArchived, ParserVersion: sepmeta.ParserVersion})
	}
	a := p.a
	a.wasm.FirstSeenLedger = seenLedger
	return true, ix.writeRows(ctx, tx, h, a)
}
