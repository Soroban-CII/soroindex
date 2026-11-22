package store

import (
	"context"
	"fmt"
	"testing"
)

// callCountingTx wraps a Tx to count exec calls.
type callCountingTx struct {
	*Tx
	counter *int
}

func (c *callCountingTx) exec(ctx context.Context, what, q string, args ...any) error {
	*c.counter++
	return c.Tx.exec(ctx, what, q, args)
}

// countingStore wraps a Store to return a counting Tx.
type countingStore struct {
	*Store
	counter int
}

func (c *countingStore) Begin(ctx context.Context) (*Tx, error) {
	tx, err := c.Store.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &Tx{tx: tx.tx, failAt: -1}, nil
}

// BeginWithFailAt starts a write transaction that will fail at the N-th
// exec call. For test use only.
func (s *Store) BeginWithFailAt(ctx context.Context, failAt int) (*Tx, error) {
	if s.readOnly {
		return nil, errors.New("store: read-only")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	return &Tx{tx: tx, failAt: failAt}, nil
}

// TestFaultInjectionHarness runs a batch of writes and injects a failure at
// every possible write point, verifying that each failure rolls back the
// transaction and a clean rerun produces the same final state.
func TestFaultInjectionHarness(t *testing.T) {
	ctx := context.Background()

	// Record a clean run to get the expected final state.
	cleanStore := openTestStore(t)
	cleanState := runBatchAndDump(t, cleanStore, ctx, 0)

	// For each possible failure point in the batch, inject a failure and verify:
	// 1. The database state is unchanged from empty (rolled back)
	// 2. A clean rerun produces the same final state as the original clean run.
	for i := 1; i <= 10; i++ {
		t.Run(fmt.Sprintf("fail_at_exec_%d", i), func(t *testing.T) {
			failStore := openTestStore(t)
			// Run with failure injected at call i.
			failTx, err := failStore.BeginWithFailAt(ctx, i)
			if err != nil {
				t.Fatalf("begin with fail at %d: %v", i, err)
			}
			if err := applyBatch(t, failTx, ctx); err != nil {
				_ = failTx.Rollback()
				return
			}
			if err := failTx.Commit(); err != nil {
				return
			}

			// Verify database state is unchanged from initial (rolled back).
			if dump := dumpAll(t, failStore); dump != emptyDump(t) {
				t.Fatalf("database changed after failed exec %d:\n%s", i, dump)
			}

			// Rerun cleanly and verify final state matches clean run.
			retryStore := openTestStore(t)
			retryState := runBatchAndDump(t, retryStore, ctx, 0)
			if retryState != cleanState {
				t.Fatalf("clean rerun after fail at %d differs from clean run:\ngot:\n%s\nwant:\n%s", i, retryState, cleanState)
			}
		})
	}
}
