package ingest

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
)

// PinnedSDKVersion is the github.com/stellar/go-stellar-sdk version in
// go.mod. It is named in ErrUnknownXDR messages so an operator knows which
// version could not decode a new protocol. TestPinnedSDKVersionMatchesGoMod
// keeps it in step with go.mod.
const PinnedSDKVersion = "v0.8.0"

// ErrUnknownXDR means a ledger's metadata is valid base64 within limits but
// the pinned XDR decoder rejects it. In practice that is a protocol upgrade
// adding a union arm or enum value the SDK does not know yet (testnet's
// Protocol 30 stopped sync with "'3' is not a valid StellarValueType").
// The fix is an SDK upgrade, never skipping the ledger, so sync stops.
var ErrUnknownXDR = errors.New("ledger metadata uses XDR this build cannot decode")

// unknownXDR wraps a metadata decode failure with what an operator needs:
// the ledger, the protocol it was closed under (read from the raw header
// when the full decode fails), and the SDK version to upgrade.
func unknownXDR(l ledgerXDR, decodeErr error) error {
	proto := "unknown"
	if p, ok := peekProtocol(l.header, l.meta); ok {
		proto = strconv.FormatUint(uint64(p), 10)
	}
	return fmt.Errorf("%w: ledger %d, protocol %s: upgrade github.com/stellar/go-stellar-sdk (pinned %s) to a version that supports protocol %s: %w",
		ErrUnknownXDR, l.seq, proto, PinnedSDKVersion, proto, decodeErr)
}

// ledgerXDR is the raw base64 fields of one getLedgers entry.
type ledgerXDR struct {
	seq          uint32
	header, meta string
}

// peekProtocol reads ledgerVersion without decoding the whole structure,
// so a header the SDK rejects can still say which protocol it came from.
// It tries headerXdr (LedgerHeaderHistoryEntry: hash[32], then
// ledgerVersion) and then metadataXdr (LedgerCloseMeta: a union
// discriminant; for v1 and v2 a LedgerCloseMetaExt; then the same
// LedgerHeaderHistoryEntry). Every offset is bounds-checked; the base64
// was already size-limited by the caller's decode attempt.
func peekProtocol(header, meta string) (uint32, bool) {
	if b, err := base64.StdEncoding.DecodeString(header); err == nil && len(b) >= 36 {
		return binary.BigEndian.Uint32(b[32:36]), true
	}
	b, err := base64.StdEncoding.DecodeString(meta)
	if err != nil || len(b) < 4 {
		return 0, false
	}
	off := 4
	switch binary.BigEndian.Uint32(b[0:4]) {
	case 0:
	case 1, 2:
		if len(b) < off+4 {
			return 0, false
		}
		switch binary.BigEndian.Uint32(b[off : off+4]) {
		case 0: // void
			off += 4
		case 1: // LedgerCloseMetaExtV1 { ExtensionPoint ext; int64 sorobanFeeWriteBytes }
			off += 4 + 4 + 8
		default:
			return 0, false
		}
	default:
		return 0, false
	}
	off += 32 // LedgerHeaderHistoryEntry.hash
	if len(b) < off+4 {
		return 0, false
	}
	return binary.BigEndian.Uint32(b[off : off+4]), true
}
