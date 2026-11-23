package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// latencyRPC wraps a fake RPC with a fixed delay per getLedgerEntries call,
// standing in for network round trips. It records the peak number of calls
// in flight, and can cancel a context after a given number of calls.
type latencyRPC struct {
	RPC
	delay     time.Duration
	mu        sync.Mutex // serializes the wrapped fake, which is not goroutine-safe
	inflight  atomic.Int32
	peak      atomic.Int32
	calls     atomic.Int32
	cancelAt  int32
	cancel    context.CancelFunc
	peakGorou atomic.Int32
}

func (l *latencyRPC) GetLedgerEntries(ctx context.Context, keys []string) (rpc.LedgerEntries, error) {
	n := l.inflight.Add(1)
	defer l.inflight.Add(-1)
	for {
		p := l.peak.Load()
		if n <= p || l.peak.CompareAndSwap(p, n) {
			break
		}
	}
	if g := int32(runtime.NumGoroutine()); g > l.peakGorou.Load() { // #nosec G115 -- goroutine count in a test
		l.peakGorou.Store(g)
	}
	if c := l.calls.Add(1); l.cancel != nil && c == l.cancelAt {
		l.cancel()
	}
	select {
	case <-time.After(l.delay):
	case <-ctx.Done():
		return rpc.LedgerEntries{}, ctx.Err()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.RPC.GetLedgerEntries(ctx, keys)
}

// paddedWasm returns base with a trailing custom section holding i, so each
// i yields a distinct, valid Wasm (and hash) with the same analysis.
func paddedWasm(base []byte, i int) []byte {
	out := append([]byte{}, base...)
	payload := binary.BigEndian.AppendUint32([]byte{3, 'p', 'a', 'd'}, uint32(i)) // #nosec G115 -- test index
	out = append(out, 0, byte(len(payload)))                                      // #nosec G115 -- payload is 8 bytes
	return append(out, payload...)
}

// seedScenario is n contracts over `distinct` distinct Wasm hashes, every
// fourth contract sharing a hash with an earlier batch, so batches prepared
// in parallel contend for the same hashes.
func seedScenario(t testing.TB, n, distinct int) (*entryRPC, string) {
	base, _ := wasmFixture(t, "token_full_sep.wasm")
	f := &entryRPC{t: t, latest: 5000, code: map[string][]byte{}, data: map[string]xdr.ContractDataEntry{},
		lastMod: map[string]uint32{}, expired: map[string]bool{}}
	var hashes []string
	for i := range distinct {
		code := paddedWasm(base, i)
		sum := sha256.Sum256(code)
		h := hex.EncodeToString(sum[:])
		f.code[h] = code
		hashes = append(hashes, h)
	}
	var csv strings.Builder
	csv.WriteString("contract_id\n")
	for i := range n {
		id := benchCID(i)
		k, err := InstanceKey(id)
		if err != nil {
			t.Fatal(err)
		}
		h := hashes[i%distinct]
		if i%4 == 3 {
			h = hashes[(i/2)%distinct]
		}
		cd := instanceEntry(t, execWasm(h), nil)
		cd.Contract = contractAddr(t, id)
		f.data[k] = cd
		f.lastMod[k] = uint32(100 + i) // #nosec G115 -- test index
		fmt.Fprintf(&csv, "%s\n", id)
	}
	return f, csv.String()
}

func benchCID(i int) string {
	var raw [32]byte
	binary.BigEndian.PutUint32(raw[:4], uint32(i+1)) // #nosec G115 -- test index
	id, err := strkey.Encode(strkey.VersionByteContract, raw[:])
	if err != nil {
		panic(err) // a 32-byte payload always encodes
	}
	return id
}

func TestFetchCodeWithBoundsConcurrency(t *testing.T) {
	f, _ := seedScenario(t, 1, 1000) // 1000 hashes = 5 getLedgerEntries calls
	var hashes []string
	for h := range f.code {
		hashes = append(hashes, h)
	}
	for _, workers := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("%d workers", workers), func(t *testing.T) {
			l := &latencyRPC{RPC: f, delay: 20 * time.Millisecond}
			base := runtime.NumGoroutine()
			got, err := FetchCodeWith(context.Background(), l, hashes, workers)
			if err != nil || len(got) != 1000 {
				t.Fatalf("fetched %d: %v", len(got), err)
			}
			if p := l.peak.Load(); p != int32(workers) { // #nosec G115 -- small
				t.Errorf("peak in flight = %d, want exactly %d", p, workers)
			}
			if g := l.peakGorou.Load(); g > int32(base+workers+2) { // #nosec G115 -- small
				t.Errorf("peak goroutines %d with base %d: pool is not bounded", g, base)
			}
		})
	}
}

func TestSeedIsDeterministicAtAnyConcurrency(t *testing.T) {
	f, csvIn := seedScenario(t, 900, 60) // 5 batches
	var dumps []string
	var stats []SeedStats
	for _, c := range []int{1, 4, 16} {
		ix, s := newIndexer(t, &latencyRPC{RPC: f, delay: time.Millisecond})
		ix.Concurrency = c
		st, err := ix.Seed(context.Background(), SeedFileSource{R: strings.NewReader(csvIn)})
		if err != nil {
			t.Fatalf("concurrency %d: %v", c, err)
		}
		dumps = append(dumps, dump(t, s))
		stats = append(stats, st)
	}
	for i := 1; i < len(dumps); i++ {
		if dumps[i] != dumps[0] || stats[i] != stats[0] {
			t.Fatalf("concurrency changed the result: %+v vs %+v", stats[i], stats[0])
		}
	}
	if stats[0].Seeded != 900 || stats[0].WasmFetched != 60 {
		t.Fatalf("stats = %+v: each hash must be fetched and counted once", stats[0])
	}
}

func TestSeedCancellationStopsWorkersAndWritesNoResumePoint(t *testing.T) {
	f, csvIn := seedScenario(t, 2000, 100) // 10 batches
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &latencyRPC{RPC: f, delay: 5 * time.Millisecond, cancelAt: 6, cancel: cancel}
	ix, s := newIndexer(t, l)
	ix.Concurrency = 4
	base := runtime.NumGoroutine()
	_, err := ix.Seed(ctx, SeedFileSource{R: strings.NewReader(csvIn)})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, err := s.State(context.Background(), store.KeyLastLedger); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a cancelled seed wrote last_ledger (%v)", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if g := runtime.NumGoroutine(); g > base {
		t.Fatalf("%d goroutines still running after Seed returned (base %d)", g, base)
	}
	calls := l.calls.Load()
	time.Sleep(50 * time.Millisecond)
	if l.calls.Load() != calls {
		t.Fatal("RPC calls continued after Seed returned")
	}
	// A rerun completes the seed: committed batches are idempotent.
	l.cancel = nil
	st, err := ix.Seed(context.Background(), SeedFileSource{R: strings.NewReader(csvIn)})
	if err != nil || st.Seeded != 2000 || st.LastLedger != 5000 {
		t.Fatalf("rerun: %+v, %v", st, err)
	}
}

// TestPrefetchBatchWritesWhatApplyWouldHave: prefetching a ledger batch on
// the pool and then applying it yields the same database as applying it
// alone, which is what the replay test exercises.
func TestPrefetchBatchWritesWhatApplyWouldHave(t *testing.T) {
	code, h := wasmFixture(t, "token_full_sep.wasm")
	partial, h2 := wasmFixture(t, "token_partial.wasm")
	notTok, h3 := wasmFixture(t, "not_token.wasm")
	h3hash, _ := ParseWasmHash(h3)
	facts := []ContractFacts{
		{Ledger: 10, Changes: []Change{instanceChange(t, seedCID(1), h, Created), instanceChange(t, seedCID(2), h2, Created)}},
		{Ledger: 11, Changes: []Change{
			{Kind: Created, Entry: &xdr.LedgerEntry{Data: xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeContractCode, ContractCode: &xdr.ContractCodeEntry{Hash: h3hash, Code: notTok}}}},
			instanceChange(t, seedCID(3), h3, Created),
		}},
		{Ledger: 12, Changes: []Change{instanceChange(t, seedCID(1), h2, Updated)}},
	}
	run := func(prefetch bool) (string, int32) {
		l := &latencyRPC{RPC: &entryRPC{t: t, latest: 20, code: map[string][]byte{h: code, h2: partial}}}
		ix, s := newIndexer(t, l)
		if prefetch {
			if err := ix.PrefetchBatch(context.Background(), Batch{From: 10, To: 12, Facts: facts}); err != nil {
				t.Fatal(err)
			}
		}
		applyFacts(t, ix, facts...)
		return dump(t, s), l.calls.Load()
	}
	plain, plainCalls := run(false)
	pre, preCalls := run(true)
	if plain != pre {
		t.Fatalf("prefetch changed the result:\nplain:\n%s\nprefetched:\n%s", plain, pre)
	}
	if plainCalls != 2 || preCalls != 1 {
		t.Fatalf("getLedgerEntries calls: %d without prefetch, %d with; want 2 and 1 (both new hashes in one call)", plainCalls, preCalls)
	}
}

// BenchmarkSeedRPCLatency seeds 2,000 contracts over 200 distinct Wasm
// hashes through a fake RPC that adds a fixed delay to every
// getLedgerEntries call, at several pool sizes. Run:
//
//	go test -run '^$' -bench BenchmarkSeedRPCLatency -benchtime 3x ./internal/ingest/
func BenchmarkSeedRPCLatency(b *testing.B) {
	f, csvIn := seedScenario(b, 2000, 200)
	for _, delay := range []time.Duration{25 * time.Millisecond, 100 * time.Millisecond} {
		for _, c := range []int{1, 4, 8} {
			b.Run(fmt.Sprintf("latency=%s/concurrency=%d", delay, c), func(b *testing.B) {
				var calls int32
				for range b.N {
					b.StopTimer()
					l := &latencyRPC{RPC: f, delay: delay}
					ix, s := newIndexer(b, l)
					ix.Concurrency = c
					b.StartTimer()
					st, err := ix.Seed(context.Background(), SeedFileSource{R: strings.NewReader(csvIn)})
					if err != nil || st.Seeded != 2000 {
						b.Fatalf("%+v %v", st, err)
					}
					b.StopTimer()
					calls = l.calls.Load()
					_ = s.Close() // benchmark cleanup; newIndexer's Cleanup closes again harmlessly
				}
				b.ReportMetric(float64(calls), "rpc-calls/op")
			})
		}
	}
}
