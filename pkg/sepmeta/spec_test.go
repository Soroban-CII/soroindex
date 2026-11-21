package sepmeta

import (
	"bytes"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// makeUDT builds a ScSpecTypeDef referencing a user-defined type by name.
func makeUDT(name string) xdr.ScSpecTypeDef {
	return xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeUdt, Udt: &xdr.ScSpecTypeUdt{Name: name}}
}

// encodeSpecEntries marshals a slice of spec entries to a byte stream suitable
// for DecodeSpec. It fails the test on any marshal error.
func encodeSpecEntries(t *testing.T, entries []xdr.ScSpecEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, e := range entries {
		if _, err := xdr.Marshal(&buf, e); err != nil {
			t.Fatalf("marshal spec entry: %v", err)
		}
	}
	return buf.Bytes()
}

// TestNestedUDTAndUnionSpecVectors is issue #39.
// It exercises structs, unions, enum references, forward references and
// truncated payloads with explicit bounds assertions.
// All vectors are deterministic and network-free.
func TestNestedUDTAndUnionSpecVectors(t *testing.T) {
	t.Run("TypeName renders nested containers with udt inner types", func(t *testing.T) {
		tests := []struct {
			want string
			def  xdr.ScSpecTypeDef
		}{
			// UDT nested as Vec element (struct-like UDT reference).
			{"Vec<udt:TokenEvent>", xdr.ScSpecTypeDef{
				Type: xdr.ScSpecTypeScSpecTypeVec,
				Vec:  &xdr.ScSpecTypeVec{ElementType: makeUDT("TokenEvent")},
			}},
			// UDT nested in Option (union-like UDT reference).
			{"Option<udt:VoteOutcome>", xdr.ScSpecTypeDef{
				Type:   xdr.ScSpecTypeScSpecTypeOption,
				Option: &xdr.ScSpecTypeOption{ValueType: makeUDT("VoteOutcome")},
			}},
			// Two UDT sides in Result (enum reference on error side).
			{"Result<udt:Status,udt:AppError>", xdr.ScSpecTypeDef{
				Type: xdr.ScSpecTypeScSpecTypeResult,
				Result: &xdr.ScSpecTypeResult{
					OkType:    makeUDT("Status"),
					ErrorType: makeUDT("AppError"),
				},
			}},
			// Map with udt key and value.
			{"Map<udt:AccountRef,udt:Balance>", xdr.ScSpecTypeDef{
				Type: xdr.ScSpecTypeScSpecTypeMap,
				Map: &xdr.ScSpecTypeMap{
					KeyType:   makeUDT("AccountRef"),
					ValueType: makeUDT("Balance"),
				},
			}},
			// Deeply nested: Vec<Option<udt:Data>>.
			{"Vec<Option<udt:Data>>", xdr.ScSpecTypeDef{
				Type: xdr.ScSpecTypeScSpecTypeVec,
				Vec: &xdr.ScSpecTypeVec{ElementType: xdr.ScSpecTypeDef{
					Type:   xdr.ScSpecTypeScSpecTypeOption,
					Option: &xdr.ScSpecTypeOption{ValueType: makeUDT("Data")},
				}},
			}},
			// UDT inside BytesN wrapper is not a container, but confirms udt name
			// renders even when nested directly beside a named-size type.
			{"udt:Proof", makeUDT("Proof")},
		}
		for _, tt := range tests {
			t.Run("renders "+tt.want, func(t *testing.T) {
				if got := TypeName(tt.def); got != tt.want {
					t.Fatalf("TypeName = %q, want %q", got, tt.want)
				}
			})
		}
	})

	t.Run("Functions extracts UDT-typed inputs and outputs from in-memory spec", func(t *testing.T) {
		entries := []xdr.ScSpecEntry{
			// Struct-typed parameters: transfer_struct(from: udt:Account, amount: udt:Amount) -> udt:Receipt.
			{
				Kind: xdr.ScSpecEntryKindScSpecEntryFunctionV0,
				FunctionV0: &xdr.ScSpecFunctionV0{
					Name: xdr.ScSymbol("transfer_struct"),
					Inputs: []xdr.ScSpecFunctionInputV0{
						{Name: "from", Type: makeUDT("Account")},
						{Name: "amount", Type: makeUDT("Amount")},
					},
					Outputs: []xdr.ScSpecTypeDef{makeUDT("Receipt")},
				},
			},
			// Union-typed input: process(cmd: udt:Command) -> void.
			{
				Kind: xdr.ScSpecEntryKindScSpecEntryFunctionV0,
				FunctionV0: &xdr.ScSpecFunctionV0{
					Name: xdr.ScSymbol("process"),
					Inputs: []xdr.ScSpecFunctionInputV0{
						{Name: "cmd", Type: makeUDT("Command")},
					},
					Outputs: []xdr.ScSpecTypeDef{},
				},
			},
			// Enum reference as return value: get_status() -> udt:Status.
			{
				Kind: xdr.ScSpecEntryKindScSpecEntryFunctionV0,
				FunctionV0: &xdr.ScSpecFunctionV0{
					Name:    xdr.ScSymbol("get_status"),
					Inputs:  []xdr.ScSpecFunctionInputV0{},
					Outputs: []xdr.ScSpecTypeDef{makeUDT("Status")},
				},
			},
			// Forward reference: fn uses udt:DefinedLater whose struct entry follows.
			{
				Kind: xdr.ScSpecEntryKindScSpecEntryFunctionV0,
				FunctionV0: &xdr.ScSpecFunctionV0{
					Name: xdr.ScSymbol("forward_fn"),
					Inputs: []xdr.ScSpecFunctionInputV0{
						{Name: "x", Type: makeUDT("DefinedLater")},
					},
					Outputs: []xdr.ScSpecTypeDef{},
				},
			},
			// The DefinedLater struct definition follows the function that references it.
			// Functions() skips this entry; it must not appear in the output.
			{
				Kind: xdr.ScSpecEntryKindScSpecEntryUdtStructV0,
				UdtStructV0: &xdr.ScSpecUdtStructV0{
					Name: "DefinedLater",
					Fields: []xdr.ScSpecUdtStructFieldV0{
						{Name: "val", Type: xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeU32}},
					},
				},
			},
		}

		fns := Functions(entries)
		// Struct entry is skipped; expect exactly the 4 function entries.
		if len(fns) != 4 {
			t.Fatalf("want 4 functions, got %d: %v", len(fns), fns)
		}
		want := []FnSig{
			sig("transfer_struct", ins("udt:Account", "udt:Amount"), "udt:Receipt"),
			sig("process", ins("udt:Command")),
			sig("get_status", nil, "udt:Status"),
			sig("forward_fn", ins("udt:DefinedLater")),
		}
		for i, w := range want {
			g := fns[i]
			if g.Name != w.Name {
				t.Errorf("fn[%d] name = %q, want %q", i, g.Name, w.Name)
			}
			if !reflect.DeepEqual(g.Inputs, w.Inputs) {
				t.Errorf("fn[%d] inputs = %v, want %v", i, g.Inputs, w.Inputs)
			}
			if !reflect.DeepEqual(g.Outputs, w.Outputs) {
				t.Errorf("fn[%d] outputs = %v, want %v", i, g.Outputs, w.Outputs)
			}
		}
	})

	t.Run("DecodeSpec with UDT types round-trips cleanly and reports correct signatures", func(t *testing.T) {
		entries := []xdr.ScSpecEntry{
			{
				Kind: xdr.ScSpecEntryKindScSpecEntryFunctionV0,
				FunctionV0: &xdr.ScSpecFunctionV0{
					Name: xdr.ScSymbol("do_work"),
					Inputs: []xdr.ScSpecFunctionInputV0{
						{Name: "cfg", Type: makeUDT("Config")},
						{Name: "data", Type: xdr.ScSpecTypeDef{
							Type: xdr.ScSpecTypeScSpecTypeVec,
							Vec:  &xdr.ScSpecTypeVec{ElementType: makeUDT("Item")},
						}},
					},
					Outputs: []xdr.ScSpecTypeDef{
						{Type: xdr.ScSpecTypeScSpecTypeResult, Result: &xdr.ScSpecTypeResult{
							OkType:    makeUDT("Output"),
							ErrorType: makeUDT("AppError"),
						}},
					},
				},
			},
		}
		section := encodeSpecEntries(t, entries)

		got, err := DecodeSpec(section, DefaultLimits())
		if err != nil {
			t.Fatalf("DecodeSpec: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("want 1 entry, got %d", len(got))
		}
		fns := Functions(got)
		if len(fns) != 1 {
			t.Fatalf("want 1 function, got %d", len(fns))
		}
		wantIn := []string{"udt:Config", "Vec<udt:Item>"}
		if !reflect.DeepEqual(fns[0].Inputs, wantIn) {
			t.Errorf("inputs = %v, want %v", fns[0].Inputs, wantIn)
		}
		wantOut := []string{"Result<udt:Output,udt:AppError>"}
		if !reflect.DeepEqual(fns[0].Outputs, wantOut) {
			t.Errorf("outputs = %v, want %v", fns[0].Outputs, wantOut)
		}
	})

	t.Run("truncated payloads with UDT types return ErrTruncated and partial results", func(t *testing.T) {
		entry1 := xdr.ScSpecEntry{
			Kind: xdr.ScSpecEntryKindScSpecEntryFunctionV0,
			FunctionV0: &xdr.ScSpecFunctionV0{
				Name: xdr.ScSymbol("fn_a"),
				Inputs: []xdr.ScSpecFunctionInputV0{
					{Name: "x", Type: makeUDT("TypeA")},
				},
				Outputs: []xdr.ScSpecTypeDef{},
			},
		}
		entry2 := xdr.ScSpecEntry{
			Kind: xdr.ScSpecEntryKindScSpecEntryFunctionV0,
			FunctionV0: &xdr.ScSpecFunctionV0{
				Name:    xdr.ScSymbol("fn_b"),
				Inputs:  []xdr.ScSpecFunctionInputV0{},
				Outputs: []xdr.ScSpecTypeDef{makeUDT("TypeB")},
			},
		}

		section := encodeSpecEntries(t, []xdr.ScSpecEntry{entry1, entry2})

		// Measure where entry1 ends.
		var e1buf bytes.Buffer
		if _, err := xdr.Marshal(&e1buf, entry1); err != nil {
			t.Fatal(err)
		}
		boundary := e1buf.Len()

		// Exactly at the boundary: one clean entry, no error.
		got, err := DecodeSpec(section[:boundary], DefaultLimits())
		if err != nil {
			t.Fatalf("at boundary: unexpected err %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("at boundary: want 1 entry, got %d", len(got))
		}

		// One byte before the boundary: ErrTruncated; at most 0 complete entries.
		got, err = DecodeSpec(section[:boundary-1], DefaultLimits())
		if !errors.Is(err, ErrTruncated) {
			t.Fatalf("boundary-1: want ErrTruncated, got %v (entries=%d)", err, len(got))
		}
		if len(got) > 1 {
			t.Fatalf("boundary-1: at most 1 complete entry expected, got %d", len(got))
		}

		// Mid-way into entry2: ErrTruncated, one complete entry.
		mid := boundary + (len(section)-boundary)/2
		if mid > boundary && mid < len(section) {
			got, err = DecodeSpec(section[:mid], DefaultLimits())
			if !errors.Is(err, ErrTruncated) {
				t.Fatalf("mid-entry2: want ErrTruncated, got %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("mid-entry2: want 1 complete entry, got %d", len(got))
			}
		}

		// Full section: 2 entries, correct signatures, no error.
		got, err = DecodeSpec(section, DefaultLimits())
		if err != nil {
			t.Fatalf("full: unexpected err %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("full: want 2 entries, got %d", len(got))
		}
		fns := Functions(got)
		if len(fns) != 2 {
			t.Fatalf("full: want 2 functions, got %d", len(fns))
		}
		// Bounds: fn_a has one UDT input; fn_b has one UDT output.
		if len(fns[0].Inputs) != 1 || fns[0].Inputs[0] != "udt:TypeA" {
			t.Errorf("fn_a inputs: want [udt:TypeA], got %v", fns[0].Inputs)
		}
		if len(fns[1].Outputs) != 1 || fns[1].Outputs[0] != "udt:TypeB" {
			t.Errorf("fn_b outputs: want [udt:TypeB], got %v", fns[1].Outputs)
		}
	})
}

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
		// 19 scalar variants exist in go-stellar-sdk v0.8.0 (unchanged since
		// v0.7.3); a new one added by
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
