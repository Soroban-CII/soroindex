package sepmeta

import (
	"bytes"
	"errors"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// seedFixtures adds every golden Wasm to the corpus, transformed by pick
// (for example, to extract one section). A nil result is skipped.
func seedFixtures(f *testing.F, pick func([]byte) []byte) {
	names, err := fs.Glob(fixtures, "*.wasm")
	if err != nil || len(names) == 0 {
		f.Fatalf("no fixtures: %v", err)
	}
	for _, n := range names {
		if b := pick(fixture(f, n)); b != nil {
			f.Add(b)
		}
	}
}

func sectionOf(name string) func([]byte) []byte {
	return func(w []byte) []byte {
		s, err := ReadCustomSections(w, []string{name}, DefaultLimits())
		if err != nil {
			return nil
		}
		return s[name]
	}
}

// isClassified reports whether err is one of the package's sentinels, so
// callers can always tell what kind of failure occurred.
func isClassified(err error) bool {
	return errors.Is(err, ErrNotWasm) || errors.Is(err, ErrTruncated) ||
		errors.Is(err, ErrMalformed) || errors.Is(err, ErrLimit)
}

func FuzzReadCustomSections(f *testing.F) {
	seedFixtures(f, func(b []byte) []byte { return b })
	f.Add([]byte{})
	f.Add(append([]byte(nil), wasmHeader...))
	small := Limits{MaxWasmBytes: 4096, MaxSectionBytes: 512, MaxSections: 16}
	names := []string{SectionMeta, SectionSpec}
	f.Fuzz(func(t *testing.T, wasm []byte) {
		for _, lim := range []Limits{DefaultLimits(), small} {
			orig := append([]byte(nil), wasm...)
			got, err := ReadCustomSections(wasm, names, lim)
			if !bytes.Equal(wasm, orig) {
				t.Fatal("input was modified")
			}
			if err != nil {
				if !isClassified(err) {
					t.Fatalf("unclassified error: %v", err)
				}
				if got != nil {
					t.Fatal("non-nil result with error")
				}
				continue
			}
			for name, s := range got {
				if name != SectionMeta && name != SectionSpec {
					t.Fatalf("unrequested section %q returned", name)
				}
				if len(s) > lim.MaxSectionBytes {
					t.Fatalf("section %q is %d bytes, over limit %d", name, len(s), lim.MaxSectionBytes)
				}
				if len(s) > 0 && len(wasm) > 0 {
					s[0] ^= 0xff // mutating the result must not touch the input
					if !bytes.Equal(wasm, orig) {
						t.Fatal("result aliases input")
					}
				}
			}
		}
	})
}

func FuzzDecodeMeta(f *testing.F) {
	seedFixtures(f, sectionOf(SectionMeta))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, section []byte) {
		entries, err := DecodeMeta(section, DefaultLimits())
		if err != nil && !isClassified(err) {
			t.Fatalf("unclassified error: %v", err)
		}
		// Whatever decoded must survive a round trip unchanged.
		var buf bytes.Buffer
		for _, e := range entries {
			v := xdr.ScMetaEntry{Kind: xdr.ScMetaKindScMetaV0, V0: &xdr.ScMetaV0{Key: e.Key, Val: e.Value}}
			if _, err := xdr.Marshal(&buf, v); err != nil {
				t.Fatalf("re-encode: %v", err)
			}
		}
		again, err2 := DecodeMeta(buf.Bytes(), DefaultLimits())
		if err2 != nil || len(again) != len(entries) || (len(entries) > 0 && !reflect.DeepEqual(again, entries)) {
			t.Fatalf("round trip changed entries: %v vs %v (%v)", entries, again, err2)
		}
		_ = ParseSEP47(entries, Lenient) // must not panic on any decoded meta
	})
}

func FuzzDecodeSpec(f *testing.F) {
	seedFixtures(f, sectionOf(SectionSpec))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, section []byte) {
		spec, err := DecodeSpec(section, DefaultLimits())
		if err != nil && !isClassified(err) {
			t.Fatalf("unclassified error: %v", err)
		}
		fns := Functions(spec)
		for _, fn := range fns {
			for _, ty := range append(append([]string{}, fn.Inputs...), fn.Outputs...) {
				if ty == "" || ty == "invalid" || strings.HasPrefix(ty, "unknown:") {
					t.Fatalf("decoded spec rendered type %q in %+v", ty, fn)
				}
			}
		}
		var buf bytes.Buffer
		for _, e := range spec {
			if _, err := xdr.Marshal(&buf, e); err != nil {
				t.Fatalf("re-encode: %v", err)
			}
		}
		again, err2 := DecodeSpec(buf.Bytes(), DefaultLimits())
		if err2 != nil || !reflect.DeepEqual(Functions(again), fns) {
			t.Fatalf("round trip changed functions (%v)", err2)
		}
	})
}

func FuzzParseSEP47(f *testing.F) {
	inputs, err := filepath.Glob(filepath.Join(vectorDir, "*.txt"))
	if err != nil || len(inputs) == 0 {
		f.Fatalf("no vectors: %v", err)
	}
	for _, in := range inputs {
		b, err := fs.ReadFile(vectorFS, filepath.Base(in))
		if err != nil {
			f.Fatal(err)
		}
		lines := strings.SplitN(strings.TrimSuffix(string(b), "\n"), "\n", 2)
		second := ""
		if len(lines) == 2 {
			second = lines[1]
		}
		f.Add(lines[0], second)
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		entries := []MetaEntry{{SEPMetaKey, a}, {"other", "41"}, {SEPMetaKey, b}}
		lenient := ParseSEP47(entries, Lenient)
		strict := ParseSEP47(entries, Strict)
		for _, c := range []SEPClaims{lenient, strict} {
			if c.EntryCount != 2 {
				t.Fatalf("EntryCount = %d, want 2", c.EntryCount)
			}
			seen := map[int]bool{}
			accepted := map[int]bool{}
			for _, tok := range c.Tokens {
				if tok.Accepted {
					accepted[tok.SEP] = true
					if strings.Contains(tok.Raw, ",") {
						t.Fatalf("accepted token %q contains a comma", tok.Raw)
					}
				}
				if len(tok.Raw) > maxValueBytes+len("…") {
					t.Fatalf("raw token of %d bytes kept", len(tok.Raw))
				}
			}
			for _, s := range c.SEPs {
				if seen[s] {
					t.Fatalf("duplicate SEP %d", s)
				}
				seen[s] = true
				if s < 0 || s > 999999 {
					t.Fatalf("SEP %d out of range", s)
				}
				if !accepted[s] {
					t.Fatalf("SEP %d has no accepted token", s)
				}
			}
		}
		for _, tok := range strict.Tokens {
			if tok.Accepted && tok.Anomaly != "" {
				t.Fatalf("strict accepted anomalous token %+v", tok)
			}
		}
		lset := map[int]bool{}
		for _, s := range lenient.SEPs {
			lset[s] = true
		}
		for _, s := range strict.SEPs {
			if !lset[s] {
				t.Fatalf("strict SEP %d missing from lenient %v", s, lenient.SEPs)
			}
		}
	})
}
