package sepmeta

import "testing"

// The SEP-56 vault fixture (testdata/contracts/vault_sep56). Expected
// signatures are written by hand from the fixture source, which cites the
// SEP-56 text and commit it copies.

var (
	// sep56Fns is SEP-56 v0.1.2 TokenizedVault, excluding its SEP-41
	// supertrait.
	sep56Fns = []FnSig{
		sig("total_supply", nil, "i128"),
		sig("query_asset", nil, "Address"),
		sig("total_assets", nil, "i128"),
		sig("convert_to_shares", ins("i128"), "i128"),
		sig("convert_to_assets", ins("i128"), "i128"),
		sig("max_deposit", ins("Address"), "i128"),
		sig("preview_deposit", ins("i128"), "i128"),
		sig("deposit", ins("i128", "Address", "Address", "Address"), "i128"),
		sig("max_mint", ins("Address"), "i128"),
		sig("preview_mint", ins("i128"), "i128"),
		sig("mint", ins("i128", "Address", "Address", "Address"), "i128"),
		sig("max_withdraw", ins("Address"), "i128"),
		sig("preview_withdraw", ins("i128"), "i128"),
		sig("withdraw", ins("i128", "Address", "Address", "Address"), "i128"),
		sig("max_redeem", ins("Address"), "i128"),
		sig("preview_redeem", ins("i128"), "i128"),
		sig("redeem", ins("i128", "Address", "Address", "Address"), "i128"),
	}
	vaultAdmin = []FnSig{sig("__constructor", ins("Address", "String", "String"))}
)

func TestVaultFixture(t *testing.T) {
	t.Run("declares [41 56] with no anomaly", func(t *testing.T) {
		checkDeclared(t, "vault_sep56.wasm", []int{41, 56})
	})
	t.Run("exposes SEP-41 for its shares, SEP-56, and its constructor", func(t *testing.T) {
		checkFunctions(t, "vault_sep56.wasm", join(vaultAdmin, sep41Common, burns,
			[]FnSig{sig("transfer", ins("Address", "MuxedAddress", "i128"))}, sep56Fns))
	})
}
