package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Soroban-CII/soroindex/internal/claims"
	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// Indexer turns network facts into rows. It fetches each Wasm once,
// analyzes it with the same Analyze phase0 uses, and writes contracts,
// versions, Wasm rows, claims, anomalies and interface matches.
type Indexer struct {
	Store   *store.Store
	RPC     RPC
	Rules   []match.RuleFile
	Matcher match.Matcher
	Limits  sepmeta.Limits
	Claims  *claims.Registry
	Log     *slog.Logger
	Now     func() time.Time
}

// EnsureWasm fetches, analyzes and writes every hash in hashes that is not
// already stored as parsed, and returns how many it fetched. seenLedger is
// recorded as first_seen_ledger (0 if unknown). Code the node no longer has
// is written as archived, which never overwrites earlier parsed data or
// deletes its claims.
func (ix *Indexer) EnsureWasm(ctx context.Context, tx *store.Tx, hashes []string, seenLedger uint32) (int, error) {
	var need []string
	for _, h := range hashes {
		ok, err := tx.HasWasm(ctx, h)
		if err != nil {
			return 0, err
		}
		if !ok {
			need = append(need, h)
		}
	}
	if len(need) == 0 {
		return 0, nil
	}
	code, err := FetchCode(ctx, ix.RPC, need)
	if err != nil {
		return 0, err
	}
	for _, h := range need {
		c := code[h]
		if c.Archived {
			if err := tx.UpsertWasm(ctx, store.WasmRow{Hash: h, FirstSeenLedger: seenLedger, ParseStatus: ParseArchived, ParserVersion: sepmeta.ParserVersion}); err != nil {
				return 0, err
			}
			continue
		}
		if err := ix.writeAnalysis(ctx, tx, h, c.Bytes, seenLedger); err != nil {
			return 0, err
		}
	}
	return len(need), nil
}

// writeAnalysis stores one Wasm's parse, claims, anomalies and matches.
func (ix *Indexer) writeAnalysis(ctx context.Context, tx *store.Tx, hash string, code []byte, seenLedger uint32) error {
	r := Analyze(hash, code, ix.Rules, ix.Matcher, ix.Limits)
	metaJSON, err := metaJSON(code, ix.Limits)
	if err != nil {
		return err
	}
	if err := tx.UpsertWasm(ctx, store.WasmRow{
		Hash: hash, SizeBytes: r.Size, FirstSeenLedger: seenLedger, HasMeta: r.HasMeta, HasSpec: r.HasSpec,
		SEPEntryCount: r.Claims.EntryCount, MetaJSON: metaJSON, ParseStatus: r.ParseStatus, ParseError: r.ParseError,
		ParserVersion: sepmeta.ParserVersion,
	}); err != nil {
		return err
	}

	// Claims: every active source replaces its own set. A source error is
	// recorded in the log; claims it found before the error are kept.
	found, derr := ix.Claims.Discover(ctx, claims.WasmArtifact{Hash: hash, Code: code}, claims.ContractRef{})
	if derr != nil {
		ix.Log.LogAttrs(ctx, slog.LevelWarn, "claim source error", slog.String("wasm", hash), slog.String("err", derr.Error()))
	}
	bySource := map[string][]store.ClaimRow{}
	for _, s := range ix.Claims.Active() {
		bySource[s.Name()] = []store.ClaimRow{}
	}
	for _, c := range found {
		bySource[c.Source] = append(bySource[c.Source], store.ClaimRow{SEP: c.SEP, RawToken: c.RawToken, Anomaly: c.Anomaly})
	}
	for src, rows := range bySource {
		if err := tx.ReplaceClaims(ctx, hash, src, rows); err != nil {
			return err
		}
	}

	var anomalies []store.AnomalyRow
	for _, t := range r.Claims.Tokens {
		if !t.Accepted && t.Anomaly != "" {
			anomalies = append(anomalies, store.AnomalyRow{RawToken: t.Raw, Anomaly: t.Anomaly})
		}
	}
	if err := tx.ReplaceAnomalies(ctx, hash, anomalies); err != nil {
		return err
	}
	return ix.writeMatches(ctx, tx, hash, r.Matches)
}

func (ix *Indexer) writeMatches(ctx context.Context, tx *store.Tx, hash string, ms []match.Result) error {
	now := ix.Now().UTC().Format(time.RFC3339)
	for _, m := range ms {
		missing, err1 := json.Marshal(m.Missing)
		mism, err2 := json.Marshal(m.Mismatched)
		variants, err3 := json.Marshal(m.MatchedVariants)
		if err1 != nil || err2 != nil || err3 != nil {
			return fmt.Errorf("encode match for %s: %v %v %v", hash, err1, err2, err3)
		}
		if err := tx.UpsertMatch(ctx, store.MatchRow{Hash: hash, SEP: m.SEP, RulesetVersion: m.RulesetVersion, Status: string(m.Status),
			OKCount: m.OKCount, MissingJSON: string(missing), MismatchedJSON: string(mism), MatchedVariantsJSON: string(variants), ComputedAt: now}); err != nil {
			return err
		}
	}
	return nil
}

// metaJSON renders all meta key/value pairs for wasm.meta_json, or "" when
// there is no readable meta section.
func metaJSON(code []byte, lim sepmeta.Limits) (string, error) {
	secs, err := sepmeta.ReadCustomSections(code, []string{sepmeta.SectionMeta}, lim)
	if err != nil {
		return "", nil // Analyze records the parse error; there is no meta to show
	}
	meta, ok := secs[sepmeta.SectionMeta]
	if !ok {
		return "", nil
	}
	entries, _ := sepmeta.DecodeMeta(meta, lim) // a partial decode still shows what was read
	type kv struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	out := make([]kv, 0, len(entries))
	for _, e := range entries {
		out = append(out, kv{Key: e.Key, Value: e.Value})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ApplyContract writes a contract's state as of ledger, opening a new
// version row if its resolved code or reference changed. from is the
// ledger the version starts at.
func (ix *Indexer) ApplyContract(ctx context.Context, tx *store.Tx, row store.ContractRow, from uint32) (upgraded bool, err error) {
	if err := tx.UpsertContract(ctx, row); err != nil {
		return false, err
	}
	return tx.SetVersion(ctx, store.Version{
		ContractID: row.ID, WasmHash: row.CurrentWasmHash, ExecRefOwner: row.ExecRefOwner, ExecRefTag: row.ExecRefTag, FromLedger: from,
	})
}
