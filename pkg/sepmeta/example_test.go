package sepmeta_test

import (
	"fmt"
	"log"
	"os"

	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// Example reads a contract's SEP-47 declaration and its function signatures
// from a compiled Wasm file.
func Example() {
	wasm, err := os.ReadFile("../../testdata/wasm/token_full_sep.wasm")
	if err != nil {
		log.Fatal(err)
	}
	lim := sepmeta.DefaultLimits()

	secs, err := sepmeta.ReadCustomSections(wasm, []string{sepmeta.SectionMeta, sepmeta.SectionSpec}, lim)
	if err != nil {
		log.Fatal(err)
	}

	entries, err := sepmeta.DecodeMeta(secs[sepmeta.SectionMeta], lim)
	if err != nil {
		log.Fatal(err)
	}
	claims := sepmeta.ParseSEP47(entries, sepmeta.Lenient)
	fmt.Println("declares:", claims.SEPs)

	spec, err := sepmeta.DecodeSpec(secs[sepmeta.SectionSpec], lim)
	if err != nil {
		log.Fatal(err)
	}
	for _, fn := range sepmeta.Functions(spec) {
		if fn.Name == "transfer" {
			fmt.Println("transfer:", fn.Inputs, "->", fn.Outputs)
		}
	}
	// Output:
	// declares: [41]
	// transfer: [Address MuxedAddress i128] -> []
}

// ExampleParseSEP47 shows how a malformed token is reported rather than
// silently dropped, and how the two modes differ.
func ExampleParseSEP47() {
	entries := []sepmeta.MetaEntry{{Key: "sep", Value: "41, SEP-40"}}
	for _, mode := range []sepmeta.Mode{sepmeta.Lenient, sepmeta.Strict} {
		c := sepmeta.ParseSEP47(entries, mode)
		fmt.Println(c.SEPs, c.Tokens[1].Raw, c.Tokens[1].Anomaly, c.Tokens[1].Accepted)
	}
	// Output:
	// [41 40] SEP-40 prefixed_token true
	// [41] SEP-40 prefixed_token false
}
