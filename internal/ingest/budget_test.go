package ingest

import (
	"testing"

	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// TestZeroXDRBudgetKeepsShippedLimits pins the values each decode site used
// before the limits became configurable. A caller that sets no budget must
// decode exactly what it decoded before, so this test fails if plumbing the
// configuration ever changes a default.
func TestZeroXDRBudgetKeepsShippedLimits(t *testing.T) {
	var zero XDRBudget
	tests := []struct {
		name     string
		got      XDRLimits
		wantLen  int
		wantDepn uint
	}{
		{"contract code was 2 MiB of base64", zero.Code(), 2 << 20, 500},
		{"a contract instance was 16 MiB", zero.Instance(), 16 << 20, 500},
		{"a CAP-85 reference entry was the XDRLimits default of 4 MiB", zero.ExecRef(), 4 << 20, 500},
		{"a data-quality check entry was 1 MiB", zero.CheckEntry(), 1 << 20, 500},
		{"ledger metadata was 64 MiB", zero.LedgerMeta(), 64 << 20, 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got.MaxBase64Len != tt.wantLen || tt.got.MaxDepth != tt.wantDepn {
				t.Fatalf("got %+v, want {MaxBase64Len:%d MaxDepth:%d}", tt.got, tt.wantLen, tt.wantDepn)
			}
		})
	}
	if DefaultXDRDepth != int(sepmeta.DefaultLimits().MaxXDRDepth) {
		t.Fatalf("DefaultXDRDepth %d disagrees with sepmeta's default depth %d", DefaultXDRDepth, sepmeta.DefaultLimits().MaxXDRDepth)
	}
}

func TestXDRBudgetAppliesConfiguration(t *testing.T) {
	t.Run("a lowered ceiling tightens every site", func(t *testing.T) {
		b := XDRBudget{MaxBase64Len: 512 << 10}
		for name, got := range map[string]XDRLimits{
			"Code": b.Code(), "Instance": b.Instance(), "ExecRef": b.ExecRef(),
			"CheckEntry": b.CheckEntry(), "LedgerMeta": b.LedgerMeta(),
		} {
			if got.MaxBase64Len != 512<<10 {
				t.Errorf("%s = %d, want the configured ceiling 524288", name, got.MaxBase64Len)
			}
		}
	})

	t.Run("a raised ceiling raises ledger metadata, the largest XDR decoded", func(t *testing.T) {
		b := XDRBudget{MaxBase64Len: 128 << 20}
		if got := b.LedgerMeta().MaxBase64Len; got != 128<<20 {
			t.Fatalf("LedgerMeta = %d, want 134217728", got)
		}
		if got := b.Instance().MaxBase64Len; got != 16<<20 {
			t.Fatalf("Instance = %d, want its own 16 MiB cap, not the ceiling", got)
		}
	})

	t.Run("the configured depth reaches every site", func(t *testing.T) {
		b := XDRBudget{MaxDepth: 64}
		for name, got := range map[string]XDRLimits{
			"Code": b.Code(), "Instance": b.Instance(), "ExecRef": b.ExecRef(),
			"CheckEntry": b.CheckEntry(), "LedgerMeta": b.LedgerMeta(),
		} {
			if got.MaxDepth != 64 {
				t.Errorf("%s depth = %d, want 64", name, got.MaxDepth)
			}
		}
	})

	t.Run("a raised MaxWasmBytes raises the contract-code ceiling with it", func(t *testing.T) {
		// Without this, raising the parser's Wasm limit would be defeated by
		// the base64 cap on the entry that carries the module.
		b := XDRBudget{MaxWasmBytes: 8 << 20}
		want := base64Len((8 << 20) + codeEntryOverheadBytes)
		if got := b.Code().MaxBase64Len; got != want {
			t.Fatalf("Code = %d, want %d", got, want)
		}
		if b.Code().MaxBase64Len <= 8<<20 {
			t.Fatalf("Code = %d, which cannot carry an 8 MiB module as base64", b.Code().MaxBase64Len)
		}
	})

	t.Run("the ceiling still clamps a code limit grown by MaxWasmBytes", func(t *testing.T) {
		b := XDRBudget{MaxWasmBytes: 8 << 20, MaxBase64Len: 3 << 20}
		if got := b.Code().MaxBase64Len; got != 3<<20 {
			t.Fatalf("Code = %d, want the configured ceiling 3145728", got)
		}
	})

	t.Run("For clamps a site maximum to the ceiling", func(t *testing.T) {
		b := XDRBudget{MaxBase64Len: 1000}
		if got := b.For(5000).MaxBase64Len; got != 1000 {
			t.Fatalf("For(5000) = %d, want 1000", got)
		}
		if got := b.For(500).MaxBase64Len; got != 500 {
			t.Fatalf("For(500) = %d, want 500", got)
		}
	})
}

func TestBase64Len(t *testing.T) {
	tests := []struct{ in, want int }{{0, 0}, {-1, 0}, {1, 4}, {2, 4}, {3, 4}, {4, 8}, {1 << 20, 1398104}}
	for _, tt := range tests {
		if got := base64Len(tt.in); got != tt.want {
			t.Errorf("base64Len(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
