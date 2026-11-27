package match

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

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
}

// Matcher holds the tunable cutoff between partial and mismatch.
type Matcher struct {
	PartialThreshold float64
	Rulesets         map[int]RuleFile
	TypeTable        sepmeta.TypeTable
}

func Match(fns []sepmeta.FnSig, r RuleFile) Result {
	return Matcher{Rulesets: map[int]RuleFile{}}.Match(fns, r)
}

func MatchWithTypes(fns []sepmeta.FnSig, types sepmeta.TypeTable, r RuleFile) Result {
	return Matcher{Rulesets: map[int]RuleFile{}, TypeTable: types}.Match(fns, r)
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
	}

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
			res.Mismatched = append(res.Mismatched, m.mismatch(rule, got[0], "duplicate", optional, r))
			return false
		}
		variants, ok := m.fits(rule, got[0], r)
		if !ok {
			res.Mismatched = append(res.Mismatched, m.mismatch(rule, got[0], "types", optional, r))
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
		check(rule, true)
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

var udtPattern = regexp.MustCompile(`udt:([A-Za-z0-9_]+)`)

func (m Matcher) resolveSpecUDT(name string, visited map[string]bool) string {
	if visited[name] {
		return "cycle:" + name
	}
	visited[name] = true
	defer delete(visited, name)

	if m.TypeTable == nil {
		return "udt:" + name
	}
	def, ok := m.TypeTable[name]
	if !ok {
		return "udt:" + name
	}
	switch def.Kind {
	case sepmeta.UDTKindStruct:
		parts := make([]string, len(def.Fields))
		for i, f := range def.Fields {
			parts[i] = m.expandType(f.Type, visited)
		}
		return "struct(" + strings.Join(parts, ",") + ")"
	case sepmeta.UDTKindUnion:
		parts := make([]string, len(def.Cases))
		for i, c := range def.Cases {
			caseParts := make([]string, len(c.Types))
			for j, t := range c.Types {
				caseParts[j] = m.expandType(t, visited)
			}
			parts[i] = "tuple(" + strings.Join(caseParts, ",") + ")"
		}
		return "union(" + strings.Join(parts, ",") + ")"
	case sepmeta.UDTKindEnum:
		return "enum(" + name + ")"
	case sepmeta.UDTKindErrorEnum:
		return "error_enum(" + name + ")"
	default:
		return "udt:" + name
	}
}

func (m Matcher) expandType(t string, visited map[string]bool) string {
	return udtPattern.ReplaceAllStringFunc(t, func(match string) string {
		name := strings.TrimPrefix(match, "udt:")
		return m.resolveSpecUDT(name, visited)
	})
}

func resolveRuleUDT(name string, r RuleFile, visited map[string]bool) string {
	if visited[name] {
		return "cycle:" + name
	}
	visited[name] = true
	defer delete(visited, name)

	if r.Types == nil {
		return "udt:" + name
	}
	def, ok := r.Types[name]
	if !ok {
		return "udt:" + name
	}
	switch def.Kind {
	case "struct":
		parts := make([]string, len(def.Fields))
		for i, alts := range def.Fields {
			parts[i] = expandRuleType(alts[0], r, visited)
		}
		return "struct(" + strings.Join(parts, ",") + ")"
	case "union":
		parts := make([]string, len(def.Cases))
		for i, alts := range def.Cases {
			caseParts := make([]string, len(alts))
			for j, t := range alts {
				caseParts[j] = expandRuleType(t, r, visited)
			}
			parts[i] = "tuple(" + strings.Join(caseParts, ",") + ")"
		}
		return "union(" + strings.Join(parts, ",") + ")"
	case "enum":
		return "enum(" + name + ")"
	case "error_enum":
		return "error_enum(" + name + ")"
	default:
		return "udt:" + name
	}
}

func expandRuleType(t string, r RuleFile, visited map[string]bool) string {
	return udtPattern.ReplaceAllStringFunc(t, func(match string) string {
		name := strings.TrimPrefix(match, "udt:")
		return resolveRuleUDT(name, r, visited)
	})
}

func (m Matcher) matchType(ruleType string, specType string, r RuleFile) bool {
	ruleExpanded := expandRuleType(ruleType, r, map[string]bool{})
	specExpanded := m.expandType(specType, map[string]bool{})
	if ruleExpanded != ruleType || specExpanded != specType {
		return ruleExpanded == specExpanded
	}
	return ruleType == specType
}

func (m Matcher) fits(rule FnRule, sig sepmeta.FnSig, r RuleFile) (map[string]string, bool) {
	if len(sig.Inputs) != len(rule.Inputs) {
		return nil, false
	}
	if len(sig.Outputs) != len(rule.Output) {
		return nil, false
	}
	for i := range sig.Outputs {
		if !m.matchType(rule.Output[i], sig.Outputs[i], r) {
			return nil, false
		}
	}
	variants := map[string]string{}
	for i, alts := range rule.Inputs {
		found := false
		for _, a := range alts {
			if m.matchType(a, sig.Inputs[i], r) {
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

func (m Matcher) mismatch(rule FnRule, got sepmeta.FnSig, reason string, optional bool, r RuleFile) Mismatch {
	exp := Signature{Inputs: []string{}, Outputs: []string{}}
	for _, out := range rule.Output {
		expanded := expandRuleType(out, r, map[string]bool{})
		exp.Outputs = append(exp.Outputs, expanded)
	}
	for _, alts := range rule.Inputs {
		expanded := expandRuleType(alts[0], r, map[string]bool{})
		exp.Inputs = append(exp.Inputs, expanded)
	}

	act := Signature{Inputs: []string{}, Outputs: []string{}}
	for _, in := range got.Inputs {
		act.Inputs = append(act.Inputs, m.expandType(in, map[string]bool{}))
	}
	for _, out := range got.Outputs {
		act.Outputs = append(act.Outputs, m.expandType(out, map[string]bool{}))
	}

	return Mismatch{
		Fn: rule.Fn, Reason: reason, Optional: optional, Expected: exp,
		Actual: act,
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
