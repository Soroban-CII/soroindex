package match

import (
	"fmt"
	"os"

	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// ExampleMatch demonstrates a partial match against the shipped SEP-41 rule
// file. The contract implements 8 of the 10 required functions; burn and
// burn_from are absent, so the status is "partial".
func ExampleMatch() {
	rules, err := LoadRules(os.DirFS("../../rules"))
	if err != nil {
		panic(err)
	}
	var sep41 RuleFile
	for _, r := range rules {
		if r.SEP == 41 {
			sep41 = r
			break
		}
	}

	// Eight of the ten SEP-41 required functions; burn and burn_from are missing.
	fns := []sepmeta.FnSig{
		fn("allowance", []string{"Address", "Address"}, "i128"),
		fn("approve", []string{"Address", "Address", "i128", "u32"}),
		fn("balance", []string{"Address"}, "i128"),
		fn("transfer", []string{"Address", "MuxedAddress", "i128"}),
		fn("transfer_from", []string{"Address", "Address", "Address", "i128"}),
		fn("decimals", nil, "u32"),
		fn("name", nil, "String"),
		fn("symbol", nil, "String"),
	}

	result := Match(fns, sep41)

	fmt.Println(result.Status)
	fmt.Println(result.OKCount, "of", result.RequiredCount, "required functions matched")
	fmt.Println("missing:", result.Missing)
	// Output:
	// partial
	// 8 of 10 required functions matched
	// missing: [burn burn_from]
}
