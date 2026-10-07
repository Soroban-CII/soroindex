package rpc

import (
	"context"
	"errors"
	"fmt"
)

// Network is the getNetwork result.
type Network struct {
	Passphrase      string `json:"passphrase"`
	ProtocolVersion uint32 `json:"protocolVersion"`
	FriendbotURL    string `json:"friendbotUrl,omitempty"`
}

// GetNetwork returns the network the RPC node serves. The indexer compares
// Passphrase with its configured network before writing anything.
func (c *Client) GetNetwork(ctx context.Context) (Network, error) {
	var n Network
	err := c.call(ctx, "getNetwork", nil, &n)
	return n, err
}

// LatestLedger is the getLatestLedger result. headerXdr and metadataXdr are
// not decoded: the indexer only needs the sequence.
type LatestLedger struct {
	ID              string `json:"id"`
	ProtocolVersion uint32 `json:"protocolVersion"`
	Sequence        uint32 `json:"sequence"`
	CloseTime       string `json:"closeTime"`
}

// GetLatestLedger returns the newest ledger the node knows.
func (c *Client) GetLatestLedger(ctx context.Context) (LatestLedger, error) {
	var l LatestLedger
	err := c.call(ctx, "getLatestLedger", nil, &l)
	return l, err
}

// MaxLedgersLimit is the largest page getLedgers accepts.
const MaxLedgersLimit = 200

// LedgersRequest selects a page of ledgers. Exactly one of StartLedger and
// Cursor must be set.
type LedgersRequest struct {
	StartLedger uint32
	Cursor      string
	Limit       uint32 // 1..200
}

// Ledger is one ledger from getLedgers. MetadataXDR is a base64
// LedgerCloseMeta; it is untrusted and must be decoded with limits.
type Ledger struct {
	Hash            string `json:"hash"`
	Sequence        uint32 `json:"sequence"`
	LedgerCloseTime string `json:"ledgerCloseTime"`
	HeaderXDR       string `json:"headerXdr"`
	MetadataXDR     string `json:"metadataXdr"`
}

// LedgersPage is the getLedgers result. OldestLedger and LatestLedger bound
// the node's retention window at the time of the call.
type LedgersPage struct {
	Ledgers               []Ledger `json:"ledgers"`
	LatestLedger          uint32   `json:"latestLedger"`
	LatestLedgerCloseTime int64    `json:"latestLedgerCloseTime"`
	OldestLedger          uint32   `json:"oldestLedger"`
	OldestLedgerCloseTime int64    `json:"oldestLedgerCloseTime"`
	Cursor                string   `json:"cursor"`
}

type ledgersParams struct {
	StartLedger uint32            `json:"startLedger,omitempty"`
	Pagination  *paginationParams `json:"pagination,omitempty"`
}

type paginationParams struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  uint32 `json:"limit,omitempty"`
}

// GetLedgers fetches one page of ledgers. The limit is always sent, because
// the server's own default (50) differs from the indexer's (200).
func (c *Client) GetLedgers(ctx context.Context, r LedgersRequest) (LedgersPage, error) {
	if (r.StartLedger == 0) == (r.Cursor == "") {
		return LedgersPage{}, errors.New("getLedgers: set exactly one of StartLedger and Cursor")
	}
	if r.Limit < 1 || r.Limit > MaxLedgersLimit {
		return LedgersPage{}, fmt.Errorf("getLedgers: limit %d outside 1..%d", r.Limit, MaxLedgersLimit)
	}
	p := ledgersParams{StartLedger: r.StartLedger, Pagination: &paginationParams{Cursor: r.Cursor, Limit: r.Limit}}
	var page LedgersPage
	err := c.call(ctx, "getLedgers", p, &page)
	return page, err
}

// MaxLedgerEntryKeys is the most keys one getLedgerEntries call accepts.
const MaxLedgerEntryKeys = 200

// LedgerEntry is one entry from getLedgerEntries. XDR is a base64
// LedgerEntryData. LiveUntilLedgerSeq is nil for entries without a TTL.
type LedgerEntry struct {
	Key                   string  `json:"key"`
	XDR                   string  `json:"xdr"`
	LastModifiedLedgerSeq uint32  `json:"lastModifiedLedgerSeq"`
	LiveUntilLedgerSeq    *uint32 `json:"liveUntilLedgerSeq,omitempty"`
}

// LedgerEntries is the getLedgerEntries result. A requested key that does
// not exist is simply absent from Entries.
type LedgerEntries struct {
	Entries      []LedgerEntry `json:"entries"`
	LatestLedger uint32        `json:"latestLedger"`
}

type ledgerEntriesParams struct {
	Keys []string `json:"keys"`
}

// GetLedgerEntries fetches up to 200 entries by base64 LedgerKey.
func (c *Client) GetLedgerEntries(ctx context.Context, keys []string) (LedgerEntries, error) {
	if len(keys) == 0 || len(keys) > MaxLedgerEntryKeys {
		return LedgerEntries{}, fmt.Errorf("getLedgerEntries: %d keys, want 1..%d", len(keys), MaxLedgerEntryKeys)
	}
	var out LedgerEntries
	err := c.call(ctx, "getLedgerEntries", ledgerEntriesParams{Keys: keys}, &out)
	return out, err
}
