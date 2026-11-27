package ingest

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// ErrLedgerGap means the RPC returned ledgers that are not the consecutive
// run that was asked for. The indexer never skips ledgers silently, so this
// stops a sync instead of being worked around.
var ErrLedgerGap = errors.New("ledgers not consecutive")

// Window is the range of ledgers an RPC node can serve.
type Window struct {
	Oldest, Latest uint32
}

// LedgerSource reads consecutive ledgers with getLedgers and extracts their
// contract facts (CLAUDE.md §5.9).
type LedgerSource struct {
	RPC RPC
	// Limit is the most ledgers per getLedgers call: 1..200, default 200.
	Limit uint32
	// MetaLimits bounds each ledger's metadata, the largest XDR the indexer
	// decodes. The sync command sets it from the operator's configured
	// ceiling (XDRBudget.LedgerMeta); zero means DefaultXDRBase64Len.
	MetaLimits XDRLimits
}

func (s *LedgerSource) limit() uint32 {
	if s.Limit == 0 || s.Limit > rpc.MaxLedgersLimit {
		return rpc.MaxLedgersLimit
	}
	return s.Limit
}

func (s *LedgerSource) metaLimits() XDRLimits {
	if s.MetaLimits.MaxBase64Len == 0 {
		return XDRLimits{MaxBase64Len: DefaultXDRBase64Len, MaxDepth: s.MetaLimits.MaxDepth}
	}
	return s.MetaLimits
}

// Window returns the node's retention window. The oldest ledger comes from a
// successful getLedgers call at the latest ledger, never from parsing the
// text of an out-of-range error.
func (s *LedgerSource) Window(ctx context.Context) (Window, error) {
	latest, err := s.RPC.GetLatestLedger(ctx)
	if err != nil {
		return Window{}, fmt.Errorf("latest ledger: %w", err)
	}
	page, err := s.RPC.GetLedgers(ctx, rpc.LedgersRequest{StartLedger: latest.Sequence, Limit: 1})
	if err != nil {
		return Window{}, fmt.Errorf("probe retention window: %w", err)
	}
	w := Window{Oldest: page.OldestLedger, Latest: max(latest.Sequence, page.LatestLedger)}
	if w.Oldest == 0 || w.Oldest > w.Latest {
		return Window{}, fmt.Errorf("RPC reported retention window %d..%d", w.Oldest, w.Latest)
	}
	return w, nil
}

// LedgerHash returns the hash (hex) of one ledger the node still retains,
// for the reset checkpoint in internal/sync. It uses the getLedgers hash
// field and decodes metadata only when a node leaves that field empty.
func (s *LedgerSource) LedgerHash(ctx context.Context, seq uint32) (string, error) {
	page, err := s.RPC.GetLedgers(ctx, rpc.LedgersRequest{StartLedger: seq, Limit: 1})
	if err != nil {
		return "", fmt.Errorf("getLedgers %d: %w", seq, err)
	}
	if len(page.Ledgers) == 0 || page.Ledgers[0].Sequence != seq {
		return "", fmt.Errorf("%w: ledger %d not returned", ErrLedgerGap, seq)
	}
	l := page.Ledgers[0]
	if l.Hash != "" {
		return l.Hash, nil
	}
	var m xdr.LedgerCloseMeta
	if err := UnmarshalBase64(l.MetadataXDR, &m, s.metaLimits()); err != nil {
		if errors.Is(err, ErrTooLarge) {
			return "", fmt.Errorf("ledger %d meta: %w", seq, err)
		}
		return "", unknownXDR(ledgerXDR{seq: seq, header: l.HeaderXDR, meta: l.MetadataXDR}, err)
	}
	h := m.LedgerHash()
	return hex.EncodeToString(h[:]), nil
}

// Batch is the facts of a consecutive run of ledgers.
type Batch struct {
	From, To uint32 // inclusive; To < From when the batch is empty
	Facts    []ContractFacts
	Latest   uint32 // the node's latest ledger when this batch was read
}

// Fetch returns the facts of ledgers from..from+n-1 (n at most the limit),
// stopping early at the node's latest ledger. It returns an empty batch when
// from is beyond the latest ledger. A page that does not start at from, or
// skips a ledger, is ErrLedgerGap.
func (s *LedgerSource) Fetch(ctx context.Context, from uint32) (Batch, error) {
	if from == 0 {
		return Batch{}, errors.New("getLedgers: ledger numbers start at 1")
	}
	page, err := s.RPC.GetLedgers(ctx, rpc.LedgersRequest{StartLedger: from, Limit: s.limit()})
	if err != nil {
		return Batch{}, fmt.Errorf("getLedgers from %d: %w", from, err)
	}
	b := Batch{From: from, To: from - 1, Latest: page.LatestLedger}
	for i, l := range page.Ledgers {
		if want := from + uint32(i); l.Sequence != want { // #nosec G115 -- i < 200
			return Batch{}, fmt.Errorf("%w: wanted ledger %d, got %d", ErrLedgerGap, want, l.Sequence)
		}
		var m xdr.LedgerCloseMeta
		if err := UnmarshalBase64(l.MetadataXDR, &m, s.metaLimits()); err != nil {
			if errors.Is(err, ErrTooLarge) {
				return Batch{}, fmt.Errorf("ledger %d meta: %w", l.Sequence, err)
			}
			return Batch{}, unknownXDR(ledgerXDR{seq: l.Sequence, header: l.HeaderXDR, meta: l.MetadataXDR}, err)
		}
		if seq := m.LedgerSequence(); seq != l.Sequence {
			return Batch{}, fmt.Errorf("ledger %d: metadata is for ledger %d", l.Sequence, seq)
		}
		f, err := ExtractContractFacts(m)
		if err != nil {
			return Batch{}, fmt.Errorf("ledger %d: %w", l.Sequence, err)
		}
		if l.Hash != "" && l.Hash != f.Hash {
			return Batch{}, fmt.Errorf("ledger %d: RPC hash %s differs from metadata hash %s", l.Sequence, l.Hash, f.Hash)
		}
		b.Facts = append(b.Facts, f)
		b.To = l.Sequence
	}
	return b, nil
}
