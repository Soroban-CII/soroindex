package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

func init() {
	commands["diff"] = command{
		summary: "compare two stored Wasm: metadata, declarations, functions and inferred verdicts",
		run:     runDiff,
	}
}

// metaKV mirrors the rows the indexer writes into wasm.meta_json. The diff
// decodes them so it can compare keys, not whole JSON documents.
type metaKV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// diffSide records which side of the comparison a hash is and what the index
// knows about its interface. needs_refetch is reported rather than inferred
// away: a Wasm with a spec but no stored signatures (analyzed before
// migration 0002) must not be read as declaring no functions.
type diffSide struct {
	Hash            string `json:"wasm_hash"`
	ParseStatus     string `json:"parse_status"`
	ParserVersion   string `json:"parser_version"`
	HasSpec         bool   `json:"has_spec"`
	FunctionsStored bool   `json:"functions_stored"`
	NeedsRefetch    bool   `json:"needs_refetch"`
}

// keyedChange is one key whose values differ. From is empty when the key is
// only on the second side and To is empty when it is only on the first. A
// key can repeat in a contractmetav0 section, so both are lists.
type keyedChange struct {
	Key  string   `json:"key"`
	From []string `json:"from,omitempty"`
	To   []string `json:"to,omitempty"`
}

// keyedDiff groups keyed changes so output never interleaves an addition
// with a removal, which reads as a change when it is not one.
type keyedDiff struct {
	Added   []keyedChange `json:"added"`
	Removed []keyedChange `json:"removed"`
	Changed []keyedChange `json:"changed"`
}

func (d keyedDiff) empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// declaredClaim is one side of a declaration. Anomaly is "" when the token
// parsed cleanly, so JSON readers see the same spelling as the CLI's
// anomaly=none.
type declaredClaim struct {
	RawToken string `json:"raw_token"`
	Anomaly  string `json:"anomaly"`
}

// declaredChange is a declaration that appeared, disappeared or changed its
// token. It belongs to the declared tier only: nothing here is evidence that
// the code implements the SEP.
type declaredChange struct {
	SEP    int            `json:"sep"`
	Source string         `json:"source"`
	From   *declaredClaim `json:"from"`
	To     *declaredClaim `json:"to"`
}

// declaredDiff groups declaration changes the way keyedDiff groups keys.
type declaredDiff struct {
	Added   []declaredChange `json:"added"`
	Removed []declaredChange `json:"removed"`
	Changed []declaredChange `json:"changed"`
}

func (d declaredDiff) empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// functionChange is one function name whose signatures differ. A name can be
// defined more than once in a spec, so each side is a list of signatures.
type functionChange struct {
	Name string   `json:"name"`
	From []string `json:"from,omitempty"`
	To   []string `json:"to,omitempty"`
}

// functionDiff reports added, removed and changed functions. Comparable is
// false when either side's signatures are not stored; the lists are then
// empty, because an absent record is not an absent function.
type functionDiff struct {
	Comparable bool             `json:"comparable"`
	Note       string           `json:"note,omitempty"`
	Added      []functionChange `json:"added"`
	Removed    []functionChange `json:"removed"`
	Changed    []functionChange `json:"changed"`
}

func (d functionDiff) empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// verdictSide is an interface match under one ruleset. It is inferred-tier
// evidence; it is never a claim and never a verification.
type verdictSide struct {
	Status         string  `json:"status"`
	RulesetVersion string  `json:"ruleset_version"`
	OKCount        int     `json:"ok_count"`
	MatchedVersion *string `json:"matched_version"`
}

// verdictChange carries the inferred verdict for one SEP on both sides. Every
// SEP matched on either side is listed, with Changed saying whether the
// upgrade moved it, so a reader sees the unchanged verdicts too.
type verdictChange struct {
	SEP     int          `json:"sep"`
	Changed bool         `json:"changed"`
	From    *verdictSide `json:"from"`
	To      *verdictSide `json:"to"`
}

// diffOutput is the --json shape of `sep47idx diff`. Every list is sorted, so
// two runs over the same database produce byte-identical output.
type diffOutput struct {
	Network    string          `json:"network"`
	AsOfLedger uint32          `json:"as_of_ledger"`
	From       diffSide        `json:"from"`
	To         diffSide        `json:"to"`
	Identical  bool            `json:"identical"`
	Metadata   keyedDiff       `json:"metadata"`
	Declared   declaredDiff    `json:"declared"`
	Anomalies  keyedDiff       `json:"anomalies"`
	Functions  functionDiff    `json:"functions"`
	Verdicts   []verdictChange `json:"inferred_verdicts"`
}

// diffInput is everything one side contributes: the stored evidence the
// `wasm` command shows, plus the function record behind it.
type diffInput struct {
	detail store.WasmDetail
	fns    store.WasmFunctions
}

func (in diffInput) side() diffSide {
	return diffSide{
		Hash: in.detail.Hash, ParseStatus: in.detail.ParseStatus, ParserVersion: in.detail.ParserVersion,
		HasSpec: in.fns.HasSpec, FunctionsStored: in.fns.Stored,
		NeedsRefetch: in.fns.HasSpec && !in.fns.Stored,
	}
}

// fnSignature renders a stored signature the way the `wasm` command renders a
// matcher signature, so the two outputs can be read side by side.
func fnSignature(f sepmeta.FnSig) string {
	return signature(match.Signature{Inputs: f.Inputs, Outputs: f.Outputs})
}

// metaValues groups a meta section's values by key. Repeated keys are normal
// (SEP-47 expects several `sep` entries), so values are collected and sorted
// rather than overwritten.
func metaValues(raw json.RawMessage, hash string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(raw) == 0 || string(raw) == "null" {
		return out, nil
	}
	var entries []metaKV
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("stored metadata of %s: %w", hash, err)
	}
	for _, e := range entries {
		out[e.Key] = append(out[e.Key], e.Value)
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out, nil
}

// diffKeyed compares two key-to-values maps. Keys are emitted in sorted
// order, which is what makes --json reproducible.
func diffKeyed(from, to map[string][]string) keyedDiff {
	d := keyedDiff{Added: []keyedChange{}, Removed: []keyedChange{}, Changed: []keyedChange{}}
	for _, k := range sortedKeys(from, to) {
		a, inA := from[k]
		b, inB := to[k]
		switch {
		case !inA:
			d.Added = append(d.Added, keyedChange{Key: k, To: b})
		case !inB:
			d.Removed = append(d.Removed, keyedChange{Key: k, From: a})
		case !equalStrings(a, b):
			d.Changed = append(d.Changed, keyedChange{Key: k, From: a, To: b})
		}
	}
	return d
}

// anomalyValues keys parse anomalies by their raw token, so a token whose
// classification changed shows as a change and not as an unrelated pair.
func anomalyValues(as []store.WasmAnomaly) map[string][]string {
	out := map[string][]string{}
	for _, a := range as {
		out[a.RawToken] = append(out[a.RawToken], a.Anomaly)
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

// claimKey identifies a declaration by SEP and claim source. Two sources can
// report the same SEP, and losing one of them would understate the change.
type claimKey struct {
	sep    int
	source string
}

func claimsByKey(cs []store.WasmClaim) map[claimKey]declaredClaim {
	out := map[claimKey]declaredClaim{}
	for _, c := range cs {
		d := declaredClaim{RawToken: c.RawToken}
		if c.Anomaly != nil {
			d.Anomaly = *c.Anomaly
		}
		out[claimKey{c.SEP, c.Source}] = d
	}
	return out
}

func diffDeclared(from, to []store.WasmClaim) declaredDiff {
	a, b := claimsByKey(from), claimsByKey(to)
	keys := make([]claimKey, 0, len(a)+len(b))
	for k := range a {
		keys = append(keys, k)
	}
	for k := range b {
		if _, dup := a[k]; !dup {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].sep != keys[j].sep {
			return keys[i].sep < keys[j].sep
		}
		return keys[i].source < keys[j].source
	})
	d := declaredDiff{Added: []declaredChange{}, Removed: []declaredChange{}, Changed: []declaredChange{}}
	for _, k := range keys {
		av, inA := a[k]
		bv, inB := b[k]
		ch := declaredChange{SEP: k.sep, Source: k.source}
		switch {
		case !inA:
			ch.To = &bv
			d.Added = append(d.Added, ch)
		case !inB:
			ch.From = &av
			d.Removed = append(d.Removed, ch)
		case av != bv:
			ch.From, ch.To = &av, &bv
			d.Changed = append(d.Changed, ch)
		}
	}
	return d
}

// signaturesByName groups a side's signatures by function name, so a
// duplicate definition is compared instead of silently replaced.
func signaturesByName(fns []sepmeta.FnSig) map[string][]string {
	out := map[string][]string{}
	for _, f := range fns {
		out[f.Name] = append(out[f.Name], fnSignature(f))
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

func diffFunctions(from, to diffInput) functionDiff {
	d := functionDiff{Added: []functionChange{}, Removed: []functionChange{}, Changed: []functionChange{}}
	var blocked []string
	for _, in := range []diffInput{from, to} {
		if in.fns.HasSpec && !in.fns.Stored {
			blocked = append(blocked, in.detail.Hash)
		}
	}
	if len(blocked) > 0 {
		d.Note = strings.Join(blocked, " and ") + ": no stored function signatures, so the interface cannot be compared; " +
			"the Wasm needs a re-fetch (analyzed before migration 0002)"
		return d
	}
	d.Comparable = true
	a, b := signaturesByName(from.fns.Functions), signaturesByName(to.fns.Functions)
	for _, name := range sortedKeys(a, b) {
		av, inA := a[name]
		bv, inB := b[name]
		switch {
		case !inA:
			d.Added = append(d.Added, functionChange{Name: name, To: bv})
		case !inB:
			d.Removed = append(d.Removed, functionChange{Name: name, From: av})
		case !equalStrings(av, bv):
			d.Changed = append(d.Changed, functionChange{Name: name, From: av, To: bv})
		}
	}
	return d
}

func verdictsBySEP(ms []store.WasmMatch) map[int]verdictSide {
	out := map[int]verdictSide{}
	for _, m := range ms {
		out[m.SEP] = verdictSide{Status: m.Status, RulesetVersion: m.RulesetVersion, OKCount: m.OKCount, MatchedVersion: m.MatchedVersion}
	}
	return out
}

func diffVerdicts(from, to []store.WasmMatch) []verdictChange {
	a, b := verdictsBySEP(from), verdictsBySEP(to)
	seps := make([]int, 0, len(a)+len(b))
	for s := range a {
		seps = append(seps, s)
	}
	for s := range b {
		if _, dup := a[s]; !dup {
			seps = append(seps, s)
		}
	}
	sort.Ints(seps)
	out := make([]verdictChange, 0, len(seps))
	for _, s := range seps {
		ch := verdictChange{SEP: s}
		if av, ok := a[s]; ok {
			ch.From = &av
		}
		if bv, ok := b[s]; ok {
			ch.To = &bv
		}
		ch.Changed = !sameVerdict(ch.From, ch.To)
		out = append(out, ch)
	}
	return out
}

func sameVerdict(a, b *verdictSide) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.Status != b.Status || a.RulesetVersion != b.RulesetVersion || a.OKCount != b.OKCount {
		return false
	}
	switch {
	case a.MatchedVersion == nil && b.MatchedVersion == nil:
		return true
	case a.MatchedVersion == nil || b.MatchedVersion == nil:
		return false
	default:
		return *a.MatchedVersion == *b.MatchedVersion
	}
}

// buildDiff compares two sides. Identical is claimed only when every section
// matched and both interfaces were comparable, so an unreadable record never
// reads as "nothing changed".
func buildDiff(network string, from, to diffInput) (diffOutput, error) {
	d := diffOutput{Network: network, AsOfLedger: from.detail.AsOfLedger, From: from.side(), To: to.side()}
	fromMeta, err := metaValues(from.detail.Meta, from.detail.Hash)
	if err != nil {
		return d, err
	}
	toMeta, err := metaValues(to.detail.Meta, to.detail.Hash)
	if err != nil {
		return d, err
	}
	d.Metadata = diffKeyed(fromMeta, toMeta)
	d.Declared = diffDeclared(from.detail.Claims, to.detail.Claims)
	d.Anomalies = diffKeyed(anomalyValues(from.detail.Anomalies), anomalyValues(to.detail.Anomalies))
	d.Functions = diffFunctions(from, to)
	d.Verdicts = diffVerdicts(from.detail.Matches, to.detail.Matches)
	d.Identical = d.Metadata.empty() && d.Declared.empty() && d.Anomalies.empty() &&
		d.Functions.Comparable && d.Functions.empty() && !anyVerdictChanged(d.Verdicts)
	return d, nil
}

func anyVerdictChanged(vs []verdictChange) bool {
	for _, v := range vs {
		if v.Changed {
			return true
		}
	}
	return false
}

func sortedKeys(maps ...map[string][]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range maps {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
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

// writeDiff renders the human report. Sections with nothing to say are left
// out, so an upgrade that only changed one thing prints one section.
func writeDiff(w io.Writer, d diffOutput) error {
	if _, err := fmt.Fprintf(w, "%s -> %s (%s), as of ledger %d\n", d.From.Hash, d.To.Hash, d.Network, d.AsOfLedger); err != nil {
		return err
	}
	for _, s := range []struct {
		label string
		side  diffSide
	}{{"from", d.From}, {"to", d.To}} {
		if _, err := fmt.Fprintf(w, "%-4s %s: parse %s, parser %s, spec %t, functions stored %t\n",
			s.label, s.side.Hash, s.side.ParseStatus, s.side.ParserVersion, s.side.HasSpec, s.side.FunctionsStored); err != nil {
			return err
		}
	}
	// The conclusion comes before the sections: the verdict list below is
	// printed for every SEP, changed or not, and would otherwise read as a
	// difference.
	if d.Identical {
		if _, err := fmt.Fprintln(w, "No differences in metadata, declarations, functions or inferred verdicts."); err != nil {
			return err
		}
	}
	if err := writeKeyed(w, "Metadata", d.Metadata); err != nil {
		return err
	}
	if err := writeDeclared(w, d.Declared); err != nil {
		return err
	}
	if err := writeKeyed(w, "Anomalies", d.Anomalies); err != nil {
		return err
	}
	if err := writeFunctions(w, d.Functions); err != nil {
		return err
	}
	return writeVerdicts(w, d.Verdicts)
}

func writeKeyed(w io.Writer, title string, d keyedDiff) error {
	if d.empty() {
		return nil
	}
	if _, err := fmt.Fprintf(w, "\n%s\n", title); err != nil {
		return err
	}
	for _, c := range d.Added {
		if _, err := fmt.Fprintf(w, "  + %s = %s\n", c.Key, strings.Join(c.To, ", ")); err != nil {
			return err
		}
	}
	for _, c := range d.Removed {
		if _, err := fmt.Fprintf(w, "  - %s = %s\n", c.Key, strings.Join(c.From, ", ")); err != nil {
			return err
		}
	}
	for _, c := range d.Changed {
		if _, err := fmt.Fprintf(w, "  ~ %s = %s -> %s\n", c.Key, strings.Join(c.From, ", "), strings.Join(c.To, ", ")); err != nil {
			return err
		}
	}
	return nil
}

func writeDeclared(w io.Writer, d declaredDiff) error {
	if d.empty() {
		return nil
	}
	if _, err := fmt.Fprintln(w, "\nDeclared SEPs"); err != nil {
		return err
	}
	for _, c := range d.Added {
		if _, err := fmt.Fprintf(w, "  + SEP-%d (%s) %s\n", c.SEP, c.Source, claimText(c.To)); err != nil {
			return err
		}
	}
	for _, c := range d.Removed {
		if _, err := fmt.Fprintf(w, "  - SEP-%d (%s) %s\n", c.SEP, c.Source, claimText(c.From)); err != nil {
			return err
		}
	}
	for _, c := range d.Changed {
		if _, err := fmt.Fprintf(w, "  ~ SEP-%d (%s) %s -> %s\n", c.SEP, c.Source, claimText(c.From), claimText(c.To)); err != nil {
			return err
		}
	}
	return nil
}

func claimText(c *declaredClaim) string {
	anomaly := "none"
	if c.Anomaly != "" {
		anomaly = c.Anomaly
	}
	return fmt.Sprintf("token=%q, anomaly=%s", c.RawToken, anomaly)
}

func writeFunctions(w io.Writer, d functionDiff) error {
	if !d.Comparable {
		_, err := fmt.Fprintf(w, "\nFunctions\n  not compared: %s\n", d.Note)
		return err
	}
	if d.empty() {
		return nil
	}
	if _, err := fmt.Fprintln(w, "\nFunctions"); err != nil {
		return err
	}
	for _, c := range d.Added {
		if _, err := fmt.Fprintf(w, "  + %s: %s\n", c.Name, strings.Join(c.To, " | ")); err != nil {
			return err
		}
	}
	for _, c := range d.Removed {
		if _, err := fmt.Fprintf(w, "  - %s: %s\n", c.Name, strings.Join(c.From, " | ")); err != nil {
			return err
		}
	}
	// A signature already contains "->", so a changed line says "was" and
	// "now" instead of adding a second arrow nobody could parse.
	for _, c := range d.Changed {
		if _, err := fmt.Fprintf(w, "  ~ %s: was %s, now %s\n", c.Name, strings.Join(c.From, " | "), strings.Join(c.To, " | ")); err != nil {
			return err
		}
	}
	return nil
}

func writeVerdicts(w io.Writer, vs []verdictChange) error {
	if len(vs) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "\nVerdicts (inferred tier; not a claim and not a verification)"); err != nil {
		return err
	}
	for _, v := range vs {
		marker := " "
		if v.Changed {
			marker = "~"
		}
		if _, err := fmt.Fprintf(w, "  %s SEP-%d: %s -> %s\n", marker, v.SEP, verdictText(v.From), verdictText(v.To)); err != nil {
			return err
		}
	}
	return nil
}

func verdictText(v *verdictSide) string {
	if v == nil {
		return "no verdict"
	}
	s := fmt.Sprintf("%s (%s, %d functions matched", v.Status, v.RulesetVersion, v.OKCount)
	if v.MatchedVersion != nil {
		s += ", implements " + *v.MatchedVersion
	}
	return s + ")"
}

// runDiff implements `sep47idx diff <hashA> <hashB> [--json]`. It reads only
// stored evidence: no Wasm is fetched, parsed or executed here, so the answer
// is exactly what the index already holds.
func runDiff(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	resolve := config.RegisterCommon(fs)
	fromHash, toHash, err := parseHashPair(fs, args)
	if err != nil {
		return readExit("diff", err, stderr)
	}
	cfg, err := resolve()
	if err != nil {
		return readExit("diff", err, stderr)
	}
	for _, h := range []string{fromHash, toHash} {
		if err = store.ValidateWasmHash(h); err != nil {
			return readExit("diff", fmt.Errorf("%s: %w", h, err), stderr)
		}
	}
	err = readIndex(cfg, func(ctx context.Context, s *store.Store, rulesets map[int]string) error {
		versions, err := ruleVersions()
		if err != nil {
			return err
		}
		from, err := readDiffSide(ctx, s, fromHash, rulesets, versions.Highest)
		if err != nil {
			return err
		}
		to, err := readDiffSide(ctx, s, toHash, rulesets, versions.Highest)
		if err != nil {
			return err
		}
		d, err := buildDiff(cfg.Network.Name, from, to)
		if err != nil {
			return err
		}
		if cfg.JSON {
			return jsonOutput(stdout, d)
		}
		return writeDiff(stdout, d)
	})
	return readExit("diff", err, stderr)
}

// readDiffSide loads one hash's stored evidence. A hash the index has never
// seen is store.ErrNotFound, which readExit turns into exit code 2.
//
// It reuses Store.Wasm so the diff can never disagree with `sep47idx wasm`
// about the same hash; the one-entry contract page it also returns is
// discarded, because a diff is about code and not about its users.
func readDiffSide(ctx context.Context, s *store.Store, hash string, rulesets map[int]string, resolve store.VersionResolver) (diffInput, error) {
	d, err := s.Wasm(ctx, hash, 1, "", rulesets)
	if err != nil {
		return diffInput{}, fmt.Errorf("%s: %w", hash, err)
	}
	if err = s.AnnotateWasm(ctx, &d, resolve); err != nil {
		return diffInput{}, err
	}
	fns, err := s.FunctionEvidence(ctx, hash)
	if err != nil {
		return diffInput{}, fmt.Errorf("%s: %w", hash, err)
	}
	return diffInput{detail: d, fns: fns}, nil
}

// parseHashPair accepts both documented argument orders, as parseObject does
// for the single-identifier commands: hashes first, or flags first.
func parseHashPair(fs *flag.FlagSet, args []string) (string, string, error) {
	errWant := errors.New("exactly two Wasm hashes are required")
	if len(args) >= 2 && !strings.HasPrefix(args[0], "-") && !strings.HasPrefix(args[1], "-") {
		a, b := args[0], args[1]
		if err := fs.Parse(args[2:]); err != nil {
			return "", "", err
		}
		if fs.NArg() != 0 {
			return "", "", errWant
		}
		return a, b, nil
	}
	if err := fs.Parse(args); err != nil {
		return "", "", err
	}
	if fs.NArg() != 2 {
		return "", "", errWant
	}
	return fs.Arg(0), fs.Arg(1), nil
}
