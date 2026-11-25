package sepmeta_test

import (
	"errors"
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

// ExampleFunctions renders the spec of the SEP-40 oracle fixture. Container
// types nest, so an optional list of a user-defined struct prints as
// Option<Vec<udt:PriceData>>.
func ExampleFunctions() {
	wasm, err := os.ReadFile("../../testdata/wasm/oracle_sep40.wasm")
	if err != nil {
		log.Fatal(err)
	}
	lim := sepmeta.DefaultLimits()

	secs, err := sepmeta.ReadCustomSections(wasm, []string{sepmeta.SectionSpec}, lim)
	if err != nil {
		log.Fatal(err)
	}
	spec, err := sepmeta.DecodeSpec(secs[sepmeta.SectionSpec], lim)
	if err != nil {
		log.Fatal(err)
	}
	for _, fn := range sepmeta.Functions(spec) {
		fmt.Println(fn.Name, fn.Inputs, "->", fn.Outputs)
	}
	// Output:
	// base [] -> [udt:Asset]
	// price [udt:Asset u64] -> [Option<udt:PriceData>]
	// assets [] -> [Vec<udt:Asset>]
	// prices [udt:Asset u32] -> [Option<Vec<udt:PriceData>>]
	// decimals [] -> [u32]
	// lastprice [udt:Asset] -> [Option<udt:PriceData>]
	// set_price [udt:Asset i128 u64] -> []
	// resolution [] -> [u32]
	// __constructor [Address udt:Asset u32 u32] -> []
}

// ExampleReadCustomSections_errLimit shows a module rejected because a custom
// section is larger than the caller allows. Every limit failure matches
// ErrLimit under errors.Is, and errors.As yields a *LimitError naming the
// limit, so callers can record why a Wasm was skipped without comparing
// error strings.
func ExampleReadCustomSections_errLimit() {
	// A minimal module: the Wasm header followed by one custom section named
	// "contractmetav0" with a 64-byte payload.
	// Both sizes are constants below 128, so each fits in one LEB128 byte
	// and the compiler rejects the conversions if that ever stops being true.
	const name = sepmeta.SectionMeta
	const bodyLen = 64
	wasm := []byte{0x00, 'a', 's', 'm', 0x01, 0x00, 0x00, 0x00}
	wasm = append(wasm, 0x00, byte(1+len(name)+bodyLen), byte(len(name)))
	wasm = append(wasm, name...)
	wasm = append(wasm, make([]byte, bodyLen)...)

	lim := sepmeta.DefaultLimits()
	lim.MaxSectionBytes = 16

	_, err := sepmeta.ReadCustomSections(wasm, []string{name}, lim)
	fmt.Println("is ErrLimit:", errors.Is(err, sepmeta.ErrLimit))

	var le *sepmeta.LimitError
	if errors.As(err, &le) {
		fmt.Println("limit:", le.Limit, "max:", le.Max, "got:", le.Got)
	}
	fmt.Println(err)

	// The same module passes once the limit allows it.
	lim.MaxSectionBytes = 64
	secs, err := sepmeta.ReadCustomSections(wasm, []string{name}, lim)
	fmt.Println("with MaxSectionBytes 64:", len(secs[name]), "bytes, err:", err)
	// Output:
	// is ErrLimit: true
	// limit: MaxSectionBytes max: 16 got: 64
	// limit exceeded: MaxSectionBytes is 16, got 64
	// with MaxSectionBytes 64: 64 bytes, err: <nil>
}
