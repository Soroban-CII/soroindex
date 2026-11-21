package phase0

import (
	"strings"
	"testing"
)

func makeSummary(network, method, parser, generated string, ledger uint32, declH, declHOf, declC, declCOf int, rulesets []string) Summary {
	var s Summary
	s.Network = network
	s.Method = method
	s.ParserVersion = parser
	s.GeneratedAt = generated
	s.LatestLedger = ledger
	s.RulesetVersions = rulesets
	s.DeclaresAny.ByHash = rate(declH, declHOf)
	s.DeclaresAny.ByContract = rate(declC, declCOf)
	return s
}

func TestTrend_AddPoint_Deterministic(t *testing.T) {
	s1 := makeSummary("testnet", "retention-window", "1", "2026-09-01T00:00:00Z", 5000000, 2, 100, 5, 200, []string{"sep41-v0.5.0"})
	s2 := makeSummary("testnet", "retention-window", "1", "2026-10-01T00:00:00Z", 5097816, 3, 107, 7, 183, []string{"sep41-v0.5.2"})

	var tr Trend
	if err := tr.AddPoint(s1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := tr.AddPoint(s2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tr.Network != "testnet" {
		t.Errorf("got network %q, want testnet", tr.Network)
	}
	if len(tr.Points) != 2 {
		t.Fatalf("got %d points, want 2", len(tr.Points))
	}
	if tr.Points[0].LatestLedger != 5000000 {
		t.Errorf("point[0] ledger %d, want 5000000", tr.Points[0].LatestLedger)
	}
	if tr.Points[1].LatestLedger != 5097816 {
		t.Errorf("point[1] ledger %d, want 5097816", tr.Points[1].LatestLedger)
	}

	// Calling AddPoint twice with the same inputs must yield an identical Trend.
	var tr2 Trend
	_ = tr2.AddPoint(s1)
	_ = tr2.AddPoint(s2)
	if len(tr2.Points) != len(tr.Points) {
		t.Errorf("non-deterministic point count: %d vs %d", len(tr2.Points), len(tr.Points))
	}
	for i, pt := range tr.Points {
		pt2 := tr2.Points[i]
		if pt.LatestLedger != pt2.LatestLedger || pt.DeclaresAnyHash.N != pt2.DeclaresAnyHash.N {
			t.Errorf("point[%d] differs between two equal constructions", i)
		}
	}
}

func TestTrend_NetworkMismatch(t *testing.T) {
	s1 := makeSummary("testnet", "retention-window", "1", "2026-09-01T00:00:00Z", 5000000, 0, 100, 0, 200, nil)
	s2 := makeSummary("mainnet", "retention-window", "1", "2026-09-01T00:00:00Z", 1000000, 0, 100, 0, 200, nil)
	var tr Trend
	if err := tr.AddPoint(s1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := tr.AddPoint(s2); err == nil {
		t.Fatal("expected error for network mismatch, got nil")
	}
	if len(tr.Points) != 1 {
		t.Errorf("bad snapshot should not be appended; got %d points", len(tr.Points))
	}
}

func TestTrend_MethodMismatch(t *testing.T) {
	s1 := makeSummary("testnet", "retention-window", "1", "2026-09-01T00:00:00Z", 5000000, 0, 100, 0, 200, nil)
	s2 := makeSummary("testnet", "full-census", "1", "2026-10-01T00:00:00Z", 5097816, 0, 100, 0, 200, nil)
	var tr Trend
	if err := tr.AddPoint(s1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := tr.AddPoint(s2); err == nil {
		t.Fatal("expected error for method mismatch, got nil")
	}
	if len(tr.Points) != 1 {
		t.Errorf("incompatible snapshot should not be appended; got %d points", len(tr.Points))
	}
}

func TestRenderTrend_Empty(t *testing.T) {
	if got := RenderTrend(Trend{}); got != "" {
		t.Errorf("empty trend should render empty string, got %q", got)
	}
}

func TestRenderTrend_Deterministic(t *testing.T) {
	s1 := makeSummary("testnet", "retention-window", "1", "2026-09-01T00:00:00Z", 5000000, 2, 100, 5, 200, []string{"sep41-v0.5.0"})
	s2 := makeSummary("testnet", "retention-window", "1", "2026-10-01T00:00:00Z", 5097816, 3, 107, 7, 183, []string{"sep41-v0.5.2"})

	build := func() string {
		var tr Trend
		_ = tr.AddPoint(s1)
		_ = tr.AddPoint(s2)
		return RenderTrend(tr)
	}

	first := build()
	second := build()
	if first != second {
		t.Error("RenderTrend is not deterministic")
	}

	// Spot-check the rendered content.
	if !strings.Contains(first, "## Trend (Testnet)") {
		t.Error("missing network heading")
	}
	if !strings.Contains(first, "5097816") {
		t.Error("missing latest ledger in output")
	}
	if !strings.Contains(first, "sep41-v0.5.2") {
		t.Error("missing ruleset version in output")
	}
	if !strings.Contains(first, "2026-09-01T00:00:00Z") {
		t.Error("missing first run timestamp")
	}
}

func TestRenderTrend_MethodLabelVisible(t *testing.T) {
	s := makeSummary("testnet", "retention-window", "1", "2026-10-01T00:00:00Z", 5097816, 0, 107, 0, 183, []string{"sep41-v0.5.2"})
	var tr Trend
	_ = tr.AddPoint(s)
	out := RenderTrend(tr)
	if !strings.Contains(out, "retention-window") {
		t.Error("method column should appear in trend output")
	}
}
