package sepmeta

import (
	"bytes"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

func sig(name string, in []string, out ...string) FnSig {
	if in == nil {
		in = []string{}
	}
	if out == nil {
		out = []string{}
	}
	return FnSig{Name: name, Inputs: in, Outputs: out}
}

func ins(t ...string) []string { return t }

// Expected signatures, written by hand from testdata/contracts/*/src/lib.rs.
// Env parameters are host context and do not appear in the spec.
var (
	tokenAdmin = []FnSig{
		sig("__constructor", ins("Address", "u32", "String", "String")),
		sig("mint", ins("Address", "i128")),
		sig("upgrade", ins("BytesN<32>")),
	}
	sep41Common = []FnSig{
		sig("allowance", ins("Address", "Address"), "i128"),
		sig("approve", ins("Address", "Address", "i128", "u32")),
		sig("balance", ins("Address"), "i128"),
		sig("transfer_from", ins("Address", "Address", "Address", "i128")),
		sig("decimals", nil, "u32"),
		sig("name", nil, "String"),
		sig("symbol", nil, "String"),
	}
	burns = []FnSig{
		sig("burn", ins("Address", "i128")),
		sig("burn_from", ins("Address", "Address", "i128")),
	}
)

func join(parts ...[]FnSig) []FnSig {
	var out []FnSig
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestFunctionsFixtures(t *testing.T) {
	tests := []struct {
		fixture string
		want    []FnSig
	}{
		{"token_full_sep.wasm", join(tokenAdmin, sep41Common, burns, []FnSig{sig("transfer", ins("Address", "MuxedAddress", "i128"))})},
		{"token_full_legacy.wasm", join(tokenAdmin, sep41Common, burns, []FnSig{sig("transfer", ins("Address", "Address", "i128"))})},
		{"token_partial.wasm", join(tokenAdmin, sep41Common, []FnSig{sig("transfer", ins("Address", "MuxedAddress", "i128"))})},
		{"not_token.wasm", []FnSig{
			sig("hello", ins("Symbol"), "Vec<Symbol>"),
			sig("add", ins("u32", "u32"), "u32"),
			sig("version", nil, "u32"),
		}},
		{"multi_sep.wasm", []FnSig{sig("decimals", nil, "u32"), sig("resolution", nil, "u32")}},
		{"no_meta.wasm", []FnSig{sig("ping", nil, "u32")}},
	}
	byName := func(s []FnSig) []FnSig {
		c := append([]FnSig(nil), s...)
		sort.Slice(c, func(i, j int) bool { return c[i].Name < c[j].Name })
		return c
	}
	for _, tt := range tests {
		t.Run(tt.fixture+" exposes exactly its source functions", func(t *testing.T) {
			spec, err := DecodeSpec(fixtureSection(t, tt.fixture, SectionSpec), DefaultLimits())
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			got := byName(Functions(spec))
			if want := byName(tt.want); !reflect.DeepEqual(got, want) {
				t.Fatalf("got\n%v\nwant\n%v", got, want)
			}
		})
	}
}

func TestFunctionsSkipsNonFunctionEntries(t *testing.T) {
	spec, err := DecodeSpec(fixtureSection(t, "token_full_sep.wasm", SectionSpec), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[xdr.ScSpecEntryKind]int{}
	for _, e := range spec {
		kinds[e.Kind]++
	}
	t.Logf("token_full_sep spec entry kinds: %v", kinds)
	if kinds[xdr.ScSpecEntryKindScSpecEntryEventV0] == 0 || kinds[xdr.ScSpecEntryKindScSpecEntryUdtErrorEnumV0] == 0 {
		t.Fatalf("expected event and error-enum entries in the token spec, got %v", kinds)
	}
	if n := len(Functions(spec)); n != kinds[xdr.ScSpecEntryKindScSpecEntryFunctionV0] {
		t.Fatalf("Functions returned %d, spec has %d function entries", n, kinds[xdr.ScSpecEntryKindScSpecEntryFunctionV0])
	}
}

func scalar(t xdr.ScSpecType) xdr.ScSpecTypeDef { return xdr.ScSpecTypeDef{Type: t} }

func TestTypeName(t *testing.T) {
	sym := scalar(xdr.ScSpecTypeScSpecTypeSymbol)
	u32 := scalar(xdr.ScSpecTypeScSpecTypeU32)
	tests := []struct {
		want string
		def  xdr.ScSpecTypeDef
	}{
		{"Val", scalar(xdr.ScSpecTypeScSpecTypeVal)},
		{"bool", scalar(xdr.ScSpecTypeScSpecTypeBool)},
		{"void", scalar(xdr.ScSpecTypeScSpecTypeVoid)},
		{"Error", scalar(xdr.ScSpecTypeScSpecTypeError)},
		{"u32", u32},
		{"i32", scalar(xdr.ScSpecTypeScSpecTypeI32)},
		{"u64", scalar(xdr.ScSpecTypeScSpecTypeU64)},
		{"i64", scalar(xdr.ScSpecTypeScSpecTypeI64)},
		{"Timepoint", scalar(xdr.ScSpecTypeScSpecTypeTimepoint)},
		{"Duration", scalar(xdr.ScSpecTypeScSpecTypeDuration)},
		{"u128", scalar(xdr.ScSpecTypeScSpecTypeU128)},
		{"i128", scalar(xdr.ScSpecTypeScSpecTypeI128)},
		{"u256", scalar(xdr.ScSpecTypeScSpecTypeU256)},
		{"i256", scalar(xdr.ScSpecTypeScSpecTypeI256)},
		{"Bytes", scalar(xdr.ScSpecTypeScSpecTypeBytes)},
		{"String", scalar(xdr.ScSpecTypeScSpecTypeString)},
		{"Symbol", sym},
		{"Address", scalar(xdr.ScSpecTypeScSpecTypeAddress)},
		{"MuxedAddress", scalar(xdr.ScSpecTypeScSpecTypeMuxedAddress)},
		{"Option<u32>", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeOption, Option: &xdr.ScSpecTypeOption{ValueType: u32}}},
		{"Result<u32,Error>", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeResult, Result: &xdr.ScSpecTypeResult{OkType: u32, ErrorType: scalar(xdr.ScSpecTypeScSpecTypeError)}}},
		{"Vec<Symbol>", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeVec, Vec: &xdr.ScSpecTypeVec{ElementType: sym}}},
		{"Map<Symbol,u32>", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeMap, Map: &xdr.ScSpecTypeMap{KeyType: sym, ValueType: u32}}},
		{"(Symbol,u32)", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeTuple, Tuple: &xdr.ScSpecTypeTuple{ValueTypes: []xdr.ScSpecTypeDef{sym, u32}}}},
		{"()", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeTuple, Tuple: &xdr.ScSpecTypeTuple{}}},
		{"BytesN<32>", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeBytesN, BytesN: &xdr.ScSpecTypeBytesN{N: 32}}},
		{"udt:AllowanceValue", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeUdt, Udt: &xdr.ScSpecTypeUdt{Name: "AllowanceValue"}}},
		{
			"Option<Vec<Map<Symbol,udt:Foo>>>",
			xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeOption, Option: &xdr.ScSpecTypeOption{ValueType: xdr.ScSpecTypeDef{
				Type: xdr.ScSpecTypeScSpecTypeVec, Vec: &xdr.ScSpecTypeVec{ElementType: xdr.ScSpecTypeDef{
					Type: xdr.ScSpecTypeScSpecTypeMap, Map: &xdr.ScSpecTypeMap{KeyType: sym, ValueType: xdr.ScSpecTypeDef{
						Type: xdr.ScSpecTypeScSpecTypeUdt, Udt: &xdr.ScSpecTypeUdt{Name: "Foo"}}}}}}}},
		},
		{"invalid", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeOption}},
		{"unknown:999", scalar(xdr.ScSpecType(999))},
	}
	for _, tt := range tests {
		t.Run("renders "+tt.want, func(t *testing.T) {
			if got := TypeName(tt.def); got != tt.want {
				t.Fatalf("TypeName = %q, want %q", got, tt.want)
			}
		})
	}
	t.Run("every scalar spec type has a name", func(t *testing.T) {
		// 19 scalar variants exist in go-stellar-sdk v0.7.3; a new one added by
		// an SDK upgrade must be named here, not rendered as unknown.
		if len(scalarNames) != 19 {
			t.Fatalf("scalarNames has %d entries, want 19", len(scalarNames))
		}
	})
}

// TestDecodeSpecEveryPrefix cuts each fixture's spec section at every byte.
// A cut on an entry boundary decodes cleanly; any other cut is ErrTruncated
// and returns exactly the entries that ended before the cut.
func TestDecodeSpecEveryPrefix(t *testing.T) {
	for _, name := range []string{"token_full_sep.wasm", "token_full_legacy.wasm", "token_partial.wasm", "not_token.wasm", "multi_sep.wasm", "no_meta.wasm"} {
		t.Run(name, func(t *testing.T) {
			section := fixtureSection(t, name, SectionSpec)
			full, err := DecodeSpec(section, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			var ends []int
			off := 0
			for _, e := range full {
				var b bytes.Buffer
				if _, err := xdr.Marshal(&b, e); err != nil {
					t.Fatal(err)
				}
				off += b.Len()
				ends = append(ends, off)
			}
			if off != len(section) {
				t.Fatalf("re-encoded length %d != section length %d", off, len(section))
			}
			t.Logf("checking %d cuts across %d entries", len(section), len(full))
			for cut := 0; cut < len(section); cut++ {
				complete := 0
				for _, e := range ends {
					if e <= cut {
						complete++
					}
				}
				onBoundary := cut == 0 || (complete > 0 && ends[complete-1] == cut)
				got, err := DecodeSpec(section[:cut], DefaultLimits())
				if len(got) != complete {
					t.Fatalf("cut %d: got %d entries, want %d", cut, len(got), complete)
				}
				if onBoundary != (err == nil) || (!onBoundary && !errors.Is(err, ErrTruncated)) {
					t.Fatalf("cut %d (boundary=%v): err = %v", cut, onBoundary, err)
				}
			}
		})
	}
}

func TestDecodeSpecErrors(t *testing.T) {
	t.Run("unknown entry kind is malformed", func(t *testing.T) {
		_, err := DecodeSpec([]byte{0, 0, 0, 99, 0, 0, 0, 0}, DefaultLimits())
		if !errors.Is(err, ErrMalformed) {
			t.Fatalf("err = %v, want ErrMalformed", err)
		}
	})
	t.Run("function name over the 32-byte symbol limit is malformed", func(t *testing.T) {
		// kind=function, doc="", name length 33 followed by 36 bytes.
		in := append([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 33}, make([]byte, 36)...)
		_, err := DecodeSpec(in, DefaultLimits())
		if !errors.Is(err, ErrMalformed) {
			t.Fatalf("err = %v, want ErrMalformed", err)
		}
	})
	t.Run("nesting deeper than MaxXDRDepth is rejected, not followed", func(t *testing.T) {
		// A function whose single input is Option<Option<...>> nested 64 deep.
		head := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 'f', 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1, 'x', 0, 0, 0}
		var nest []byte
		for i := 0; i < 64; i++ {
			nest = append(nest, 0, 0, 0x03, 0xe8) // SC_SPEC_TYPE_OPTION = 1000
		}
		in := append(append(head, nest...), 0, 0, 0, 4, 0, 0, 0, 0) // innermost u32; no outputs
		if _, err := DecodeSpec(in, DefaultLimits()); err != nil {
			t.Fatalf("depth 64 under default limit: unexpected err %v", err)
		}
		_, err := DecodeSpec(in, Limits{MaxXDRDepth: 16})
		if err == nil {
			t.Fatal("depth 64 with MaxXDRDepth 16: want error, got nil")
		}
	})
}
