// Package match checks a contract's function signatures against a SEP's
// rule file. It produces the "inferred" tier: what the interface matches,
// which is evidence about shape only, never a claim and never proof of
// behavior (CLAUDE.md §5.6, §5.7).
package match

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
)

// ErrInvalidRules means a rule file failed validation.
var ErrInvalidRules = errors.New("invalid rule file")

// RuleFile is one SEP's interface rules (rules/sep-NNNN.json).
type RuleFile struct {
	SEP            int      `json:"sep"`
	RulesetVersion string   `json:"ruleset_version"`
	Source         string   `json:"source"`
	Required       []FnRule `json:"required"`
	Optional       []FnRule `json:"optional"`
}

// FnRule describes one function. Inputs has one element per parameter, each
// listing the types accepted at that position; Output is empty for a
// function that returns nothing.
type FnRule struct {
	Fn     string     `json:"fn"`
	Inputs [][]string `json:"inputs"`
	Output []string   `json:"output"`
}

// These mirror rules/schema.json, which CI checks with a JSON Schema
// validator. Keep the two in step.
var (
	fnNameRe   = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)
	typeRe     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_:<>,()]*$`)
	rulesetRe  = regexp.MustCompile(`^sep[0-9]{1,6}-v[0-9]+\.[0-9]+\.[0-9]+(-r[0-9]+)?$`)
	ruleFileRe = regexp.MustCompile(`^sep-[0-9]{4,6}\.json$`)
)

// ParseRuleFile decodes and validates one rule file. Unknown fields are
// rejected so a typo cannot silently drop a rule.
func ParseRuleFile(b []byte) (RuleFile, error) {
	var r RuleFile
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return RuleFile{}, fmt.Errorf("%w: %w", ErrInvalidRules, err)
	}
	if dec.More() {
		return RuleFile{}, fmt.Errorf("%w: trailing data", ErrInvalidRules)
	}
	if err := r.validate(); err != nil {
		return RuleFile{}, fmt.Errorf("%w: sep %d: %w", ErrInvalidRules, r.SEP, err)
	}
	return r, nil
}

func (r RuleFile) validate() error {
	switch {
	case r.SEP < 1 || r.SEP > 999999:
		return fmt.Errorf("sep %d out of range", r.SEP)
	case !rulesetRe.MatchString(r.RulesetVersion):
		return fmt.Errorf("ruleset_version %q does not match %s", r.RulesetVersion, rulesetRe)
	case len(r.Source) < 20:
		return errors.New("source must say where the rules came from")
	case len(r.Required) == 0:
		return errors.New("required is empty")
	case r.Optional == nil:
		return errors.New("optional must be present (use [])")
	}
	seen := map[string]bool{}
	for _, f := range append(append([]FnRule{}, r.Required...), r.Optional...) {
		if !fnNameRe.MatchString(f.Fn) {
			return fmt.Errorf("function name %q invalid", f.Fn)
		}
		if seen[f.Fn] {
			return fmt.Errorf("function %q listed twice", f.Fn)
		}
		seen[f.Fn] = true
		if f.Inputs == nil || f.Output == nil {
			return fmt.Errorf("%s: inputs and output must be present", f.Fn)
		}
		if len(f.Output) > 1 {
			return fmt.Errorf("%s: at most one output type", f.Fn)
		}
		for i, alts := range f.Inputs {
			if len(alts) == 0 {
				return fmt.Errorf("%s: input %d accepts no type", f.Fn, i)
			}
			dup := map[string]bool{}
			for _, t := range alts {
				if !typeRe.MatchString(t) || dup[t] {
					return fmt.Errorf("%s: input %d type %q invalid or repeated", f.Fn, i, t)
				}
				dup[t] = true
			}
		}
		for _, t := range f.Output {
			if !typeRe.MatchString(t) {
				return fmt.Errorf("%s: output type %q invalid", f.Fn, t)
			}
		}
	}
	return nil
}

// LoadRules reads every sep-NNNN.json in fsys, validates each, and returns
// them sorted by SEP. Two files for the same SEP are an error.
func LoadRules(fsys fs.FS) ([]RuleFile, error) {
	names, err := fs.Glob(fsys, "sep-*.json")
	if err != nil {
		return nil, err
	}
	var out []RuleFile
	bySEP := map[int]string{}
	for _, n := range names {
		if !ruleFileRe.MatchString(path.Base(n)) {
			return nil, fmt.Errorf("%w: unexpected file name %q", ErrInvalidRules, n)
		}
		b, err := fs.ReadFile(fsys, n)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", n, err)
		}
		r, err := ParseRuleFile(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}
		if want := fmt.Sprintf("sep-%04d.json", r.SEP); path.Base(n) != want {
			return nil, fmt.Errorf("%w: %s holds sep %d; name it %s", ErrInvalidRules, n, r.SEP, want)
		}
		if prev, dup := bySEP[r.SEP]; dup {
			return nil, fmt.Errorf("%w: sep %d in both %s and %s", ErrInvalidRules, r.SEP, prev, n)
		}
		bySEP[r.SEP] = n
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SEP < out[j].SEP })
	return out, nil
}
