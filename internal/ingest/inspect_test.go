package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// TestInspectMatchesStoredEvidence writes every golden fixture through the
// indexer's own write path, reads it back with store.Wasm (what `sep47idx
// wasm` prints), and requires Inspect to produce the same fields without a
// database.
func TestInspectMatchesStoredEvidence(t *testing.T) {
	fixtures := []string{"token_full_sep.wasm", "token_full_legacy.wasm", "token_partial.wasm", "not_token.wasm", "multi_sep.wasm", "no_meta.wasm", "truncated.wasm"}
	ctx := context.Background()
	ix, s := newIndexer(t, nil)
	rulesets := map[int]string{}
	for _, rf := range ix.Rules {
		rulesets[rf.SEP] = rf.RulesetVersion
	}
	for _, name := range fixtures {
		t.Run(name+" inspect equals indexed detail", func(t *testing.T) {
			code, hash := wasmFixture(t, name)
			tx, err := s.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := ix.writeAnalysis(ctx, tx, hash, code, 7); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			stored, err := s.Wasm(ctx, hash, 1, "", rulesets)
			if err != nil {
				t.Fatal(err)
			}
			covResolver := func(sep int, fns []sepmeta.FnSig) (store.CoverageSummary, []string, bool) {
				for _, rf := range ix.Rules {
					if rf.SEP == sep {
						res := ix.Matcher.Match(fns, rf)
						return store.CoverageSummary{RequiredOK: res.Coverage.RequiredOK, OptionalOK: res.Coverage.OptionalOK, Extra: res.Coverage.Extra}, res.Extra, true
					}
				}
				return store.CoverageSummary{}, nil, false
			}
			if err := s.AnnotateWasmWithCoverage(ctx, &stored, nil, covResolver); err != nil {
				t.Fatal(err)
			}
			got, err := (&Indexer{Rules: ix.Rules, Matcher: ix.Matcher, Limits: ix.Limits, Claims: ix.Claims, Now: ix.Now}).Inspect(ctx, code)
			if err != nil {
				t.Fatal(err)
			}
			want := Evidence{Hash: stored.Hash, Meta: stored.Meta, Claims: stored.Claims, Anomalies: stored.Anomalies, Matches: stored.Matches,
				ParseStatus: stored.ParseStatus, ParseError: stored.ParseError, ParserVersion: stored.ParserVersion}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want)
			if !bytes.Equal(gotJSON, wantJSON) {
				t.Fatalf("inspect drifted from the indexer\n got: %s\nwant: %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestInspectRecordsMalformedInput(t *testing.T) {
	ix, _ := newIndexer(t, nil)
	e, err := ix.Inspect(context.Background(), []byte("not wasm"))
	if err != nil {
		t.Fatal(err)
	}
	if e.ParseStatus != ParseError || e.ParseError == nil || len(e.Claims) != 0 || string(e.Meta) != "null" {
		t.Fatalf("%+v", e)
	}
	if len(e.Matches) != len(ix.Rules) {
		t.Fatalf("want one no_spec result per rule file, got %d", len(e.Matches))
	}
	for _, m := range e.Matches {
		if m.Status != "no_spec" {
			t.Fatalf("malformed input matched: %+v", m)
		}
	}
}
