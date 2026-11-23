// Package sync advances an index through consecutive, final Stellar ledgers.
package sync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/Soroban-CII/soroindex/internal/ingest"
	"github.com/Soroban-CII/soroindex/internal/store"
)

// ErrRetentionGap means the resume point is older than the RPC's retained history.
var ErrRetentionGap = errors.New("resume point is outside RPC retention; seed or backfill the index")

// ErrNetworkReset means the RPC serves a different ledger history than the
// one this index was built from, under the same network passphrase: testnet
// (or futurenet) was reset. The index describes contracts that no longer
// exist, and continuing would mix two histories in one database, so sync
// refuses before writing anything. Start a new index in a new --db file.
var ErrNetworkReset = errors.New("network was reset: the RPC's ledger history no longer matches this index; start a new index with a new --db file")

// DefaultResetThreshold is how far, in ledgers, the stored last_ledger may
// be ahead of the RPC's latest ledger before sync calls it a reset. A
// lagging or failed-over node is a few ledgers behind; 17280 ledgers is
// about a day at 5 seconds per ledger, which no usable node is behind.
const DefaultResetThreshold = 17280

// LedgerHasher is implemented by sources that can return the hash of one
// retained ledger. The loop uses it to compare the stored checkpoint with
// the node's history before writing; ingest.LedgerSource implements it.
type LedgerHasher interface {
	LedgerHash(ctx context.Context, seq uint32) (string, error)
}

// Source supplies consecutive ledger batches and fresh retention/tip observations.
type Source interface {
	Window(context.Context) (ingest.Window, error)
	Fetch(context.Context, uint32) (ingest.Batch, error)
}

// Loop commits facts and the resume point atomically, then follows the tip if requested.
type Loop struct {
	Store            *store.Store
	Source           Source
	Apply            func(context.Context, *store.Tx, ingest.ContractFacts) error
	RetryWasmFetches func(context.Context, *store.Tx) (int, int, error)
	StartLedger      uint32
	Follow           bool
	Interval         time.Duration
	// Progress runs after a batch commits or a fresh tip confirms caught_up.
	Progress func(last, latest uint32, caughtUp bool)
	// Prefetch, when set, runs after a batch is fetched and before its
	// transaction opens (Wasm fetch and analysis on a bounded pool). It
	// writes nothing; a cancelled prefetch leaves last_ledger unchanged.
	Prefetch func(context.Context, ingest.Batch) error
	// ResetThreshold overrides DefaultResetThreshold (ledgers).
	ResetThreshold uint32
	// Log receives protocol-change records; nil discards them.
	Log *slog.Logger
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
	checked := false // the hash checkpoint is compared once, before the first write
	protocol, haveProtocol, err := l.Store.LastProtocol(ctx)
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
			if last-w.Latest > l.resetThreshold() {
				return fmt.Errorf("%w (stored last_ledger %d is %d ledgers ahead of the RPC's latest %d)", ErrNetworkReset, last, last-w.Latest, w.Latest)
			}
			return fmt.Errorf("sync: stored ledger %d exceeds RPC latest %d", last, w.Latest)
		}
		if !checked {
			if err := l.checkCheckpoint(ctx, w); err != nil {
				return err
			}
			checked = true
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
		if l.Prefetch != nil {
			if err := l.Prefetch(ctx, b); err != nil {
				return fmt.Errorf("prefetch ledgers %d..%d: %w", b.From, b.To, err)
			}
		}
		if err := l.commit(ctx, b); err != nil {
			return err
		}
		for _, f := range b.Facts {
			if f.Protocol == 0 {
				continue
			}
			if haveProtocol && f.Protocol != protocol && l.Log != nil {
				l.Log.InfoContext(ctx, "protocol change", "ledger", f.Ledger, "from_protocol", protocol, "to_protocol", f.Protocol)
			}
			protocol, haveProtocol = f.Protocol, true
		}
		last = b.To
		if l.Progress != nil {
			l.Progress(last, b.Latest, false)
		}
		// Retry queued Wasm fetches after each batch in a separate transaction.
		if l.RetryWasmFetches != nil {
			tx, err := l.Store.Begin(ctx)
			if err != nil {
				return fmt.Errorf("begin retry transaction: %w", err)
			}
			if _, _, err := l.RetryWasmFetches(ctx, tx); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("retry wasm fetches: %w", err)
			}
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("commit retry transaction: %w", err)
			}
		}
		// Window calls getLatestLedger again before any caught_up report.
	}
}

func (l *Loop) resetThreshold() uint32 {
	if l.ResetThreshold > 0 {
		return l.ResetThreshold
	}
	return DefaultResetThreshold
}

// checkCheckpoint compares the stored ledger hash with the node's hash for
// the same ledger. A mismatch is ErrNetworkReset. It is skipped when no
// checkpoint exists yet (fresh or seed-only index), when the source cannot
// return hashes, or when the checkpoint ledger is outside the node's
// retention (the retention-gap check already guards that case).
func (l *Loop) checkCheckpoint(ctx context.Context, w ingest.Window) error {
	h, ok := l.Source.(LedgerHasher)
	if !ok {
		return nil
	}
	cp, err := l.Store.Checkpoint(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if cp.Ledger < w.Oldest || cp.Ledger > w.Latest {
		return nil
	}
	got, err := h.LedgerHash(ctx, cp.Ledger)
	if err != nil {
		return fmt.Errorf("checkpoint ledger %d: %w", cp.Ledger, err)
	}
	if got != cp.Hash {
		return fmt.Errorf("%w (ledger %d: index has hash %s, RPC has %s)", ErrNetworkReset, cp.Ledger, cp.Hash, got)
	}
	return nil
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
	if len(b.Facts) > 0 {
		lastFact := b.Facts[len(b.Facts)-1]
		if lastFact.Protocol != 0 {
			if err := tx.SetLastProtocol(ctx, lastFact.Protocol); err != nil {
				return err
			}
		}
		if lastFact.Hash != "" {
			if err := tx.SetCheckpoint(ctx, store.Checkpoint{Ledger: lastFact.Ledger, Hash: lastFact.Hash}); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
