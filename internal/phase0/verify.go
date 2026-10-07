package phase0

import (
	"fmt"
	"io"

	"github.com/Soroban-CII/soroindex/internal/store"
)

// InstanceArchivalChecked reports whether a summary's census read each
// contract's instance TTL. The testnet sample does (CurrentInstances). The
// Hubble-based mainnet census cannot, because the export carries no TTLs,
// so --verify-db then counts archived instances as live on the store side.
func InstanceArchivalChecked(s Summary) bool { return s.Sample != nil }

// Check is one number compared between a Phase 0 summary and the store.
type Check struct {
	Name   string
	Phase0 int
	Store  int
}

// OK reports whether the two agree.
func (c Check) OK() bool { return c.Phase0 == c.Store }

// Verify lists every number that a store seeded from this census's
// contracts must reproduce exactly. Only like is compared with like: live
// contracts by kind, measured contracts, and hashes some measured contract
// runs. Phase 0's other by-hash numbers include code no contract runs, which
// a seed never stores.
func Verify(s Summary, t store.Totals) []Check {
	out := []Check{
		{"live SAC contracts", s.Contracts.SAC, t.LiveSAC},
		{"live wasm contracts", s.Contracts.Wasm, t.LiveWasm},
		{"live wasm_ref contracts", s.Contracts.WasmRef, t.LiveWasmRef},
		{"unresolved wasm_ref", s.Contracts.WasmRefUnresolved, t.WasmRefUnresolved},
		{"measured contracts", s.Contracts.Measured, t.MeasuredContracts},
		{"hashes run by a measured contract", s.Hashes.UsedByContract, t.UsedHashes},
		{"contracts declaring any SEP", s.DeclaresAny.ByContract.N, t.DeclaringContracts},
		{"used hashes declaring any SEP", s.UsedHashes.Declaring, t.DeclaringUsedHashes},
		{"undeclared gap, contracts", s.SEP41.UndeclaredGap.Contracts, t.GapContracts},
		{"undeclared gap, used hashes", s.UsedHashes.Gap, t.GapUsedHashes},
	}
	for _, st := range []string{"match", "partial", "mismatch", "no_spec"} {
		out = append(out,
			Check{"SEP-41 " + st + ", contracts", s.SEP41.Status[st].Contracts, t.SEP41ByContract[st]},
			Check{"SEP-41 " + st + ", used hashes", s.UsedHashes.SEP41[st], t.SEP41ByUsedHash[st]})
	}
	return out
}

// PrintChecks writes the comparison as a table and reports whether every
// number agrees.
func PrintChecks(w io.Writer, checks []Check) (bool, error) {
	all := true
	if _, err := fmt.Fprintf(w, "%-36s %10s %10s\n", "check", "phase0", "store"); err != nil {
		return false, err
	}
	for _, c := range checks {
		mark := "ok"
		if !c.OK() {
			mark, all = "MISMATCH", false
		}
		if _, err := fmt.Fprintf(w, "%-36s %10d %10d  %s\n", c.Name, c.Phase0, c.Store, mark); err != nil {
			return false, err
		}
	}
	return all, nil
}
