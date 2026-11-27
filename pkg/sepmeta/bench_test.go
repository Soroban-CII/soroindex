package sepmeta

import (
	"bytes"
	"io/fs"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// Allocation benchmarks for every parse step over every golden fixture.
// `make bench-budget` runs them with -benchmem and compares allocs/op and
// B/op with pkg/sepmeta/testdata/alloc-budgets.txt (scripts/benchbudget).
// Only allocations are checked: they are deterministic for a given Go and
// SDK version, while ns/op on a shared CI runner is not.
//
// Sub-benchmark names are the fixture file name without ".wasm", so a
// budget line reads, for example, "BenchmarkDecodeSpec/token_full_sep".

// benchFixture is one golden module and what each step needs as input.
type benchFixture struct {
	name    string
	wasm    []byte
	meta    []byte // nil if the module has no meta section or cannot be read
	spec    []byte
	entries []MetaEntry
	specEnt []xdr.ScSpecEntry
}

func benchFixtures(b *testing.B) []benchFixture {
	b.Helper()
	names, err := fs.Glob(fixtures, "*.wasm")
	if err != nil || len(names) == 0 {
		b.Fatalf("no fixtures: %v", err)
	}
	var out []benchFixture
	for _, n := range names {
		f := benchFixture{name: strings.TrimSuffix(n, ".wasm"), wasm: fixture(b, n)}
		if secs, err := ReadCustomSections(f.wasm, []string{SectionMeta, SectionSpec}, DefaultLimits()); err == nil {
			f.meta, f.spec = secs[SectionMeta], secs[SectionSpec]
			f.entries, _ = DecodeMeta(f.meta, DefaultLimits()) // fixtures decode cleanly; truncated.wasm fails above
			f.specEnt, _ = DecodeSpec(f.spec, DefaultLimits())
		}
		out = append(out, f)
	}
	return out
}

var benchNames = []string{SectionMeta, SectionSpec}

func BenchmarkReadCustomSections(b *testing.B) {
	for _, f := range benchFixtures(b) {
		b.Run(f.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(f.wasm)))
			for b.Loop() {
				_, _ = ReadCustomSections(f.wasm, benchNames, DefaultLimits()) // truncated.wasm errors by design; the error path is measured too
			}
		})
	}
}

func BenchmarkReadCustomSectionsAt(b *testing.B) {
	for _, f := range benchFixtures(b) {
		r := bytes.NewReader(f.wasm)
		b.Run(f.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(f.wasm)))
			for b.Loop() {
				_, _ = ReadCustomSectionsAt(r, int64(len(f.wasm)), benchNames, DefaultLimits()) // as above
			}
		})
	}
}

func BenchmarkDecodeMeta(b *testing.B) {
	for _, f := range benchFixtures(b) {
		if f.meta == nil {
			continue // no_meta and truncated have no meta payload to decode
		}
		b.Run(f.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(f.meta)))
			for b.Loop() {
				if _, err := DecodeMeta(f.meta, DefaultLimits()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkDecodeSpec(b *testing.B) {
	for _, f := range benchFixtures(b) {
		if f.spec == nil {
			continue
		}
		b.Run(f.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(f.spec)))
			for b.Loop() {
				if _, err := DecodeSpec(f.spec, DefaultLimits()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkParseSEP47(b *testing.B) {
	for _, f := range benchFixtures(b) {
		if f.meta == nil {
			continue
		}
		b.Run(f.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				ParseSEP47(f.entries, Lenient)
			}
		})
	}
}

func BenchmarkFunctions(b *testing.B) {
	for _, f := range benchFixtures(b) {
		if f.spec == nil {
			continue
		}
		b.Run(f.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				Functions(f.specEnt)
			}
		})
	}
}
