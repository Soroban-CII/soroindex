package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Soroban-CII/soroindex/internal/claims"
	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
	"github.com/Soroban-CII/soroindex/rules"
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

// analysis is every row writeAnalysis stores for one Wasm. It is built
// before anything is written, so Inspect can show exactly what the indexer
// would store without opening a database.
type analysis struct {
	wasm      store.WasmRow
	claims    map[string][]store.ClaimRow // by active source; an empty slice clears that source
	anomalies []store.AnomalyRow
	matches   []store.MatchRow
}

// analyze parses one Wasm and turns the result into rows. A claim source
// error is logged and the claims found before it are kept.
func (ix *Indexer) analyze(ctx context.Context, hash string, code []byte, seenLedger uint32) (analysis, error) {
	r := Analyze(hash, code, ix.Rules, ix.Matcher, ix.Limits)
	metaJSON, err := metaJSON(code, ix.Limits)
	if err != nil {
		return analysis{}, err
	}
	fnJSON := ""
	if r.HasSpec {
		b, err := json.Marshal(r.Functions)
		if err != nil {
			return analysis{}, fmt.Errorf("encode functions of %s: %w", hash, err)
		}
		fnJSON = string(b)
	}
	a := analysis{wasm: store.WasmRow{
		Hash: hash, SizeBytes: r.Size, FirstSeenLedger: seenLedger, HasMeta: r.HasMeta, HasSpec: r.HasSpec,
		SEPEntryCount: r.Claims.EntryCount, MetaJSON: metaJSON, FunctionsJSON: fnJSON, ParseStatus: r.ParseStatus, ParseError: r.ParseError,
		ParserVersion: sepmeta.ParserVersion,
	}, claims: map[string][]store.ClaimRow{}}

	found, derr := ix.Claims.Discover(ctx, claims.WasmArtifact{Hash: hash, Code: code}, claims.ContractRef{})
	if derr != nil && ix.Log != nil {
		ix.Log.LogAttrs(ctx, slog.LevelWarn, "claim source error", slog.String("wasm", hash), slog.String("err", derr.Error()))
	}
	for _, s := range ix.Claims.Active() {
		a.claims[s.Name()] = []store.ClaimRow{}
	}
	for _, c := range found {
		a.claims[c.Source] = append(a.claims[c.Source], store.ClaimRow{SEP: c.SEP, RawToken: c.RawToken, Anomaly: c.Anomaly})
	}
	for _, t := range r.Claims.Tokens {
		if !t.Accepted && t.Anomaly != "" {
			a.anomalies = append(a.anomalies, store.AnomalyRow{RawToken: t.Raw, Anomaly: t.Anomaly})
		}
	}
	a.matches, err = ix.matchRows(hash, r.Matches)
	return a, err
}

// writeAnalysis stores one Wasm's parse, claims, anomalies and matches.
func (ix *Indexer) writeAnalysis(ctx context.Context, tx *store.Tx, hash string, code []byte, seenLedger uint32) error {
	a, err := ix.analyze(ctx, hash, code, seenLedger)
	if err != nil {
		return err
	}
	if err := tx.UpsertWasm(ctx, a.wasm); err != nil {
		return err
	}
	for src, rows := range a.claims {
		if err := tx.ReplaceClaims(ctx, hash, src, rows); err != nil {
			return err
		}
	}
	if err := tx.ReplaceAnomalies(ctx, hash, a.anomalies); err != nil {
		return err
	}
	for _, m := range a.matches {
		if err := tx.UpsertMatch(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func (ix *Indexer) writeMatches(ctx context.Context, tx *store.Tx, hash string, ms []match.Result) error {
	rows, err := ix.matchRows(hash, ms)
	if err != nil {
		return err
	}
	for _, m := range rows {
		if err := tx.UpsertMatch(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

// matchRows encodes match results as interface_matches rows, each with the
// SHA-256 of the rule file that produced it.
func (ix *Indexer) matchRows(hash string, ms []match.Result) ([]store.MatchRow, error) {
	now := ix.Now().UTC().Format(time.RFC3339)
	out := make([]store.MatchRow, 0, len(ms))
	for _, m := range ms {
		missing, err1 := json.Marshal(m.Missing)
		mism, err2 := json.Marshal(m.Mismatched)
		variants, err3 := json.Marshal(m.MatchedVariants)
		if err1 != nil || err2 != nil || err3 != nil {
			return nil, fmt.Errorf("encode match for %s: %v %v %v", hash, err1, err2, err3)
		}
		// The rule files live in rules/sep-NNNN.json; look up by SEP number.
		var ruleFileSHA256 string
		for _, rf := range ix.Rules {
			if rf.SEP == m.SEP {
				data, err := rules.FS.ReadFile(fmt.Sprintf("sep-%04d.json", m.SEP))
				if err == nil {
					h := sha256.Sum256(data)
					ruleFileSHA256 = hex.EncodeToString(h[:])
				}
				break
			}
		}
		out = append(out, store.MatchRow{Hash: hash, SEP: m.SEP, RulesetVersion: m.RulesetVersion, RuleFileSHA256: ruleFileSHA256, Status: string(m.Status),
			OKCount: m.OKCount, MissingJSON: string(missing), MismatchedJSON: string(mism), MatchedVariantsJSON: string(variants), ComputedAt: now})
	}
	return out, nil
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
