// Command benchbudget checks `go test -bench -benchmem` output against a
// committed file of allocation budgets, so an allocation regression in the
// parser fails CI with a message saying which benchmark grew and by how much.
//
// Only allocs/op and B/op are checked. They are deterministic for a given Go
// toolchain and dependency set; ns/op on a shared runner is not, so it is
// reported but never compared.
//
// Usage:
//
//	go test ./pkg/sepmeta -run '^$' -bench ... -benchmem | benchbudget -budgets FILE
//	go test ./pkg/sepmeta -run '^$' -bench ... -benchmem | benchbudget -budgets FILE -write
//
// With -write it rewrites FILE from the results, adding headroom (10%, at
// least 2 allocations and 64 bytes), instead of checking. If
// GITHUB_STEP_SUMMARY is set, a Markdown table of every result is appended
// to it.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

func main() {
	budgets := flag.String("budgets", "", "budget file")
	write := flag.Bool("write", false, "rewrite the budget file from the results instead of checking")
	flag.Parse()
	if *budgets == "" {
		fmt.Fprintln(os.Stderr, "benchbudget: -budgets is required")
		os.Exit(2)
	}
	results, err := parseResults(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "benchbudget:", err)
		os.Exit(1)
	}
	if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		if err := appendSummary(path, results); err != nil {
			fmt.Fprintln(os.Stderr, "benchbudget: write summary:", err)
		}
	}
	if *write {
		if err := os.WriteFile(*budgets, []byte(formatBudgets(results)), 0o644); err != nil { // #nosec G306 G703 -- developer-supplied path; committed public file
			fmt.Fprintln(os.Stderr, "benchbudget:", err)
			os.Exit(1)
		}
		fmt.Printf("benchbudget: wrote %d budgets to %s\n", len(results), *budgets)
		return
	}
	b, err := os.ReadFile(*budgets) // #nosec G304 G703 -- developer-supplied path
	if err != nil {
		fmt.Fprintln(os.Stderr, "benchbudget:", err)
		os.Exit(1)
	}
	want, err := parseBudgets(string(b))
	if err != nil {
		fmt.Fprintf(os.Stderr, "benchbudget: %s: %v\n", *budgets, err)
		os.Exit(1)
	}
	problems := check(results, want)
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "FAIL", p)
	}
	if len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "\nbenchbudget: %d problem(s). If an increase is intended, run `make bench-budget-update`\n"+
			"and commit %s with the reason in the commit body.\n", len(problems), *budgets)
		os.Exit(1)
	}
	fmt.Printf("benchbudget: %d benchmarks within budget\n", len(results))
}

// result is one benchmark line.
type result struct {
	Name   string
	NsOp   float64
	BOp    int64
	Allocs int64
}

// budget is the most a benchmark may allocate per operation.
type budget struct {
	Allocs int64
	BOp    int64
}

// parseResults reads benchmark lines such as
//
//	BenchmarkDecodeSpec/token_full_sep-10  1000  19253 ns/op  82.07 MB/s  12080 B/op  219 allocs/op
//
// and drops the -GOMAXPROCS suffix from each name. A line without B/op and
// allocs/op is an error: the run was not made with -benchmem.
func parseResults(r io.Reader) ([]result, error) {
	var out []result
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || !strings.HasPrefix(f[0], "Benchmark") {
			continue
		}
		res := result{Name: trimProcs(f[0]), BOp: -1, Allocs: -1}
		for i := 2; i+1 < len(f); i += 2 {
			v, unit := f[i], f[i+1]
			switch unit {
			case "ns/op":
				res.NsOp, _ = strconv.ParseFloat(v, 64) // informational only
			case "B/op":
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("%s: B/op %q: %w", res.Name, v, err)
				}
				res.BOp = n
			case "allocs/op":
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("%s: allocs/op %q: %w", res.Name, v, err)
				}
				res.Allocs = n
			}
		}
		if res.BOp < 0 || res.Allocs < 0 {
			return nil, fmt.Errorf("%s: no B/op or allocs/op; run go test with -benchmem", res.Name)
		}
		if seen[res.Name] {
			return nil, fmt.Errorf("%s: reported twice; run with -count=1", res.Name)
		}
		seen[res.Name] = true
		out = append(out, res)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("no benchmark results on stdin")
	}
	return out, nil
}

// trimProcs removes a trailing "-N" (GOMAXPROCS) from a benchmark name.
func trimProcs(name string) string {
	i := strings.LastIndexByte(name, '-')
	if i < 0 {
		return name
	}
	if _, err := strconv.Atoi(name[i+1:]); err != nil {
		return name
	}
	return name[:i]
}

// parseBudgets reads lines of "name max-allocs/op max-B/op". Blank lines and
// lines starting with # are ignored.
func parseBudgets(s string) (map[string]budget, error) {
	out := map[string]budget{}
	for i, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 3 {
			return nil, fmt.Errorf("line %d: want 3 fields (name allocs bytes), got %d", i+1, len(f))
		}
		a, err1 := strconv.ParseInt(f[1], 10, 64)
		b, err2 := strconv.ParseInt(f[2], 10, 64)
		if err1 != nil || err2 != nil || a < 0 || b < 0 {
			return nil, fmt.Errorf("line %d: budgets must be non-negative integers", i+1)
		}
		if _, dup := out[f[0]]; dup {
			return nil, fmt.Errorf("line %d: %s listed twice", i+1, f[0])
		}
		out[f[0]] = budget{Allocs: a, BOp: b}
	}
	return out, nil
}

// check returns one message per problem: a benchmark over budget, a
// benchmark with no budget, or a budget with no benchmark.
func check(results []result, want map[string]budget) []string {
	var problems []string
	got := map[string]bool{}
	for _, r := range results {
		got[r.Name] = true
		b, ok := want[r.Name]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: no budget (new benchmark? run `make bench-budget-update`)", r.Name))
			continue
		}
		if r.Allocs > b.Allocs {
			problems = append(problems, fmt.Sprintf("%s: %d allocs/op exceeds budget %d (%s)", r.Name, r.Allocs, b.Allocs, pct(r.Allocs, b.Allocs)))
		}
		if r.BOp > b.BOp {
			problems = append(problems, fmt.Sprintf("%s: %d B/op exceeds budget %d (%s)", r.Name, r.BOp, b.BOp, pct(r.BOp, b.BOp)))
		}
	}
	var stale []string
	for name := range want {
		if !got[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	for _, name := range stale {
		problems = append(problems, fmt.Sprintf("%s: budgeted but not run (renamed or removed? update the budget file)", name))
	}
	return problems
}

func pct(got, limit int64) string {
	if limit == 0 {
		return "budget is 0"
	}
	return fmt.Sprintf("+%.1f%%", float64(got-limit)*100/float64(limit))
}

// headroom adds 10% to a measured value, at least floor.
func headroom(v, floor int64) int64 {
	return v + max((v+9)/10, floor)
}

func formatBudgets(results []result) string {
	sorted := append([]result(nil), results...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var b strings.Builder
	b.WriteString("# Allocation budgets for pkg/sepmeta benchmarks, checked by scripts/benchbudget\n")
	b.WriteString("# (make bench-budget). Each is the measured value plus 10% (at least 2 allocs\n")
	b.WriteString("# and 64 bytes). Regenerate with `make bench-budget-update` and say why in the\n")
	b.WriteString("# commit body.\n#\n")
	b.WriteString("# name\tmax-allocs/op\tmax-B/op\n")
	for _, r := range sorted {
		fmt.Fprintf(&b, "%s\t%d\t%d\n", r.Name, headroom(r.Allocs, 2), headroom(r.BOp, 64))
	}
	return b.String()
}

func appendSummary(path string, results []result) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) // #nosec G302 G304 G703 -- path set by the GitHub runner
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("| Benchmark | ns/op | B/op | allocs/op |\n| --- | ---: | ---: | ---: |\n")
	for _, r := range results {
		fmt.Fprintf(&b, "| `%s` | %.0f | %d | %d |\n", r.Name, r.NsOp, r.BOp, r.Allocs)
	}
	if _, err := f.WriteString(b.String()); err != nil {
		_ = f.Close() // the write error is the one to report
		return err
	}
	return f.Close()
}
