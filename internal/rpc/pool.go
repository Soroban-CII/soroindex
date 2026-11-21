package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// ErrNoProviders is returned when every URL in the pool has been tried and all failed.
var ErrNoProviders = errors.New("rpc pool: all providers failed")

// ErrPassphraseMismatch is returned when a provider's network passphrase does
// not match the configured one. That provider is permanently removed from the
// pool so it can never silently inject data for a different network.
var ErrPassphraseMismatch = errors.New("rpc pool: network passphrase mismatch")

// PoolConfig holds settings for all providers in a Pool.
type PoolConfig struct {
	// URLs is an ordered list of RPC endpoint URLs. The first URL is preferred;
	// later URLs are failover candidates tried in order.
	URLs []string

	// Passphrase is the expected network passphrase (e.g. config.Testnet.Passphrase).
	// A provider whose getNetwork returns a different passphrase is permanently
	// excluded from the pool to prevent cross-network data contamination.
	Passphrase string

	// Per-request settings — zero values fall back to the same defaults as Config.
	Timeout          time.Duration
	Concurrency      int
	MaxAttempts      int
	BaseBackoff      time.Duration
	MaxBackoff       time.Duration
	MaxResponseBytes int64
	HTTPClient       *http.Client
	Logger           *slog.Logger
}

type poolEntry struct {
	client *Client
	mu     sync.RWMutex
	vetted bool // GetNetwork succeeded and passphrase confirmed
	bad    bool // wrong passphrase — skip forever
}

// Pool holds an ordered list of RPC clients and fails over between them on
// any error. Before accepting data from a provider for the first time it
// verifies the provider's network passphrase with getNetwork, so a
// misconfigured or hostile endpoint cannot silently inject data from a
// different network.
//
// A provider is permanently removed when its passphrase does not match.
// A provider that is temporarily unavailable is retried on subsequent calls.
//
// Pool is safe for concurrent use.
type Pool struct {
	entries    []*poolEntry
	passphrase string
	log        *slog.Logger
}

// NewPool returns a Pool. It does no I/O.
func NewPool(cfg PoolConfig) (*Pool, error) {
	if len(cfg.URLs) == 0 {
		return nil, errors.New("rpc pool: at least one URL is required")
	}
	if cfg.Passphrase == "" {
		return nil, errors.New("rpc pool: Passphrase is required")
	}
	p := &Pool{passphrase: cfg.Passphrase, log: cfg.Logger}
	if p.log == nil {
		p.log = slog.Default()
	}
	for _, url := range cfg.URLs {
		c, err := New(Config{
			URL:              url,
			Timeout:          cfg.Timeout,
			Concurrency:      cfg.Concurrency,
			MaxAttempts:      cfg.MaxAttempts,
			BaseBackoff:      cfg.BaseBackoff,
			MaxBackoff:       cfg.MaxBackoff,
			MaxResponseBytes: cfg.MaxResponseBytes,
			HTTPClient:       cfg.HTTPClient,
			Logger:           cfg.Logger,
		})
		if err != nil {
			return nil, fmt.Errorf("rpc pool: provider %q: %w", url, err)
		}
		p.entries = append(p.entries, &poolEntry{client: c})
	}
	return p, nil
}

// ensureVetted checks that e's network passphrase has been verified. On first
// use it calls getNetwork; on a match it marks e vetted, on a mismatch it marks
// e permanently bad. If the provider is temporarily unavailable the error is
// returned and e is not marked bad, so the next call retries.
func (p *Pool) ensureVetted(ctx context.Context, e *poolEntry) error {
	e.mu.RLock()
	vetted, bad := e.vetted, e.bad
	e.mu.RUnlock()
	if vetted {
		return nil
	}
	if bad {
		return ErrPassphraseMismatch
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.vetted {
		return nil // another goroutine verified it while we waited
	}
	if e.bad {
		return ErrPassphraseMismatch
	}
	n, err := e.client.GetNetwork(ctx)
	if err != nil {
		return err // temporarily unavailable; not permanently bad
	}
	if n.Passphrase != p.passphrase {
		e.bad = true
		return fmt.Errorf("%w: got %q, want %q", ErrPassphraseMismatch, n.Passphrase, p.passphrase)
	}
	e.vetted = true
	return nil
}

// do calls fn on the first provider that is available and passes passphrase
// verification. On any error it moves to the next provider. It returns
// ErrNoProviders if every provider was tried and all failed.
func (p *Pool) do(ctx context.Context, fn func(*Client) error) error {
	var lastErr error
	for _, e := range p.entries {
		e.mu.RLock()
		bad := e.bad
		e.mu.RUnlock()
		if bad {
			continue
		}
		if err := p.ensureVetted(ctx, e); err != nil {
			e.mu.RLock()
			isPermanent := e.bad
			e.mu.RUnlock()
			if isPermanent {
				p.log.LogAttrs(ctx, slog.LevelWarn, "rpc pool: provider excluded (passphrase mismatch)", slog.String("err", err.Error()))
			} else {
				p.log.LogAttrs(ctx, slog.LevelWarn, "rpc pool: provider unavailable for vetting", slog.String("err", err.Error()))
			}
			lastErr = err
			continue
		}
		if err := fn(e.client); err != nil {
			p.log.LogAttrs(ctx, slog.LevelWarn, "rpc pool: provider failed, trying next", slog.String("err", err.Error()))
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr != nil {
		return fmt.Errorf("%w: %w", ErrNoProviders, lastErr)
	}
	return ErrNoProviders
}

// GetNetwork returns the network from the first working, correctly-configured provider.
func (p *Pool) GetNetwork(ctx context.Context) (Network, error) {
	var out Network
	err := p.do(ctx, func(c *Client) error {
		var err error
		out, err = c.GetNetwork(ctx)
		return err
	})
	return out, err
}

// GetLatestLedger returns the latest ledger from the first working provider.
func (p *Pool) GetLatestLedger(ctx context.Context) (LatestLedger, error) {
	var out LatestLedger
	err := p.do(ctx, func(c *Client) error {
		var err error
		out, err = c.GetLatestLedger(ctx)
		return err
	})
	return out, err
}

// GetLedgers fetches one page of ledgers from the first working provider.
func (p *Pool) GetLedgers(ctx context.Context, r LedgersRequest) (LedgersPage, error) {
	var out LedgersPage
	err := p.do(ctx, func(c *Client) error {
		var err error
		out, err = c.GetLedgers(ctx, r)
		return err
	})
	return out, err
}

// GetLedgerEntries fetches entries from the first working provider.
func (p *Pool) GetLedgerEntries(ctx context.Context, keys []string) (LedgerEntries, error) {
	var out LedgerEntries
	err := p.do(ctx, func(c *Client) error {
		var err error
		out, err = c.GetLedgerEntries(ctx, keys)
		return err
	})
	return out, err
}
