package main

import (
	"strings"
	"testing"
)

const sample = `goos: linux
goarch: amd64
pkg: github.com/Soroban-CII/soroindex/pkg/sepmeta
BenchmarkDecodeSpec/token_full_sep-4   	    1000	     19253 ns/op	  82.07 MB/s	   12080 B/op	     219 allocs/op
BenchmarkParseSEP47/multi_sep          	    1000	       190.0 ns/op	      72 B/op	       3 allocs/op
PASS
ok  	github.com/Soroban-CII/soroindex/pkg/sepmeta	1.651s
`

func TestParseResultsReadsBenchmemLines(t *testing.T) {
	got, err := parseResults(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	want := []result{
		{Name: "BenchmarkDecodeSpec/token_full_sep", NsOp: 19253, BOp: 12080, Allocs: 219},
		{Name: "BenchmarkParseSEP47/multi_sep", NsOp: 190, BOp: 72, Allocs: 3},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("result %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseResultsRejectsRunsWithoutBenchmem(t *testing.T) {
	_, err := parseResults(strings.NewReader("BenchmarkX-8  100  50 ns/op\n"))
	if err == nil || !strings.Contains(err.Error(), "-benchmem") {
		t.Fatalf("err = %v, want a -benchmem hint", err)
	}
	if _, err := parseResults(strings.NewReader("PASS\n")); err == nil {
		t.Fatal("no results: want an error")
	}
	dup := "BenchmarkX  1  1 ns/op  1 B/op  1 allocs/op\nBenchmarkX  1  1 ns/op  1 B/op  1 allocs/op\n"
	if _, err := parseResults(strings.NewReader(dup)); err == nil || !strings.Contains(err.Error(), "-count=1") {
		t.Fatalf("duplicate result: err = %v, want a -count=1 hint", err)
	}
}

func TestTrimProcsDropsOnlyANumericSuffix(t *testing.T) {
	for in, want := range map[string]string{
		"BenchmarkA/b-10":       "BenchmarkA/b",
		"BenchmarkA/b":          "BenchmarkA/b",
		"BenchmarkA/token-full": "BenchmarkA/token-full",
	} {
		if got := trimProcs(in); got != want {
			t.Errorf("trimProcs(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckReportsEveryKindOfProblem(t *testing.T) {
	results := []result{
		{Name: "BenchmarkOK", BOp: 100, Allocs: 5},
		{Name: "BenchmarkOver", BOp: 300, Allocs: 12},
		{Name: "BenchmarkNew", BOp: 1, Allocs: 1},
	}
	budgets := map[string]budget{
		"BenchmarkOK":   {Allocs: 5, BOp: 100}, // exactly at budget passes
		"BenchmarkOver": {Allocs: 10, BOp: 200},
		"BenchmarkGone": {Allocs: 1, BOp: 1},
	}
	got := strings.Join(check(results, budgets), "\n")
	for _, want := range []string{
		"BenchmarkOver: 12 allocs/op exceeds budget 10 (+20.0%)",
		"BenchmarkOver: 300 B/op exceeds budget 200 (+50.0%)",
		"BenchmarkNew: no budget",
		"BenchmarkGone: budgeted but not run",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "BenchmarkOK") {
		t.Errorf("a result at its budget was reported:\n%s", got)
	}
}

func TestWrittenBudgetsParseBackWithHeadroom(t *testing.T) {
	text := formatBudgets([]result{{Name: "BenchmarkB", BOp: 1000, Allocs: 30}, {Name: "BenchmarkA", BOp: 10, Allocs: 1}})
	b, err := parseBudgets(text)
	if err != nil {
		t.Fatal(err)
	}
	if b["BenchmarkB"] != (budget{Allocs: 33, BOp: 1100}) {
		t.Errorf("BenchmarkB budget = %+v, want 10%% headroom {33 1100}", b["BenchmarkB"])
	}
	if b["BenchmarkA"] != (budget{Allocs: 3, BOp: 74}) {
		t.Errorf("BenchmarkA budget = %+v, want the minimum headroom {3 74}", b["BenchmarkA"])
	}
	if strings.Index(text, "BenchmarkA") > strings.Index(text, "BenchmarkB") {
		t.Error("budgets are not sorted by name")
	}
}

func TestParseBudgetsRejectsBadLines(t *testing.T) {
	for _, in := range []string{"BenchmarkA 1\n", "BenchmarkA x 2\n", "BenchmarkA -1 2\n", "BenchmarkA 1 2\nBenchmarkA 1 2\n"} {
		if _, err := parseBudgets(in); err == nil {
			t.Errorf("parseBudgets(%q): want an error", in)
		}
	}
}
