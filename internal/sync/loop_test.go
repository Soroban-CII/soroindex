package sync

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/ingest"
	"github.com/Soroban-CII/soroindex/internal/store"
)

type source struct {
	oldest, latest uint32
	windows        int
	advance        bool
}

func (s *source) Window(context.Context) (ingest.Window, error) {
	s.windows++
	if s.advance && s.windows == 2 {
		s.latest++
	}
	return ingest.Window{Oldest: s.oldest, Latest: s.latest}, nil
}
func (s *source) Fetch(_ context.Context, from uint32) (ingest.Batch, error) {
	b := ingest.Batch{From: from, To: s.latest, Latest: s.latest}
	for n := from; n <= s.latest; n++ {
		b.Facts = append(b.Facts, ingest.ContractFacts{Ledger: n})
	}
	return b, nil
}
func testStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "sync.db"), store.Options{Passphrase: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func writeFact(ctx context.Context, tx *store.Tx, f ingest.ContractFacts) error {
	return tx.UpsertContract(ctx, store.ContractRow{ID: "C1", Kind: "sac", UpdatedLedger: f.Ledger})
}
func TestCrashMidBatchRollsBackAndResumes(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.SetState(context.Background(), store.KeyLastLedger, "9"); err != nil {
		t.Fatal(err)
	}
	writes := 0
	l := Loop{Store: s, Source: &source{oldest: 1, latest: 12}, Apply: func(ctx context.Context, tx *store.Tx, f ingest.ContractFacts) error {
		if err := writeFact(ctx, tx, f); err != nil {
			return err
		}
		writes++
		if writes == 2 {
			cancel()
			return ctx.Err()
		}
		return nil
	}}
	if err := l.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("crash: %v", err)
	}
	if last, err := s.State(context.Background(), store.KeyLastLedger); err != nil || last != "9" {
		t.Fatalf("last %q: %v", last, err)
	}
	var n int
	if err := s.DB.QueryRow(`SELECT count(*) FROM contracts`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial batch leaked %d: %v", n, err)
	}
	l.Apply = writeFact
	if err := l.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if last, err := s.State(context.Background(), store.KeyLastLedger); err != nil || last != "12" {
		t.Fatalf("resume %q: %v", last, err)
	}
}
func TestRetentionGapWritesNothing(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "resume"}[existing], func(t *testing.T) {
			s := testStore(t)
			if existing {
				if err := s.SetState(context.Background(), store.KeyLastLedger, "9"); err != nil {
					t.Fatal(err)
				}
			}
			var before int
			if err := s.DB.QueryRow(`SELECT count(*) FROM sync_state`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			l := Loop{Store: s, Source: &source{oldest: 11, latest: 20}, StartLedger: 10, Apply: func(context.Context, *store.Tx, ingest.ContractFacts) error {
				t.Fatal("apply called across retention gap")
				return nil
			}}
			if err := l.Run(context.Background()); !errors.Is(err, ErrRetentionGap) {
				t.Fatal(err)
			}
			var after int
			if err := s.DB.QueryRow(`SELECT count(*) FROM sync_state`).Scan(&after); err != nil || after != before {
				t.Fatalf("state changed %d -> %d: %v", before, after, err)
			}
		})
	}
}
func TestCaughtUpRechecksTip(t *testing.T) {
	s := testStore(t)
	src := &source{oldest: 1, latest: 10, advance: true}
	caught := uint32(0)
	l := Loop{Store: s, Source: src, StartLedger: 10, Apply: writeFact, Progress: func(last, latest uint32, done bool) {
		if done {
			caught = last
			if last != latest {
				t.Fatal("incorrect caught_up")
			}
		}
	}}
	if err := l.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if caught != 11 || src.windows != 3 {
		t.Fatalf("caught at %d, windows %d", caught, src.windows)
	}
}
func TestFirstRunRequiresStartOrSeed(t *testing.T) {
	l := Loop{Store: testStore(t), Source: &source{oldest: 1, latest: 10}, Apply: writeFact}
	if err := l.Run(context.Background()); err == nil {
		t.Fatal("uninitialized index accepted")
	}
}
func TestFollowCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := testStore(t)
	l := Loop{Store: s, Source: &source{oldest: 1, latest: 10}, StartLedger: 10, Follow: true, Apply: writeFact, Progress: func(_, _ uint32, done bool) {
		if done {
			cancel()
		}
	}}
	if err := l.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// dumpAll renders every row of every table, in a stable order, so two dumps
// are equal exactly when the database contents are equal.
func dumpAll(t *testing.T, s *store.Store) string {
	t.Helper()
	tables, err := s.DB.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var n string
		if err := tables.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if err := tables.Close(); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, n := range names {
		rows, err := s.DB.Query(`SELECT * FROM "` + n + `" ORDER BY 1`) // #nosec G202 -- table names come from sqlite_master in a test
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "== %s %v\n", n, cols)
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "%v\n", vals)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return b.String()
}

// TestRetentionGapLeavesEveryTableUnchanged strengthens
// TestRetentionGapWritesNothing: instead of counting sync_state rows, it
// compares a dump of every table before and after the refused run, on an
// index that already holds a contract, a version and a resume point.
func TestRetentionGapLeavesEveryTableUnchanged(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.UpsertContract(ctx, store.ContractRow{ID: "C1", Kind: "sac", UpdatedLedger: 9}); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.SetVersion(ctx, store.Version{ContractID: "C1", FromLedger: 9}); err != nil {
		t.Fatal(err)
	}
	if err = tx.SetLastLedger(ctx, 9); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	before := dumpAll(t, s)
	if !strings.Contains(before, "C1") || !strings.Contains(before, "last_ledger") {
		t.Fatalf("setup not visible in dump:\n%s", before)
	}
	l := Loop{Store: s, Source: &source{oldest: 11, latest: 20}, Apply: func(context.Context, *store.Tx, ingest.ContractFacts) error {
		t.Fatal("apply called across retention gap")
		return nil
	}}
	if err := l.Run(ctx); !errors.Is(err, ErrRetentionGap) {
		t.Fatal(err)
	}
	if after := dumpAll(t, s); after != before {
		t.Fatalf("database changed across a refused run:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// --- Issue #92: --stop-ledger ---

func TestStopLedgerTruncatesBatchAndExits(t *testing.T) {
	s := testStore(t)
	applied := []uint32{}
	l := Loop{Store: s, Source: &source{oldest: 1, latest: 20}, StartLedger: 10, StopLedger: 15, Apply: func(ctx context.Context, tx *store.Tx, f ingest.ContractFacts) error {
		applied = append(applied, f.Ledger)
		return writeFact(ctx, tx, f)
	}}
	if err := l.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	last, err := s.State(context.Background(), store.KeyLastLedger)
	if err != nil || last != "15" {
		t.Fatalf("last_ledger = %q: %v", last, err)
	}
	// Ledgers 10..15 should have been applied (StartLedger means start from 10, so last = 9 before run).
	if len(applied) == 0 || applied[len(applied)-1] != 15 {
		t.Fatalf("applied ledgers %v: expected last=15", applied)
	}
	for _, n := range applied {
		if n > 15 {
			t.Fatalf("ledger %d committed past StopLedger=15", n)
		}
	}
}

func TestStopLedgerAtBatchEdge(t *testing.T) {
	s := testStore(t)
	// Source returns exactly 11 ledgers (10..20). StopLedger=20 == batch end.
	l := Loop{Store: s, Source: &source{oldest: 1, latest: 20}, StartLedger: 10, StopLedger: 20, Apply: writeFact}
	if err := l.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if last, err := s.State(context.Background(), store.KeyLastLedger); err != nil || last != "20" {
		t.Fatalf("last_ledger = %q: %v", last, err)
	}
}

func TestStopLedgerAlreadyPastExitsImmediately(t *testing.T) {
	s := testStore(t)
	if err := s.SetState(context.Background(), store.KeyLastLedger, "20"); err != nil {
		t.Fatal(err)
	}
	applied := 0
	l := Loop{Store: s, Source: &source{oldest: 1, latest: 30}, StopLedger: 15, Apply: func(ctx context.Context, tx *store.Tx, f ingest.ContractFacts) error {
		applied++
		return writeFact(ctx, tx, f)
	}}
	if err := l.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if applied != 0 {
		t.Fatalf("applied %d ledgers but should have exited immediately", applied)
	}
}

func TestStopLedgerBeforeStartRejected(t *testing.T) {
	l := Loop{Store: testStore(t), Source: &source{oldest: 1, latest: 20}, StartLedger: 10, StopLedger: 5, Apply: writeFact}
	err := l.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "stop-ledger") {
		t.Fatalf("expected stop-ledger error, got %v", err)
	}
}

// --- Issue #91: cross-provider hash verification ---

// hashSource is a Source whose Fetch populates Batch.Hashes with generated hash values.
type hashSource struct {
	source
	hashFn func(ledger uint32) string
}

func (hs *hashSource) Fetch(ctx context.Context, from uint32) (ingest.Batch, error) {
	b, err := hs.source.Fetch(ctx, from)
	if err != nil {
		return b, err
	}
	for n := b.From; n <= b.To; n++ {
		b.Hashes = append(b.Hashes, hs.hashFn(n))
	}
	return b, nil
}

func TestVerifyBatchHashMismatchHaltsLoop(t *testing.T) {
	s := testStore(t)
	// Primary source produces hashes "hash-N"; verifier sees "DIFFERENT-N".
	primary := &hashSource{source: source{oldest: 1, latest: 10}, hashFn: func(n uint32) string { return fmt.Sprintf("hash-%d", n) }}
	l := Loop{Store: s, Source: primary, StartLedger: 1, Apply: writeFact,
		VerifyBatch: func(_ context.Context, b ingest.Batch) error {
			for i, h := range b.Hashes {
				badHash := fmt.Sprintf("DIFFERENT-%d", b.From+uint32(i)) // #nosec G115 -- i < 200
				if h == badHash {
					return fmt.Errorf("ledger %d hash mismatch: primary %s, secondary %s", b.From+uint32(i), h, badHash)
				}
				// Simulate a mismatch on the first ledger.
				return fmt.Errorf("ledger %d hash mismatch: primary %s, secondary %s", b.From, h, badHash)
			}
			return nil
		}}
	err := l.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("expected hash mismatch error, got %v", err)
	}
	// Nothing should have been committed.
	if _, err2 := s.State(context.Background(), store.KeyLastLedger); !errors.Is(err2, store.ErrNotFound) {
		t.Fatal("last_ledger should not be set after a verify failure")
	}
}

func TestVerifyBatchSuccessCommits(t *testing.T) {
	s := testStore(t)
	primary := &hashSource{source: source{oldest: 1, latest: 10}, hashFn: func(n uint32) string { return fmt.Sprintf("hash-%d", n) }}
	calls := 0
	l := Loop{Store: s, Source: primary, StartLedger: 1, Apply: writeFact,
		VerifyBatch: func(_ context.Context, b ingest.Batch) error {
			calls++
			return nil // agreement: no mismatch
		}}
	if err := l.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls == 0 {
		t.Fatal("VerifyBatch was never called")
	}
	if last, err := s.State(context.Background(), store.KeyLastLedger); err != nil || last != "10" {
		t.Fatalf("last_ledger = %q: %v", last, err)
	}
}
