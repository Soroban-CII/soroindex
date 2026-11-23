package sync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/ingest"
	"github.com/Soroban-CII/soroindex/internal/store"
)

// hashedSource is the fake RPC of loop_test.go extended with ledger hashes
// and protocols, so it implements LedgerHasher. epoch changes every hash,
// which is what a reset looks like when ledger numbers line up again.
type hashedSource struct {
	source
	epoch    string
	protocol func(seq uint32) uint32
	hashReqs int
}

func (s *hashedSource) hash(seq uint32) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s-%d", s.epoch, seq))
	return hex.EncodeToString(sum[:])
}
func (s *hashedSource) LedgerHash(_ context.Context, seq uint32) (string, error) {
	s.hashReqs++
	return s.hash(seq), nil
}
func (s *hashedSource) Fetch(ctx context.Context, from uint32) (ingest.Batch, error) {
	b, err := s.source.Fetch(ctx, from)
	if len(b.Facts) > 200 { // getLedgers returns at most 200 ledgers
		b.Facts, b.To = b.Facts[:200], from+199
	}
	for i := range b.Facts {
		b.Facts[i].Hash = s.hash(b.Facts[i].Ledger)
		b.Facts[i].Protocol = 29
		if s.protocol != nil {
			b.Facts[i].Protocol = s.protocol(b.Facts[i].Ledger)
		}
	}
	return b, err
}

// indexTo syncs a fresh store to ledger `to` with a "before reset" history.
func indexTo(t *testing.T, to uint32) (*store.Store, *hashedSource) {
	t.Helper()
	s := testStore(t)
	src := &hashedSource{source: source{oldest: 1, latest: to}, epoch: "before"}
	l := Loop{Store: s, Source: src, StartLedger: 1, Apply: writeFact}
	if err := l.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s, src
}

func TestNetworkResetDetected(t *testing.T) {
	tests := []struct {
		name           string
		oldest, latest uint32
		epoch          string
	}{
		{"stored last_ledger far ahead of the RPC's latest", 1, 50, "after"},
		{"same ledger numbers, different checkpoint hash", 1, 20000, "after"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := indexTo(t, 18000)
			before := dumpAll(t, s)
			if !strings.Contains(before, "ledger_checkpoint") {
				t.Fatalf("no checkpoint recorded:\n%s", before)
			}
			src := &hashedSource{source: source{oldest: tt.oldest, latest: tt.latest}, epoch: tt.epoch}
			l := Loop{Store: s, Source: src, Apply: func(context.Context, *store.Tx, ingest.ContractFacts) error {
				t.Fatal("apply called after a reset")
				return nil
			}}
			err := l.Run(context.Background())
			if !errors.Is(err, ErrNetworkReset) {
				t.Fatalf("err = %v, want ErrNetworkReset", err)
			}
			if !strings.Contains(err.Error(), "new index") {
				t.Errorf("message does not tell the operator what to do: %v", err)
			}
			if after := dumpAll(t, s); after != before {
				t.Fatalf("database changed after a detected reset:\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

func TestSmallTipLagIsNotAReset(t *testing.T) {
	s, _ := indexTo(t, 100)
	src := &hashedSource{source: source{oldest: 1, latest: 95}, epoch: "before"}
	err := (&Loop{Store: s, Source: src, Apply: writeFact}).Run(context.Background())
	if err == nil || errors.Is(err, ErrNetworkReset) {
		t.Fatalf("a node 5 ledgers behind: err = %v, want the generic behind-tip error", err)
	}
}

func TestMatchingCheckpointContinues(t *testing.T) {
	s, _ := indexTo(t, 100)
	src := &hashedSource{source: source{oldest: 1, latest: 120}, epoch: "before"}
	if err := (&Loop{Store: s, Source: src, Apply: writeFact}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if src.hashReqs != 1 {
		t.Errorf("checkpoint compared %d times, want once per run", src.hashReqs)
	}
	cp, err := s.Checkpoint(context.Background())
	if err != nil || cp.Ledger != 120 || cp.Hash != src.hash(120) {
		t.Fatalf("checkpoint = %+v, %v; want ledger 120", cp, err)
	}
}

func TestCheckpointOutsideRetentionIsNotCompared(t *testing.T) {
	s, _ := indexTo(t, 100)
	// last_ledger 100 == oldest-1: resumable, but ledger 100 itself is gone.
	src := &hashedSource{source: source{oldest: 101, latest: 110}, epoch: "after"}
	if err := (&Loop{Store: s, Source: src, Apply: writeFact}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if src.hashReqs != 0 {
		t.Fatalf("compared a checkpoint the node no longer retains")
	}
}

func TestProtocolStoredAndChangesLogged(t *testing.T) {
	s := testStore(t)
	var logs bytes.Buffer
	src := &hashedSource{source: source{oldest: 1, latest: 10}, epoch: "x", protocol: func(seq uint32) uint32 {
		if seq >= 7 {
			return 30
		}
		return 29
	}}
	l := Loop{Store: s, Source: src, StartLedger: 1, Apply: writeFact, Log: slog.New(slog.NewJSONHandler(&logs, nil))}
	if err := l.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, ok, err := s.LastProtocol(context.Background())
	if err != nil || !ok || p != 30 {
		t.Fatalf("last_protocol = %d %v %v, want 30", p, ok, err)
	}
	if got := strings.Count(logs.String(), `"msg":"protocol change"`); got != 1 ||
		!strings.Contains(logs.String(), `"ledger":7,"from_protocol":29,"to_protocol":30`) {
		t.Fatalf("protocol change log:\n%s", logs.String())
	}
}

// TestPrefetchCancellationKeepsPreviousLastLedger: cancelling while a
// batch's Wasm is being prefetched commits nothing for that batch.
func TestPrefetchCancellationKeepsPreviousLastLedger(t *testing.T) {
	s, _ := indexTo(t, 9)
	before := dumpAll(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := &hashedSource{source: source{oldest: 1, latest: 12}, epoch: "before"}
	l := Loop{Store: s, Source: src, Apply: writeFact, Prefetch: func(ctx context.Context, _ ingest.Batch) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	}}
	if err := l.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if after := dumpAll(t, s); after != before {
		t.Fatalf("cancelled prefetch changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
