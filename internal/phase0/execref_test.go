package phase0

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/ingest"
)

// The census fixture has three references from one owner: v1 resolved from
// the export (token_partial), v2 resolved by RPC (multi_sep), and "gone",
// which is missing and must be counted, not dropped.
func TestExecRefReportFromCensus(t *testing.T) {
	f, in, _ := mainnetFixture(t)
	res, err := RunMainnet(context.Background(), f, in, testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	e := res.Summary.ExecRefs
	if e == nil {
		t.Fatal("exec_refs section missing")
	}
	if e.Owners != 1 || e.References != 3 || e.Contracts != 3 || e.Resolved != 2 || e.Missing != 1 || e.Archived != 0 {
		t.Fatalf("exec refs = %+v", *e)
	}
	byTag := map[string]ExecRefRow{}
	for _, r := range e.Top {
		byTag[r.Tag] = r
	}
	if r := byTag["v1"]; r.Status != "resolved" || r.SEP41 != "partial" || !reflect.DeepEqual(r.Declared, []int{41}) {
		t.Errorf("v1 = %+v", r)
	}
	if r := byTag["v2"]; r.Status != "resolved" || !reflect.DeepEqual(r.Declared, []int{41, 40}) {
		t.Errorf("v2 = %+v", r)
	}
	if r := byTag["gone"]; r.Status != "missing" || r.WasmHash != "" || len(r.Declared) != 0 || r.SEP41 != "" {
		t.Errorf("gone = %+v; an unresolved reference claims nothing", r)
	}

	dir := t.TempDir()
	if err := Write(dir, res); err != nil {
		t.Fatal(err)
	}
	md, err := os.ReadFile(filepath.Join(dir, "ADOPTION.md")) // #nosec G304 -- test temp dir
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"### Executable references (CAP-85)", "| Owner contracts | 1 |",
		"| References (owner, tag) | 3 |", "| … references unresolved: entry missing | 1 |", "| 1 | 3 | 3 |",
		"| `" + cid(200) + "` | `gone` | 1 | missing | – | – |", "| `" + cid(200) + "` | `v1` | 1 | resolved | 41 | `partial` |"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("ADOPTION.md lacks %q", want)
		}
	}
	for _, banned := range []string{"Soroswap", "Blend", "Aquarius"} {
		if strings.Contains(string(md), banned) {
			t.Errorf("ADOPTION.md labels a reference with project name %q", banned)
		}
	}
}

func TestSummarizeExecRefsFanoutAndArchived(t *testing.T) {
	var cs []Contract
	add := func(n int, owner, tag string, mod func(*Contract)) {
		for range n {
			c := Contract{Kind: ingest.KindWasmRef, RefOwner: owner, RefTag: tag, WasmHash: "aa"}
			if mod != nil {
				mod(&c)
			}
			cs = append(cs, c)
		}
	}
	add(1200, "OA", "big", nil)
	add(150, "OA", "mid", nil)
	add(12, "OB", "small", nil)
	add(2, "OB", "pair", func(c *Contract) { c.WasmHash, c.Unresolved, c.RefArchived = "", true, true })
	add(1, "OC", "one", func(c *Contract) { c.WasmHash, c.Unresolved = "", true })
	add(5, "OC", "dead", func(c *Contract) { c.Archived = true }) // archived instances are not counted
	cs = append(cs, Contract{Kind: ingest.KindWasm, WasmHash: "bb"}, Contract{Kind: ingest.KindSAC})

	e := SummarizeExecRefs(cs, map[string]ingest.WasmResult{})
	if e.Owners != 3 || e.References != 5 || e.Contracts != 1365 || e.Resolved != 3 || e.Archived != 1 || e.Missing != 1 {
		t.Fatalf("report = %+v", *e)
	}
	want := []FanoutBucket{{1, 1, 1, 1}, {2, 9, 1, 2}, {10, 99, 1, 12}, {100, 999, 1, 150}, {1000, 0, 1, 1200}}
	if !reflect.DeepEqual(e.Fanout, want) {
		t.Errorf("fanout = %+v, want %+v", e.Fanout, want)
	}
	if e.Top[0].Tag != "big" || e.Top[0].Contracts != 1200 || e.Top[3].Status != "archived" || e.Top[4].Status != "missing" {
		t.Errorf("top = %+v", e.Top)
	}
	if SummarizeExecRefs([]Contract{{Kind: ingest.KindSAC}}, nil) != nil {
		t.Error("no wasm_ref contracts should give no section")
	}
}

func TestExecRefTopIsBounded(t *testing.T) {
	var cs []Contract
	for i := range ExecRefTopN + 5 {
		cs = append(cs, Contract{Kind: ingest.KindWasmRef, RefOwner: "O", RefTag: string(rune('a' + i)), Unresolved: true})
	}
	e := SummarizeExecRefs(cs, nil)
	if e.References != ExecRefTopN+5 || len(e.Top) != ExecRefTopN || e.Missing != ExecRefTopN+5 {
		t.Fatalf("references %d, top %d, missing %d", e.References, len(e.Top), e.Missing)
	}
}
