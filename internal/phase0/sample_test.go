package phase0

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/ingest"
	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"
)

type movingRetentionRPC struct {
	*fakeRPC
	beforePage    func(context.Context, rpc.LedgersRequest) error
	latestError   error
	latestCalls   int
	pageCalls     int
	dataPageCalls int
}

func (f *movingRetentionRPC) GetLatestLedger(ctx context.Context) (rpc.LatestLedger, error) {
	f.latestCalls++
	if f.latestCalls > 1 && f.latestError != nil {
		return rpc.LatestLedger{}, f.latestError
	}
	return f.fakeRPC.GetLatestLedger(ctx)
}

func (f *movingRetentionRPC) GetLedgers(ctx context.Context, r rpc.LedgersRequest) (rpc.LedgersPage, error) {
	f.pageCalls++
	if r.StartLedger != f.latest {
		f.dataPageCalls++
		if f.beforePage != nil {
			if err := f.beforePage(ctx, r); err != nil {
				return rpc.LedgersPage{}, err
			}
		}
	}
	if r.StartLedger < f.oldest {
		return rpc.LedgersPage{}, &rpc.Error{Code: -32600, Message: "free text, not bounds"}
	}
	return f.fakeRPC.GetLedgers(ctx, r)
}

func retentionFixture(t *testing.T) *movingRetentionRPC {
	t.Helper()
	code := fixtureCode(t, "token_full_legacy.wasm")
	hash := hashOf(code["token_full_legacy.wasm"])
	f := &fakeRPC{t: t, code: code, oldest: 101, latest: 112, data: map[string]xdr.ContractDataEntry{}}
	template := realLedger(t)
	for seq := uint32(101); seq <= 112; seq++ {
		id := cid(byte(seq - 100))
		cd := instanceCD(t, id, wasmExec(hash))
		key, err := ingest.InstanceKey(id)
		if err != nil {
			t.Fatal(err)
		}
		f.data[key] = cd
		f.ledgers = append(f.ledgers, ledgerWithInstances(t, template, seq, cd))
	}
	return &movingRetentionRPC{fakeRPC: f}
}

func TestSampleRecoversMovingRetentionWithoutChangingStrata(t *testing.T) {
	for _, tt := range []struct {
		name       string
		expiresAt  uint32
		newOldest  uint32
		target     int
		wantIDs    []string
		wantGaps   []LedgerRange
		perStratum []int
	}{
		{"before first page", 101, 102, 3, []string{cid(2), cid(5), cid(9)}, []LedgerRange{{101, 101}}, []int{1, 1, 1}},
		{"between pages", 102, 104, 6, []string{cid(1), cid(4), cid(5), cid(6), cid(9), cid(10)}, []LedgerRange{{102, 103}}, []int{2, 2, 2}},
		{"entire stratum expires", 101, 106, 3, []string{cid(6), cid(9)}, []LedgerRange{{101, 104}, {105, 105}}, []int{0, 1, 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := retentionFixture(t)
			f.beforePage = func(_ context.Context, r rpc.LedgersRequest) error {
				if r.StartLedger == tt.expiresAt {
					f.oldest = tt.newOldest
				}
				return nil
			}
			ids, st, err := SampleTestnet(context.Background(), f, SampleConfig{Target: tt.target, Strata: 3, PageSize: 1}, testOptions(t).Log)
			if err != nil || !reflect.DeepEqual(ids, tt.wantIDs) || !reflect.DeepEqual(st.RetentionGaps, tt.wantGaps) {
				t.Fatal(ids, st, err)
			}
			if st.OldestLedger != 101 || st.LatestLedger != 112 || st.Strata != 3 || st.LedgersScanned != len(tt.wantIDs) || !reflect.DeepEqual(st.PerStratum, tt.perStratum) {
				t.Fatal("original sampling plan or measured coverage changed", st)
			}
			if f.latestCalls != 2 {
				t.Fatal("expected exactly one retention refresh", f.latestCalls)
			}
		})
	}
}

func TestSampleRetentionRecoveryIsBounded(t *testing.T) {
	f := retentionFixture(t)
	f.beforePage = func(_ context.Context, r rpc.LedgersRequest) error {
		f.oldest = r.StartLedger + 1
		return nil
	}
	_, st, err := SampleTestnet(context.Background(), f, SampleConfig{Target: 1, Strata: 1, PageSize: 1}, testOptions(t).Log)
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32600 || f.latestCalls != 4 || f.dataPageCalls != 4 || len(st.RetentionGaps) != 3 {
		t.Fatal(st, err, f.latestCalls, f.dataPageCalls)
	}
}

func TestSampleResetsRetentionRetriesAfterSuccessfulPage(t *testing.T) {
	f := retentionFixture(t)
	f.beforePage = func(_ context.Context, r rpc.LedgersRequest) error {
		if r.StartLedger%2 == 1 {
			f.oldest = r.StartLedger + 1
		}
		return nil
	}
	ids, st, err := SampleTestnet(context.Background(), f, SampleConfig{Target: 4, Strata: 1, PageSize: 1}, testOptions(t).Log)
	if err != nil || !reflect.DeepEqual(ids, []string{cid(2), cid(4), cid(6), cid(8)}) || len(st.RetentionGaps) != 4 || f.latestCalls != 5 {
		t.Fatal(ids, st, err, f.latestCalls)
	}
}

func TestSampleRecoveryPreservesStratumScanLimit(t *testing.T) {
	f := retentionFixture(t)
	f.beforePage = func(_ context.Context, r rpc.LedgersRequest) error {
		if r.StartLedger == 101 {
			f.oldest = 104
		}
		return nil
	}
	ids, st, err := SampleTestnet(context.Background(), f, SampleConfig{Target: 3, Strata: 3, PageSize: 1, MaxLedgersPerStratum: 2}, testOptions(t).Log)
	if err != nil || !reflect.DeepEqual(ids, []string{cid(5), cid(9)}) || !reflect.DeepEqual(st.RetentionGaps, []LedgerRange{{101, 102}}) {
		t.Fatal(ids, st, err)
	}
}

func TestSampleDoesNotHideUnrelatedErrors(t *testing.T) {
	for _, tt := range []struct {
		name          string
		pageError     error
		probeError    error
		wantRefreshes int
	}{
		{"range code without expired start", &rpc.Error{Code: -32600, Message: "invalid request"}, nil, 1},
		{"different RPC error", &rpc.Error{Code: -32603, Message: "internal error"}, nil, 0},
		{"authorization error", &rpc.HTTPError{Status: 403}, nil, 0},
		{"untyped lookalike message", errors.New("start ledger must be between oldest and latest"), nil, 0},
		{"failed refresh", &rpc.Error{Code: -32600}, errors.New("probe unavailable"), 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := retentionFixture(t)
			f.latestError = tt.probeError
			f.beforePage = func(context.Context, rpc.LedgersRequest) error { return fmt.Errorf("wrapped: %w", tt.pageError) }
			_, st, err := SampleTestnet(context.Background(), f, SampleConfig{Target: 1, Strata: 1, PageSize: 1}, testOptions(t).Log)
			if !errors.Is(err, tt.pageError) || len(st.RetentionGaps) != 0 || f.latestCalls != 1+tt.wantRefreshes || f.dataPageCalls != 1 {
				t.Fatal(st, err, f.latestCalls, f.dataPageCalls)
			}
			if tt.probeError != nil && !errors.Is(err, tt.probeError) {
				t.Fatal("refresh failure hidden", err)
			}
		})
	}
}

func TestSampleStopsRecoveryOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := retentionFixture(t)
	f.beforePage = func(context.Context, rpc.LedgersRequest) error {
		cancel()
		return &rpc.Error{Code: -32600}
	}
	_, st, err := SampleTestnet(ctx, f, SampleConfig{Target: 1, Strata: 1, PageSize: 1}, testOptions(t).Log)
	if !errors.Is(err, context.Canceled) || f.latestCalls != 1 || f.dataPageCalls != 1 || len(st.RetentionGaps) != 0 {
		t.Fatal(st, err, f.latestCalls, f.dataPageCalls)
	}
}

func TestSamplingReportPersistsRetentionLoss(t *testing.T) {
	f := retentionFixture(t)
	f.beforePage = func(_ context.Context, r rpc.LedgersRequest) error {
		if r.StartLedger == 101 {
			f.oldest = 102
		}
		return nil
	}
	res, err := RunTestnet(context.Background(), f, SampleConfig{Target: 3, Strata: 3, PageSize: 1}, testOptions(t))
	if err != nil || !strings.Contains(res.Summary.Method, "coverage is incomplete") {
		t.Fatal(res.Summary.Method, err)
	}
	dir := t.TempDir()
	if err := Write(dir, res); err != nil {
		t.Fatal(err)
	}
	saved, err := ReadSummary(dir, "testnet")
	if err != nil || saved.Sample == nil || !reflect.DeepEqual(saved.Sample.RetentionGaps, []LedgerRange{{101, 101}}) {
		t.Fatal(saved.Sample, err)
	}
	md, err := fs.ReadFile(os.DirFS(dir), "ADOPTION.md")
	if err != nil || !strings.Contains(string(md), "**Incomplete sampling coverage:**") || !strings.Contains(string(md), "101-101") {
		t.Fatal(string(md), err)
	}
	stable := retentionFixture(t)
	_, stats, err := SampleTestnet(context.Background(), stable, SampleConfig{Target: 1, Strata: 1, PageSize: 1}, testOptions(t).Log)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(stats)
	if err != nil || strings.Contains(string(encoded), "retention_gaps") {
		t.Fatal(string(encoded), err)
	}
}
