package sepmeta

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files under testdata")

const vectorDir = "../../testdata/vectors"

// readVector parses a vector .txt: one sep entry value per "\n"-terminated
// line. It splits on "\n" only, so a "\r" inside a value is preserved.
func readVector(t *testing.T, path string) []MetaEntry {
	t.Helper()
	b, err := os.ReadFile(path) // #nosec G304 -- path comes from a Glob over the committed vector dir
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.HasSuffix(s, "\n") {
		t.Fatalf("%s: last line must end in \\n", path)
	}
	var entries []MetaEntry
	for _, line := range strings.Split(strings.TrimSuffix(s, "\n"), "\n") {
		entries = append(entries, MetaEntry{Key: SEPMetaKey, Value: line})
	}
	return entries
}

func TestVectors(t *testing.T) {
	inputs, err := filepath.Glob(filepath.Join(vectorDir, "*.txt"))
	if err != nil || len(inputs) == 0 {
		t.Fatalf("no vectors found: %v", err)
	}
	sort.Strings(inputs)
	var table strings.Builder
	fmt.Fprintf(&table, "\n%-26s | %-8s | %-12s | %s\n", "vector", "mode", "seps", "anomalies")
	for _, in := range inputs {
		name := strings.TrimSuffix(filepath.Base(in), ".txt")
		entries := readVector(t, in)
		for _, m := range []struct {
			mode Mode
			ext  string
		}{{Lenient, "lenient"}, {Strict, "strict"}} {
			t.Run(name+"/"+m.ext+" matches golden", func(t *testing.T) {
				got := ParseSEP47(entries, m.mode)
				fmt.Fprintf(&table, "%-26s | %-8s | %-12s | %s\n", name, m.ext, fmt.Sprint(got.SEPs), anomalies(got))
				gotJSON, err := json.MarshalIndent(got, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				gotJSON = append(gotJSON, '\n')
				golden := filepath.Join(vectorDir, name+"."+m.ext+".json")
				if *update {
					if err := os.WriteFile(golden, gotJSON, 0o600); err != nil {
						t.Fatal(err)
					}
					return
				}
				want, err := os.ReadFile(golden) // #nosec G304 -- path derived from the committed vector dir
				if err != nil {
					t.Fatalf("missing golden (run with -update): %v", err)
				}
				if !bytes.Equal(gotJSON, want) {
					t.Fatalf("%s differs from golden\ngot:\n%s\nwant:\n%s", golden, gotJSON, want)
				}
			})
		}
	}
	t.Log(table.String())
}

func anomalies(c SEPClaims) string {
	var a []string
	for _, tok := range c.Tokens {
		if tok.Anomaly != "" {
			a = append(a, tok.Anomaly)
		}
	}
	if len(a) == 0 {
		return "-"
	}
	return strings.Join(a, ",")
}

// TestParseSEP47Rules states each §5.2 rule directly, independent of the
// golden files, so a wrong golden cannot make the suite pass.
func TestParseSEP47Rules(t *testing.T) {
	sep := func(vals ...string) []MetaEntry {
		var e []MetaEntry
		for _, v := range vals {
			e = append(e, MetaEntry{Key: SEPMetaKey, Value: v})
		}
		return e
	}
	tests := []struct {
		name        string
		entries     []MetaEntry
		mode        Mode
		wantSEPs    []int
		wantAnomaly []string // per token, in order
		wantEntries int
	}{
		{"canonical number is accepted", sep("41"), Strict, []int{41}, []string{""}, 1},
		{"list keeps first-seen order", sep("46,41"), Strict, []int{46, 41}, []string{"", ""}, 1},
		{"ASCII whitespace is trimmed", sep(" 41 ,\t46\n"), Strict, []int{41, 46}, []string{"", ""}, 1},
		{"leading zero is accepted in lenient mode", sep("041"), Lenient, []int{41}, []string{AnomalyLeadingZero}, 1},
		{"leading zero is dropped in strict mode", sep("041"), Strict, []int{}, []string{AnomalyLeadingZero}, 1},
		{"SEP- prefix is accepted in lenient mode", sep("SEP-41"), Lenient, []int{41}, []string{AnomalyPrefixedToken}, 1},
		{"lowercase sep prefix is accepted in lenient mode", sep("sep41"), Lenient, []int{41}, []string{AnomalyPrefixedToken}, 1},
		{"prefix is dropped in strict mode", sep("SEP-41"), Strict, []int{}, []string{AnomalyPrefixedToken}, 1},
		{"other prefix separators are non-numeric", sep("SEP 41", "SEP_41", "SEP--41"), Lenient, []int{}, []string{AnomalyNonNumeric, AnomalyNonNumeric, AnomalyNonNumeric}, 3},
		{"bare prefix is non-numeric", sep("SEP-"), Lenient, []int{}, []string{AnomalyNonNumeric}, 1},
		{"letters are non-numeric", sep("abc"), Lenient, []int{}, []string{AnomalyNonNumeric}, 1},
		{"decimal point is non-numeric", sep("4.1"), Lenient, []int{}, []string{AnomalyNonNumeric}, 1},
		{"sign is non-numeric", sep("-1", "+41"), Lenient, []int{}, []string{AnomalyNonNumeric, AnomalyNonNumeric}, 2},
		{"non-ASCII digits are non-numeric", sep("٤١"), Lenient, []int{}, []string{AnomalyNonNumeric}, 1},
		{"empty value is one empty token", sep(""), Lenient, []int{}, []string{AnomalyEmptyToken}, 1},
		{"double comma yields an empty token", sep("41,,46"), Lenient, []int{41, 46}, []string{"", AnomalyEmptyToken, ""}, 1},
		{"trailing comma yields an empty token", sep("41,"), Lenient, []int{41}, []string{"", AnomalyEmptyToken}, 1},
		{"six digits are accepted", sep("999999"), Strict, []int{999999}, []string{""}, 1},
		{"seven digits are oversized", sep("1234567"), Lenient, []int{}, []string{AnomalyOversized}, 1},
		{"value over 4 KiB is one oversized token", sep(strings.Repeat("41,", 1400)), Lenient, []int{}, []string{AnomalyOversized}, 1},
		{"duplicates are deduplicated", sep("41,41"), Strict, []int{41}, []string{"", ""}, 1},
		{"separate entries are unioned with no anomaly", sep("41", "40"), Strict, []int{41, 40}, []string{"", ""}, 2},
		{"duplicate across entries is deduplicated", sep("41", "41,40"), Strict, []int{41, 40}, []string{"", "", ""}, 2},
		{
			"keys are case-sensitive and other keys are ignored",
			[]MetaEntry{{"SEP", "41"}, {"seps", "41"}, {"rsver", "1.98.1"}, {"sep", "40"}},
			Strict, []int{40}, []string{""}, 1,
		},
		{"no entries yields empty claims", nil, Lenient, []int{}, nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseSEP47(tt.entries, tt.mode)
			if !reflect.DeepEqual(got.SEPs, tt.wantSEPs) {
				t.Errorf("SEPs = %v, want %v", got.SEPs, tt.wantSEPs)
			}
			var gotAnom []string
			for _, tok := range got.Tokens {
				gotAnom = append(gotAnom, tok.Anomaly)
			}
			if !reflect.DeepEqual(gotAnom, tt.wantAnomaly) {
				t.Errorf("anomalies = %q, want %q", gotAnom, tt.wantAnomaly)
			}
			if got.EntryCount != tt.wantEntries {
				t.Errorf("EntryCount = %d, want %d", got.EntryCount, tt.wantEntries)
			}
		})
	}
}

func TestParseSEP47TokenFields(t *testing.T) {
	got := ParseSEP47([]MetaEntry{{SEPMetaKey, " 041 ,SEP-46"}}, Strict)
	want := []Token{
		{Raw: "041", SEP: 41, Anomaly: AnomalyLeadingZero, Accepted: false},
		{Raw: "SEP-46", SEP: 46, Anomaly: AnomalyPrefixedToken, Accepted: false},
	}
	if !reflect.DeepEqual(got.Tokens, want) {
		t.Fatalf("tokens = %+v\nwant     %+v", got.Tokens, want)
	}
}

func TestOversizedRawIsCapped(t *testing.T) {
	t.Run("raw text is capped at 64 bytes plus an ellipsis", func(t *testing.T) {
		got := ParseSEP47([]MetaEntry{{SEPMetaKey, strings.Repeat("9", 5000)}}, Lenient)
		if raw := got.Tokens[0].Raw; raw != strings.Repeat("9", 64)+"…" {
			t.Fatalf("raw = %q", raw)
		}
	})
	t.Run("cap does not split a multi-byte character", func(t *testing.T) {
		v := strings.Repeat("a", 63) + "é" + strings.Repeat("b", 5000) // é is 2 bytes, starting at byte 63
		raw := ParseSEP47([]MetaEntry{{SEPMetaKey, v}}, Lenient).Tokens[0].Raw
		if raw != strings.Repeat("a", 63)+"…" {
			t.Fatalf("raw = %q", raw)
		}
	})
}

// TestMultiSepFixture is the STOP B check: the two-module fixture yields
// [41 40], EntryCount 2, and no anomaly.
func TestMultiSepFixture(t *testing.T) {
	entries, err := DecodeMeta(fixtureSection(t, "multi_sep.wasm", SectionMeta), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []Mode{Lenient, Strict} {
		got := ParseSEP47(entries, mode)
		t.Logf("mode %d: SEPs=%v EntryCount=%d anomalies=%s", mode, got.SEPs, got.EntryCount, anomalies(got))
		if !reflect.DeepEqual(got.SEPs, []int{41, 40}) || got.EntryCount != 2 || anomalies(got) != "-" {
			t.Fatalf("mode %d: got %+v, want SEPs [41 40], EntryCount 2, no anomaly", mode, got)
		}
	}
}
