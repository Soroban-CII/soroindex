package match

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"

	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// historyFileRe names a historical rule file: history/sep-0041-v0.3.0.json.
var historyFileRe = regexp.MustCompile(`^sep-([0-9]{4,6})-v([0-9]+)\.([0-9]+)\.([0-9]+)\.json$`)

// rulesetVersionRe splits a ruleset_version into its SEP version.
var rulesetVersionRe = regexp.MustCompile(`^sep[0-9]{1,6}-v([0-9]+)\.([0-9]+)\.([0-9]+)(?:-r[0-9]+)?$`)

// Versions lists, per SEP, the current rule file and every historical
// version shipped in rules/history, newest first. It answers "which
// version of this SEP does the interface implement", which is reported
// next to, and never changes, the stored match status.
type Versions struct {
	bySEP map[int][]RuleFile
}

// LoadVersions reads history/sep-NNNN-vX.Y.Z.json from fsys and combines
// it with the current rule files. A historical file must describe a SEP
// that has a current file, name exactly one type per input position (each
// version's text names one type), carry a ruleset_version equal to its file
// name's version, and be older than the current file.
func LoadVersions(fsys fs.FS, current []RuleFile) (Versions, error) {
	v := Versions{bySEP: map[int][]RuleFile{}}
	cur := map[int]RuleFile{}
	for _, r := range current {
		cur[r.SEP] = r
		v.bySEP[r.SEP] = []RuleFile{r}
	}
	names, err := fs.Glob(fsys, "history/*.json")
	if err != nil {
		return Versions{}, err
	}
	for _, n := range names {
		m := historyFileRe.FindStringSubmatch(path.Base(n))
		if m == nil {
			return Versions{}, fmt.Errorf("%w: unexpected history file name %q", ErrInvalidRules, n)
		}
		b, err := fs.ReadFile(fsys, n)
		if err != nil {
			return Versions{}, fmt.Errorf("read %s: %w", n, err)
		}
		r, err := ParseRuleFile(b)
		if err != nil {
			return Versions{}, fmt.Errorf("%s: %w", n, err)
		}
		sep, _ := strconv.Atoi(m[1]) // the regexp admits digits only
		want := fmt.Sprintf("sep%d-v%s.%s.%s", sep, m[2], m[3], m[4])
		c, ok := cur[sep]
		switch {
		case r.SEP != sep:
			return Versions{}, fmt.Errorf("%w: %s holds sep %d", ErrInvalidRules, n, r.SEP)
		case r.RulesetVersion != want:
			return Versions{}, fmt.Errorf("%w: %s has ruleset_version %q, want %q", ErrInvalidRules, n, r.RulesetVersion, want)
		case !ok:
			return Versions{}, fmt.Errorf("%w: %s has no current rule file sep-%04d.json", ErrInvalidRules, n, sep)
		case len(r.Requires) > 0:
			return Versions{}, fmt.Errorf("%w: %s: historical versions describe one interface; requires is not allowed", ErrInvalidRules, n)
		case compareVersions(r.RulesetVersion, c.RulesetVersion) >= 0:
			return Versions{}, fmt.Errorf("%w: %s is not older than the current %s", ErrInvalidRules, n, c.RulesetVersion)
		}
		for _, f := range append(append([]FnRule{}, r.Required...), r.Optional...) {
			for i, alts := range f.Inputs {
				if len(alts) != 1 {
					return Versions{}, fmt.Errorf("%w: %s: %s input %d lists %d types; a historical version names exactly one", ErrInvalidRules, n, f.Fn, i, len(alts))
				}
			}
		}
		for _, have := range v.bySEP[sep] {
			if have.RulesetVersion == r.RulesetVersion {
				return Versions{}, fmt.Errorf("%w: %s listed twice", ErrInvalidRules, r.RulesetVersion)
			}
		}
		v.bySEP[sep] = append(v.bySEP[sep], r)
	}
	for sep := range v.bySEP {
		rs := v.bySEP[sep]
		sort.SliceStable(rs, func(i, j int) bool { return compareVersions(rs[i].RulesetVersion, rs[j].RulesetVersion) > 0 })
	}
	return v, nil
}

// Highest returns the ruleset_version of the newest version of sep that
// fns implement, or "" when none does (or sep has no rule file).
//
// Each version is checked strictly: only the first type listed at each
// input position counts, because that is the type that version's text
// names. Later types in the current file are accepted for compatibility
// with earlier versions, so they make the current status "match" without
// making the Wasm an implementation of the current version. With
// transfer.to: Address, a token matches the current SEP-41 rule file but
// implements v0.3.0, the last version before MuxedAddress.
func (v Versions) Highest(sep int, fns []sepmeta.FnSig) string {
	for _, r := range v.bySEP[sep] {
		if (Matcher{}).Match(fns, strict(r)).Status == StatusMatch {
			return r.RulesetVersion
		}
	}
	return ""
}

// Known returns the versions shipped for sep, newest first.
func (v Versions) Known(sep int) []string {
	var out []string
	for _, r := range v.bySEP[sep] {
		out = append(out, r.RulesetVersion)
	}
	return out
}

// strict keeps only the first accepted type at each input position and
// drops requires, so a version check is about this SEP's own interface.
func strict(r RuleFile) RuleFile {
	narrow := func(fs []FnRule) []FnRule {
		out := make([]FnRule, len(fs))
		for i, f := range fs {
			in := make([][]string, len(f.Inputs))
			for j, alts := range f.Inputs {
				in[j] = alts[:1]
			}
			out[i] = FnRule{Fn: f.Fn, Inputs: in, Output: f.Output}
		}
		return out
	}
	r.Required, r.Optional, r.Requires = narrow(r.Required), narrow(r.Optional), nil
	return r
}

// compareVersions orders two ruleset_versions by SEP version (major,
// minor, patch); unparsable versions sort first.
func compareVersions(a, b string) int {
	pa, pb := parseVersion(a), parseVersion(b)
	for i := range pa {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func parseVersion(s string) [3]int {
	m := rulesetVersionRe.FindStringSubmatch(s)
	if m == nil {
		return [3]int{-1, -1, -1}
	}
	var out [3]int
	for i := range out {
		out[i], _ = strconv.Atoi(m[i+1]) // digits only, by the regexp
	}
	return out
}
