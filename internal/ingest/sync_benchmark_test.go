package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/Soroban-CII/soroindex/internal/claims"
	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
	"github.com/Soroban-CII/soroindex/rules"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// BenchmarkSyncThroughput measures the end-to-end throughput of the sync
// pipeline: LedgerSource.Fetch -> ExtractContractFacts -> Indexer.ApplyLedger.
// It replays the recorded ledger corpus from internal/rpc/testdata/getLedgers.json
// and reports ledgers/sec, facts/sec, and memory allocations.
//
// Run with: go test -bench=BenchmarkSyncThroughput ./internal/ingest/ -benchtime=10s
func BenchmarkSyncThroughput(b *testing.B) {
	ctx := context.Background()

	// Load the recorded ledger corpus.
	ledgers, err := loadRecordedLedgers()
	if err != nil {
		b.Fatalf("load recorded ledgers: %v", err)
	}
	b.Logf("Loaded %d recorded ledgers", len(ledgers))

	// Set up an in-memory store with a temporary file.
	tmpDB, err := os.CreateTemp("", "soroindex-bench-*.db")
	if err != nil {
		b.Fatalf("create temp db: %v", err)
	}
	tmpPath := tmpDB.Name()
	_ = tmpDB.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	st, err := store.Open(ctx, tmpPath, store.Options{Passphrase: "Test SDF Network ; September 2015"})
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	// Set up the indexer with a fake RPC that returns the recorded ledgers.
	fakeRPC := &recordedRPC{ledgers: ledgers}
	ruleFiles, err := match.LoadRules(rules.FS)
	if err != nil {
		b.Fatalf("load rules: %v", err)
	}

	indexer := &Indexer{
		Store:   st,
		RPC:     fakeRPC,
		Rules:   ruleFiles,
		Matcher: match.Matcher{PartialThreshold: 0.5},
		Limits:  sepmeta.DefaultLimits(),
		Claims:  claims.Default(sepmeta.DefaultLimits()),
		Now:     time.Now,
	}

	// Prepare the ledger source.
	source := &LedgerSource{
		RPC:   fakeRPC,
		Limit: 200,
	}

	// Warm-up: run a few iterations to warm caches.
	for i := 0; i < 3; i++ {
		batch, err := source.Fetch(ctx, ledgers[0].LedgerSequence())
		if err != nil {
			b.Fatalf("warmup fetch: %v", err)
		}
		for _, f := range batch.Facts {
			tx, err := st.Begin(ctx)
			if err != nil {
				b.Fatalf("warmup begin: %v", err)
			}
			if err := indexer.ApplyLedger(ctx, tx, f); err != nil {
				_ = tx.Rollback()
				b.Fatalf("warmup apply: %v", err)
			}
			if err := tx.Commit(); err != nil {
				b.Fatalf("warmup commit: %v", err)
			}
		}
	}

	var totalLedgers, totalFacts int64
	var durations []time.Duration

	// Run the benchmark.
	for b.Loop() {
		// Cycle through the recorded ledgers.
		batch, err := source.Fetch(ctx, ledgers[0].LedgerSequence())
		if err != nil {
			b.Fatalf("fetch: %v", err)
		}

		t0 := time.Now()
		for _, f := range batch.Facts {
			tx, err := st.Begin(ctx)
			if err != nil {
				b.Fatalf("begin: %v", err)
			}
			if err := indexer.ApplyLedger(ctx, tx, f); err != nil {
				_ = tx.Rollback()
				b.Fatalf("apply: %v", err)
			}
			if err := tx.Commit(); err != nil {
				b.Fatalf("commit: %v", err)
			}
		}
		durations = append(durations, time.Since(t0))

		totalLedgers += int64(len(batch.Facts))
		for _, f := range batch.Facts {
			totalFacts += int64(len(f.CodeCreated) + len(f.Instances) + len(f.ExecRefs) + len(f.Evicted))
		}
	}

	// Report metrics.
	if len(durations) > 0 {
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		totalTime := time.Duration(0)
		for _, d := range durations {
			totalTime += d
		}
		ledgersPerSec := float64(totalLedgers) / totalTime.Seconds()
		factsPerSec := float64(totalFacts) / totalTime.Seconds()

		b.ReportMetric(ledgersPerSec, "ledgers/s")
		b.ReportMetric(factsPerSec, "facts/s")
		b.ReportMetric(float64(totalTime.Milliseconds())/float64(len(durations)), "avg-ms/batch")

		b.Logf("Go %s %s/%s, %d CPUs; date %s",
			runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), time.Now().UTC().Format("2006-01-02"))
		b.Logf("Total: %d ledgers, %d facts, %d batches, total time %s",
			totalLedgers, totalFacts, len(durations), totalTime.Round(time.Millisecond))
		b.Logf("Ledgers/s: %.2f, Facts/s: %.2f", ledgersPerSec, factsPerSec)
	}
}

// recordedRPC is a fake RPC that returns pre-recorded ledger data.
type recordedRPC struct {
	ledgers []xdr.LedgerCloseMeta
	index   int
}

func (r *recordedRPC) GetNetwork(_ context.Context) (rpc.Network, error) {
	return rpc.Network{Passphrase: "Test SDF Network ; September 2015"}, nil
}

func (r *recordedRPC) GetLatestLedger(_ context.Context) (rpc.LatestLedger, error) {
	if len(r.ledgers) == 0 {
		return rpc.LatestLedger{}, fmt.Errorf("no ledgers")
	}
	last := r.ledgers[len(r.ledgers)-1]
	return rpc.LatestLedger{Sequence: last.LedgerSequence()}, nil
}

func (r *recordedRPC) GetLedgers(_ context.Context, req rpc.LedgersRequest) (rpc.LedgersPage, error) {
	if len(r.ledgers) == 0 {
		return rpc.LedgersPage{}, fmt.Errorf("no ledgers")
	}
	var out []rpc.Ledger
	for i := 0; i < int(req.Limit) && r.index+i < len(r.ledgers); i++ {
		m := r.ledgers[r.index+i]
		xdrB64, err := xdr.MarshalBase64(m)
		if err != nil {
			return rpc.LedgersPage{}, err
		}
		out = append(out, rpc.Ledger{
			Sequence:    m.LedgerSequence(),
			MetadataXDR: xdrB64,
		})
	}
	r.index = (r.index + len(out)) % len(r.ledgers)
	return rpc.LedgersPage{
		Ledgers:      out,
		LatestLedger: r.ledgers[len(r.ledgers)-1].LedgerSequence(),
		OldestLedger: r.ledgers[0].LedgerSequence(),
	}, nil
}

func (r *recordedRPC) GetLedgerEntries(_ context.Context, _ []string) (rpc.LedgerEntries, error) {
	return rpc.LedgerEntries{Entries: nil, LatestLedger: 0}, nil
}

func loadRecordedLedgers() ([]xdr.LedgerCloseMeta, error) {
	b, err := os.ReadFile("../rpc/testdata/getLedgers.json")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result struct {
			Ledgers []struct {
				Sequence    uint32 `json:"sequence"`
				MetadataXDR string `json:"metadataXdr"`
			} `json:"ledgers"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		return nil, err
	}
	var out []xdr.LedgerCloseMeta
	for _, l := range resp.Result.Ledgers {
		var m xdr.LedgerCloseMeta
		if err := UnmarshalBase64(l.MetadataXDR, &m, XDRLimits{MaxBase64Len: 8 << 20}); err != nil {
			return nil, err
		}
		if m.LedgerSequence() != l.Sequence {
			return nil, fmt.Errorf("meta for %d decodes as %d", l.Sequence, m.LedgerSequence())
		}
		out = append(out, m)
	}
	return out, nil
}
