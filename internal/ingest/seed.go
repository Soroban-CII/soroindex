package ingest

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// SeedRow is one contract to seed. WasmHash is a hint only: the indexer
// reads the instance on chain and trusts that instead.
type SeedRow struct {
	ContractID string
	WasmHash   string
}

// BackfillSource lists contracts to seed the index with, so a new index
// does not depend on RPC history it cannot reach. SeedFileSource reads a
// CSV; a data-lake source is planned (out of scope for v0.1.0).
type BackfillSource interface {
	Name() string
	Contracts(ctx context.Context) ([]SeedRow, error)
}

// SeedFileSource reads a CSV with a contract_id column and an optional
// wasm_hash column, identified by header; other columns are ignored, so the
// Hubble instance export (scripts/hubble-export.sql Q2) works as is. A file
// with no header row whose first field is a contract ID is read as
// contract_id[,wasm_hash].
type SeedFileSource struct {
	R io.Reader
}

// Name implements BackfillSource.
func (SeedFileSource) Name() string { return "seed-file" }

// Contracts implements BackfillSource. Duplicates are dropped; an invalid
// contract ID is an error naming its line.
func (s SeedFileSource) Contracts(_ context.Context) ([]SeedRow, error) {
	cr := csv.NewReader(s.R)
	cr.FieldsPerRecord = -1
	first, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return nil, errors.New("seed file is empty")
	}
	if err != nil {
		return nil, fmt.Errorf("seed file line 1: %w", err)
	}
	idCol, hashCol := 0, 1
	var rows [][]string
	if isContractID(strings.TrimSpace(first[0])) {
		rows = append(rows, first) // headerless
	} else {
		idCol, hashCol = -1, -1
		for i, h := range first {
			switch strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")) {
			case "contract_id":
				idCol = i
			case "wasm_hash":
				hashCol = i
			}
		}
		if idCol < 0 {
			return nil, fmt.Errorf("seed file header %v has no contract_id column", first)
		}
	}
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("seed file: %w", err)
		}
		rows = append(rows, rec)
	}
	seen := map[string]bool{}
	var out []SeedRow
	for i, rec := range rows {
		if idCol >= len(rec) {
			return nil, fmt.Errorf("seed file row %d: no contract_id field", i+1)
		}
		id := strings.TrimSpace(rec[idCol])
		if !isContractID(id) {
			return nil, fmt.Errorf("seed file row %d: %q is not a contract ID", i+1, id)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		r := SeedRow{ContractID: id}
		if hashCol >= 0 && hashCol < len(rec) {
			r.WasmHash = strings.ToLower(strings.TrimSpace(rec[hashCol]))
		}
		out = append(out, r)
	}
	return out, nil
}

func isContractID(s string) bool {
	_, err := strkey.Decode(strkey.VersionByteContract, s)
	return err == nil
}

// SeedStats summarizes a seed run.
type SeedStats struct {
	Requested    int
	Seeded       int    // instance found and written
	NotFound     int    // no instance entry: not deployed here, or evicted
	Archived     int    // instance returned but its TTL has passed
	HintMismatch int    // the file's wasm_hash differed from the instance
	WasmFetched  int    // hashes fetched and written (each hash once)
	LastLedger   uint32 // sync_state.last_ledger after the seed
}

// Seed writes every contract src lists: its current instance, resolved
// code, Wasm analysis and an initial version row. Each batch of up to 200
// contracts is one transaction. Seeding is idempotent, so a crashed seed is
// resumed by running it again.
//
// Batches are prepared (instance reads, reference resolution, Wasm fetch
// and analysis) by a bounded pool of ix.Concurrency workers, at most
// 2*Concurrency batches ahead of the writer, and committed strictly in file
// order. A failed or cancelled batch stops the pool and nothing after it is
// committed; last_ledger is only written once every batch has committed.
//
// The initial version starts at the instance entry's lastModifiedLedgerSeq:
// the contract's earlier history is unknown to a seed, and the index does
// not claim it. last_ledger is set to the lowest latestLedger any batch
// reported, so incremental sync replays from a point every batch reflects.
func (ix *Indexer) Seed(ctx context.Context, src BackfillSource) (SeedStats, error) {
	rows, err := src.Contracts(ctx)
	if err != nil {
		return SeedStats{}, err
	}
	st := SeedStats{Requested: len(rows)}
	var batches [][]SeedRow
	for start := 0; start < len(rows); start += rpc.MaxLedgerEntryKeys {
		batches = append(batches, rows[start:min(start+rpc.MaxLedgerEntryKeys, len(rows))])
	}
	ix.dropPrefetched()
	defer ix.dropPrefetched()
	done := 0
	err = ix.pipeline(ctx, len(batches), func(ctx context.Context, i int) (seedPrepared, error) {
		return ix.prepareSeed(ctx, batches[i])
	}, func(ctx context.Context, i int, p seedPrepared) error {
		if err := ix.commitSeed(ctx, p, &st); err != nil {
			return fmt.Errorf("seed batch at %d: %w", i*rpc.MaxLedgerEntryKeys, err)
		}
		if st.LastLedger == 0 || p.latest < st.LastLedger {
			st.LastLedger = p.latest
		}
		done += len(batches[i])
		ix.Log.LogAttrs(ctx, slog.LevelInfo, "seed batch", slog.Int("done", done), slog.Int("of", len(rows)))
		return nil
	})
	if err != nil {
		return st, err
	}
	if st.LastLedger == 0 {
		return st, nil
	}
	// Keep the lower of the existing and new resume points. A lower point
	// only replays ledgers (idempotent); a higher one could skip some.
	tx, err := ix.Store.Begin(ctx)
	if err != nil {
		return st, err
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit
	if cur, err := tx.State(ctx, store.KeyLastLedger); err == nil && cur != "" {
		var have uint32
		if _, err := fmt.Sscan(cur, &have); err == nil && have < st.LastLedger {
			st.LastLedger = have
		}
	}
	if err := tx.SetLastLedger(ctx, st.LastLedger); err != nil {
		return st, err
	}
	return st, tx.Commit()
}

// pipeline runs prepare(i) for i in [0, n) on ix.workers() goroutines, at
// most 2*workers items ahead of commit, and calls commit(i) on the calling
// goroutine in index order. The first error from either side cancels every
// worker and is returned after they have all stopped, so no goroutine
// outlives the call and nothing after the failed item is committed.
func pipeline[T any](ctx context.Context, n, workers int, prepare func(context.Context, int) (T, error), commit func(context.Context, int, T) error) error {
	if workers <= 0 {
		workers = DefaultConcurrency
	}
	ctx, cancel := context.WithCancel(ctx)
	type slot struct {
		done chan struct{}
		v    T
		err  error
	}
	slots := make([]slot, n)
	for i := range slots {
		slots[i].done = make(chan struct{})
	}
	ahead := make(chan struct{}, 2*workers) // prepared but not yet committed
	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // dispatcher: hands out indexes in order, bounded by ahead
		defer wg.Done()
		defer close(jobs)
		for i := range n {
			select {
			case ahead <- struct{}{}:
			case <-ctx.Done():
				return
			}
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	for range min(workers, max(n, 1)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				slots[i].v, slots[i].err = prepare(ctx, i)
				close(slots[i].done)
			}
		}()
	}
	stop := func(err error) error {
		cancel()
		wg.Wait()
		return err
	}
	for i := range slots {
		select {
		case <-slots[i].done:
		case <-ctx.Done():
			return stop(ctx.Err())
		}
		if slots[i].err != nil {
			return stop(slots[i].err)
		}
		if err := commit(ctx, i, slots[i].v); err != nil {
			return stop(err)
		}
		var zero T
		slots[i].v = zero // release the batch's memory
		<-ahead
	}
	return stop(nil)
}

func (ix *Indexer) pipeline(ctx context.Context, n int, prepare func(context.Context, int) (seedPrepared, error), commit func(context.Context, int, seedPrepared) error) error {
	return pipeline(ctx, n, ix.workers(), prepare, commit)
}

// seeded is one contract instance read from the node.
type seeded struct {
	inst     Instance
	from     uint32
	archived bool
}

// seedPrepared is everything one seed batch read from the network, ready
// to be written in a single transaction.
type seedPrepared struct {
	got          []seeded
	notFound     int
	hintMismatch int
	refHash      map[[2]string]string
	refArchived  map[[2]string]bool
	refLedger    map[[2]string]uint32
	hashes       []string // resolved Wasm hashes, deduplicated
	latest       uint32
}

// prepareSeed does every network read for one batch without opening a
// transaction: instances, CAP-85 references, and a Wasm prefetch.
func (ix *Indexer) prepareSeed(ctx context.Context, batch []SeedRow) (seedPrepared, error) {
	var p seedPrepared
	keyToRow := map[string]SeedRow{}
	keys := make([]string, 0, len(batch))
	for _, r := range batch {
		k, err := InstanceKey(r.ContractID)
		if err != nil {
			return p, err
		}
		keyToRow[k] = r
		keys = append(keys, k)
	}
	res, err := ix.RPC.GetLedgerEntries(ctx, keys)
	if err != nil {
		return p, err
	}
	p.latest = res.LatestLedger
	for _, e := range res.Entries {
		row, ok := keyToRow[e.Key]
		if !ok {
			return p, fmt.Errorf("node returned unrequested key %s", e.Key)
		}
		data, err := DecodeLedgerEntryDataBase64(e.XDR, XDRLimits{MaxBase64Len: 16 << 20})
		if err != nil || data.Type != xdr.LedgerEntryTypeContractData {
			return p, fmt.Errorf("instance of %s: undecodable (%v)", row.ContractID, err)
		}
		inst, err := DecodeInstance(*data.ContractData)
		if err != nil {
			return p, fmt.Errorf("instance of %s: %w", row.ContractID, err)
		}
		archived := e.LiveUntilLedgerSeq != nil && *e.LiveUntilLedgerSeq < res.LatestLedger
		p.got = append(p.got, seeded{inst: inst, from: e.LastModifiedLedgerSeq, archived: archived})
		if row.WasmHash != "" && inst.Kind == KindWasm && row.WasmHash != inst.WasmHash {
			p.hintMismatch++
		}
	}
	p.notFound = len(batch) - len(p.got)

	// Resolve CAP-85 references for this batch.
	p.refHash = map[[2]string]string{}
	p.refArchived = map[[2]string]bool{}
	p.refLedger = map[[2]string]uint32{}
	var refKeys []string
	keyToRef := map[string][2]string{}
	for _, s := range p.got {
		if s.inst.Kind != KindWasmRef {
			continue
		}
		ref := [2]string{s.inst.RefOwner, s.inst.RefTag}
		if _, dup := p.refHash[ref]; dup {
			continue
		}
		p.refHash[ref] = ""
		k, err := ExecRefKey(ref[0], ref[1])
		if err != nil {
			return p, err
		}
		keyToRef[k] = ref
		refKeys = append(refKeys, k)
	}
	for start := 0; start < len(refKeys); start += rpc.MaxLedgerEntryKeys {
		rr, err := ix.RPC.GetLedgerEntries(ctx, refKeys[start:min(start+rpc.MaxLedgerEntryKeys, len(refKeys))])
		if err != nil {
			return p, err
		}
		for _, e := range rr.Entries {
			data, err := DecodeLedgerEntryDataBase64(e.XDR, XDRLimits{})
			if err != nil || data.Type != xdr.LedgerEntryTypeContractData {
				return p, fmt.Errorf("exec ref %s: undecodable (%v)", e.Key, err)
			}
			ref, err := DecodeExecRef(*data.ContractData)
			if err != nil {
				return p, err
			}
			k := keyToRef[e.Key]
			if k != [2]string{ref.Owner, ref.Tag} {
				return p, fmt.Errorf("node returned exec ref %s/%s for key %s", ref.Owner, ref.Tag, e.Key)
			}
			p.refHash[k] = ref.WasmHash
			p.refLedger[k] = e.LastModifiedLedgerSeq
			p.refArchived[k] = e.LiveUntilLedgerSeq != nil && *e.LiveUntilLedgerSeq < rr.LatestLedger
		}
	}
	var hashes []string
	for i, s := range p.got {
		h := s.inst.WasmHash
		if s.inst.Kind == KindWasmRef {
			ref := [2]string{s.inst.RefOwner, s.inst.RefTag}
			if p.refArchived[ref] {
				h = "" // an archived reference resolves to nothing: claim nothing
			} else {
				h = p.refHash[ref]
			}
			p.got[i].inst.WasmHash = h
		}
		if h != "" {
			hashes = append(hashes, h)
		}
	}
	p.hashes = dedupe(hashes)
	return p, ix.Prefetch(ctx, p.hashes)
}

// commitSeed writes one prepared batch in one transaction. EnsureWasm takes
// the batch's prefetched analyses from the cache, so the only network calls
// left here are for hashes whose prefetch was still in flight elsewhere.
func (ix *Indexer) commitSeed(ctx context.Context, p seedPrepared, st *SeedStats) error {
	tx, err := ix.Store.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit

	for _, ref := range sortedRefs(p.refHash) {
		h := p.refHash[ref]
		if err := tx.UpsertExecRef(ctx, ref[0], ref[1], h, p.refLedger[ref], p.refArchived[ref] || h == ""); err != nil {
			return err
		}
	}
	fetched, err := ix.EnsureWasm(ctx, tx, p.hashes, 0)
	if err != nil {
		return err
	}
	seededN, archivedN := 0, 0
	for _, s := range p.got {
		in := s.inst
		row := store.ContractRow{ID: in.ContractID, Kind: string(in.Kind), CurrentWasmHash: in.WasmHash,
			ExecRefOwner: in.RefOwner, ExecRefTag: in.RefTag, SACAsset: in.SACAsset, UpdatedLedger: s.from, Archived: s.archived}
		if in.Kind == KindSAC {
			row.CurrentWasmHash = ""
		}
		if _, err := ix.ApplyContract(ctx, tx, row, s.from); err != nil {
			return err
		}
		seededN++
		if s.archived {
			archivedN++
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Stats count only committed batches.
	st.WasmFetched += fetched
	st.Seeded += seededN
	st.Archived += archivedN
	st.NotFound += p.notFound
	st.HintMismatch += p.hintMismatch
	return nil
}

// sortedRefs orders executable references so writes are deterministic.
func sortedRefs(m map[[2]string]string) [][2]string {
	out := make([][2]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
