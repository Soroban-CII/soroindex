package sepmeta

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// Cross-implementation conformance. scripts/record-conformance.sh runs
// stellar-cli and the Python stellar-sdk on every golden fixture and on
// every meta vector (wrapped in a minimal module), and commits what they
// report under testdata/conformance/. This test compares sepmeta with those
// recordings, so it needs neither tool.
//
// Agreement is the default. Every disagreement must carry one of the
// difference IDs below, and every ID must be documented, with its reason, in
// the table in docs/how-it-works.md. An ID that no input exhibits any more
// fails the test too, so the table cannot go stale.

const conformanceDir = "../../testdata/conformance"

// conformanceDifferences are the known, intentional differences. The keys
// are the IDs used in docs/how-it-works.md.
var conformanceDifferences = map[string]string{ // #nosec G101 -- difference IDs and prose, not credentials
	"leading-zero":     "Python accepts 041 as 41 even in strict mode; sepmeta strict drops it",
	"prefixed-token":   "Python skips SEP-41; sepmeta lenient reads it as 41 with an anomaly",
	"unicode-digits":   "Python accepts non-ASCII decimal digits; sepmeta requires ASCII",
	"oversized-digits": "Python accepts 7+ digit numbers; sepmeta drops them",
	"oversized-value":  "Python splits a sep value over 4 KiB; sepmeta accepts nothing from it",
	"strict-raises":    "Python strict mode raises for the whole contract; sepmeta strict drops the token",
	"cli-escapes":      "stellar-cli JSON escapes control and non-ASCII bytes in meta values",
	"cli-absent-section": "stellar-cli exits with an error when the meta or spec section is absent; " +
		"sepmeta reports no entries",
}

type sepsResult struct {
	OK    bool   `json:"ok"`
	SEPs  []int  `json:"seps"`
	Error string `json:"error"`
}

type conformanceRecord struct {
	Input struct {
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		SHA256 string `json:"wasm_sha256"`
	} `json:"input"`
	CLI struct {
		Meta struct {
			OK      bool        `json:"ok"`
			Entries [][2]string `json:"entries"`
			Error   string      `json:"error"`
		} `json:"meta"`
		Interface struct {
			OK        bool   `json:"ok"`
			Error     string `json:"error"`
			Functions []struct {
				Name    string            `json:"name"`
				Inputs  []json.RawMessage `json:"inputs"`
				Outputs []json.RawMessage `json:"outputs"`
			} `json:"functions"`
		} `json:"interface"`
	} `json:"stellar_cli"`
	Py struct {
		OK         bool        `json:"ok"`
		Error      string      `json:"error"`
		Items      [][2]string `json:"items"`
		Seps       sepsResult  `json:"supported_seps"`
		SepsStrict sepsResult  `json:"supported_seps_strict"`
		Parts      [][]struct {
			Part    string     `json:"part"`
			Lenient sepsResult `json:"lenient"`
			Strict  sepsResult `json:"strict"`
		} `json:"parts"`
	} `json:"py_stellar_sdk"`
}

// sepmetaView is everything sepmeta reports for one module.
type sepmetaView struct {
	readErr  error // ReadCustomSections
	metaErr  error // DecodeMeta
	entries  []MetaEntry
	hasSpec  bool
	specErr  error
	fns      []FnSig
	lenient  SEPClaims
	strict   SEPClaims
	metaFail bool // any error on the meta path
}

func viewOf(wasm []byte) sepmetaView {
	var v sepmetaView
	secs, err := ReadCustomSections(wasm, []string{SectionMeta, SectionSpec}, DefaultLimits())
	v.readErr = err
	if err == nil {
		v.entries, v.metaErr = DecodeMeta(secs[SectionMeta], DefaultLimits())
		var spec []xdr.ScSpecEntry
		_, v.hasSpec = secs[SectionSpec]
		spec, v.specErr = DecodeSpec(secs[SectionSpec], DefaultLimits())
		v.fns = Functions(spec)
	}
	v.metaFail = v.readErr != nil || v.metaErr != nil
	v.lenient = ParseSEP47(v.entries, Lenient)
	v.strict = ParseSEP47(v.entries, Strict)
	return v
}

// vectorModule wraps a vector's lines in a module with one contractmetav0
// section, as `fixturetool metawasm` does; the recorded hash proves the two
// built the same bytes.
func vectorModule(t *testing.T, name string) []byte {
	t.Helper()
	var meta bytes.Buffer
	for _, e := range readVector(t, filepath.Join(vectorDir, name+".txt")) {
		if _, err := xdr.Marshal(&meta, xdr.ScMetaEntry{Kind: xdr.ScMetaKindScMetaV0, V0: &xdr.ScMetaV0{Key: e.Key, Val: e.Value}}); err != nil {
			t.Fatal(err)
		}
	}
	return module(custom(SectionMeta, meta.Bytes()))
}

// cliTypeNames maps stellar-cli's JSON spelling of scalar spec types to
// sepmeta's canonical names.
var cliTypeNames = map[string]string{
	"val": "Val", "bool": "bool", "void": "void", "error": "Error",
	"u32": "u32", "i32": "i32", "u64": "u64", "i64": "i64",
	"timepoint": "Timepoint", "duration": "Duration",
	"u128": "u128", "i128": "i128", "u256": "u256", "i256": "i256",
	"bytes": "Bytes", "string": "String", "symbol": "Symbol",
	"address": "Address", "muxed_address": "MuxedAddress",
}

// cliType renders a type from stellar-cli's interface JSON in sepmeta's
// canonical form, so the two can be compared as strings.
func cliType(raw json.RawMessage) (string, error) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if n, ok := cliTypeNames[s]; ok {
			return n, nil
		}
		return "", fmt.Errorf("unknown scalar %q", s)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || len(obj) != 1 {
		return "", fmt.Errorf("unexpected type JSON %s", raw)
	}
	for kind, body := range obj {
		var f map[string]json.RawMessage
		if err := json.Unmarshal(body, &f); err != nil {
			return "", err
		}
		sub := func(k string) (string, error) { return cliType(f[k]) }
		switch kind {
		case "option":
			v, err := sub("value_type")
			return "Option<" + v + ">", err
		case "vec":
			v, err := sub("element_type")
			return "Vec<" + v + ">", err
		case "map":
			k, err1 := sub("key_type")
			v, err2 := sub("value_type")
			return "Map<" + k + "," + v + ">", firstErr(err1, err2)
		case "result":
			ok, err1 := sub("ok_type")
			er, err2 := sub("error_type")
			return "Result<" + ok + "," + er + ">", firstErr(err1, err2)
		case "tuple":
			var vals []json.RawMessage
			if err := json.Unmarshal(f["value_types"], &vals); err != nil {
				return "", err
			}
			parts := make([]string, len(vals))
			for i, v := range vals {
				p, err := cliType(v)
				if err != nil {
					return "", err
				}
				parts[i] = p
			}
			return "(" + strings.Join(parts, ",") + ")", nil
		case "bytes_n":
			var n uint32
			if err := json.Unmarshal(f["n"], &n); err != nil {
				return "", err
			}
			return "BytesN<" + strconv.FormatUint(uint64(n), 10) + ">", nil
		case "udt":
			var name string
			if err := json.Unmarshal(f["name"], &name); err != nil {
				return "", err
			}
			return "udt:" + name, nil
		}
		return "", fmt.Errorf("unknown type kind %q", kind)
	}
	return "", fmt.Errorf("unreachable")
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// acceptedSEP returns the single SEP a one-part parse accepted, or -1.
func acceptedSEP(c SEPClaims) int {
	if len(c.SEPs) == 1 {
		return c.SEPs[0]
	}
	return -1
}

func pySEP(r sepsResult) int {
	if r.OK && len(r.SEPs) == 1 {
		return r.SEPs[0]
	}
	return -1
}

// partDifference classifies a disagreement between sepmeta and Python on
// one comma-separated part. "" means they agree.
func partDifference(part string, py struct {
	Part    string     `json:"part"`
	Lenient sepsResult `json:"lenient"`
	Strict  sepsResult `json:"strict"`
}) string {
	entries := []MetaEntry{{SEPMetaKey, part}}
	lax, strict := ParseSEP47(entries, Lenient), ParseSEP47(entries, Strict)
	if acceptedSEP(lax) == pySEP(py.Lenient) && acceptedSEP(strict) == pySEP(py.Strict) {
		return ""
	}
	anomaly := ""
	if len(lax.Tokens) == 1 {
		anomaly = lax.Tokens[0].Anomaly
	}
	switch anomaly {
	case AnomalyLeadingZero:
		return "leading-zero"
	case AnomalyPrefixedToken:
		return "prefixed-token"
	case AnomalyNonNumeric:
		return "unicode-digits"
	case AnomalyOversized:
		return "oversized-digits"
	}
	return "unexplained part " + strconv.Quote(part)
}

func TestConformanceWithStellarCLIAndPythonSDK(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(conformanceDir, "*-*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no recordings in %s (run scripts/record-conformance.sh): %v", conformanceDir, err)
	}
	sort.Strings(files)
	seen := map[string]bool{}
	recorded := map[string]bool{}
	var table strings.Builder
	fmt.Fprintf(&table, "\n%-34s | %-14s | %-14s | %-14s | %s\n", "input", "sepmeta", "python", "python strict", "differences")

	for _, f := range files {
		b, err := os.ReadFile(f) // #nosec G304 -- path from a Glob over the committed recordings
		if err != nil {
			t.Fatal(err)
		}
		var rec conformanceRecord
		if err := json.Unmarshal(b, &rec); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		id := rec.Input.Kind + ":" + rec.Input.Name
		recorded[id] = true
		var diffs []string
		note := func(d string) {
			if !contains(diffs, d) {
				diffs = append(diffs, d)
			}
			seen[d] = true
		}

		t.Run(id+" agrees with stellar-cli and Python or differs only as documented", func(t *testing.T) {
			var wasm []byte
			switch rec.Input.Kind {
			case "fixture":
				wasm = fixture(t, rec.Input.Name+".wasm")
			case "vector":
				wasm = vectorModule(t, rec.Input.Name)
			default:
				t.Fatalf("unknown input kind %q", rec.Input.Kind)
			}
			sum := sha256.Sum256(wasm)
			if got := hex.EncodeToString(sum[:]); got != rec.Input.SHA256 {
				t.Fatalf("module hash %s, recorded %s: the recording is for different bytes", got, rec.Input.SHA256)
			}
			v := viewOf(wasm)

			// stellar-cli meta: same pairs, or the same failure.
			metaAbsent := v.readErr == nil && !hasSection(wasm, SectionMeta)
			if !rec.CLI.Meta.OK && metaAbsent && strings.Contains(rec.CLI.Meta.Error, "no meta present") {
				note("cli-absent-section")
			} else if rec.CLI.Meta.OK == v.metaFail {
				t.Errorf("stellar-cli meta ok=%v (%s), sepmeta error read=%v decode=%v", rec.CLI.Meta.OK, rec.CLI.Meta.Error, v.readErr, v.metaErr)
			} else if rec.CLI.Meta.OK && !sameEntries(rec.CLI.Meta.Entries, v.entries) {
				if !sameEntries(rec.CLI.Meta.Entries, escapedLikeCLI(v.entries)) {
					t.Errorf("stellar-cli meta %q, sepmeta %q", rec.CLI.Meta.Entries, v.entries)
				}
				note("cli-escapes")
			}

			// stellar-cli interface: same functions and types, or both none.
			cliNone := !rec.CLI.Interface.OK
			sepNone := v.readErr != nil || !v.hasSpec || v.specErr != nil
			if cliNone && v.readErr == nil && !v.hasSpec && strings.Contains(rec.CLI.Interface.Error, "no interface present") {
				note("cli-absent-section")
			}
			if cliNone != sepNone {
				t.Errorf("stellar-cli interface ok=%v (%s), sepmeta has spec=%v (read %v, decode %v)", rec.CLI.Interface.OK, rec.CLI.Interface.Error, v.hasSpec, v.readErr, v.specErr)
			} else if !cliNone {
				var cliFns []FnSig
				for _, fn := range rec.CLI.Interface.Functions {
					sig := FnSig{Name: fn.Name, Inputs: []string{}, Outputs: []string{}}
					for _, in := range fn.Inputs {
						n, err := cliType(in)
						if err != nil {
							t.Fatalf("%s input: %v", fn.Name, err)
						}
						sig.Inputs = append(sig.Inputs, n)
					}
					for _, o := range fn.Outputs {
						n, err := cliType(o)
						if err != nil {
							t.Fatalf("%s output: %v", fn.Name, err)
						}
						sig.Outputs = append(sig.Outputs, n)
					}
					cliFns = append(cliFns, sig)
				}
				if !reflect.DeepEqual(cliFns, v.fns) {
					t.Errorf("stellar-cli functions %+v\nsepmeta functions %+v", cliFns, v.fns)
				}
			}

			// Python: same meta pairs, or the same failure.
			if rec.Py.OK == v.metaFail {
				t.Fatalf("Python ok=%v (%s), sepmeta error read=%v decode=%v", rec.Py.OK, rec.Py.Error, v.readErr, v.metaErr)
			}
			if !rec.Py.OK {
				fmt.Fprintf(&table, "%-34s | %-14s | %-14s | %-14s | %s\n", id, "error", "error", "error", "")
				return
			}
			if !sameEntries(rec.Py.Items, v.entries) {
				t.Errorf("Python items %q, sepmeta entries %q", rec.Py.Items, v.entries)
			}

			// Python, part by part: each sep value split on commas.
			var values []string
			for _, e := range v.entries {
				if e.Key == SEPMetaKey {
					values = append(values, e.Value)
				}
			}
			if len(values) != len(rec.Py.Parts) {
				t.Fatalf("%d sep values, Python recorded %d", len(values), len(rec.Py.Parts))
			}
			partDiffs := 0
			for i, val := range values {
				parts := strings.Split(val, ",")
				if len(parts) != len(rec.Py.Parts[i]) {
					t.Fatalf("value %d: %d parts, Python recorded %d", i, len(parts), len(rec.Py.Parts[i]))
				}
				if len(val) > maxValueBytes {
					note("oversized-value") // sepmeta does not split it at all
					continue
				}
				for j, p := range parts {
					if rec.Py.Parts[i][j].Part != p {
						t.Fatalf("value %d part %d: %q, Python recorded %q", i, j, p, rec.Py.Parts[i][j].Part)
					}
					if d := partDifference(p, rec.Py.Parts[i][j]); d != "" {
						if _, ok := conformanceDifferences[d]; !ok {
							t.Errorf("value %d part %d: %s", i, j, d)
						}
						note(d)
						partDiffs++
					}
				}
			}

			// Whole contract: any disagreement must follow from the parts.
			if !reflect.DeepEqual(rec.Py.Seps.SEPs, v.lenient.SEPs) && partDiffs == 0 && !contains(diffs, "oversized-value") {
				t.Errorf("lenient: Python %v, sepmeta %v, with no part-level difference to explain it", rec.Py.Seps.SEPs, v.lenient.SEPs)
			}
			pyStrict := fmt.Sprint(rec.Py.SepsStrict.SEPs)
			if !rec.Py.SepsStrict.OK {
				note("strict-raises")
				pyStrict = "raises"
			} else if !reflect.DeepEqual(rec.Py.SepsStrict.SEPs, v.strict.SEPs) && partDiffs == 0 && !contains(diffs, "oversized-value") {
				t.Errorf("strict: Python %v, sepmeta %v, with no part-level difference to explain it", rec.Py.SepsStrict.SEPs, v.strict.SEPs)
			}
			fmt.Fprintf(&table, "%-34s | %-14s | %-14s | %-14s | %s\n", id, fmt.Sprint(v.lenient.SEPs), fmt.Sprint(rec.Py.Seps.SEPs), pyStrict, strings.Join(diffs, ","))
		})
	}

	t.Run("every fixture and vector has a recording", func(t *testing.T) {
		wasms, _ := filepath.Glob("../../testdata/wasm/*.wasm")
		vecs, _ := filepath.Glob(filepath.Join(vectorDir, "*.txt"))
		for _, w := range wasms {
			if id := "fixture:" + strings.TrimSuffix(filepath.Base(w), ".wasm"); !recorded[id] {
				t.Errorf("%s has no recording; run scripts/record-conformance.sh", id)
			}
		}
		for _, v := range vecs {
			if id := "vector:" + strings.TrimSuffix(filepath.Base(v), ".txt"); !recorded[id] {
				t.Errorf("%s has no recording; run scripts/record-conformance.sh", id)
			}
		}
	})

	t.Run("every difference is observed and documented in docs/how-it-works.md", func(t *testing.T) {
		doc, err := os.ReadFile("../../docs/how-it-works.md")
		if err != nil {
			t.Fatal(err)
		}
		for id := range conformanceDifferences {
			if !seen[id] {
				t.Errorf("difference %q is listed but no recording shows it; remove it from the list and the docs", id)
			}
			if !bytes.Contains(doc, []byte("| `"+id+"` |")) {
				t.Errorf("difference %q has no row in the docs/how-it-works.md table", id)
			}
		}
	})
	t.Log(table.String())
}

// hasSection reports whether a well-formed module has a custom section.
func hasSection(wasm []byte, name string) bool {
	secs, err := ReadCustomSections(wasm, []string{name}, DefaultLimits())
	_, ok := secs[name]
	return err == nil && ok
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func sameEntries(pairs [][2]string, entries []MetaEntry) bool {
	if len(pairs) != len(entries) {
		return false
	}
	for i, p := range pairs {
		if p[0] != entries[i].Key || p[1] != entries[i].Value {
			return false
		}
	}
	return true
}

// escapedLikeCLI renders entries the way stellar-cli's JSON prints meta
// strings: printable ASCII as is, \t \n \r as two-character escapes and any
// other byte as \xNN. It covers only what the recordings contain; anything
// else still fails the comparison.
func escapedLikeCLI(entries []MetaEntry) []MetaEntry {
	esc := func(s string) string {
		var b strings.Builder
		for i := 0; i < len(s); i++ {
			switch c := s[i]; {
			case c == '\t':
				b.WriteString(`\t`)
			case c == '\n':
				b.WriteString(`\n`)
			case c == '\r':
				b.WriteString(`\r`)
			case c >= 0x20 && c < 0x7f:
				b.WriteByte(c)
			default:
				fmt.Fprintf(&b, `\x%02x`, c)
			}
		}
		return b.String()
	}
	out := make([]MetaEntry, len(entries))
	for i, e := range entries {
		out[i] = MetaEntry{Key: esc(e.Key), Value: esc(e.Value)}
	}
	return out
}
