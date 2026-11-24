package match

import (
	"fmt"
	"sort"

	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// Status is the outcome of matching one Wasm against one rule file. The
// values are stored and returned by the API; do not rename them.
type Status string

// Statuses (CLAUDE.md §5.7).
const (
	StatusMatch    Status = "match"    // every required function present with accepted types
	StatusPartial  Status = "partial"  // at least PartialThreshold of required functions OK
	StatusMismatch Status = "mismatch" // fewer than PartialThreshold OK
	StatusNoSpec   Status = "no_spec"  // the Wasm has no contractspecv0 section
)

// DefaultPartialThreshold is match.partial_threshold's default.
const DefaultPartialThreshold = 0.5

// Signature is a function's types, as expected by a rule or found in a spec.
type Signature struct {
	Inputs  []string `json:"inputs"`
	Outputs []string `json:"outputs"`
}

// Mismatch is a function present under the right name with the wrong types.
type Mismatch struct {
	Fn       string    `json:"fn"`
	Reason   string    `json:"reason"` // "types" or "duplicate"
	Optional bool      `json:"optional,omitempty"`
	Expected Signature `json:"expected"` // first accepted type at each position
	Actual   Signature `json:"actual"`
}

type Coverage struct {
	RequiredOK int `json:"required_ok"`
	OptionalOK int `json:"optional_ok"`
	Extra      int `json:"extra"`
}

// Result is the outcome of matching one Wasm against one rule file.
type Result struct {
	SEP             int               `json:"sep"`
	RulesetVersion  string            `json:"ruleset_version"`
	Status          Status            `json:"status"`
	OKCount         int               `json:"ok_count"`
	RequiredCount   int               `json:"required_count"`
	Missing         []string          `json:"missing"`
	Mismatched      []Mismatch        `json:"mismatched"`
	MatchedVariants map[string]string `json:"matched_variants"`
	Extra           []string          `json:"extra"`
	Coverage        Coverage          `json:"coverage"`
}

// Matcher holds the tunable cutoff between partial and mismatch.
type Matcher struct {
	// PartialThreshold is the fraction of required functions that must be
	// OK for "partial" rather than "mismatch". Zero means the default, 0.5.
	PartialThreshold float64
	// Rulesets maps SEP numbers to their RuleFiles, used when evaluating
	// requires dependencies. May be nil when not available.
	Rulesets map[int]RuleFile
}

// Match compares a spec's functions with a rule file using the default
// threshold. See Matcher.Match.
func Match(fns []sepmeta.FnSig, r RuleFile) Result {
	return Matcher{Rulesets: map[int]RuleFile{}}.Match(fns, r)
}

// NoSpec is the result for a Wasm that has no spec section at all.
func NoSpec(r RuleFile) Result {
	return Result{
		SEP:             r.SEP,
		RulesetVersion:  r.RulesetVersion,
		Status:          StatusNoSpec,
		RequiredCount:   len(r.Required),
		Missing:         []string{},
		Mismatched:      []Mismatch{},
		MatchedVariants: map[string]string{},
		Extra:           []string{},
		Coverage:        Coverage{},
	}
}

// Match compares types and their order, never parameter names: a spec
// records names, but a SEP's interface is defined by types.
//
// A required function is OK when exactly one function of that name exists
// and each input position holds one of the accepted types, with the
// expected outputs. A name that appears more than once in the spec is
// counted as mismatched, since it is ambiguous which one is called. Extra
// functions not in the rule file are ignored. Optional functions never
// affect the status, but a present optional with wrong types is listed.
//
// If the Matcher has Rulesets set and the rule file has a non-empty Requires
// list, each required SEP's required functions are also matched against the
// Wasm spec, and the composite status reflects whether the SEP and all required
// SEPs match.
func (m Matcher) Match(fns []sepmeta.FnSig, r RuleFile) Result {
	threshold := m.PartialThreshold
	if threshold <= 0 {
		threshold = DefaultPartialThreshold
	}
	byName := map[string][]sepmeta.FnSig{}
	for _, f := range fns {
		byName[f.Name] = append(byName[f.Name], f)
	}
	// Total number of required functions: own required + required SEPs' required.
	totalRequired := len(r.Required) + len(r.Requires)
	res := Result{
		SEP:             r.SEP,
		RulesetVersion:  r.RulesetVersion,
		RequiredCount:   totalRequired,
		Missing:         []string{},
		Mismatched:      []Mismatch{},
		MatchedVariants: map[string]string{},
		Extra:           []string{},
	}

	inRules := map[string]bool{}
	for _, rule := range r.Required {
		inRules[rule.Fn] = true
	}
	for _, rule := range r.Optional {
		inRules[rule.Fn] = true
	}
	if len(r.Requires) > 0 && m.Rulesets != nil {
		for _, requiredSep := range r.Requires {
			if reqRule, ok := m.Rulesets[requiredSep]; ok {
				for _, rule := range reqRule.Required {
					inRules[rule.Fn] = true
				}
				for _, rule := range reqRule.Optional {
					inRules[rule.Fn] = true
				}
			}
		}
	}

	optionalOK := 0
	// check reports whether a function of the given name matches the rule,
	// recording missing/mismatched evidence as side effects.
	check := func(rule FnRule, optional bool) bool {
		got := byName[rule.Fn]
		switch {
		case len(got) == 0:
			if !optional {
				res.Missing = append(res.Missing, rule.Fn)
			}
			return false
		case len(got) > 1:
			res.Mismatched = append(res.Mismatched, mismatch(rule, got[0], "duplicate", optional))
			return false
		}
		variants, ok := fits(rule, got[0])
		if !ok {
			res.Mismatched = append(res.Mismatched, mismatch(rule, got[0], "types", optional))
			return false
		}
		for k, v := range variants {
			res.MatchedVariants[k] = v
		}
		return true
	}

	// Match the SEP's own required functions (as before).
	for _, rule := range r.Required {
		if check(rule, false) {
			res.OKCount++
		}
	}
	// Match optional functions from the rule file (as before).
	for _, rule := range r.Optional {
		if check(rule, true) {
			optionalOK++
		}
	}

	// Match required SEPs from the requires list, if Rulesets are available.
	if len(r.Requires) > 0 && m.Rulesets != nil {
		for _, requiredSep := range r.Requires {
			reqRule, ok := m.Rulesets[requiredSep]
			if !ok {
				// Rule file for required SEP not found; record as missing.
				res.Missing = append(res.Missing, fmt.Sprintf("sep-%d", requiredSep))
				continue
			}
			// Check each required function of the required SEP against the Wasm spec.
			for _, rule := range reqRule.Required {
				if check(rule, false) {
					res.OKCount++
				}
			}
		}
	}

	seenExtra := map[string]bool{}
	for _, f := range fns {
		if !inRules[f.Name] && !seenExtra[f.Name] {
			res.Extra = append(res.Extra, f.Name)
			seenExtra[f.Name] = true
		}
	}

	res.Coverage = Coverage{
		RequiredOK: res.OKCount,
		OptionalOK: optionalOK,
		Extra:      len(res.Extra),
	}

	sort.Strings(res.Missing)
	sort.Slice(res.Mismatched, func(i, j int) bool { return res.Mismatched[i].Fn < res.Mismatched[j].Fn })

	switch {
	case res.OKCount == res.RequiredCount:
		res.Status = StatusMatch
	case float64(res.OKCount) >= threshold*float64(res.RequiredCount):
		res.Status = StatusPartial
	default:
		res.Status = StatusMismatch
	}
	return res
}

func fits(rule FnRule, sig sepmeta.FnSig) (map[string]string, bool) {
	if len(sig.Inputs) != len(rule.Inputs) || !equal(sig.Outputs, rule.Output) {
		return nil, false
	}
	variants := map[string]string{}
	for i, alts := range rule.Inputs {
		found := false
		for _, a := range alts {
			if sig.Inputs[i] == a {
				found = true
				break
			}
		}
		if !found {
			return nil, false
		}
		if len(alts) > 1 {
			variants[fmt.Sprintf("%s.inputs[%d]", rule.Fn, i)] = sig.Inputs[i]
		}
	}
	return variants, true
}

func mismatch(rule FnRule, got sepmeta.FnSig, reason string, optional bool) Mismatch {
	exp := Signature{Inputs: []string{}, Outputs: append([]string{}, rule.Output...)}
	for _, alts := range rule.Inputs {
		exp.Inputs = append(exp.Inputs, alts[0])
	}
	return Mismatch{
		Fn: rule.Fn, Reason: reason, Optional: optional, Expected: exp,
		Actual: Signature{Inputs: nonNil(got.Inputs), Outputs: nonNil(got.Outputs)},
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
