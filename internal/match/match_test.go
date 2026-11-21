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
	return loadRule(t, 41)
}

func loadRule(t *testing.T, sep int) RuleFile {
	t.Helper()
	rules, err := LoadRules(os.DirFS("../../rules"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rules {
		if r.SEP == sep {
			return r
		}
	}
	t.Fatalf("no rule file for SEP-%d", sep)
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

// fullNFT returns a spec satisfying every SEP-50 rule, using u64 for TokenID/Balance.
func fullNFT() []sepmeta.FnSig {
	return []sepmeta.FnSig{
		fn("balance",             []string{"Address"},                               "u64"),
		fn("owner_of",            []string{"u64"},                                   "Address"),
		fn("transfer",            []string{"Address", "Address", "u64"}),
		fn("transfer_from",       []string{"Address", "Address", "Address", "u64"}),
		fn("approve",             []string{"Address", "Address", "u64", "u32"}),
		fn("approve_for_all",     []string{"Address", "Address", "u32"}),
		fn("get_approved",        []string{"u64"},                                   "Option<Address>"),
		fn("is_approved_for_all", []string{"Address", "Address"},                   "bool"),
		fn("name",                nil,                                               "String"),
		fn("symbol",              nil,                                               "String"),
		fn("token_uri",           []string{"u64"},                                   "String"),
	}
}

// fullVault returns a spec satisfying every SEP-56 rule (SEP-41 base + vault functions).
func fullVault() []sepmeta.FnSig {
	return []sepmeta.FnSig{
		fn("allowance",         []string{"Address", "Address"},                               "i128"),
		fn("approve",           []string{"Address", "Address", "i128", "u32"}),
		fn("balance",           []string{"Address"},                                          "i128"),
		fn("transfer",          []string{"Address", "MuxedAddress", "i128"}),
		fn("transfer_from",     []string{"Address", "Address", "Address", "i128"}),
		fn("burn",              []string{"Address", "i128"}),
		fn("burn_from",         []string{"Address", "Address", "i128"}),
		fn("decimals",          nil,                                                          "u32"),
		fn("name",              nil,                                                          "String"),
		fn("symbol",            nil,                                                          "String"),
		fn("total_supply",      nil,                                                          "i128"),
		fn("query_asset",       nil,                                                          "Address"),
		fn("total_assets",      nil,                                                          "i128"),
		fn("convert_to_shares", []string{"i128"},                                             "i128"),
		fn("convert_to_assets", []string{"i128"},                                             "i128"),
		fn("max_deposit",       []string{"Address"},                                          "i128"),
		fn("preview_deposit",   []string{"i128"},                                             "i128"),
		fn("deposit",           []string{"i128", "Address", "Address", "Address"},            "i128"),
		fn("max_mint",          []string{"Address"},                                          "i128"),
		fn("preview_mint",      []string{"i128"},                                             "i128"),
		fn("mint",              []string{"i128", "Address", "Address", "Address"},            "i128"),
		fn("max_withdraw",      []string{"Address"},                                          "i128"),
		fn("preview_withdraw",  []string{"i128"},                                             "i128"),
		fn("withdraw",          []string{"i128", "Address", "Address", "Address"},            "i128"),
		fn("max_redeem",        []string{"Address"},                                          "i128"),
		fn("preview_redeem",    []string{"i128"},                                             "i128"),
		fn("redeem",            []string{"i128", "Address", "Address", "Address"},            "i128"),
	}
}

func TestMatchSEP50Rules(t *testing.T) {
	r := loadRule(t, 50)
	if len(r.Required) != 11 {
		t.Fatalf("SEP-50 required count = %d, want 11", len(r.Required))
	}
	t.Run("full NFT is match", func(t *testing.T) {
		got := Match(fullNFT(), r)
		if got.Status != StatusMatch || got.OKCount != 11 {
			t.Fatalf("status %s ok %d, want match 11", got.Status, got.OKCount)
		}
	})
	t.Run("u32 TokenID as input also matches", func(t *testing.T) {
		fns := fullNFT()
		for i, f := range fns {
			for j, in := range f.Inputs {
				if in == "u64" {
					fns[i].Inputs[j] = "u32"
				}
			}
			// Do not change balance output: rule file specifies u64 output.
		}
		got := Match(fns, r)
		if got.Status != StatusMatch || got.OKCount != 11 {
			t.Fatalf("u32 input variant: status %s ok %d, want match 11", got.Status, got.OKCount)
		}
	})
	t.Run("missing 6 of 11 is mismatch", func(t *testing.T) {
		got := Match(without(fullNFT(), "approve", "approve_for_all", "get_approved", "is_approved_for_all", "token_uri", "transfer_from"), r)
		if got.Status != StatusMismatch || got.OKCount != 5 {
			t.Fatalf("status %s ok %d, want mismatch 5", got.Status, got.OKCount)
		}
	})
	t.Run("missing 5 of 11 is partial", func(t *testing.T) {
		got := Match(without(fullNFT(), "approve", "approve_for_all", "get_approved", "is_approved_for_all", "token_uri"), r)
		if got.Status != StatusPartial || got.OKCount != 6 {
			t.Fatalf("status %s ok %d, want partial 6", got.Status, got.OKCount)
		}
	})
	t.Run("wrong output on balance is mismatched", func(t *testing.T) {
		fns := append(without(fullNFT(), "balance"), fn("balance", []string{"Address"}, "i128"))
		got := Match(fns, r)
		if got.Status != StatusPartial || got.OKCount != 10 || len(got.Mismatched) != 1 {
			t.Fatalf("status %s ok %d mis %d, want partial 10 1-mismatch", got.Status, got.OKCount, len(got.Mismatched))
		}
	})
	t.Run("ruleset version and SEP carried", func(t *testing.T) {
		got := Match(fullNFT(), r)
		if got.RulesetVersion != "sep50-v0.1.0" || got.SEP != 50 {
			t.Errorf("result carries %d/%s", got.SEP, got.RulesetVersion)
		}
	})
}

func TestMatchSEP56Rules(t *testing.T) {
	r := loadRule(t, 56)
	if len(r.Required) != 27 {
		t.Fatalf("SEP-56 required count = %d, want 27", len(r.Required))
	}
	t.Run("full vault is match", func(t *testing.T) {
		got := Match(fullVault(), r)
		if got.Status != StatusMatch || got.OKCount != 27 {
			t.Fatalf("status %s ok %d, want match 27", got.Status, got.OKCount)
		}
	})
	t.Run("transfer.to Address also matches", func(t *testing.T) {
		fns := fullVault()
		for i, f := range fns {
			if f.Name == "transfer" && len(f.Inputs) > 1 && f.Inputs[1] == "MuxedAddress" {
				fns[i].Inputs[1] = "Address"
			}
		}
		got := Match(fns, r)
		if got.Status != StatusMatch || got.OKCount != 27 {
			t.Fatalf("Address variant: status %s ok %d, want match 27", got.Status, got.OKCount)
		}
	})
	t.Run("missing vault functions only is partial", func(t *testing.T) {
		got := Match(without(fullVault(), "total_supply", "query_asset", "total_assets",
			"convert_to_shares", "convert_to_assets", "max_deposit", "preview_deposit",
			"deposit", "max_mint", "preview_mint", "mint", "max_withdraw", "preview_withdraw"), r)
		if got.Status != StatusPartial || got.OKCount != 14 {
			t.Fatalf("status %s ok %d, want partial 14", got.Status, got.OKCount)
		}
	})
	t.Run("empty spec is mismatch", func(t *testing.T) {
		got := Match(nil, r)
		if got.Status != StatusMismatch || got.OKCount != 0 {
			t.Fatalf("status %s ok %d, want mismatch 0", got.Status, got.OKCount)
		}
	})
	t.Run("ruleset version and SEP carried", func(t *testing.T) {
		got := Match(fullVault(), r)
		if got.RulesetVersion != "sep56-v0.1.2" || got.SEP != 56 {
			t.Errorf("result carries %d/%s", got.SEP, got.RulesetVersion)
		}
	})
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
