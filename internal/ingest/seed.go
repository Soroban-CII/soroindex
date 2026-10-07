package ingest

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

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
	for start := 0; start < len(rows); start += rpc.MaxLedgerEntryKeys {
		batch := rows[start:min(start+rpc.MaxLedgerEntryKeys, len(rows))]
		latest, err := ix.seedBatch(ctx, batch, &st)
		if err != nil {
			return st, fmt.Errorf("seed batch at %d: %w", start, err)
		}
		if st.LastLedger == 0 || latest < st.LastLedger {
			st.LastLedger = latest
		}
		ix.Log.LogAttrs(ctx, slog.LevelInfo, "seed batch", slog.Int("done", start+len(batch)), slog.Int("of", len(rows)))
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

func (ix *Indexer) seedBatch(ctx context.Context, batch []SeedRow, st *SeedStats) (uint32, error) {
	keyToRow := map[string]SeedRow{}
	keys := make([]string, 0, len(batch))
	for _, r := range batch {
		k, err := InstanceKey(r.ContractID)
		if err != nil {
			return 0, err
		}
		keyToRow[k] = r
		keys = append(keys, k)
	}
	res, err := ix.RPC.GetLedgerEntries(ctx, keys)
	if err != nil {
		return 0, err
	}
	type seeded struct {
		inst     Instance
		from     uint32
		archived bool
	}
	var got []seeded
	for _, e := range res.Entries {
		row, ok := keyToRow[e.Key]
		if !ok {
			return 0, fmt.Errorf("node returned unrequested key %s", e.Key)
		}
		data, err := DecodeLedgerEntryDataBase64(e.XDR, XDRLimits{MaxBase64Len: 16 << 20})
		if err != nil || data.Type != xdr.LedgerEntryTypeContractData {
			return 0, fmt.Errorf("instance of %s: undecodable (%v)", row.ContractID, err)
		}
		inst, err := DecodeInstance(*data.ContractData)
		if err != nil {
			return 0, fmt.Errorf("instance of %s: %w", row.ContractID, err)
		}
		archived := e.LiveUntilLedgerSeq != nil && *e.LiveUntilLedgerSeq < res.LatestLedger
		got = append(got, seeded{inst: inst, from: e.LastModifiedLedgerSeq, archived: archived})
		if row.WasmHash != "" && inst.Kind == KindWasm && row.WasmHash != inst.WasmHash {
			st.HintMismatch++
		}
	}
	st.NotFound += len(batch) - len(got)

	// Resolve CAP-85 references for this batch.
	refHash := map[[2]string]string{}
	refArchived := map[[2]string]bool{}
	refLedger := map[[2]string]uint32{}
	var refKeys []string
	keyToRef := map[string][2]string{}
	for _, s := range got {
		if s.inst.Kind != KindWasmRef {
			continue
		}
		ref := [2]string{s.inst.RefOwner, s.inst.RefTag}
		if _, dup := refHash[ref]; dup {
			continue
		}
		refHash[ref] = ""
		k, err := ExecRefKey(ref[0], ref[1])
		if err != nil {
			return 0, err
		}
		keyToRef[k] = ref
		refKeys = append(refKeys, k)
	}
	for start := 0; start < len(refKeys); start += rpc.MaxLedgerEntryKeys {
		rr, err := ix.RPC.GetLedgerEntries(ctx, refKeys[start:min(start+rpc.MaxLedgerEntryKeys, len(refKeys))])
		if err != nil {
			return 0, err
		}
		for _, e := range rr.Entries {
			data, err := DecodeLedgerEntryDataBase64(e.XDR, XDRLimits{})
			if err != nil || data.Type != xdr.LedgerEntryTypeContractData {
				return 0, fmt.Errorf("exec ref %s: undecodable (%v)", e.Key, err)
			}
			ref, err := DecodeExecRef(*data.ContractData)
			if err != nil {
				return 0, err
			}
			k := keyToRef[e.Key]
			if k != [2]string{ref.Owner, ref.Tag} {
				return 0, fmt.Errorf("node returned exec ref %s/%s for key %s", ref.Owner, ref.Tag, e.Key)
			}
			refHash[k] = ref.WasmHash
			refLedger[k] = e.LastModifiedLedgerSeq
			refArchived[k] = e.LiveUntilLedgerSeq != nil && *e.LiveUntilLedgerSeq < rr.LatestLedger
		}
	}

	tx, err := ix.Store.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit

	var hashes []string
	for ref, h := range refHash {
		if err := tx.UpsertExecRef(ctx, ref[0], ref[1], h, refLedger[ref], refArchived[ref] || h == ""); err != nil {
			return 0, err
		}
	}
	for i, s := range got {
		h := s.inst.WasmHash
		if s.inst.Kind == KindWasmRef {
			ref := [2]string{s.inst.RefOwner, s.inst.RefTag}
			if refArchived[ref] {
				h = "" // an archived reference resolves to nothing: claim nothing
			} else {
				h = refHash[ref]
			}
			got[i].inst.WasmHash = h
		}
		if h != "" {
			hashes = append(hashes, h)
		}
	}
	fetched, err := ix.EnsureWasm(ctx, tx, dedupe(hashes), 0)
	if err != nil {
		return 0, err
	}
	st.WasmFetched += fetched

	for _, s := range got {
		in := s.inst
		row := store.ContractRow{ID: in.ContractID, Kind: string(in.Kind), CurrentWasmHash: in.WasmHash,
			ExecRefOwner: in.RefOwner, ExecRefTag: in.RefTag, SACAsset: in.SACAsset, UpdatedLedger: s.from, Archived: s.archived}
		if in.Kind == KindSAC {
			row.CurrentWasmHash = ""
		}
		if _, err := ix.ApplyContract(ctx, tx, row, s.from); err != nil {
			return 0, err
		}
		st.Seeded++
		if s.archived {
			st.Archived++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return res.LatestLedger, nil
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
