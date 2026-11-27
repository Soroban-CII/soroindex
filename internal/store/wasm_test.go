package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestFunctionEvidenceSeparatesEveryReasonSignaturesAreAbsent pins the three
// answers a caller must be able to tell apart: an unknown hash, a Wasm with
// no spec, and a Wasm that has a spec but whose signatures were never stored
// (analyzed before migration 0002) and therefore needs a re-fetch.
func TestFunctionEvidenceSeparatesEveryReasonSignaturesAreAbsent(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	withSpec := strings.Repeat("a", 64)
	noSpec := strings.Repeat("b", 64)
	preMigration := strings.Repeat("c", 64)
	badJSON := strings.Repeat("d", 64)
	for _, w := range []WasmRow{
		{Hash: withSpec, HasSpec: true, FunctionsJSON: `[{"name":"balance","inputs":["Address"],"outputs":["i128"]}]`, ParseStatus: "ok", ParserVersion: "1"},
		{Hash: noSpec, ParseStatus: "ok", ParserVersion: "1"},
		{Hash: preMigration, HasSpec: true, ParseStatus: "ok", ParserVersion: "1"},
		{Hash: badJSON, HasSpec: true, FunctionsJSON: `{`, ParseStatus: "ok", ParserVersion: "1"},
	} {
		if err := tx.UpsertWasm(ctx, w); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	t.Run("a stored spec yields its signatures", func(t *testing.T) {
		got, err := s.FunctionEvidence(ctx, withSpec)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Stored || !got.HasSpec || len(got.Functions) != 1 || got.Functions[0].Name != "balance" {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("no spec is not a missing record", func(t *testing.T) {
		got, err := s.FunctionEvidence(ctx, noSpec)
		if err != nil {
			t.Fatal(err)
		}
		if got.Stored || got.HasSpec || len(got.Functions) != 0 {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("a spec with no stored signatures needs a re-fetch", func(t *testing.T) {
		got, err := s.FunctionEvidence(ctx, preMigration)
		if err != nil {
			t.Fatal(err)
		}
		if got.Stored || !got.HasSpec {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("an unknown hash is ErrNotFound", func(t *testing.T) {
		if _, err := s.FunctionEvidence(ctx, strings.Repeat("e", 64)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
	t.Run("a hash that is not canonical is ErrBadQuery", func(t *testing.T) {
		for _, h := range []string{"", "abc", strings.ToUpper(withSpec), strings.Repeat("z", 64)} {
			if _, err := s.FunctionEvidence(ctx, h); !errors.Is(err, ErrBadQuery) {
				t.Fatalf("%q: err = %v, want ErrBadQuery", h, err)
			}
		}
	})
	t.Run("corrupt stored signatures are an error naming the hash", func(t *testing.T) {
		_, err := s.FunctionEvidence(ctx, badJSON)
		if err == nil || !strings.Contains(err.Error(), badJSON) {
			t.Fatalf("err = %v", err)
		}
	})
}
