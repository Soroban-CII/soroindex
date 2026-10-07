// Package phase0 measures SEP-47 adoption across a network: the adoption
// report that gates the rest of the project (CLAUDE.md §5.10). It parses
// Wasm with pkg/sepmeta and checks interfaces with internal/match; it never
// executes Wasm.
package phase0

import (
	"errors"

	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// ParseStatus values match the wasm.parse_status column (CLAUDE.md §5.8).
const (
	ParseOK       = "ok"       // every section present decoded fully
	ParsePartial  = "partial"  // a section decoded only in part
	ParseError    = "error"    // the module could not be read at all
	ParseArchived = "archived" // the code entry is missing or archived; not fetched
)

// WasmResult is everything phase0 learns from one Wasm.
type WasmResult struct {
	Hash        string
	Size        int
	ParseStatus string
	ParseError  string
	HasMeta     bool
	HasSpec     bool
	Claims      sepmeta.SEPClaims
	Matches     []match.Result // one per rule file, in rule order
}

// Analyze parses one Wasm and matches it against every rule file. Malformed
// input is recorded in the result, never returned as an error: one bad
// contract must not stop a census.
func Analyze(hash string, code []byte, rules []match.RuleFile, m match.Matcher, lim sepmeta.Limits) WasmResult {
	r := WasmResult{Hash: hash, Size: len(code), ParseStatus: ParseOK, Claims: sepmeta.ParseSEP47(nil, sepmeta.Lenient)}
	secs, err := sepmeta.ReadCustomSections(code, []string{sepmeta.SectionMeta, sepmeta.SectionSpec}, lim)
	if err != nil {
		r.ParseStatus, r.ParseError = ParseError, err.Error()
		for _, rf := range rules {
			r.Matches = append(r.Matches, match.NoSpec(rf))
		}
		return r
	}
	var errs []error
	if meta, ok := secs[sepmeta.SectionMeta]; ok {
		r.HasMeta = true
		entries, err := sepmeta.DecodeMeta(meta, lim)
		if err != nil {
			errs = append(errs, err)
		}
		// Entries decoded before an error still count: the declaration is
		// what the contract says, and the anomaly is recorded alongside.
		r.Claims = sepmeta.ParseSEP47(entries, sepmeta.Lenient)
	}
	var fns []sepmeta.FnSig
	if spec, ok := secs[sepmeta.SectionSpec]; ok {
		r.HasSpec = true
		entries, err := sepmeta.DecodeSpec(spec, lim)
		if err != nil {
			errs = append(errs, err)
		}
		fns = sepmeta.Functions(entries)
	}
	for _, rf := range rules {
		if !r.HasSpec {
			r.Matches = append(r.Matches, match.NoSpec(rf))
			continue
		}
		r.Matches = append(r.Matches, m.Match(fns, rf))
	}
	if len(errs) > 0 {
		r.ParseStatus, r.ParseError = ParsePartial, errors.Join(errs...).Error()
	}
	return r
}

// Archived is the result for a Wasm whose code could not be fetched.
func Archived(hash string, rules []match.RuleFile) WasmResult {
	r := WasmResult{Hash: hash, ParseStatus: ParseArchived, Claims: sepmeta.ParseSEP47(nil, sepmeta.Lenient)}
	for _, rf := range rules {
		r.Matches = append(r.Matches, match.NoSpec(rf))
	}
	return r
}

// MatchFor returns the result for one SEP, or false if no rule file covers it.
func (w WasmResult) MatchFor(sep int) (match.Result, bool) {
	for _, m := range w.Matches {
		if m.SEP == sep {
			return m, true
		}
	}
	return match.Result{}, false
}

// Declares reports whether the Wasm's meta declares sep.
func (w WasmResult) Declares(sep int) bool {
	for _, s := range w.Claims.SEPs {
		if s == sep {
			return true
		}
	}
	return false
}
