package ingest

import (
	"context"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// withStellarValueExt re-encodes a recorded ledger's metadata with its
// header's StellarValue extension replaced. Protocol 30 (CAP-0088) closes
// every ledger with a millisecond arm, SIGNED_MS (3) or EMPTY_TX_SET_MS (4);
// go-stellar-sdk v0.7.3 rejected both, which stopped testnet sync at ledger
// 5620968 on 4 November 2026.
func withStellarValueExt(t *testing.T, l rpc.Ledger, ext xdr.StellarValueExt) rpc.Ledger {
	t.Helper()
	var m xdr.LedgerCloseMeta
	if err := UnmarshalBase64(l.MetadataXDR, &m, XDRLimits{MaxBase64Len: 64 << 20}); err != nil {
		t.Fatal(err)
	}
	switch m.V {
	case 0:
		m.V0.LedgerHeader.Header.ScpValue.Ext = ext
	case 1:
		m.V1.LedgerHeader.Header.ScpValue.Ext = ext
	case 2:
		m.V2.LedgerHeader.Header.ScpValue.Ext = ext
	default:
		t.Fatalf("recorded ledger has meta v%d", m.V)
	}
	s, err := xdr.MarshalBase64(m)
	if err != nil {
		t.Fatal(err)
	}
	l.MetadataXDR = s
	return l
}

func TestLedgerSourceDecodesProtocol30Headers(t *testing.T) {
	rec := recordedPage(t)
	sig := xdr.LedgerCloseValueSignature{
		NodeId:    xdr.NodeId{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: &xdr.Uint256{}},
		Signature: xdr.Signature(make([]byte, 64)),
	}
	tests := []struct {
		name string
		ext  xdr.StellarValueExt
	}{
		{"a SIGNED_MS header decodes and yields the same facts", xdr.StellarValueExt{
			V: xdr.StellarValueTypeStellarValueSignedMs,
			SignedMsValue: &xdr.StellarValueSignedMsValue{
				CloseTimeMs:      xdr.TimePointMs(1_762_592_441_000),
				LcValueSignature: sig,
			},
		}},
		{"an EMPTY_TX_SET_MS header decodes and yields the same facts", xdr.StellarValueExt{
			V: xdr.StellarValueTypeStellarValueEmptyTxSetMs,
			ProposedMsValue: &xdr.StellarValueProposedMsValue{
				CloseTimeMs:           xdr.TimePointMs(1_762_592_441_000),
				PreviousLedgerVersion: 29,
				LcValueSignature:      sig,
			},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ledgers := []rpc.Ledger{withStellarValueExt(t, rec[0], tt.ext), withStellarValueExt(t, rec[1], tt.ext)}
			src := &LedgerSource{RPC: &pageRPC{ledgers: ledgers, oldest: 1, latest: 5062912}}
			b, err := src.Fetch(context.Background(), 5062907)
			if err != nil {
				t.Fatalf("protocol 30 header rejected: %v", err)
			}
			// Counts established in TestRecordedTestnetLedgers for the same ledgers.
			if b.To != 5062908 || len(b.Facts) != 2 ||
				len(b.Facts[0].Instances) != 11 || len(b.Facts[1].Instances) != 6 || len(b.Facts[1].CodeCreated) != 1 {
				t.Fatalf("batch to %d, %d facts", b.To, len(b.Facts))
			}
		})
	}
}
