package match

import (
	"errors"
	"io/fs"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

func sep41(t *testing.T) RuleFile {
	t.Helper()
	rules, err := LoadRules(os.DirFS("../../rules"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rules {
		if r.SEP == 41 {
			return r
		}
	}
	t.Fatal("no sep-0041.json")
	return RuleFile{}
}

// fixtureFns returns Functions() of a golden Wasm, or ok=false if it has no spec.
func fixtureFns(t *testing.T, name string) ([]sepmeta.FnSig, bool) {
	t.Helper()
	w, err := fs.ReadFile(os.DirFS("../../testdata/wasm"), name)
	if err != nil {
		t.Fatal(err)
	}
	secs, err := sepmeta.ReadCustomSections(w, []string{sepmeta.SectionSpec}, sepmeta.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	s, ok := secs[sepmeta.SectionSpec]
	if !ok {
		return nil, false
	}
	spec, err := sepmeta.DecodeSpec(s, sepmeta.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return sepmeta.Functions(spec), true
}

func TestMatchFixtures(t *testing.T) {
	r := sep41(t)
	tests := []struct {
		fixture    string
		status     Status
		ok         int
		missing    []string
		mismatched []string
		variantTo  string
	}{
		{"token_full_sep.wasm", StatusMatch, 10, []string{}, nil, "MuxedAddress"},
		{"token_full_legacy.wasm", StatusMatch, 10, []string{}, nil, "Address"},
		{"token_partial.wasm", StatusPartial, 8, []string{"burn", "burn_from"}, nil, "MuxedAddress"},
		{"not_token.wasm", StatusMismatch, 0, []string{"allowance", "approve", "balance", "burn", "burn_from", "decimals", "name", "symbol", "transfer", "transfer_from"}, nil, ""},
		{"multi_sep.wasm", StatusMismatch, 1, []string{"allowance", "approve", "balance", "burn", "burn_from", "name", "symbol", "transfer", "transfer_from"}, nil, ""},
		{"no_meta.wasm", StatusMismatch, 0, []string{"allowance", "approve", "balance", "burn", "burn_from", "decimals", "name", "symbol", "transfer", "transfer_from"}, nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.fixture+" is "+string(tt.status), func(t *testing.T) {
			fns, ok := fixtureFns(t, tt.fixture)
			if !ok {
				t.Fatal("fixture has no spec")
			}
			got := Match(fns, r)
			if got.Status != tt.status || got.OKCount != tt.ok || got.RequiredCount != 10 {
				t.Fatalf("status %s ok %d/%d, want %s %d/10", got.Status, got.OKCount, got.RequiredCount, tt.status, tt.ok)
			}
			if !reflect.DeepEqual(got.Missing, tt.missing) {
				t.Errorf("missing = %v, want %v", got.Missing, tt.missing)
			}
			if len(got.Mismatched) != 0 {
				t.Errorf("mismatched = %+v, want none", got.Mismatched)
			}
			if v := got.MatchedVariants["transfer.inputs[1]"]; v != tt.variantTo {
				t.Errorf("transfer.inputs[1] variant = %q, want %q", v, tt.variantTo)
			}
			if got.RulesetVersion != "sep41-v0.5.2" || got.SEP != 41 {
				t.Errorf("result carries %d/%s", got.SEP, got.RulesetVersion)
			}
		})
	}
}

func fn(name string, in []string, out ...string) sepmeta.FnSig {
	if in == nil {
		in = []string{}
	}
	if out == nil {
		out = []string{}
	}
	return sepmeta.FnSig{Name: name, Inputs: in, Outputs: out}
}

// fullToken returns a spec satisfying every SEP-41 rule.
func fullToken() []sepmeta.FnSig {
	return []sepmeta.FnSig{
		fn("allowance", []string{"Address", "Address"}, "i128"),
		fn("approve", []string{"Address", "Address", "i128", "u32"}),
		fn("balance", []string{"Address"}, "i128"),
		fn("transfer", []string{"Address", "MuxedAddress", "i128"}),
		fn("transfer_from", []string{"Address", "Address", "Address", "i128"}),
		fn("burn", []string{"Address", "i128"}),
		fn("burn_from", []string{"Address", "Address", "i128"}),
		fn("decimals", nil, "u32"),
		fn("name", nil, "String"),
		fn("symbol", nil, "String"),
	}
}

func without(fns []sepmeta.FnSig, names ...string) []sepmeta.FnSig {
	drop := map[string]bool{}
	for _, n := range names {
		drop[n] = true
	}
	var out []sepmeta.FnSig
	for _, f := range fns {
		if !drop[f.Name] {
			out = append(out, f)
		}
	}
	return out
}

func TestMatchRules(t *testing.T) {
	r := sep41(t)
	replace := func(f sepmeta.FnSig) []sepmeta.FnSig { return append(without(fullToken(), f.Name), f) }
	tests := []struct {
		name       string
		fns        []sepmeta.FnSig
		matcher    Matcher
		wantStatus Status
		wantOK     int
		wantMis    []Mismatch
	}{
		{"extra functions are ignored", append(fullToken(), fn("mint", []string{"Address", "i128"})), Matcher{}, StatusMatch, 10, nil},
		{"5 of 10 is partial at the default threshold", without(fullToken(), "burn", "burn_from", "approve", "allowance", "transfer_from"), Matcher{}, StatusPartial, 5, nil},
		{"4 of 10 is mismatch at the default threshold", without(fullToken(), "burn", "burn_from", "approve", "allowance", "transfer_from", "name"), Matcher{}, StatusMismatch, 4, nil},
		{"8 of 10 is mismatch at threshold 0.9", without(fullToken(), "burn", "burn_from"), Matcher{PartialThreshold: 0.9}, StatusMismatch, 8, nil},
		{"none present is mismatch", nil, Matcher{}, StatusMismatch, 0, nil},
		{
			"swapped parameter order is mismatched",
			replace(fn("approve", []string{"Address", "Address", "u32", "i128"})), Matcher{}, StatusPartial, 9,
			[]Mismatch{{Fn: "approve", Reason: "types", Expected: Signature{[]string{"Address", "Address", "i128", "u32"}, []string{}}, Actual: Signature{[]string{"Address", "Address", "u32", "i128"}, []string{}}}},
		},
		{
			"wrong output type is mismatched",
			replace(fn("balance", []string{"Address"}, "u128")), Matcher{}, StatusPartial, 9,
			[]Mismatch{{Fn: "balance", Reason: "types", Expected: Signature{[]string{"Address"}, []string{"i128"}}, Actual: Signature{[]string{"Address"}, []string{"u128"}}}},
		},
		{
			"missing output is mismatched",
			replace(fn("decimals", nil)), Matcher{}, StatusPartial, 9,
			[]Mismatch{{Fn: "decimals", Reason: "types", Expected: Signature{[]string{}, []string{"u32"}}, Actual: Signature{[]string{}, []string{}}}},
		},
		{
			"extra parameter is mismatched",
			replace(fn("burn", []string{"Address", "i128", "u32"})), Matcher{}, StatusPartial, 9,
			[]Mismatch{{Fn: "burn", Reason: "types", Expected: Signature{[]string{"Address", "i128"}, []string{}}, Actual: Signature{[]string{"Address", "i128", "u32"}, []string{}}}},
		},
		{
			"type outside the accepted set is mismatched",
			replace(fn("transfer", []string{"Address", "String", "i128"})), Matcher{}, StatusPartial, 9,
			[]Mismatch{{Fn: "transfer", Reason: "types", Expected: Signature{[]string{"Address", "MuxedAddress", "i128"}, []string{}}, Actual: Signature{[]string{"Address", "String", "i128"}, []string{}}}},
		},
		{
			"a duplicated name is mismatched, not ok",
			append(fullToken(), fn("balance", []string{"Address"}, "i128")), Matcher{}, StatusPartial, 9,
			[]Mismatch{{Fn: "balance", Reason: "duplicate", Expected: Signature{[]string{"Address"}, []string{"i128"}}, Actual: Signature{[]string{"Address"}, []string{"i128"}}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.matcher.Match(tt.fns, r)
			if got.Status != tt.wantStatus || got.OKCount != tt.wantOK {
				t.Fatalf("status %s ok %d, want %s %d", got.Status, got.OKCount, tt.wantStatus, tt.wantOK)
			}
			want := tt.wantMis
			if want == nil {
				want = []Mismatch{}
			}
			if !reflect.DeepEqual(got.Mismatched, want) {
				t.Fatalf("mismatched = %+v\nwant %+v", got.Mismatched, want)
			}
		})
	}
}

func TestOptionalFunctions(t *testing.T) {
	r := RuleFile{
		SEP: 9, RulesetVersion: "sep9-v1.0.0", Source: "test rule file, not a real SEP",
		Required: []FnRule{{Fn: "a", Inputs: [][]string{}, Output: []string{"u32"}}},
		Optional: []FnRule{{Fn: "b", Inputs: [][]string{{"u32"}}, Output: []string{}}},
	}
	t.Run("absent optional does not affect status or missing", func(t *testing.T) {
		got := Match([]sepmeta.FnSig{fn("a", nil, "u32")}, r)
		if got.Status != StatusMatch || len(got.Missing) != 0 {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("wrong optional is listed but status still match", func(t *testing.T) {
		got := Match([]sepmeta.FnSig{fn("a", nil, "u32"), fn("b", []string{"i32"})}, r)
		if got.Status != StatusMatch || len(got.Mismatched) != 1 || !got.Mismatched[0].Optional {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestNoSpec(t *testing.T) {
	got := NoSpec(sep41(t))
	if got.Status != StatusNoSpec || got.OKCount != 0 || got.RequiredCount != 10 || got.Missing == nil || got.MatchedVariants == nil {
		t.Fatalf("got %+v", got)
	}
}

func TestParseRuleFileRejects(t *testing.T) {
	good := `{"sep":41,"ruleset_version":"sep41-v0.5.2","source":"a source long enough to pass","required":[{"fn":"x","inputs":[["u32"]],"output":[]}],"optional":[]}`
	tests := []struct{ name, body string }{
		{"unknown field", strings.Replace(good, `"optional":[]`, `"optional":[],"extra":1`, 1)},
		{"trailing data", good + `{}`},
		{"sep zero", strings.Replace(good, `"sep":41`, `"sep":0`, 1)},
		{"bad ruleset_version", strings.Replace(good, `sep41-v0.5.2`, `v1`, 1)},
		{"short source", strings.Replace(good, `a source long enough to pass`, `x`, 1)},
		{"empty required", strings.Replace(good, `[{"fn":"x","inputs":[["u32"]],"output":[]}]`, `[]`, 1)},
		{"missing optional", strings.Replace(good, `,"optional":[]`, ``, 1)},
		{"bad function name", strings.Replace(good, `"fn":"x"`, `"fn":"has space"`, 1)},
		{"position accepting nothing", strings.Replace(good, `[["u32"]]`, `[[]]`, 1)},
		{"repeated accepted type", strings.Replace(good, `[["u32"]]`, `[["u32","u32"]]`, 1)},
		{"invalid type name", strings.Replace(good, `[["u32"]]`, `[["u 32"]]`, 1)},
		{"two outputs", strings.Replace(good, `"output":[]`, `"output":["u32","u32"]`, 1)},
		{"missing inputs", strings.Replace(good, `"inputs":[["u32"]],`, ``, 1)},
		{"duplicate function", strings.Replace(good, `"optional":[]`, `"optional":[{"fn":"x","inputs":[],"output":[]}]`, 1)},
	}
	if _, err := ParseRuleFile([]byte(good)); err != nil {
		t.Fatalf("baseline rule rejected: %v", err)
	}
	for _, tt := range tests {
		t.Run("rejects "+tt.name, func(t *testing.T) {
			if _, err := ParseRuleFile([]byte(tt.body)); !errors.Is(err, ErrInvalidRules) {
				t.Fatalf("err = %v, want ErrInvalidRules", err)
			}
		})
	}
}

func TestLoadRules(t *testing.T) {
	body := func(sep string) *fstest.MapFile {
		return &fstest.MapFile{Data: []byte(`{"sep":` + sep + `,"ruleset_version":"sep` + sep + `-v1.0.0","source":"a source long enough to pass","required":[{"fn":"x","inputs":[],"output":[]}],"optional":[]}`)}
	}
	t.Run("repository rules all load", func(t *testing.T) {
		rules, err := LoadRules(os.DirFS("../../rules"))
		if err != nil || len(rules) == 0 {
			t.Fatalf("rules %d, err %v", len(rules), err)
		}
	})
	t.Run("sorted by SEP", func(t *testing.T) {
		rules, err := LoadRules(fstest.MapFS{"sep-0050.json": body("50"), "sep-0040.json": body("40")})
		if err != nil || rules[0].SEP != 40 || rules[1].SEP != 50 {
			t.Fatalf("got %v, %v", rules, err)
		}
	})
	t.Run("file name must match its sep", func(t *testing.T) {
		if _, err := LoadRules(fstest.MapFS{"sep-0041.json": body("40")}); !errors.Is(err, ErrInvalidRules) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unexpected file name is rejected", func(t *testing.T) {
		if _, err := LoadRules(fstest.MapFS{"sep-41.json": body("41")}); !errors.Is(err, ErrInvalidRules) {
			t.Fatalf("err = %v", err)
		}
	})
}
