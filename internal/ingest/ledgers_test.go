package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/rpc"
)

// pageRPC serves getLedgers from a fixed list of ledgers.
type pageRPC struct {
	entryRPC
	ledgers        []rpc.Ledger
	oldest, latest uint32
	reqs           []rpc.LedgersRequest
}

func (p *pageRPC) GetLatestLedger(context.Context) (rpc.LatestLedger, error) {
	return rpc.LatestLedger{Sequence: p.latest}, nil
}

func (p *pageRPC) GetLedgers(_ context.Context, r rpc.LedgersRequest) (rpc.LedgersPage, error) {
	p.reqs = append(p.reqs, r)
	page := rpc.LedgersPage{OldestLedger: p.oldest, LatestLedger: p.latest}
	for _, l := range p.ledgers {
		if l.Sequence >= r.StartLedger && len(page.Ledgers) < int(r.Limit) {
			page.Ledgers = append(page.Ledgers, l)
		}
	}
	return page, nil
}

// recordedPage loads the two real testnet ledgers (5062907, 5062908).
func recordedPage(t *testing.T) []rpc.Ledger {
	t.Helper()
	b, err := os.ReadFile("../rpc/testdata/getLedgers.json")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Result rpc.LedgersPage `json:"result"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Result.Ledgers
}

func TestLedgerSourceRecorded(t *testing.T) {
	p := &pageRPC{ledgers: recordedPage(t), oldest: 4941953, latest: 5062912}
	src := &LedgerSource{RPC: p}

	w, err := src.Window(context.Background())
	if err != nil || w != (Window{Oldest: 4941953, Latest: 5062912}) {
		t.Fatalf("window = %+v, %v", w, err)
	}
	b, err := src.Fetch(context.Background(), 5062907)
	if err != nil {
		t.Fatal(err)
	}
	if b.From != 5062907 || b.To != 5062908 || len(b.Facts) != 2 || b.Latest != 5062912 {
		t.Fatalf("batch %d..%d, %d facts, latest %d", b.From, b.To, len(b.Facts), b.Latest)
	}
	// Counts established in TestRecordedTestnetLedgers.
	if len(b.Facts[0].Instances) != 11 || len(b.Facts[1].Instances) != 6 || len(b.Facts[1].CodeCreated) != 1 {
		t.Fatalf("instances %d/%d, code created %d", len(b.Facts[0].Instances), len(b.Facts[1].Instances), len(b.Facts[1].CodeCreated))
	}
	if got := p.reqs[len(p.reqs)-1]; got.StartLedger != 5062907 || got.Limit != 200 {
		t.Fatalf("request = %+v, want start 5062907 limit 200", got)
	}
}

func TestLedgerSourceNeverSkips(t *testing.T) {
	rec := recordedPage(t)
	tests := []struct {
		name    string
		ledgers []rpc.Ledger
		from    uint32
		wantErr error
	}{
		{"a page starting after the requested ledger is a gap", rec[1:], 5062907, ErrLedgerGap},
		{"a page with a hole is a gap", []rpc.Ledger{rec[0], {Sequence: 5062909, MetadataXDR: rec[1].MetadataXDR}}, 5062907, ErrLedgerGap},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &LedgerSource{RPC: &pageRPC{ledgers: tt.ledgers, oldest: 1, latest: 5062912}}
			if _, err := src.Fetch(context.Background(), tt.from); !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
	t.Run("metadata for a different ledger than labelled is rejected", func(t *testing.T) {
		bad := []rpc.Ledger{{Sequence: 5062907, MetadataXDR: rec[1].MetadataXDR}}
		src := &LedgerSource{RPC: &pageRPC{ledgers: bad, oldest: 1, latest: 5062912}}
		if _, err := src.Fetch(context.Background(), 5062907); err == nil {
			t.Fatal("mislabelled metadata accepted")
		}
	})
	t.Run("beyond the latest ledger is an empty batch, not an error", func(t *testing.T) {
		src := &LedgerSource{RPC: &pageRPC{ledgers: rec, oldest: 1, latest: 5062908}}
		b, err := src.Fetch(context.Background(), 5062909)
		if err != nil || len(b.Facts) != 0 || b.To != 5062908 {
			t.Fatalf("batch %+v, err %v", b, err)
		}
	})
	t.Run("ledger 0 is rejected", func(t *testing.T) {
		if _, err := (&LedgerSource{RPC: &pageRPC{}}).Fetch(context.Background(), 0); err == nil {
			t.Fatal("ledger 0 accepted")
		}
	})
	t.Run("an RPC reporting an impossible window is an error", func(t *testing.T) {
		if _, err := (&LedgerSource{RPC: &pageRPC{oldest: 0, latest: 10}}).Window(context.Background()); err == nil {
			t.Fatal("window with oldest 0 accepted")
		}
	})
}
