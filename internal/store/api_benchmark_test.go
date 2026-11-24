package store

import (
	"context"
	"os"
	"runtime"
	"sort"
	"strconv"
	"testing"
	"time"
)

// benchFixture reads the fixture size from the environment so the same
// benchmark runs at the default 1M claims or, through `make bench-10m`, at
// 10M claims and 1M contracts.
//
//	SEP47IDX_BENCH_HASHES     declaring Wasm hashes; claims = 10 x this (default 100000)
//	SEP47IDX_BENCH_CONTRACTS  contracts (default 100000)
//	SEP47IDX_BENCH_DB         keep the fixture at this path and reuse it on the next run
func benchFixture(b *testing.B) (catalogFixture, string) {
	b.Helper()
	f := catalogFixture{Hashes: 100000, Contracts: 100000}
	for _, v := range []struct {
		env string
		dst *int
	}{{"SEP47IDX_BENCH_HASHES", &f.Hashes}, {"SEP47IDX_BENCH_CONTRACTS", &f.Contracts}} {
		if s := os.Getenv(v.env); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n < 100 {
				b.Fatalf("%s=%q: want an integer of at least 100", v.env, s)
			}
			*v.dst = n
		}
	}
	return f, os.Getenv("SEP47IDX_BENCH_DB")
}

// apiQuery is one public query, run the way the API handler runs it.
type apiQuery struct {
	name string
	run  func(context.Context, *Store, fixtureInfo, int) (int, error)
	// fullPage requires every call to return a full page of 50, proving the
	// query did the work a real page needs.
	fullPage bool
}

// cursorAt walks deterministic positions through the first 80% of the sorted
// contract IDs, so catalog pages start mid-keyspace like a paging client.
func cursorAt(in fixtureInfo, i int) string {
	return encodeContractCursor(in.IDs[(i*7919)%(len(in.IDs)*8/10)], nil)
}

var apiQueries = []apiQuery{
	{name: "contracts/declared/implements=41", fullPage: true, run: func(ctx context.Context, s *Store, in fixtureInfo, i int) (int, error) {
		p, err := s.Contracts(ctx, Filter{Implements: []int{41}, Limit: 50, Cursor: cursorAt(in, i)})
		return len(p.Contracts), err
	}},
	// No contract declares SEP-1001, so the keyset scan reads every live
	// contract before returning an empty page: the catalog's worst case.
	{name: "contracts/declared/implements=1001-none", run: func(ctx context.Context, s *Store, _ fixtureInfo, _ int) (int, error) {
		p, err := s.Contracts(ctx, Filter{Implements: []int{1001}, Limit: 50})
		return len(p.Contracts), err
	}},
	{name: "contracts/declared/no-filter", fullPage: true, run: func(ctx context.Context, s *Store, in fixtureInfo, i int) (int, error) {
		p, err := s.Contracts(ctx, Filter{Limit: 50, Cursor: cursorAt(in, i)})
		return len(p.Contracts), err
	}},
	{name: "contracts/declared/implements=41,42", fullPage: true, run: func(ctx context.Context, s *Store, in fixtureInfo, i int) (int, error) {
		p, err := s.Contracts(ctx, Filter{Implements: []int{41, 42}, Limit: 50, Cursor: cursorAt(in, i)})
		return len(p.Contracts), err
	}},
	{name: "contracts/declared/implements_any=41,951", fullPage: true, run: func(ctx context.Context, s *Store, in fixtureInfo, i int) (int, error) {
		p, err := s.Contracts(ctx, Filter{ImplementsAny: []int{41, 951}, Limit: 50, Cursor: cursorAt(in, i)})
		return len(p.Contracts), err
	}},
	{name: "contracts/declared/implements=41&kind=wasm_ref", run: func(ctx context.Context, s *Store, in fixtureInfo, i int) (int, error) {
		p, err := s.Contracts(ctx, Filter{Implements: []int{41}, Kind: "wasm_ref", Limit: 50, Cursor: cursorAt(in, i)})
		return len(p.Contracts), err
	}},
	{name: "contracts/inferred/implements=41", fullPage: true, run: func(ctx context.Context, s *Store, in fixtureInfo, i int) (int, error) {
		p, err := s.Contracts(ctx, Filter{Tier: "inferred", Implements: []int{41}, Limit: 50, Cursor: cursorAt(in, i), Rulesets: fixtureRulesets})
		return len(p.Contracts), err
	}},
	{name: "contracts/verified/implements=41", run: func(ctx context.Context, s *Store, in fixtureInfo, i int) (int, error) {
		p, err := s.Contracts(ctx, Filter{Tier: "verified", Implements: []int{41}, Limit: 50, Cursor: cursorAt(in, i)})
		return len(p.Contracts), err
	}},
	{name: "contracts/protocol/implements=41", fullPage: true, run: func(ctx context.Context, s *Store, in fixtureInfo, i int) (int, error) {
		p, err := s.Contracts(ctx, Filter{Tier: "protocol", Implements: []int{41}, Limit: 50, Cursor: cursorAt(in, i)})
		return len(p.Contracts), err
	}},
	{name: "contracts/{id}", run: func(ctx context.Context, s *Store, in fixtureInfo, i int) (int, error) {
		d, err := s.Contract(ctx, in.IDs[(i*7919)%len(in.IDs)], "testnet", fixtureRulesets)
		return len(d.History), err
	}},
	{name: "contracts/{id}/history", run: func(ctx context.Context, s *Store, in fixtureInfo, i int) (int, error) {
		d, err := s.HistoryPage(ctx, in.IDs[(i*7919)%len(in.IDs)], "testnet", fixtureRulesets, 50, "")
		return len(d.History), err
	}},
	{name: "wasm/{hash}", run: func(ctx context.Context, s *Store, in fixtureInfo, i int) (int, error) {
		d, err := s.Wasm(ctx, fixtureHash((i*7919)%in.Hashes), 50, "", fixtureRulesets)
		return len(d.Claims), err
	}},
	{name: "seps", run: func(ctx context.Context, s *Store, _ fixtureInfo, _ int) (int, error) {
		d, err := s.SEPs(ctx, 50, "", fixtureRulesets)
		return len(d.SEPs), err
	}},
	{name: "stats", run: func(ctx context.Context, s *Store, _ fixtureInfo, _ int) (int, error) {
		d, err := s.Stats(ctx, "testnet", fixtureRulesets)
		return d.TotalContracts, err
	}},
	{name: "gap/41", run: func(ctx context.Context, s *Store, in fixtureInfo, i int) (int, error) {
		p, err := s.Gap(ctx, 41, fixtureRuleset, 50, cursorAt(in, i))
		return len(p.Contracts), err
	}},
}

func percentile(sorted []time.Duration, p int) float64 {
	return float64(sorted[(len(sorted)-1)*p/100]) / float64(time.Millisecond)
}

// BenchmarkAPIQueries measures p50 and p95 for every query behind the API,
// through the read-only Store methods the handlers call (row decoding and
// cursor encoding included, HTTP and JSON encoding excluded). Before timing,
// it asserts the same query plans as TestQueryPlansUseIntendedIndexes on the
// large fixture, so plan and timing evidence come from one database.
//
// Run at 10M claims with `make bench-10m`.
func BenchmarkAPIQueries(b *testing.B) {
	ctx := context.Background()
	f, keep := benchFixture(b)
	start := time.Now()
	ro, in, path := openFixture(b, f, keep)
	b.Logf("go %s %s/%s, %d CPUs; date %s", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), time.Now().UTC().Format("2006-01-02"))
	b.Logf("fixture: %d claims, %d contracts, %d wasm hashes, %d contract_versions, %d interface_matches, %d SEP-41 hashes; db %d bytes; ready in %s",
		in.Claims, in.Contracts, in.Hashes, in.Versions, in.Matches, in.SEP41Hashes, in.SizeBytes, time.Since(start).Round(time.Second))
	checkPlans(ctx, b, path, ro.DB, in)
	if b.Failed() {
		return
	}
	for _, q := range apiQueries {
		b.Run(q.name, func(b *testing.B) {
			// Warm the page cache and the statement path for this query.
			for i := range 3 {
				if _, err := q.run(ctx, ro, in, i); err != nil {
					b.Fatal(err)
				}
			}
			var durations []time.Duration
			i := 0
			for b.Loop() {
				t0 := time.Now()
				n, err := q.run(ctx, ro, in, i)
				durations = append(durations, time.Since(t0))
				if err != nil {
					b.Fatal(err)
				}
				if q.fullPage && n != 50 {
					b.Fatalf("iteration %d returned %d rows, want a full page of 50", i, n)
				}
				i++
			}
			sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
			b.ReportMetric(percentile(durations, 50), "p50-ms")
			b.ReportMetric(percentile(durations, 95), "p95-ms")
		})
	}
}
