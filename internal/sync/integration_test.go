//go:build integration

package sync

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
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
func TestFollowTestnet30Minutes(t *testing.T) {
	url := os.Getenv("SEP47IDX_RPC_URL")
	if url == "" {
		url = config.Testnet.DefaultRPCURL
	}
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	client, err := rpc.New(rpc.Config{URL: url, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 31*time.Minute)
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
	s, err := store.Open(ctx, t.TempDir()+"/follow.db", store.Options{Passphrase: network.Passphrase})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ix := &ingest.Indexer{Store: s, RPC: client, Rules: rf, Limits: sepmeta.DefaultLimits(), Claims: claims.Default(sepmeta.DefaultLimits()), Log: log, Now: time.Now}
	followCtx, stop := context.WithTimeout(ctx, 30*time.Minute)
	defer stop()
	l := Loop{Store: s, Source: &ingest.LedgerSource{RPC: client}, Apply: ix.ApplyLedger, StartLedger: tip.Sequence, Follow: true}
	l.Progress = func(last, latest uint32, done bool) {
		if done {
			t.Logf("caught_up last=%d latest=%d lag=%d", last, latest, latest-last)
		}
	}
	err = l.Run(followCtx)
	if !errors.Is(err, context.DeadlineExceeded) || followCtx.Err() == nil {
		t.Fatalf("follow: %v", err)
	}
	lastText, err := s.State(ctx, store.KeyLastLedger)
	if err != nil {
		t.Fatal(err)
	}
	last, err := strconv.ParseUint(lastText, 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := client.GetLatestLedger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if last <= uint64(tip.Sequence) || last > uint64(latest.Sequence) || uint64(latest.Sequence)-last >= 10 {
		t.Fatalf("start=%d last=%d latest=%d; expected advancement and lag under 10", tip.Sequence, last, latest.Sequence)
	}
	t.Logf("30-minute follow passed: start=%d last=%d latest=%d lag=%d", tip.Sequence, last, latest.Sequence, uint64(latest.Sequence)-last)
}
