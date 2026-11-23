package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// Evidence is what `sep47idx wasm` shows about one Wasm's code, built from a
// local file instead of the database. Field names and order follow
// store.WasmDetail, so `inspect --json` and `wasm --json` agree field for
// field on the parts that come from the code itself. Live users and the
// indexed ledger are not part of it: a local file has neither.
type Evidence struct {
	Hash          string              `json:"wasm_hash"`
	Meta          json.RawMessage     `json:"meta"`
	Claims        []store.WasmClaim   `json:"claims"`
	Anomalies     []store.WasmAnomaly `json:"anomalies"`
	Matches       []store.WasmMatch   `json:"interface_matches"`
	ParseStatus   string              `json:"parse_status"`
	ParseError    *string             `json:"parse_error"`
	ParserVersion string              `json:"parser_version"`
}

// Inspect analyzes code the way the indexer does and returns the rows it
// would store, in the order the store reads them back. It needs no
// database and no RPC. Malformed Wasm is recorded in ParseStatus, as the
// indexer records it, and is not an error.
func (ix *Indexer) Inspect(ctx context.Context, code []byte) (Evidence, error) {
	sum := sha256.Sum256(code)
	hash := hex.EncodeToString(sum[:])
	a, err := ix.analyze(ctx, hash, code, 0)
	if err != nil {
		return Evidence{}, err
	}
	e := Evidence{Hash: hash, Meta: json.RawMessage("null"), Claims: []store.WasmClaim{}, Anomalies: []store.WasmAnomaly{}, Matches: []store.WasmMatch{},
		ParseStatus: a.wasm.ParseStatus, ParserVersion: a.wasm.ParserVersion}
	if a.wasm.MetaJSON != "" {
		if !json.Valid([]byte(a.wasm.MetaJSON)) {
			return Evidence{}, errors.New("inspect: metadata did not encode as JSON")
		}
		e.Meta = json.RawMessage(a.wasm.MetaJSON)
	}
	if a.wasm.ParseError != "" {
		msg := a.wasm.ParseError
		e.ParseError = &msg
	}

	// wasm_claims keys on (sep, source) and a later row replaces an earlier
	// one; the store reads them ordered by sep, then source.
	type claimKey struct {
		sep    int
		source string
	}
	claimsByKey := map[claimKey]store.WasmClaim{}
	for source, rows := range a.claims {
		for _, c := range rows {
			wc := store.WasmClaim{SEP: c.SEP, Source: source, RawToken: c.RawToken}
			if c.Anomaly != "" {
				anomaly := c.Anomaly
				wc.Anomaly = &anomaly
			}
			claimsByKey[claimKey{c.SEP, source}] = wc
		}
	}
	for _, c := range claimsByKey {
		e.Claims = append(e.Claims, c)
	}
	sort.Slice(e.Claims, func(i, j int) bool {
		if e.Claims[i].SEP != e.Claims[j].SEP {
			return e.Claims[i].SEP < e.Claims[j].SEP
		}
		return e.Claims[i].Source < e.Claims[j].Source
	})

	// parse_anomalies keys on raw_token and keeps the first row; the store
	// reads them ordered by raw_token.
	seen := map[string]bool{}
	for _, an := range a.anomalies {
		if seen[an.RawToken] {
			continue
		}
		seen[an.RawToken] = true
		e.Anomalies = append(e.Anomalies, store.WasmAnomaly(an))
	}
	sort.Slice(e.Anomalies, func(i, j int) bool { return e.Anomalies[i].RawToken < e.Anomalies[j].RawToken })

	var fns []sepmeta.FnSig
	haveFns := ix.Versions != nil && a.wasm.FunctionsJSON != "" && json.Unmarshal([]byte(a.wasm.FunctionsJSON), &fns) == nil
	for _, m := range a.matches {
		wm := store.WasmMatch{SEP: m.SEP, RulesetVersion: m.RulesetVersion, Status: m.Status, OKCount: m.OKCount,
			Missing: json.RawMessage(m.MissingJSON), Mismatched: json.RawMessage(m.MismatchedJSON), MatchedVariants: json.RawMessage(m.MatchedVariantsJSON)}
		if haveFns && m.Status != "no_spec" {
			if v := ix.Versions(m.SEP, fns); v != "" {
				wm.MatchedVersion = &v
			}
		}
		e.Matches = append(e.Matches, wm)
	}
	sort.SliceStable(e.Matches, func(i, j int) bool { return e.Matches[i].SEP < e.Matches[j].SEP })
	return e, nil
}
