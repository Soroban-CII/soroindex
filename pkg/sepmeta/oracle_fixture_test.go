package sepmeta

import (
	"reflect"
	"sort"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// The SEP-40 oracle fixture (testdata/contracts/oracle_sep40). Expected
// signatures are written by hand from the fixture source, which cites the
// SEP-40 text and commit it copies.

var (
	// sep40Fns is SEP-40 v0.1.0 PriceFeedTrait.
	sep40Fns = []FnSig{
		sig("base", nil, "udt:Asset"),
		sig("assets", nil, "Vec<udt:Asset>"),
		sig("decimals", nil, "u32"),
		sig("resolution", nil, "u32"),
		sig("price", ins("udt:Asset", "u64"), "Option<udt:PriceData>"),
		sig("prices", ins("udt:Asset", "u32"), "Option<Vec<udt:PriceData>>"),
		sig("lastprice", ins("udt:Asset"), "Option<udt:PriceData>"),
	}
	oracleAdmin = []FnSig{
		sig("__constructor", ins("Address", "udt:Asset", "u32", "u32")),
		sig("set_price", ins("udt:Asset", "i128", "u64")),
	}
)

// checkDeclared asserts a fixture's single sep meta entry parses to want in
// both modes, with no anomaly.
func checkDeclared(t *testing.T, fixture string, want []int) {
	t.Helper()
	entries, err := DecodeMeta(fixtureSection(t, fixture, SectionMeta), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []Mode{Lenient, Strict} {
		got := ParseSEP47(entries, mode)
		if !reflect.DeepEqual(got.SEPs, want) || got.EntryCount != 1 || anomalies(got) != "-" {
			t.Fatalf("mode %d: got SEPs=%v EntryCount=%d anomalies=%s, want %v, 1, -",
				mode, got.SEPs, got.EntryCount, anomalies(got), want)
		}
	}
}

// checkFunctions asserts Functions() of a fixture equals want, ignoring order.
func checkFunctions(t *testing.T, fixture string, want []FnSig) {
	t.Helper()
	spec, err := DecodeSpec(fixtureSection(t, fixture, SectionSpec), DefaultLimits())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got, want := sortedByName(Functions(spec)), sortedByName(want); !reflect.DeepEqual(got, want) {
		t.Fatalf("got\n%v\nwant\n%v", got, want)
	}
}

// sortedByName sorts stably, so fixtures that repeat a name keep spec order.
func sortedByName(s []FnSig) []FnSig {
	c := append([]FnSig(nil), s...)
	sort.SliceStable(c, func(i, j int) bool { return c[i].Name < c[j].Name })
	return c
}

func TestOracleFixture(t *testing.T) {
	t.Run("declares [40] with no anomaly", func(t *testing.T) {
		checkDeclared(t, "oracle_sep40.wasm", []int{40})
	})
	t.Run("exposes the SEP-40 functions plus its admin setup", func(t *testing.T) {
		checkFunctions(t, "oracle_sep40.wasm", join(oracleAdmin, sep40Fns))
	})
}

// TestOracleFixtureUDTs checks the user-defined types SEP-40 declares are in
// the spec with the SEP's exact fields and cases, so the udt:Asset and
// udt:PriceData names in Functions refer to real definitions.
func TestOracleFixtureUDTs(t *testing.T) {
	spec, err := DecodeSpec(fixtureSection(t, "oracle_sep40.wasm", SectionSpec), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	var structs, unions int
	for _, e := range spec {
		switch e.Kind {
		case xdr.ScSpecEntryKindScSpecEntryUdtStructV0:
			s := e.MustUdtStructV0()
			if s.Name != "PriceData" {
				continue
			}
			structs++
			var got []string
			for _, f := range s.Fields {
				got = append(got, string(f.Name)+":"+TypeName(f.Type))
			}
			if want := []string{"price:i128", "timestamp:u64"}; !reflect.DeepEqual(got, want) {
				t.Errorf("PriceData fields = %v, want %v", got, want)
			}
		case xdr.ScSpecEntryKindScSpecEntryUdtUnionV0:
			u := e.MustUdtUnionV0()
			if u.Name != "Asset" {
				continue
			}
			unions++
			var got []string
			for _, c := range u.Cases {
				tc, ok := c.GetTupleCase()
				if !ok {
					t.Fatalf("Asset case %v is not a tuple case", c)
				}
				name := string(tc.Name)
				for _, ty := range tc.Type {
					name += "(" + TypeName(ty) + ")"
				}
				got = append(got, name)
			}
			if want := []string{"Stellar(Address)", "Other(Symbol)"}; !reflect.DeepEqual(got, want) {
				t.Errorf("Asset cases = %v, want %v", got, want)
			}
		}
	}
	if structs != 1 || unions != 1 {
		t.Fatalf("found %d PriceData structs and %d Asset unions, want 1 of each", structs, unions)
	}
}
