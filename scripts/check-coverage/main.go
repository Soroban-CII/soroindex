// Package main verifies code coverage against committed per-package thresholds.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type thresholdMap map[string]float64

type pkgStats struct {
	covered int
	total   int
}

func main() {
	profilePath := flag.String("profile", "coverage.out", "path to coverprofile")
	thresholdsPath := flag.String("thresholds", "scripts/coverage-thresholds.json", "path to committed thresholds JSON")
	flag.Parse()

	if err := run(*profilePath, *thresholdsPath); err != nil {
		fmt.Fprintf(os.Stderr, "coverage-check: %v\n", err)
		os.Exit(1)
	}
}

func run(profilePath, thresholdsPath string) error {
	cleanThresholds := filepath.Clean(thresholdsPath)
	threshData, err := os.ReadFile(cleanThresholds) // #nosec G304 G703
	if err != nil {
		return fmt.Errorf("read thresholds %s: %w", thresholdsPath, err)
	}
	var thresholds thresholdMap
	if err := json.Unmarshal(threshData, &thresholds); err != nil {
		return fmt.Errorf("parse thresholds: %w", err)
	}

	cleanProfile := filepath.Clean(profilePath)
	f, err := os.Open(cleanProfile) // #nosec G304 G703
	if err != nil {
		return fmt.Errorf("open profile %s: %w", profilePath, err)
	}
	defer func() { _ = f.Close() }()

	pkgs := make(map[string]*pkgStats)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "mode:") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 3 {
			continue
		}
		loc := parts[0]
		stmts, err1 := strconv.Atoi(parts[1])
		count, err2 := strconv.Atoi(parts[2])
		if err1 != nil || err2 != nil {
			continue
		}
		// extract package path
		filePart := strings.Split(loc, ":")[0]
		pkgIdx := strings.LastIndex(filePart, "/")
		if pkgIdx == -1 {
			continue
		}
		fullPkg := filePart[:pkgIdx]
		// Normalize: strip module prefix github.com/Soroban-CII/soroindex/
		shortPkg := strings.TrimPrefix(fullPkg, "github.com/Soroban-CII/soroindex/")
		if strings.HasPrefix(shortPkg, "scripts") {
			continue
		}

		stat := pkgs[shortPkg]
		if stat == nil {
			stat = &pkgStats{}
			pkgs[shortPkg] = stat
		}
		stat.total += stmts
		if count > 0 {
			stat.covered += stmts
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan profile: %w", err)
	}

	var names []string
	for name := range pkgs {
		names = append(names, name)
	}
	sort.Strings(names)

	fmt.Println("-----------------------------------------------------------------------------")
	fmt.Printf("%-32s %-12s %-12s %s\n", "Package", "Coverage", "Threshold", "Status")
	fmt.Println("-----------------------------------------------------------------------------")

	var failed []string
	var totalCovered, totalStmts int

	// Write summary markdown if GITHUB_STEP_SUMMARY exists
	summaryFile := os.Getenv("GITHUB_STEP_SUMMARY")
	var summaryLines []string
	summaryLines = append(summaryLines, "### Test Coverage by Package", "", "| Package | Coverage | Threshold | Status |", "|---|---|---|---|")

	for _, name := range names {
		stat := pkgs[name]
		totalCovered += stat.covered
		totalStmts += stat.total

		pct := 0.0
		if stat.total > 0 {
			pct = (float64(stat.covered) / float64(stat.total)) * 100.0
		}
		// round to 1 decimal place
		pctRounded := math.Round(pct*10) / 10

		thresh, hasThresh := thresholds[name]
		status := "PASS"
		if !hasThresh {
			status = "MISSING THRESHOLD"
			failed = append(failed, fmt.Sprintf("%s: missing committed threshold (actual: %.1f%%)", name, pctRounded))
		} else if pctRounded < thresh {
			status = "FAIL"
			failed = append(failed, fmt.Sprintf("%s: %.1f%% is below threshold %.1f%%", name, pctRounded, thresh))
		}

		threshStr := "-"
		if hasThresh {
			threshStr = fmt.Sprintf("%.1f%%", thresh)
		}
		fmt.Printf("%-32s %-12s %-12s %s\n", name, fmt.Sprintf("%.1f%%", pctRounded), threshStr, status)
		summaryLines = append(summaryLines, fmt.Sprintf("| `%s` | %.1f%% | %s | %s |", name, pctRounded, threshStr, status))
	}

	totalPct := 0.0
	if totalStmts > 0 {
		totalPct = (float64(totalCovered) / float64(totalStmts)) * 100.0
	}
	fmt.Println("-----------------------------------------------------------------------------")
	fmt.Printf("Total statements: %d/%d (%.1f%%)\n", totalCovered, totalStmts, totalPct)

	if summaryFile != "" {
		summaryLines = append(summaryLines, "", fmt.Sprintf("**Total coverage:** %d/%d (%.1f%%)", totalCovered, totalStmts, totalPct))
		cleanSummary := filepath.Clean(summaryFile)
		_ = os.WriteFile(cleanSummary, []byte(strings.Join(summaryLines, "\n")+"\n"), 0600) // #nosec G306 G703
	}

	if len(failed) > 0 {
		return fmt.Errorf("coverage check failed:\n  %s", strings.Join(failed, "\n  "))
	}
	return nil
}
