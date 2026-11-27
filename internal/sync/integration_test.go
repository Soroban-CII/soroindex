//go:build integration

package sync

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Soroban-CII/soroindex/internal/claims"
	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/ingest"
	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
	"github.com/Soroban-CII/soroindex/rules"
)

// TestFollowTestnet30Minutes is the Stage F live check. It uses a temporary
// database, never submits transactions, and requires the testnet passphrase.
//
// The follow duration, the thresholds and the sample and index paths come
// from the SEP47IDX_SOAK_* variables (soak.go). With none set it follows for
// 30 minutes against a throwaway index, which is what the nightly
// integration job runs. The soak workflow sets SEP47IDX_SOAK_DURATION and
// the state paths to run the same check for hours at a time, as one segment
// of a longer chain, so there is one follow test and not two.
func TestFollowTestnet30Minutes(t *testing.T) {
	cfg, err := ParseSoakConfig(os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("follow %s, sampling every %s; segment %d; thresholds %s: lag %d ledgers, memory growth %d bytes or x%.2f",
		cfg.Duration, cfg.SampleInterval, cfg.Segment,
		map[bool]string{true: "enforced", false: "reported only"}[cfg.Enforce],
		cfg.MaxLag, cfg.MaxGrowthBytes, cfg.MaxGrowthRatio)
	url := os.Getenv("SEP47IDX_RPC_URL")
	if url == "" {
		url = config.Testnet.DefaultRPCURL
	}
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	client, err := rpc.New(rpc.Config{URL: url, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	// Two minutes above the follow duration: enough for the checks that
	// run after the follow context expires.
	ctx, cancel := context.WithTimeout(t.Context(), cfg.Duration+2*time.Minute)
	defer cancel()
	network, err := client.GetNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if network.Passphrase != config.Testnet.Passphrase {
		t.Fatal("integration test requires testnet")
	}
	tip, err := client.GetLatestLedger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rf, err := match.LoadRules(rules.FS)
	if err != nil {
		t.Fatal(err)
	}
	// An empty SEP47IDX_SOAK_DB keeps the nightly job stateless; a soak
	// segment names a database carried over from the previous segment and
	// resumes it from its stored last_ledger.
	dbPath := cfg.DBPath
	resuming := false
	if dbPath == "" {
		dbPath = filepath.Join(t.TempDir(), "follow.db")
	} else if dir := filepath.Dir(dbPath); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(dbPath); err == nil {
			if info.Size() == 0 {
				t.Fatalf("resume database %s is empty", dbPath)
			}
			resuming = true
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	t.Logf("index %s; starting tip %d", dbPath, tip.Sequence)
	s, err := store.Open(ctx, dbPath, store.Options{Passphrase: network.Passphrase})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	initialState, err := CheckSoakDatabase(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if resuming {
		if initialState.LastLedger == 0 {
			t.Fatal("resume database has no durable sync position")
		}
		t.Logf("resuming last_ledger %d from checkpoint %s", initialState.LastLedger, initialState.Checkpoint)
	}
	sampler, err := NewSoakSampler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ix := &ingest.Indexer{Store: s, RPC: client, Rules: rf, Limits: sepmeta.DefaultLimits(), Claims: claims.Default(sepmeta.DefaultLimits()), Log: log, Now: time.Now}
	followCtx, stop := context.WithTimeout(ctx, cfg.Duration)
	defer stop()
	l := Loop{Store: s, Source: &ingest.LedgerSource{RPC: client}, Apply: ix.ApplyLedger, StartLedger: tip.Sequence, Follow: true}
	l.Progress = func(last, latest uint32, done bool) {
		if sample, ok := sampler.Observe(last, latest, done); ok {
			t.Logf("sample t+%.0fs last=%d latest=%d lag=%d caught_up=%t %s=%d",
				sample.ElapsedSeconds, sample.LastLedger, sample.LatestLedger, sample.Lag, sample.CaughtUp,
				sample.MemorySource, sample.MemoryBytes)
		}
	}
	runErr := l.Run(followCtx)

	// Read the final position before judging anything, so a threshold
	// failure still reports where the index got to.
	lastText, stateErr := s.State(ctx, store.KeyLastLedger)
	var last uint64
	if stateErr == nil {
		last, stateErr = strconv.ParseUint(lastText, 10, 32)
	}
	latest, latestErr := client.GetLatestLedger(ctx)
	if stateErr == nil && latestErr == nil {
		sampler.Record(uint32(last), latest.Sequence, true)
	}
	finalState, databaseErr := CheckSoakDatabase(ctx, s)
	if databaseErr != nil {
		t.Errorf("final database: %v", databaseErr)
	} else if finalState.LastLedger != uint32(last) {
		t.Errorf("final checkpoint ledger %d differs from observed last_ledger %d", finalState.LastLedger, last)
	} else if resuming && finalState.LastLedger <= initialState.LastLedger {
		t.Errorf("resume did not advance: started at %d, finished at %d", initialState.LastLedger, finalState.LastLedger)
	} else {
		t.Logf("database integrity ok; checkpoint %s", finalState.Checkpoint)
	}
	if err := sampler.Close(); err != nil {
		t.Errorf("sample log: %v", err)
	}
	// With a sample directory the series spans every segment so far; without
	// one it is this process's samples.
	series := sampler.Samples()
	if cfg.SampleDir != "" {
		loaded, err := LoadSoakSamples(cfg.SampleDir)
		if err != nil {
			t.Errorf("load samples: %v", err)
		} else {
			series = loaded
		}
	}
	report := EvaluateSoak(series, cfg)
	t.Logf("\n%s", report.Markdown())
	if err := WriteSoakReport(cfg.ReportPath, report); err != nil {
		t.Errorf("write report: %v", err)
	}
	// Enforcement is opt-in. Without SEP47IDX_SOAK_ENFORCE the thresholds
	// are reported and nothing more, so this test's pass/fail conditions
	// are the ones it had before the soak existed: the follow must end in
	// DeadlineExceeded, the index must have advanced, and the final lag
	// must be under 10 ledgers. The soak workflow sets the variable.
	if err := report.EnforcedErr(cfg); err != nil {
		t.Error(err)
	} else if err := report.Err(); err != nil {
		t.Logf("thresholds not enforced (%s unset): %v", EnvSoakEnforce, err)
	}

	if !errors.Is(runErr, context.DeadlineExceeded) || followCtx.Err() == nil {
		t.Fatalf("follow: %v", runErr)
	}
	if stateErr != nil {
		t.Fatal(stateErr)
	}
	if latestErr != nil {
		t.Fatal(latestErr)
	}
	if last <= uint64(tip.Sequence) || last > uint64(latest.Sequence) || uint64(latest.Sequence)-last >= 10 {
		t.Fatalf("start=%d last=%d latest=%d; expected advancement and lag under 10", tip.Sequence, last, latest.Sequence)
	}
	t.Logf("%s follow passed: start=%d last=%d latest=%d lag=%d", cfg.Duration, tip.Sequence, last, latest.Sequence, uint64(latest.Sequence)-last)
}
