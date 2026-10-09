// Package sync advances an index through consecutive, final Stellar ledgers.
package sync

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Soroban-CII/soroindex/internal/ingest"
	"github.com/Soroban-CII/soroindex/internal/store"
)

// ErrRetentionGap means the resume point is older than the RPC's retained history.
var ErrRetentionGap = errors.New("resume point is outside RPC retention; seed or backfill the index")

// Source supplies consecutive ledger batches and fresh retention/tip observations.
type Source interface {
	Window(context.Context) (ingest.Window, error)
	Fetch(context.Context, uint32) (ingest.Batch, error)
}

// Loop commits facts and the resume point atomically, then follows the tip if requested.
type Loop struct {
	Store       *store.Store
	Source      Source
	Apply       func(context.Context, *store.Tx, ingest.ContractFacts) error
	StartLedger uint32
	Follow      bool
	Interval    time.Duration
	// Progress runs after a batch commits or a fresh tip confirms caught_up.
	Progress func(last, latest uint32, caughtUp bool)
}

// Run resumes stored progress; StartLedger is required only for an unseeded index.
// Retention is checked before any writes, including initialization of last_ledger.
func (l *Loop) Run(ctx context.Context) error {
	if l.Store == nil || l.Source == nil || l.Apply == nil {
		return errors.New("sync: store, source and apply are required")
	}
	if l.Interval < 0 {
		return errors.New("sync: interval must be positive")
	}
	interval := l.Interval
	if interval == 0 {
		interval = 5 * time.Second
	}
	last, err := l.lastLedger(ctx)
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		w, err := l.Source.Window(ctx)
		if err != nil {
			return err
		}
		if w.Oldest == 0 || w.Oldest > w.Latest {
			return errors.New("sync: invalid retention window")
		}
		if last < w.Oldest-1 {
			return fmt.Errorf("%w: last %d, oldest %d", ErrRetentionGap, last, w.Oldest)
		}
		if last > w.Latest {
			return fmt.Errorf("sync: stored ledger %d exceeds RPC latest %d", last, w.Latest)
		}
		if last == w.Latest {
			if l.Progress != nil {
				l.Progress(last, w.Latest, true)
			}
			if !l.Follow {
				return nil
			}
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		b, err := l.Source.Fetch(ctx, last+1)
		if err != nil {
			return err
		}
		if b.From != last+1 || b.To < b.From || b.To-b.From >= 200 || len(b.Facts) != int(b.To-b.From+1) {
			return fmt.Errorf("%w: incomplete batch %d..%d", ingest.ErrLedgerGap, b.From, b.To)
		}
		if b.To > b.Latest {
			return fmt.Errorf("sync: batch %d exceeds latest %d", b.To, b.Latest)
		}
		if err := l.commit(ctx, b); err != nil {
			return err
		}
		last = b.To
		if l.Progress != nil {
			l.Progress(last, b.Latest, false)
		}
		// Window calls getLatestLedger again before any caught_up report.
	}
}

func (l *Loop) lastLedger(ctx context.Context) (uint32, error) {
	s, err := l.Store.State(ctx, store.KeyLastLedger)
	if errors.Is(err, store.ErrNotFound) {
		if l.StartLedger == 0 {
			return 0, errors.New("sync: first run requires --seed or --start-ledger")
		}
		return l.StartLedger - 1, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid last_ledger %q: %w", s, err)
	}
	return uint32(n), nil
}

func (l *Loop) commit(ctx context.Context, b ingest.Batch) error {
	tx, err := l.Store.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit
	for i, f := range b.Facts {
		if f.Ledger != b.From+uint32(i) {
			return ingest.ErrLedgerGap
		} // #nosec G115 -- batch has at most 200 ledgers
		if err := l.Apply(ctx, tx, f); err != nil {
			return fmt.Errorf("apply ledger %d: %w", f.Ledger, err)
		}
	}
	if err := tx.SetLastLedger(ctx, b.To); err != nil {
		return err
	}
	return tx.Commit()
}
