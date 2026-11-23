package sepmeta

import (
	"strings"
	"testing"
)

// The SEP-50 NFT fixtures (testdata/contracts/nft_sep50, nft_sep50_partial).
// Expected signatures are written by hand from the fixture source, which
// cites the SEP-50 text and commit it copies.

var (
	// sep50FnsButApproveForAll is SEP-50 v0.1.0 NonFungibleToken with
	// TokenID = Balance = u32, less approve_for_all, which the partial
	// variant omits.
	sep50FnsButApproveForAll = []FnSig{
		sig("balance", ins("Address"), "u32"),
		sig("owner_of", ins("u32"), "Address"),
		sig("transfer", ins("Address", "Address", "u32")),
		sig("transfer_from", ins("Address", "Address", "Address", "u32")),
		sig("approve", ins("Address", "Address", "u32", "u32")),
		sig("get_approved", ins("u32"), "Option<Address>"),
		sig("is_approved_for_all", ins("Address", "Address"), "bool"),
		sig("name", nil, "String"),
		sig("symbol", nil, "String"),
		sig("token_uri", ins("u32"), "String"),
	}
	approveForAll = []FnSig{sig("approve_for_all", ins("Address", "Address", "u32"))}
	nftAdmin      = []FnSig{
		sig("__constructor", ins("Address", "String", "String", "String")),
		sig("mint", ins("Address"), "u32"),
	}
)

func TestNFTFixtures(t *testing.T) {
	tests := []struct {
		fixture string
		want    []FnSig
	}{
		{"nft_sep50.wasm", join(nftAdmin, sep50FnsButApproveForAll, approveForAll)},
		{"nft_sep50_partial.wasm", join(nftAdmin, sep50FnsButApproveForAll)},
	}
	for _, tt := range tests {
		t.Run(tt.fixture+" declares [50] with no anomaly", func(t *testing.T) {
			checkDeclared(t, tt.fixture, []int{50})
		})
		t.Run(tt.fixture+" exposes exactly its source functions", func(t *testing.T) {
			checkFunctions(t, tt.fixture, tt.want)
		})
	}
}

// TestNFTFixtureHasNoUndefinedTypes guards the alias pitfall recorded in
// nft_sep50's source: a Rust alias such as TokenID in a contract signature
// becomes a reference to a UDT the spec never defines.
func TestNFTFixtureHasNoUndefinedTypes(t *testing.T) {
	for _, name := range []string{"nft_sep50.wasm", "nft_sep50_partial.wasm"} {
		t.Run(name, func(t *testing.T) {
			spec, err := DecodeSpec(fixtureSection(t, name, SectionSpec), DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range Functions(spec) {
				for _, ty := range append(append([]string{}, f.Inputs...), f.Outputs...) {
					if strings.HasPrefix(ty, "udt:") {
						t.Errorf("%s uses %s", f.Name, ty)
					}
				}
			}
		})
	}
}
