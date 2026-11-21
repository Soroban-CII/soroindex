package store

import (
	"context"
	"testing"
)

// TestDisagreementFilter is issue #40.
// It verifies the four status values, combined AND/OR filtering via the
// statusSet, SAC exclusion, and keyset pagination.
func TestDisagreementFilter(t *testing.T) {
	ctx := context.Background()
	fix := catalogFixture{Hashes: 100, Contracts: 100}
	ro, _, _ := openFixture(t, fix, "")

	rulesets := map[int]string{41: fixtureRuleset}

	t.Run("empty statuses returns all disagreement rows", func(t *testing.T) {
		rows, err := ro.DisagreementFilter(ctx, nil, rulesets, 500, "", 0)
		if err != nil {
			t.Fatalf("DisagreementFilter: %v", err)
		}
		// Fixture contracts declare SEP 41 for some hashes; those with
		// partial/mismatch/no_spec match status should appear.
		for _, r := range rows {
			switch r.Status {
			case DisagreementPartial, DisagreementMismatch, DisagreementNoSpec, DisagreementAbsentRule:
			default:
				t.Errorf("unexpected status %q for contract %s sep %d", r.Status, r.ContractID, r.SEP)
			}
		}
	})

	t.Run("partial filter returns only partial rows", func(t *testing.T) {
		rows, err := ro.DisagreementFilter(ctx, []DisagreementStatus{DisagreementPartial}, rulesets, 500, "", 0)
		if err != nil {
			t.Fatalf("DisagreementFilter partial: %v", err)
		}
		for _, r := range rows {
			if r.Status != DisagreementPartial {
				t.Errorf("got status %q, want %q", r.Status, DisagreementPartial)
			}
		}
	})

	t.Run("mismatch filter returns only mismatch rows", func(t *testing.T) {
		rows, err := ro.DisagreementFilter(ctx, []DisagreementStatus{DisagreementMismatch}, rulesets, 500, "", 0)
		if err != nil {
			t.Fatalf("DisagreementFilter mismatch: %v", err)
		}
		for _, r := range rows {
			if r.Status != DisagreementMismatch {
				t.Errorf("got status %q, want %q", r.Status, DisagreementMismatch)
			}
		}
	})

	t.Run("no_spec with no matching ruleset is absent-rule", func(t *testing.T) {
		// Using an empty rulesets map means no rules are loaded, so every
		// no_spec row should be classified as absent-rule.
		rows, err := ro.DisagreementFilter(ctx, []DisagreementStatus{DisagreementAbsentRule}, map[int]string{}, 500, "", 0)
		if err != nil {
			t.Fatalf("DisagreementFilter absent-rule: %v", err)
		}
		for _, r := range rows {
			if r.Status != DisagreementAbsentRule {
				t.Errorf("got status %q, want absent-rule", r.Status)
			}
		}
	})

	t.Run("OR combination: partial and mismatch together", func(t *testing.T) {
		want := []DisagreementStatus{DisagreementPartial, DisagreementMismatch}
		rows, err := ro.DisagreementFilter(ctx, want, rulesets, 500, "", 0)
		if err != nil {
			t.Fatalf("DisagreementFilter OR: %v", err)
		}
		for _, r := range rows {
			if r.Status != DisagreementPartial && r.Status != DisagreementMismatch {
				t.Errorf("got status %q outside requested set", r.Status)
			}
		}
	})

	t.Run("SAC contracts never appear", func(t *testing.T) {
		rows, err := ro.DisagreementFilter(ctx, nil, rulesets, 500, "", 0)
		if err != nil {
			t.Fatalf("DisagreementFilter SAC: %v", err)
		}
		for _, r := range rows {
			var kind string
			if err := ro.DB.QueryRowContext(ctx, `SELECT kind FROM contracts WHERE contract_id = ?`, r.ContractID).Scan(&kind); err != nil {
				t.Fatalf("kind lookup: %v", err)
			}
			if kind == "sac" {
				t.Errorf("SAC %s appeared in disagreement results", r.ContractID)
			}
		}
	})

	t.Run("keyset pagination returns consistent results", func(t *testing.T) {
		paginate := func(pageSize int) []DisagreementRow {
			t.Helper()
			var all []DisagreementRow
			afterID := ""
			afterSEP := 0
			for {
				page, err := ro.DisagreementFilter(ctx, nil, rulesets, pageSize, afterID, afterSEP)
				if err != nil {
					t.Fatalf("page (size=%d): %v", pageSize, err)
				}
				all = append(all, page...)
				if len(page) < pageSize {
					break
				}
				last := page[len(page)-1]
				afterID = last.ContractID
				afterSEP = last.SEP
			}
			return all
		}

		small := paginate(5)
		large := paginate(100)

		if len(small) != len(large) {
			t.Fatalf("small-page count = %d, large-page count = %d", len(small), len(large))
		}
		for i := range large {
			if small[i].ContractID != large[i].ContractID || small[i].SEP != large[i].SEP {
				t.Errorf("row %d: small (%s,%d) != large (%s,%d)", i, small[i].ContractID, small[i].SEP, large[i].ContractID, large[i].SEP)
			}
		}
	})

	t.Run("invalid status returns ErrBadQuery", func(t *testing.T) {
		_, err := ro.DisagreementFilter(ctx, []DisagreementStatus{"unknown"}, rulesets, 50, "", 0)
		if err != ErrBadQuery {
			t.Errorf("want ErrBadQuery, got %v", err)
		}
	})

	t.Run("limit over 500 returns ErrBadQuery", func(t *testing.T) {
		_, err := ro.DisagreementFilter(ctx, nil, rulesets, 501, "", 0)
		if err != ErrBadQuery {
			t.Errorf("want ErrBadQuery, got %v", err)
		}
	})
}
