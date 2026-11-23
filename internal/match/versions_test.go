package match

import (
	"errors"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
	"github.com/Soroban-CII/soroindex/rules"
)

func shippedVersions(t *testing.T) Versions {
	t.Helper()
	cur, err := LoadRules(rules.FS)
	if err != nil {
		t.Fatal(err)
	}
	v, err := LoadVersions(rules.FS, cur)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestShippedSEP41Versions(t *testing.T) {
	got := shippedVersions(t).Known(41)
	want := []string{"sep41-v0.5.2", "sep41-v0.3.0", "sep41-v0.1.0"}
	if !slices.Equal(got, want) {
		t.Fatalf("SEP-41 versions = %v, want %v (newest first)", got, want)
	}
}

func TestHighestVersionOnGoldenFixtures(t *testing.T) {
	v := shippedVersions(t)
	tests := []struct{ fixture, want string }{
		{"token_full_sep.wasm", "sep41-v0.5.2"},    // transfer.to: MuxedAddress
		{"token_full_legacy.wasm", "sep41-v0.3.0"}, // transfer.to: Address, before v0.4.0
		{"token_partial.wasm", ""},                 // no burn, burn_from: no version
		{"not_token.wasm", ""},
	}
	for _, tt := range tests {
		t.Run(tt.fixture+" implements "+tt.want, func(t *testing.T) {
			fns, ok := fixtureFns(t, tt.fixture)
			if !ok {
				t.Fatal("fixture has no spec")
			}
			if got := v.Highest(41, fns); got != tt.want {
				t.Fatalf("Highest = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHighestVersionLeavesCurrentStatusUnchanged(t *testing.T) {
	cur, err := LoadRules(rules.FS)
	if err != nil {
		t.Fatal(err)
	}
	fns, _ := fixtureFns(t, "token_full_legacy.wasm")
	if r := Match(fns, cur[0]); r.Status != StatusMatch || r.RulesetVersion != "sep41-v0.5.2" {
		t.Fatalf("legacy token against the current file: %s %s, want match sep41-v0.5.2", r.Status, r.RulesetVersion)
	}
}

func TestHighestVersionWithSpendableBalanceIsStillNewest(t *testing.T) {
	v := shippedVersions(t)
	fns, _ := fixtureFns(t, "token_full_legacy.wasm")
	fns = append(fns, sepmeta.FnSig{Name: "spendable_balance", Inputs: []string{"Address"}, Outputs: []string{"i128"}})
	if got := v.Highest(41, fns); got != "sep41-v0.3.0" {
		t.Fatalf("Highest = %q: an extra function must not lower the version", got)
	}
	// Every SEP-41 version requires burn, so without it no version matches.
	var noBurn []sepmeta.FnSig
	for _, f := range fns {
		if f.Name != "burn" {
			noBurn = append(noBurn, f)
		}
	}
	if got := v.Highest(41, noBurn); got != "" {
		t.Fatalf("Highest without burn = %q, want none", got)
	}
}

func TestLoadVersionsRejects(t *testing.T) {
	cur, err := LoadRules(rules.FS)
	if err != nil {
		t.Fatal(err)
	}
	good, err := rules.FS.ReadFile("history/sep-0041-v0.3.0.json")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		file string
		body []byte
	}{
		{"a file name that is not sep-NNNN-vX.Y.Z.json", "history/sep41.json", good},
		{"a ruleset_version that differs from the file name", "history/sep-0041-v0.2.0.json", good},
		{"a version not older than the current file", "history/sep-0041-v0.5.2.json", []byte(`{"sep":41,"ruleset_version":"sep41-v0.5.2","source":"stellar-protocol sep-0041.md test","required":[{"fn":"name","inputs":[],"output":["String"]}],"optional":[]}`)},
		{"a SEP without a current file", "history/sep-0050-v0.1.0.json", []byte(`{"sep":50,"ruleset_version":"sep50-v0.1.0","source":"stellar-protocol sep-0050.md test","required":[{"fn":"name","inputs":[],"output":["String"]}],"optional":[]}`)},
		{"two types at one input position", "history/sep-0041-v0.2.0.json", []byte(`{"sep":41,"ruleset_version":"sep41-v0.2.0","source":"stellar-protocol sep-0041.md test","required":[{"fn":"transfer","inputs":[["Address"],["MuxedAddress","Address"],["i128"]],"output":[]}],"optional":[]}`)},
	}
	for _, tt := range tests {
		t.Run("rejects "+tt.name, func(t *testing.T) {
			_, err := LoadVersions(fstest.MapFS{tt.file: {Data: tt.body}}, cur)
			if !errors.Is(err, ErrInvalidRules) {
				t.Fatalf("err = %v, want ErrInvalidRules", err)
			}
		})
	}
}
