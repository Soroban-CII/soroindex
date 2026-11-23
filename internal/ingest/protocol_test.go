package ingest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// withFutureStellarValueType re-encodes a recorded ledger with its header's
// StellarValueExt discriminant set to v, a value the pinned SDK does not
// know. The SDK refuses to encode an unknown arm, so the ledger is encoded
// with the BASIC arm (a bare 4-byte discriminant at the end of the
// StellarValue) and that discriminant is patched in place, the same way a
// future protocol's bytes would arrive.
func withFutureStellarValueType(t *testing.T, l rpc.Ledger, v uint32) rpc.Ledger {
	t.Helper()
	l = withStellarValueExt(t, l, xdr.StellarValueExt{V: xdr.StellarValueTypeStellarValueBasic})
	var m xdr.LedgerCloseMeta
	if err := UnmarshalBase64(l.MetadataXDR, &m, XDRLimits{MaxBase64Len: 64 << 20}); err != nil {
		t.Fatal(err)
	}
	sv, err := m.LedgerHeaderHistoryEntry().Header.ScpValue.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(l.MetadataXDR)
	if err != nil {
		t.Fatal(err)
	}
	at := bytes.Index(raw, sv)
	if at < 0 || bytes.Contains(raw[at+1:], sv) {
		t.Fatal("StellarValue not found exactly once in the metadata")
	}
	binary.BigEndian.PutUint32(raw[at+len(sv)-4:], v)
	l.MetadataXDR = base64.StdEncoding.EncodeToString(raw)
	l.HeaderXDR = "" // force the protocol to be read from the metadata itself
	return l
}

func TestUnknownXDRNamesLedgerProtocolAndSDK(t *testing.T) {
	rec := recordedPage(t)
	var m xdr.LedgerCloseMeta
	if err := UnmarshalBase64(rec[0].MetadataXDR, &m, XDRLimits{MaxBase64Len: 64 << 20}); err != nil {
		t.Fatal(err)
	}
	proto := m.ProtocolVersion()
	bad := withFutureStellarValueType(t, rec[0], 7)
	src := &LedgerSource{RPC: &pageRPC{ledgers: []rpc.Ledger{bad, rec[1]}, oldest: 1, latest: 5062912}}
	_, err := src.Fetch(context.Background(), 5062907)
	if !errors.Is(err, ErrUnknownXDR) {
		t.Fatalf("future StellarValueType: err = %v, want ErrUnknownXDR", err)
	}
	for _, want := range []string{"ledger 5062907", "protocol " + strconv.FormatUint(uint64(proto), 10), "pinned " + PinnedSDKVersion, "github.com/stellar/go-stellar-sdk"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not contain %q", err, want)
		}
	}
}

func TestOversizedMetaIsNotUnknownXDR(t *testing.T) {
	rec := recordedPage(t)
	src := &LedgerSource{RPC: &pageRPC{ledgers: rec, oldest: 1, latest: 5062912}, MetaLimits: XDRLimits{MaxBase64Len: 16}}
	_, err := src.Fetch(context.Background(), 5062907)
	if !errors.Is(err, ErrTooLarge) || errors.Is(err, ErrUnknownXDR) {
		t.Fatalf("err = %v, want ErrTooLarge only", err)
	}
}

func TestPeekProtocolMatchesDecoder(t *testing.T) {
	for _, l := range recordedPage(t) {
		var m xdr.LedgerCloseMeta
		if err := UnmarshalBase64(l.MetadataXDR, &m, XDRLimits{MaxBase64Len: 64 << 20}); err != nil {
			t.Fatal(err)
		}
		want := m.ProtocolVersion()
		if p, ok := peekProtocol(l.HeaderXDR, ""); l.HeaderXDR != "" && (!ok || p != want) {
			t.Errorf("ledger %d from header: %d %v, want %d", l.Sequence, p, ok, want)
		}
		if p, ok := peekProtocol("", l.MetadataXDR); !ok || p != want {
			t.Errorf("ledger %d from metadata: %d %v, want %d", l.Sequence, p, ok, want)
		}
	}
	for _, junk := range []string{"", "AAAA", "AAAAAQ==", "!!"} {
		if _, ok := peekProtocol("", junk); ok {
			t.Errorf("peekProtocol(%q) claimed a protocol", junk)
		}
	}
}

func TestFetchRecordsProtocolAndHash(t *testing.T) {
	rec := recordedPage(t)
	src := &LedgerSource{RPC: &pageRPC{ledgers: rec, oldest: 1, latest: 5062912}}
	b, err := src.Fetch(context.Background(), 5062907)
	if err != nil {
		t.Fatal(err)
	}
	for i, f := range b.Facts {
		if f.Protocol == 0 || f.Hash != rec[i].Hash {
			t.Errorf("ledger %d: protocol %d hash %s, want RPC hash %s", f.Ledger, f.Protocol, f.Hash, rec[i].Hash)
		}
	}
	h, err := src.LedgerHash(context.Background(), 5062908)
	if err != nil || h != rec[1].Hash {
		t.Fatalf("LedgerHash = %s, %v", h, err)
	}
}

func TestPinnedSDKVersionMatchesGoMod(t *testing.T) {
	b, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^\s*github\.com/stellar/go-stellar-sdk (v\S+)`).FindSubmatch(b)
	if m == nil || string(m[1]) != PinnedSDKVersion {
		t.Fatalf("go.mod pins %q, PinnedSDKVersion is %q", m, PinnedSDKVersion)
	}
}
