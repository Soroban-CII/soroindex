// Package outreach turns the inferred undeclared gap into review-ready
// suggestions for contract authors: the contracts running one Wasm hash, the
// exact contractmeta! line to add, and a pull-request body.
//
// Every match here is inferred from function types. A group is never a claim
// that a contract implements a SEP, and output never invents a project name:
// the index only knows contract IDs and Wasm hashes.
package outreach

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed pr_template.md
var prTemplate string

// Group is one undeclared-gap Wasm and the live contracts running it.
type Group struct {
	WasmHash  string   `json:"wasm_hash"`
	OKCount   int      `json:"ok_count"`
	Contracts []string `json:"contracts"`
	// ContractMeta is the declaration to add; PRTemplate is the reviewed
	// body in pr_template.md rendered for this group.
	ContractMeta string `json:"contractmeta"`
	PRTemplate   string `json:"pr_template"`
}

// GroupOutputs fills the derived fields of every group. Callers pass groups
// read from the index so no output logic lives in the CLI handlers.
func GroupOutputs(sep int, ruleset string, groups []Group) []Group {
	out := make([]Group, 0, len(groups))
	for _, g := range groups {
		g.ContractMeta = SEPLine(sep)
		g.PRTemplate = PRBody(sep, ruleset, g)
		if g.Contracts == nil {
			g.Contracts = []string{}
		}
		out = append(out, g)
	}
	return out
}

// SEPLine is the exact declaration a Rust contract adds.
func SEPLine(sep int) string {
	return fmt.Sprintf("contractmeta!(key = \"sep\", val = \"%d\");", sep)
}

// PRBody renders the committed pull-request template for one group.
func PRBody(sep int, ruleset string, g Group) string {
	contracts := make([]string, 0, len(g.Contracts))
	for _, id := range g.Contracts {
		contracts = append(contracts, "- `"+id+"`")
	}
	r := strings.NewReplacer(
		"{{SEP}}", fmt.Sprint(sep),
		"{{RULESET}}", ruleset,
		"{{WASM_HASH}}", g.WasmHash,
		"{{CONTRACT_COUNT}}", fmt.Sprint(len(g.Contracts)),
		"{{CONTRACTS}}", strings.Join(contracts, "\n"),
		"{{SEP_LINE}}", SEPLine(sep),
	)
	return r.Replace(prTemplate)
}

// Markdown renders the outreach report. totalGroups and totalContracts are
// the uncapped gap, so a capped report says how much it left out.
func Markdown(sep int, ruleset string, groups []Group, totalGroups, totalContracts int) string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	p("# SEP-%d outreach for undeclared interfaces\n\n", sep)
	p("**Inferred, not declared.** Every group below matches the SEP-%d interface (%s) in the index's rule file ", sep, ruleset)
	p("and declares no SEP. The match compares function types; it does not show that the contract behaves as the SEP describes. ")
	p("A contract is listed by ID and a group by Wasm hash because that is all the index records; no project name is inferred.\n\n")
	if totalGroups > len(groups) {
		p("Showing the %d largest of %d Wasm hashes by contract count (%d of %d live contracts). Raise `--limit` to see more.\n\n",
			len(groups), totalGroups, contractsShown(groups), totalContracts)
	} else {
		p("%d Wasm hashes / %d live contracts.\n\n", totalGroups, totalContracts)
	}

	for _, g := range groups {
		p("## Wasm `%s` — %d contract(s)\n\n", g.WasmHash, len(g.Contracts))
		for _, id := range g.Contracts {
			p("- `%s`\n", id)
		}
		p("\nAdd this line to the contract crate:\n\n")
		p("```rust\n%s\n```\n\n", SEPLine(sep))
		p("<details>\n<summary>Pull-request body</summary>\n\n")
		p("%s\n", PRBody(sep, ruleset, g))
		p("</details>\n\n")
	}
	return b.String()
}

func contractsShown(groups []Group) int {
	n := 0
	for _, g := range groups {
		n += len(g.Contracts)
	}
	return n
}
