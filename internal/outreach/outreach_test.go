package outreach

import (
	"strings"
	"testing"
)

func TestSEPLineAndPRBodyFillTemplate(t *testing.T) {
	g := Group{WasmHash: strings.Repeat("ab", 32), OKCount: 10, Contracts: []string{"C1", "C2"}}
	if got, want := SEPLine(41), `contractmeta!(key = "sep", val = "41");`; got != want {
		t.Fatalf("SEPLine = %q, want %q", got, want)
	}
	body := PRBody(41, "sep41-v0.5.2", g)
	for _, want := range []string{
		"Declare SEP-41 in the contract metadata",
		"`" + g.WasmHash + "`",
		"`sep41-v0.5.2`",
		"2 live contract(s)",
		"- `C1`\n- `C2`",
		SEPLine(41),
		"Declared SEP-41",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pull-request body lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "{{") {
		t.Errorf("unreplaced placeholder in body:\n%s", body)
	}
	if strings.Contains(strings.ToLower(body), "audit") {
		t.Errorf("template must not imply an audit:\n%s", body)
	}
}

func TestMarkdownIsLabelledInferredAndNamesNoProject(t *testing.T) {
	groups := []Group{
		{WasmHash: strings.Repeat("aa", 32), OKCount: 10, Contracts: []string{"C1", "C2"}},
		{WasmHash: strings.Repeat("bb", 32), OKCount: 9, Contracts: []string{"C3"}},
	}
	md := Markdown(41, "sep41-v0.5.2", groups, 2, 3)
	for _, want := range []string{
		"**Inferred, not declared.**",
		"## Wasm `" + groups[0].WasmHash + "` — 2 contract(s)",
		"- `C1`\n- `C2`",
		SEPLine(41),
		"<details>\n<summary>Pull-request body</summary>",
		"2 Wasm hashes / 3 live contracts.",
		"no project name is inferred",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "{{") {
		t.Errorf("unreplaced placeholder:\n%s", md)
	}

	capped := Markdown(41, "sep41-v0.5.2", groups[:1], 9, 40)
	if !strings.Contains(capped, "Showing the 1 largest of 9 Wasm hashes by contract count (2 of 40 live contracts). Raise `--limit` to see more.") {
		t.Errorf("capped report does not say what it left out:\n%s", capped)
	}
}

func TestGroupOutputsFillsDerivedFields(t *testing.T) {
	got := GroupOutputs(40, "sep40-v1", []Group{{WasmHash: "h", Contracts: nil}})
	if len(got) != 1 || got[0].ContractMeta != SEPLine(40) || !strings.Contains(got[0].PRTemplate, "SEP-40") || got[0].Contracts == nil {
		t.Fatalf("GroupOutputs = %+v", got)
	}
}
